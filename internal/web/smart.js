"use strict";

// The smart playlist editor: a rule as a tree of conditions, with the
// tracks it matches now shown beneath and re-run as it is edited.  A
// rule is the arguments of a Track/query (doc/types.md).  The editor
// keeps a tree of its own, compiles it to the filter on every change,
// and reads any stored filter back into the tree, since AND, OR, NOT
// and the named conditions are all a filter can hold.  Nothing reaches
// the server until Save, except the preview queries.
const smart = (() => {

// ---- Fields and operators ----

// A field is what a row is about.  Its kind decides the operators and
// the input; the condition names are the server's.
const FIELDS = [
  { key: "title",      label: "Title",          kind: "string", is: "title",      contains: "titleContains" },
  { key: "artist",     label: "Artist",         kind: "string", is: "artist",     contains: "artistContains",     has: "hasArtist", suggest: () => distinct(albumList.map(a => a.artist)) },
  { key: "albumTitle", label: "Album",          kind: "string", is: "albumTitle", contains: "albumTitleContains", has: "hasAlbum",  suggest: () => distinct(albumList.map(a => a.title)) },
  { key: "genre",      label: "Genre",          kind: "string", is: "genre",      contains: "genreContains",      has: "hasGenre" },
  { key: "rating",     label: "Rating",         kind: "number", atLeast: "ratingAtLeast",    atMost: "ratingAtMost",    has: "hasRating", step: 0.5, min: 0, max: 5, unit: "stars" },
  { key: "playCount",  label: "Play count",     kind: "number", atLeast: "playCountAtLeast", atMost: "playCountAtMost", step: 1, min: 0, unit: "plays" },
  { key: "duration",   label: "Length",         kind: "duration", atLeast: "durationAtLeast", atMost: "durationAtMost" },
  { key: "lastPlayed", label: "Last played",    kind: "date" },
  { key: "playlist",   label: "Playlist",       kind: "ref", is: "inPlaylist" },
  { key: "smart",      label: "Smart playlist", kind: "ref", is: "inSmartPlaylist" },
  { key: "audio",      label: "Audio",          kind: "audio" },
];
const FIELD = Object.fromEntries(FIELDS.map(f => [f.key, f]));

const OPS = {
  is: "is", isNot: "is not", contains: "contains", notContains: "doesn't contain",
  anyOf: "is any of", noneOf: "is none of", set: "is set", unset: "is not set",
  atLeast: "is at least", atMost: "is at most",
  before: "was before", after: "was on or after", within: "was within the last", notWithin: "was not within the last",
  in: "is in", notIn: "is not in", has: "is present", hasNot: "is missing",
};

// The negated operators match a track whose value is missing too, as
// the server defines NOT; the row says so.
const NEGATED = { isNot: "is", notContains: "contains", noneOf: "anyOf", notWithin: "within", notIn: "in" };
const NEGATION = Object.fromEntries(Object.entries(NEGATED).map(([n, p]) => [p, n]));

function opsFor(f) {
  switch (f.kind) {
    case "string": return ["is", "isNot", "contains", "notContains", "anyOf", "noneOf", ...(f.has ? ["set", "unset"] : [])];
    case "number": return ["atLeast", "atMost", "is", ...(f.has ? ["set", "unset"] : [])];
    case "duration": return ["atLeast", "atMost"];
    case "date": return ["within", "notWithin", "before", "after"];
    case "ref": return ["in", "notIn"];
    case "audio": return ["has", "hasNot"];
  }
}

function distinct(values) {
  return [...new Set(values.filter(Boolean))].sort((a, b) => fold(a).localeCompare(fold(b)));
}

// ---- The tree ----

// A node is a group, {kind: "group", op: all|any|none, items}, or a row,
// {kind: "row", field, op, value}.  A row's value is a string, a number
// (seconds for a length, days or a YYYY-MM-DD date for last played), an
// id for a reference, or a list of strings for any-of and none-of.
function group(op, items = []) { return { kind: "group", op, items }; }
function row(field, op, value = "") { return { kind: "row", field, op, value }; }
function newRow() { return row("genre", "is"); }

// compile turns the tree into a Track/query filter, or throws for a row
// that is not finished.
function compile(g) {
  if (g.kind === "row") return compileRow(g);
  const conditions = g.items.map(compile);
  return { operator: { all: "AND", any: "OR", none: "NOT" }[g.op], conditions };
}

function compileRow(r) {
  const f = FIELD[r.field];
  const v = r.value;
  const need = () => {
    if (v === "" || v == null || (Array.isArray(v) && !v.length)) throw new Error(`"${f.label} ${OPS[r.op]}" needs a value.`);
    return v;
  };
  const not = c => ({ operator: "NOT", conditions: [c] });
  switch (r.op) {
    case "is": return f.kind === "number" ? { [f.atLeast]: Number(need()), [f.atMost]: Number(v) } : { [f.is]: need() };
    case "isNot": return not({ [f.is]: need() });
    case "contains": return { [f.contains]: need() };
    case "notContains": return not({ [f.contains]: need() });
    case "anyOf": return { operator: "OR", conditions: need().map(x => ({ [f.is]: x })) };
    case "noneOf": return { operator: "NOT", conditions: need().map(x => ({ [f.is]: x })) };
    case "set": return { [f.has]: true };
    case "unset": return { [f.has]: false };
    case "atLeast": return { [f.atLeast]: Number(need()) };
    case "atMost": return { [f.atMost]: Number(need()) };
    case "before": return { lastPlayedBefore: `${need()}T00:00:00Z` };
    case "after": return { lastPlayedAfter: `${need()}T00:00:00Z` };
    case "within": return { lastPlayedWithinDays: Number(need()) };
    case "notWithin": return not({ lastPlayedWithinDays: Number(need()) });
    case "in": return { [f.is]: need() };
    case "notIn": return not({ [f.is]: need() });
    case "has": return { hasAudio: true };
    case "hasNot": return { hasAudio: false };
  }
  throw new Error(`no operator ${r.op}`);
}

// COND maps each condition name the server knows to the row it makes.
const COND = {};
for (const f of FIELDS) {
  if (f.is) COND[f.is] = v => row(f.key, f.kind === "ref" ? "in" : "is", v);
  if (f.contains) COND[f.contains] = v => row(f.key, "contains", v);
  if (f.has) COND[f.has] = v => row(f.key, v ? "set" : "unset");
  if (f.atLeast) COND[f.atLeast] = v => row(f.key, "atLeast", v);
  if (f.atMost) COND[f.atMost] = v => row(f.key, "atMost", v);
}
COND.lastPlayedBefore = v => row("lastPlayed", "before", v.slice(0, 10));
COND.lastPlayedAfter = v => row("lastPlayed", "after", v.slice(0, 10));
COND.lastPlayedWithinDays = v => row("lastPlayed", "within", v);
COND.hasAudio = v => row("audio", v ? "has" : "hasNot");

// parse reads a stored filter into a tree whose root is a group.
function parse(filter) {
  if (filter == null) return group("all");
  const node = parseNode(filter);
  return node.kind === "group" ? node : group("all", [node]);
}

function parseNode(f) {
  if (f.operator) {
    const items = (f.conditions || []).map(parseNode);
    const op = { AND: "all", OR: "any", NOT: "none" }[f.operator];
    if (!op) throw new Error(`unknown operator ${f.operator}`);
    if (op === "none" && items.length === 1 && items[0].kind === "row" && NEGATION[items[0].op]) {
      return { ...items[0], op: NEGATION[items[0].op] };
    }
    // Several "is" rows on one string field, any or none of them, are
    // one row with a list.
    if (op !== "all" && items.length && items.every(i => i.kind === "row" && i.op === "is" && i.field === items[0].field && FIELD[i.field].kind === "string")) {
      return row(items[0].field, op === "any" ? "anyOf" : "noneOf", items.map(i => i.value));
    }
    return group(op, items);
  }
  const keys = Object.keys(f);
  // A condition on both bounds of one number, equal, is "is".
  if (keys.length === 2) {
    const nf = FIELDS.find(x => x.kind === "number" && keys.includes(x.atLeast) && keys.includes(x.atMost));
    if (nf && f[nf.atLeast] === f[nf.atMost]) return row(nf.key, "is", f[nf.atLeast]);
  }
  const rows = keys.map(k => {
    if (!COND[k]) throw new Error(`The rule has a condition this editor doesn't know: ${k}.`);
    return COND[k](f[k]);
  });
  return rows.length === 1 ? rows[0] : group("all", rows);
}

// ---- Sorting and caps ----

const SORTS = [
  ["title", "Title"], ["artist", "Artist"], ["albumTitle", "Album"], ["albumOrder", "Album order"],
  ["genre", "Genre"], ["rating", "Rating"], ["playCount", "Play count"], ["lastPlayed", "Last played"],
  ["duration", "Length"], ["random", "Random"],
];

function seed() { return crypto.randomUUID(); }

function comparator(c) {
  const out = { property: c.property };
  if (c.isAscending === false) out.isAscending = false;
  if (c.property === "random") out.seed = c.seed;
  return out;
}

// ---- State ----

// editing holds the rule as the page has it: the tree, the sort and the
// caps, the title, what the server has (for telling a change), and the
// preview's last answer.  pos is the preview's page.
let editing = null;
let previewSeq = 0, previewTimer = null;

function leaving() {
  if (!editing || location.hash === editing.hash) return true;
  if (!isDirty() || confirm("Leave without saving this smart playlist?")) {
    editing = null;
    return true;
  }
  location.hash = editing.hash;
  return false;
}

window.addEventListener("beforeunload", ev => { if (editing && isDirty()) ev.preventDefault(); });

// rule is what Save would send: the properties of the playlist as the
// page has them.  A root group of one condition is stored as that
// condition, and no condition at all as null, matching everything.
function rule() {
  const e = editing, t = e.tree;
  return {
    title: e.title,
    filter: !t.items.length ? null : t.items.length === 1 && t.op !== "none" ? compile(t.items[0]) : compile(t),
    sort: e.sort.map(comparator),
    limit: e.limit, limitBytes: e.limitBytes, limitSeconds: e.limitSeconds,
  };
}

function serialized() {
  try { return JSON.stringify(rule()); } catch { return "unfinished"; }
}

function isDirty() { return serialized() !== editing.original; }

// open starts editing a playlist as stored, a copy of one (no id, a new
// title), or a blank one.
async function open(pl, { id = pl?.id ?? null, title = pl?.title ?? "" } = {}) {
  const [p, s] = await jmap(["Playlist/get", { ids: null, properties: ["id", "title"] }],
                            ["SmartPlaylist/get", { ids: null, properties: ["id", "title"] }]);
  const byTitle = (a, b) => fold(a.title).localeCompare(fold(b.title));
  editing = {
    id, hash: `#smart/${id ?? "new"}`,
    back: id ?? pl?.id ?? null, // where Cancel goes: this playlist, or the one copied
    title,
    tree: pl ? parse(pl.filter) : group("all", [newRow()]),
    sort: (pl?.sort ?? []).map(c => ({ ...c })),
    limit: pl?.limit ?? null, limitBytes: pl?.limitBytes ?? null, limitSeconds: pl?.limitSeconds ?? null,
    pos: 0, preview: null, error: null,
    playlists: p.list.sort(byTitle),
    smarts: s.list.filter(x => x.id !== id).sort(byTitle),
  };
  editing.original = id ? serialized() : null;
  if (location.hash !== editing.hash) location.hash = editing.hash;
  render();
  schedulePreview(0);
}

// ---- Rendering ----

function render() {
  const e = editing;
  show(
    h("h2", {}, gear(), e.id ? "Edit smart playlist" : "New smart playlist"),
    h("div", { class: "editor" },
      h("div", { class: "field" }, h("label", {}, "Title"),
        h("input", { type: "text", value: e.title, placeholder: "Title", size: 40, oninput: ev => { e.title = ev.target.value; document.querySelector(".savebar")?.replaceWith(saveBar()); } })),
      FIELDS.filter(f => f.suggest).map(f => h("datalist", { id: `suggest-${f.key}` }, f.suggest().map(v => h("option", { value: v })))),
      h("h3", {}, "Match"),
      h("div", { class: "rule" }, groupView(e.tree, null)),
      h("h3", {}, "Order"),
      sortView(),
      h("h3", {}, "Caps"),
      capsView(),
    ),
    h("h3", {}, "Preview"),
    h("div", { class: "preview" }, previewView()),
    saveBar(),
  );
}

// changed is called after any edit: structural ones redraw the editor,
// typing redraws only what depends on the rule, so the input keeps focus.
function changed(structural = true) {
  if (structural) render();
  else {
    document.querySelector(".savebar")?.replaceWith(saveBar());
  }
  editing.pos = 0;
  schedulePreview();
}

function groupView(g, parent) {
  const which = h("select", { onchange: ev => { g.op = ev.target.value; changed(); } },
    [["all", "All of these"], ["any", "Any of these"], ["none", "None of these"]].map(([v, label]) =>
      h("option", { value: v, selected: g.op === v || null }, label)));
  const remove = parent && h("button", { class: "x", title: "Remove this group", onclick: () => { parent.items.splice(parent.items.indexOf(g), 1); changed(); } }, "×");
  return h("div", { class: "group" },
    h("div", { class: "head" }, which,
      g.op === "none" ? h("span", { class: "hint" }, "matches tracks with none of these, missing values included") : null,
      remove),
    g.items.map(n => n.kind === "group" ? groupView(n, g) : rowView(n, g)),
    h("div", { class: "adders" },
      h("button", { onclick: () => { g.items.push(newRow()); changed(); } }, "+ Condition"),
      h("button", { onclick: () => { g.items.push(group("all", [newRow()])); changed(); } }, "+ Group")));
}

function rowView(r, g) {
  const f = FIELD[r.field];
  const field = h("select", { onchange: ev => { r.field = ev.target.value; r.op = opsFor(FIELD[r.field])[0]; r.value = ""; changed(); } },
    FIELDS.map(x => h("option", { value: x.key, selected: x.key === r.field || null }, x.label)));
  const op = h("select", { onchange: ev => {
    const was = r.op;
    r.op = ev.target.value;
    const listy = o => o === "anyOf" || o === "noneOf";
    if (listy(r.op) !== listy(was)) r.value = listy(r.op) ? (r.value ? [r.value] : []) : (r.value[0] ?? "");
    changed();
  } }, opsFor(f).map(o => h("option", { value: o, selected: o === r.op || null }, OPS[o])));
  const el = h("div", { class: "row" }, field, op, valueView(r, f),
    NEGATED[r.op] ? h("span", { class: "hint", title: "The server's NOT: a track with no value here can't match the condition, so it matches its negation." }, `also tracks with no ${f.label.toLowerCase()}`) : null,
    h("button", { class: "x", title: "Remove this condition", onclick: () => { g.items.splice(g.items.indexOf(r), 1); changed(); } }, "×"));
  return el;
}

function valueView(r, f) {
  const typed = ev => { r.value = ev.target.value; changed(false); };
  switch (r.op) {
    case "set": case "unset": case "has": case "hasNot": return null;
    case "anyOf": case "noneOf": return chipsView(r, f);
    case "in": case "notIn": {
      const list = f.key === "smart" ? editing.smarts : editing.playlists;
      return h("select", { onchange: ev => { r.value = ev.target.value; changed(false); } },
        h("option", { value: "", selected: !r.value || null }, "choose…"),
        list.map(p => h("option", { value: p.id, selected: p.id === r.value || null }, p.title)));
    }
    case "before": case "after": return h("input", { type: "date", value: r.value, onchange: typed });
    case "within": case "notWithin": return h("span", {}, h("input", { type: "number", min: 1, step: 1, value: r.value, size: 5, oninput: typed }), " days");
  }
  switch (f.kind) {
    case "string": {
      return h("input", { type: "text", value: r.value, list: f.suggest ? `suggest-${f.key}` : null, oninput: typed });
    }
    case "number":
      return h("span", {}, h("input", { type: "number", min: f.min, max: f.max, step: f.step, value: r.value, oninput: typed }), ` ${f.unit}`);
    case "duration":
      return h("input", { type: "text", value: r.value === "" ? "" : clock(r.value), placeholder: "m:ss", size: 6, oninput: ev => {
        r.value = unclock(ev.target.value);
        ev.target.setCustomValidity(r.value === "" && ev.target.value ? "minutes:seconds, or seconds" : "");
        changed(false);
      } });
  }
  return null;
}

function clock(s) { return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`; }
function unclock(text) {
  const m = text.trim().match(/^(?:(\d+):)?(\d+)$/);
  if (!m) return "";
  return Number(m[1] || 0) * 60 + Number(m[2]);
}

// chipsView is the list of values of an any-of or none-of row; a value
// is added by Enter or a comma, and removed by its ×.  The row redraws
// itself so the input keeps focus.
function chipsView(r, f) {
  const values = Array.isArray(r.value) ? r.value : [];
  const listId = f.suggest ? `suggest-${f.key}` : null;
  let el;
  const redraw = () => { r.value = values; const n = chipsView(r, f); el.replaceWith(n); n.querySelector("input").focus(); changed(false); };
  const add = text => { const v = text.trim(); if (v && !values.includes(v)) { values.push(v); redraw(); } };
  const input = h("input", { type: "text", list: listId, placeholder: values.length ? "" : "value, value, …",
    onkeydown: ev => {
      if (ev.key === "Enter" || ev.key === ",") { ev.preventDefault(); add(ev.target.value); }
      if (ev.key === "Backspace" && !ev.target.value && values.length) { values.pop(); redraw(); }
    },
    onchange: ev => add(ev.target.value),
    onblur: ev => { if (ev.target.value.trim()) add(ev.target.value); } });
  el = h("span", { class: "chips" },
    values.map(v => h("span", { class: "chip" }, v, h("button", { title: "Remove", onclick: () => { values.splice(values.indexOf(v), 1); redraw(); } }, "×"))),
    input);
  return el;
}

function sortView() {
  const e = editing;
  return h("div", { class: "sorts" },
    e.sort.map((c, i) => h("div", { class: "row" },
      h("select", { onchange: ev => { c.property = ev.target.value; if (c.property === "random" && !c.seed) c.seed = seed(); changed(); } },
        SORTS.map(([v, label]) => h("option", { value: v, selected: c.property === v || null }, label))),
      c.property === "random"
        ? h("button", { title: "A new seed: a different order", onclick: () => { c.seed = seed(); changed(); } }, "Reshuffle")
        : h("select", { onchange: ev => { c.isAscending = ev.target.value === "asc"; changed(); } },
          h("option", { value: "asc", selected: c.isAscending !== false || null }, c.property === "lastPlayed" ? "least recent first" : "ascending"),
          h("option", { value: "desc", selected: c.isAscending === false || null }, c.property === "lastPlayed" ? "most recent first" : "descending")),
      i > 0 && h("button", { class: "x", title: "Sort by this earlier", onclick: () => { e.sort.splice(i - 1, 0, ...e.sort.splice(i, 1)); changed(); } }, "↑"),
      h("button", { class: "x", title: "Remove", onclick: () => { e.sort.splice(i, 1); changed(); } }, "×"))),
    h("div", { class: "adders" }, h("button", { onclick: () => { e.sort.push({ property: "albumOrder" }); changed(); } }, "+ Sort")),
    !e.sort.length && h("div", { class: "hint" }, "No order: tracks come as the server finds them."));
}

function capsView() {
  const e = editing;
  const cap = (label, get, set, unit, scale = 1) => h("div", { class: "row" },
    h("label", {}, label),
    h("input", { type: "number", min: 0, step: "any", value: get() == null ? "" : get() / scale, oninput: ev => {
      const n = Number(ev.target.value);
      set(ev.target.value === "" || !(n > 0) ? null : Math.round(n * scale));
      changed(false);
    } }), ` ${unit}`);
  return h("div", {},
    cap("At most", () => e.limit, v => { e.limit = v; }, "tracks"),
    cap("At most", () => e.limitBytes, v => { e.limitBytes = v; }, "MB", 1024 * 1024),
    cap("At most", () => e.limitSeconds, v => { e.limitSeconds = v; }, "minutes", 60),
    h("div", { class: "hint" }, "Caps cut the sorted list; a track with no audio or no known length counts as nothing toward MB and minutes."));
}

// ---- Preview ----

function schedulePreview(delay = 400) {
  clearTimeout(previewTimer);
  previewTimer = setTimeout(runPreview, delay);
}

// runPreview runs the rule as it stands, for the page shown.  A rule
// with a track cap is paged within it; the other caps the server
// applies itself, with total and position of the capped list.  A
// stale answer, from before a later edit, is dropped.
async function runPreview() {
  const e = editing;
  if (!e) return;
  const seq = ++previewSeq;
  let args;
  try {
    const r = rule();
    args = { filter: r.filter, sort: r.sort, position: e.pos, limit: PAGE, calculateTotal: true };
    if (r.limit != null) args.limit = Math.max(0, Math.min(PAGE, r.limit - e.pos));
    if (r.limitBytes != null) args.limitBytes = r.limitBytes;
    if (r.limitSeconds != null) args.limitSeconds = r.limitSeconds;
  } catch (err) {
    e.error = err.message; e.preview = null;
    return drawPreview();
  }
  e.running = true;
  drawPreview();
  try {
    const [q, g] = await jmap(["Track/query", args], ["Track/get", { ids: ["#", 0, "Track/query", "/ids"] }]);
    if (seq !== previewSeq) return;
    g.list.forEach(t => tracks.set(t.id, t));
    const total = e.limit != null ? Math.min(q.total, e.limit) : q.total;
    e.preview = { ids: q.ids, total, matched: q.total };
    e.error = null;
  } catch (err) {
    if (seq !== previewSeq) return;
    e.error = err.message; e.preview = null;
  }
  e.running = false;
  drawPreview();
}

function drawPreview() {
  document.querySelector(".preview")?.replaceChildren(...[previewView()].flat().filter(Boolean));
  document.querySelector(".savebar")?.replaceWith(saveBar());
}

function previewView() {
  const e = editing;
  if (e.error) return h("p", { class: "error" }, e.error);
  if (!e.preview) return h("p", { class: "sub" }, "Running the rule…");
  const { ids, total, matched } = e.preview;
  const list = ids.map(id => tracks.get(id));
  const go = pos => { e.pos = pos; runPreview(); };
  const cut = e.limit != null && matched > e.limit ? ` (the first ${e.limit} of ${matched} matching)` : "";
  return [
    h("div", { class: "sub" }, `${total} tracks${cut}`, e.running ? " · running…" : ""),
    pager(e.pos, total, go),
    trackTable(list, { number: true, start: e.pos, album: true }),
    pager(e.pos, total, go),
  ];
}

// ---- Saving ----

function saveBar() {
  const e = editing;
  let problem = e.error;
  if (!problem && !e.title.trim()) problem = "The playlist needs a title.";
  const dirty = !problem && isDirty();
  return h("div", { class: "savebar" },
    h("button", { disabled: !dirty || null, onclick: save }, e.id ? "Save changes" : "Create playlist"),
    e.id && h("button", { onclick: destroy }, "Delete"),
    h("button", { onclick: () => { editing = null; location.hash = e.back ? `#smart/${e.back}` : "#playlists"; } }, "Cancel"),
    h("span", { class: dirty ? "dirty" : "sub" }, problem ? problem : dirty ? "Unsaved changes" : "Nothing to save"));
}

// save creates or updates the playlist and opens it.  The server may
// answer with a seed it chose, but the page always sends its own.
async function save() {
  const e = editing;
  const props = rule();
  status("saving…");
  try {
    const [r] = await jmap(["SmartPlaylist/set", e.id ? { update: { [e.id]: props } } : { create: { s: props } }]);
    const failed = Object.values(r.notCreated || {}).concat(Object.values(r.notUpdated || {}));
    if (failed.length) throw new Error(failed.map(f => `${f.type}${f.description ? ": " + f.description : ""}`).join("; "));
    status("");
    const id = e.id || r.created.s.id;
    editing = null;
    if (location.hash === `#smart/${id}`) route(); else location.hash = `#smart/${id}`;
  } catch (err) {
    status("");
    alert(`Couldn't save: ${err.message}`);
  }
}

async function destroy() {
  const e = editing;
  if (!confirm(`Delete the smart playlist "${e.title}"?`)) return;
  try {
    const [r] = await jmap(["SmartPlaylist/set", { destroy: [e.id] }]);
    const failed = r.notDestroyed?.[e.id];
    if (failed) throw new Error(`${failed.type}${failed.description ? ": " + failed.description : ""}`);
    editing = null;
    location.hash = "#playlists";
  } catch (err) {
    alert(`Couldn't delete: ${err.message}`);
  }
}

// ---- Entry points ----

// page shows #smart/<id>: the editor when that playlist is being
// edited, or a new one is, else the playlist's own page.
async function page(id) {
  if (editing && (editing.id ?? "new") === id) return render();
  if (id === "new") return open(null);
  await renderSmart(id);
}

function edit(pl) { return open(pl); }
function duplicate(pl) { return open(pl, { id: null, title: `${pl.title} copy` }); }

return { page, edit, duplicate, leaving, parse, compile };
})();

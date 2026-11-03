"use strict";

// The review tab: the albums with something wrong, and a page for each
// where it can be put right.  Everything here asks the server under the
// review capability, which is what gives an album its issues and the
// pictures it could have; see doc/types.md.  An album's page edits a
// copy and saves it whole, in one request.
const review = (() => {

const ISSUES = [
  ["noArt", "No art", "Tracks with audio, but no image."],
  ["artOverrides", "Mixed art", "Some tracks show a picture other than the album's."],
  ["artTie", "Art on a tie", "The rule picked the album's image on a tie."],
  ["folderArt", "Folder art differs", "The image file beside the tracks differs from pictures they carry."],
  ["unnumbered", "Numbered from nowhere", "A track was placed after the rest because nothing numbered it."],
  ["gaps", "Missing tracks", "Tracks with no audio."],
];
const ISSUE = Object.fromEntries(ISSUES.map(([k, label, why]) => [k, { label, why }]));

const NUMBER_FROM = {
  tags: "tags", catalog: "catalog", file: "filename", user: "set by hand", none: "nowhere",
};

function call(...calls) { return jmapUsing([CAP_CORE, CAP_JUKE, CAP_REVIEW], ...calls); }

function issueTags(issues) {
  return issues.map(i => h("span", { class: "tag", title: ISSUE[i]?.why }, ISSUE[i]?.label || i));
}

// ---- The queue ----

function listParams() {
  const p = new URLSearchParams(location.hash.split("?")[1] || "");
  return { issue: p.get("issue") || "", pos: Number(p.get("pos")) || 0 };
}

function goList(p) {
  const out = new URLSearchParams();
  if (p.issue) out.set("issue", p.issue);
  if (p.pos) out.set("pos", p.pos);
  location.hash = "review" + (out.toString() ? "?" + out : "");
}

async function list() {
  const p = listParams();
  const filter = p.issue ? { issue: p.issue } : { hasIssues: true };
  const [q, g] = await call(
    ["Album/query", { filter, sort: [{ property: "title" }], position: p.pos, limit: PAGE, calculateTotal: true }],
    ["Album/get", { ids: ["#", 0, "Album/query", "/ids"], properties: ["title", "artist", "issues"] }],
  );
  const which = h("select", { onchange: ev => goList({ issue: ev.target.value, pos: 0 }) },
    h("option", { value: "", selected: !p.issue || null }, "Any issue"),
    ISSUES.map(([k, label]) => h("option", { value: k, selected: p.issue === k || null }, label)));
  show(
    h("h2", {}, "Albums to review"),
    h("div", { class: "controls" }, which, h("span", { class: "sub" }, `${q.total} albums`)),
    h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Artist"), h("th", {}, "Title"), h("th", {}, "Issues"))),
      h("tbody", {}, g.list.map(a => h("tr", {},
        h("td", {}, orNone(a.artist, "compilation")),
        h("td", {}, h("a", { href: `#review/${a.id}` }, a.title)),
        h("td", {}, issueTags(a.issues)))))),
    pager(p.pos, q.total, pos => goList({ ...p, pos }), "albums"),
  );
}

// ---- One album ----

// What's being edited: the album and its tracks as the server has them,
// and the art, each track's own art, and the disc order as they are on
// the page.  Null between albums.
let editing = null;
let dragging = null;

// leaving is asked by the router before it shows another view.  A page
// with unsaved changes asks first, and on no puts its own hash back,
// which brings the router here again with nothing to ask.
function leaving() {
  if (!editing || location.hash === editing.hash) return true;
  if (!isDirty() || confirm("Leave without saving this album's changes?")) {
    editing = null;
    return true;
  }
  location.hash = editing.hash;
  return false;
}

window.addEventListener("beforeunload", ev => { if (editing && isDirty()) ev.preventDefault(); });

async function album(id) {
  if (!editing || editing.id !== id) {
    const [g] = await call(["Album/get", { ids: [id] }]);
    const a = g.list[0];
    if (!a) return show(h("p", { class: "error" }, `No album ${id}.`));
    const ids = a.discs.flatMap(d => d.trackIds);
    const ts = new Map();
    for (let i = 0; i < ids.length; i += maxGet) {
      const [t] = await call(["Track/get", { ids: ids.slice(i, i + maxGet) }]);
      t.list.forEach(t => { ts.set(t.id, t); tracks.set(t.id, t); });
    }
    editing = {
      id, hash: `#review/${id}`, album: a, tracks: ts, uploads: [],
      discs: a.discs.map(d => [...d.trackIds]),
      artwork: a.artworkBlobId,
      trackArt: new Map(ids.map(t => [t, ts.get(t).artworkBlobId])),
    };
  }
  render();
}

function discsChanged() {
  const e = editing;
  return JSON.stringify(e.discs) !== JSON.stringify(e.album.discs.map(d => d.trackIds));
}

function isDirty() {
  const e = editing;
  return e.artwork !== e.album.artworkBlobId
    || [...e.trackArt].some(([id, art]) => art !== e.tracks.get(id).artworkBlobId)
    || discsChanged();
}

function revert() {
  const e = editing;
  e.artwork = e.album.artworkBlobId;
  e.discs = e.album.discs.map(d => [...d.trackIds]);
  for (const id of e.trackArt.keys()) e.trackArt.set(id, e.tracks.get(id).artworkBlobId);
}

// candidates are the album's, numbered, then anything uploaded on this
// page, with the art it has now added in front if it came from
// somewhere else.
function candidates() {
  const a = editing.album;
  const out = a.artworkCandidates.map((c, i) => ({ ...c, n: i + 1 }));
  for (const u of editing.uploads) {
    if (!out.some(c => c.blobId === u.blobId)) out.push({ ...u, trackIds: [], uploaded: true, n: out.length + 1 });
  }
  if (a.artworkBlobId && !out.some(c => c.blobId === a.artworkBlobId)) {
    out.unshift({ blobId: a.artworkBlobId, type: a.artworkType, trackIds: [], elsewhere: true, n: 0 });
  }
  return out;
}

function candidateName(c) {
  if (c.elsewhere) return "Chosen before";
  if (c.uploaded) return `Picture ${c.n}, uploaded`;
  return c.folder ? `Picture ${c.n}, the file beside the tracks` : `Picture ${c.n}`;
}

// well is where an image file is dropped, or picked, to be uploaded
// and chosen as the album's art.
function well() {
  const input = h("input", { type: "file", accept: "image/*", onchange: ev => { if (ev.target.files[0]) upload(ev.target.files[0]); } });
  const el = h("div", { class: "well" },
    h("div", {}, "Drop an image here to use it, or ", h("label", {}, "choose a file", input)));
  el.addEventListener("dragover", ev => {
    if (![...ev.dataTransfer.types].includes("Files")) return;
    ev.preventDefault();
    el.classList.add("over");
  });
  el.addEventListener("dragleave", () => el.classList.remove("over"));
  el.addEventListener("drop", ev => {
    el.classList.remove("over");
    const file = ev.dataTransfer.files[0];
    if (!file) return;
    ev.preventDefault();
    upload(file);
  });
  return el;
}

// upload sends an image to the server, which stores it as artwork and
// answers with its id, and chooses it for the album.
async function upload(file) {
  const e = editing;
  status(`uploading ${file.name}…`);
  try {
    const resp = await fetch(session.uploadUrl.replace("{accountId}", encodeURIComponent(accountId)), {
      method: "POST", headers: { "Content-Type": file.type || "application/octet-stream" }, body: file,
    });
    const text = await resp.text();
    if (!resp.ok) throw new Error(text.trim() || `${resp.status}`);
    const r = JSON.parse(text);
    if (editing !== e) return;
    if (!e.uploads.some(u => u.blobId === r.blobId)) e.uploads.push({ blobId: r.blobId, type: r.type });
    status("");
    choose(r.blobId);
  } catch (err) {
    status("");
    alert(`Couldn't upload ${file.name}: ${err.message}`);
  }
}

function render() {
  const e = editing, a = e.album;
  const cs = candidates();
  show(
    h("h2", {}, a.title),
    h("div", { class: "sub" },
      a.artist || "Compilation", " · ", issueTags(a.issues), " · ",
      h("a", { href: `#album/${a.id}` }, "Open in Albums")),
    h("h3", {}, "Art"),
    h("div", { class: "cards" }, cs.map(card), noArtCard(), well()),
    h("h3", {}, "Tracks"),
    h("p", { class: "sub" }, "Drag a track to put it where it belongs; the numbers follow."),
    trackTable(cs),
    h("h3", {}, "MusicBrainz"),
    musicBrainz(),
    saveBar(),
  );
}

// choose makes an image the album's, for every track.  As on the
// server, that clears every track's own art, so it is offered on the
// image the album already has too, as the way to clear them all.
function choose(blobId) {
  editing.artwork = blobId;
  for (const id of editing.trackArt.keys()) editing.trackArt.set(id, null);
  render();
}

function card(c) {
  const chosen = editing.artwork === c.blobId;
  const names = c.trackIds.map(t => editing.tracks.get(t)?.title).filter(Boolean);
  return h("div", { class: "card" + (chosen ? " chosen" : "") },
    h("img", { src: imageURL(c.blobId, c.type), alt: "" }),
    h("div", { class: "label" },
      h("div", {}, h("b", {}, candidateName(c)), chosen ? " · the album's art" : null),
      names.length
        ? h("div", { title: names.join("\n") }, `In ${names.length} file${names.length === 1 ? "" : "s"}: `, names.join(", "))
        : c.folder || c.uploaded ? null : h("div", {}, "In no file here")),
    h("button", { title: "Make this the album's art, and show it for every track", onclick: () => choose(c.blobId) }, "Always use this"));
}

function noArtCard() {
  const chosen = editing.artwork == null;
  return h("div", { class: "card" + (chosen ? " chosen" : "") },
    h("div", { class: "blank" }, "∅"),
    h("div", { class: "label" }, h("div", {}, h("b", {}, "No art"), chosen ? " · the album has none" : null)),
    h("button", { title: "Give the album no art, and show none for any track", onclick: () => choose(null) }, "Always use none"));
}

function numberTag(source) {
  if (!source) return "";
  return h("span", { class: "tag" + (source === "none" ? " bad" : "") }, NUMBER_FROM[source] || source);
}

function trackTable(cs) {
  const e = editing;
  const multi = e.discs.length > 1;
  const playable = e.discs.flat().map(id => e.tracks.get(id)).filter(t => t.blobId);
  return h("table", {},
    h("thead", {}, h("tr", {},
      h("th"), h("th", { class: "num" }, "#"), h("th"), h("th", {}, "Title"),
      h("th", {}, "Number from"), h("th", {}, "In its file"), h("th", {}, "Shows"))),
    e.discs.map((ids, di) => h("tbody", {},
      multi && h("tr", { class: "group" }, h("td", { colspan: 7 }, e.album.discs[di]?.title || `Disc ${di + 1}`)),
      ids.map((id, i) => row(e.tracks.get(id), di, i, playable, cs)))));
}

function row(t, di, i, playable, cs) {
  const carried = cs.find(c => c.trackIds.includes(t.id));
  const tr = h("tr", { class: t.blobId ? "" : "gap", "data-track": t.id, draggable: "true" },
    h("td", {}, h("span", { class: "handle", title: "Drag to reorder" }, "☰")),
    h("td", { class: "num" }, i + 1),
    h("td", {}, t.blobId
      ? h("button", { class: "play", title: "Play from here", onclick: () => player.play(playable.map(p => p.id), playable.indexOf(t)) }, "▶")
      : h("span", { class: "no-audio", title: "No audio" }, "⊘")),
    h("td", { class: "title" }, t.title),
    h("td", {}, numberTag(t.numberSource)),
    h("td", {}, carried ? h("img", { class: "thumb", src: imageURL(carried.blobId, carried.type), title: candidateName(carried) }) : ""),
    h("td", {}, ownArt(t, cs)),
  );
  tr.addEventListener("dragstart", ev => { dragging = t.id; ev.dataTransfer.effectAllowed = "move"; });
  tr.addEventListener("dragover", ev => {
    if (!dragging || dragging === t.id) return;
    ev.preventDefault();
    const box = tr.getBoundingClientRect();
    const after = ev.clientY > box.top + box.height / 2;
    tr.classList.toggle("drop-before", !after);
    tr.classList.toggle("drop-after", after);
  });
  tr.addEventListener("dragleave", () => tr.classList.remove("drop-before", "drop-after"));
  tr.addEventListener("drop", ev => {
    ev.preventDefault();
    const after = tr.classList.contains("drop-after");
    tr.classList.remove("drop-before", "drop-after");
    if (dragging) move(dragging, di, i + (after ? 1 : 0));
    dragging = null;
  });
  tr.addEventListener("dragend", () => { dragging = null; });
  return tr;
}

// ownArt is the choice of what a track shows: its album's art, or one
// of the pictures.
function ownArt(t, cs) {
  const own = editing.trackArt.get(t.id);
  if (!cs.length && !own) return "";
  const options = cs.filter(c => !c.elsewhere).map(c =>
    h("option", { value: c.blobId, selected: own === c.blobId || null }, `Picture ${c.n}`));
  if (own && !cs.some(c => c.blobId === own && !c.elsewhere)) {
    options.unshift(h("option", { value: own, selected: true }, "Its own, chosen before"));
  }
  return h("select", { onchange: ev => { editing.trackArt.set(t.id, ev.target.value || null); render(); } },
    h("option", { value: "", selected: !own || null }, "The album's art"),
    options);
}

// move puts a track at index of disc di, taking it from wherever it was.
function move(id, di, index) {
  const discs = editing.discs;
  for (const d of discs) {
    const at = d.indexOf(id);
    if (at < 0) continue;
    d.splice(at, 1);
    if (d === discs[di] && at < index) index--;
  }
  discs[di].splice(index, 0, id);
  render();
}

function saveBar() {
  const dirty = isDirty();
  return h("div", { class: "savebar" },
    h("button", { disabled: !dirty || null, onclick: save }, "Save changes"),
    h("button", { disabled: !dirty || null, onclick: () => { revert(); render(); } }, "Revert"),
    h("span", { class: dirty ? "dirty" : "sub" }, dirty ? "Unsaved changes" : "Nothing to save"));
}

// save sends the album's changes as one request: the album's art and
// discs in one Album/set, then each track's own art in one Track/set,
// since choosing album art clears the tracks' own on the server as it
// did on the page.  Then the album is fetched afresh, and the copy the
// Albums tab holds is brought up to date.
async function save() {
  const e = editing, a = e.album;
  const update = {};
  const artChanged = e.artwork !== a.artworkBlobId;
  if (artChanged) update.artworkBlobId = e.artwork;
  if (discsChanged()) update.discs = e.discs.map((ids, di) => ({ trackIds: ids, title: a.discs[di]?.title ?? null }));
  const trackUpdates = {};
  for (const [id, art] of e.trackArt) {
    const onServer = artChanged ? null : e.tracks.get(id).artworkBlobId;
    if (art !== onServer) trackUpdates[id] = { artworkBlobId: art };
  }
  const calls = [];
  if (Object.keys(update).length) calls.push(["Album/set", { update: { [a.id]: update } }]);
  if (Object.keys(trackUpdates).length) calls.push(["Track/set", { update: trackUpdates }]);
  if (!calls.length) return;
  status("saving…");
  try {
    for (const r of await call(...calls)) {
      const failed = Object.values(r.notUpdated || {});
      if (failed.length) throw new Error(failed.map(f => `${f.type}${f.description ? ": " + f.description : ""}`).join("; "));
    }
    status("");
    const mb = e.mb, uploads = e.uploads;
    editing = null;
    await album(a.id);
    editing.uploads = uploads;
    if (mb?.releases) {
      editing.mb = mb;
      if (mb.release?.media) editing.mb.matches = matchTracks(mb.release);
      render();
    }
    const fresh = editing.album;
    if (albums.has(a.id)) {
      albums.set(a.id, { ...albums.get(a.id), discs: fresh.discs, artworkBlobId: fresh.artworkBlobId, artworkType: fresh.artworkType });
    }
  } catch (err) {
    status("");
    alert(`Couldn't save: ${err.message}`);
  }
}

// ---- MusicBrainz ----

// A lookup asks MusicBrainz for releases with the album's title and
// artist, straight from the browser (its API allows that), then for the
// chosen release's track list, and matches each of its tracks to one of
// the album's by title, or failing that by length.  Applying the result
// puts the album's tracks in the release's order, on its media; tracks
// it doesn't match stay on their disc, after the rest.  Nothing is saved
// until Save.
const MB = "https://musicbrainz.org/ws/2/";

async function mbFetch(path) {
  const resp = await fetch(MB + path + (path.includes("?") ? "&" : "?") + "fmt=json", { headers: { Accept: "application/json" } });
  if (!resp.ok) throw new Error(`MusicBrainz: ${resp.status}`);
  return resp.json();
}

function mbQuote(s) { return `"${s.replace(/["\\]/g, " ")}"`; }

function same(s) { return (s || "").toLocaleLowerCase().normalize("NFKD").replace(/[^\p{L}\p{N}]+/gu, ""); }

function musicBrainz() {
  const mb = editing.mb;
  if (!mb) {
    return h("p", {}, h("button", { onclick: lookup }, "Look up this album"),
      h("span", { class: "sub" }, " for the track order of a release that matches."));
  }
  if (mb.error) return h("p", { class: "error" }, mb.error);
  if (!mb.releases) return h("p", { class: "sub" }, "Asking MusicBrainz…");
  if (!mb.releases.length) return h("p", { class: "none" }, "No release found with that title and artist.");
  return [
    h("table", {},
      h("thead", {}, h("tr", {}, h("th"), h("th", {}, "Release"), h("th", {}, "Artist"), h("th", {}, "Date"), h("th", {}, "Country"), h("th", { class: "num" }, "Tracks"))),
      h("tbody", {}, mb.releases.map(r => h("tr", { class: mb.release?.id === r.id ? "playing" : "" },
        h("td", {}, h("button", { onclick: () => pick(r.id) }, mb.release?.id === r.id ? "Chosen" : "Choose")),
        h("td", {}, h("a", { href: `https://musicbrainz.org/release/${r.id}`, target: "_blank" }, r.title),
          r.disambiguation ? h("span", { class: "sub" }, ` (${r.disambiguation})`) : null),
        h("td", {}, (r["artist-credit"] || []).map(c => c.name + (c.joinphrase || "")).join("")),
        h("td", { class: "date" }, r.date || ""),
        h("td", {}, r.country || ""),
        h("td", { class: "num" }, r["track-count"] ?? ""))))),
    mb.release && !mb.release.media ? h("p", { class: "sub" }, "Fetching the track list…") : null,
    mb.release?.media ? releaseTracks(mb.release) : null,
  ];
}

async function lookup() {
  const e = editing, a = e.album;
  e.mb = {};
  render();
  try {
    let query = `release:${mbQuote(a.title)}`;
    if (a.artist) query += ` AND artist:${mbQuote(a.artist)}`;
    const r = await mbFetch(`release/?query=${encodeURIComponent(query)}&limit=8`);
    if (editing !== e) return;
    e.mb.releases = r.releases || [];
  } catch (err) {
    e.mb.error = err.message;
  }
  render();
}

async function pick(id) {
  const e = editing;
  e.mb.release = { id };
  render();
  try {
    const r = await mbFetch(`release/${id}?inc=recordings+artist-credits`);
    if (editing !== e || e.mb.release?.id !== id) return;
    e.mb.release = r;
    e.mb.matches = matchTracks(r);
  } catch (err) {
    e.mb.error = err.message;
  }
  render();
}

// matchTracks pairs each track of a release with one of the album's:
// by title first, then by length within three seconds, each album
// track to at most one.  It returns, per medium, the album track ids in
// the release's order, or null for a release track nothing matched.
function matchTracks(release) {
  const local = editing.discs.flat().map(id => editing.tracks.get(id));
  const taken = new Set();
  const claim = pred => {
    const t = local.find(t => !taken.has(t.id) && pred(t));
    if (t) taken.add(t.id);
    return t ? t.id : null;
  };
  return (release.media || []).map(m => (m.tracks || []).map(mt => {
    const title = same(mt.title || mt.recording?.title);
    const seconds = (mt.length ?? mt.recording?.length) / 1000;
    return claim(t => same(t.title) === title)
      || (seconds ? claim(t => t.duration && Math.abs(t.duration - seconds) <= 3) : null);
  }));
}

function releaseTracks(release) {
  const e = editing;
  const matched = e.mb.matches.flat().filter(Boolean).length;
  const total = e.mb.matches.flat().length;
  const order = e.discs.flat();
  const inOrder = e.mb.matches.flat().filter(Boolean).every((id, i, ids) => i === 0 || order.indexOf(ids[i - 1]) < order.indexOf(id));
  return [
    h("p", { class: "sub" },
      `${matched} of ${total} release tracks matched`, inOrder && matched ? ", and the album already has them in that order" : "", ". ",
      h("button", { disabled: !matched || null, onclick: applyOrder }, "Put the album in this order")),
    h("table", {},
      h("thead", {}, h("tr", {}, h("th", { class: "num" }, "#"), h("th", {}, "Release track"), h("th", { class: "num" }, "Length"), h("th", {}, "Matches"))),
      release.media.map((m, mi) => h("tbody", {},
        release.media.length > 1 && h("tr", { class: "group" }, h("td", { colspan: 4 }, m.title || `${m.format || "Medium"} ${mi + 1}`)),
        (m.tracks || []).map((mt, ti) => {
          const id = e.mb.matches[mi][ti];
          const t = id && e.tracks.get(id);
          const seconds = (mt.length ?? mt.recording?.length) / 1000;
          return h("tr", {},
            h("td", { class: "num" }, mt.number || mt.position),
            h("td", {}, mt.title || mt.recording?.title),
            h("td", { class: "num" }, seconds ? `${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, "0")}` : ""),
            h("td", {}, t ? t.title : h("span", { class: "none" }, "nothing here")));
        })))),
  ];
}

// applyOrder arranges the discs as the release has its media, matched
// tracks first in its order, then whatever was on each disc and matched
// nothing, in the order it was.
function applyOrder() {
  const e = editing;
  const placed = new Set(e.mb.matches.flat().filter(Boolean));
  const discs = e.mb.matches.map(ids => ids.filter(Boolean));
  e.discs.forEach((ids, di) => {
    const rest = ids.filter(id => !placed.has(id));
    while (discs.length <= di) discs.push([]);
    discs[Math.min(di, discs.length - 1)].push(...rest);
  });
  while (discs.length > 1 && !discs[discs.length - 1].length) discs.pop();
  e.discs = discs;
  render();
}

return { list, album, leaving };
})();

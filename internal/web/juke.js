"use strict";

const CAP_CORE = "urn:ietf:params:jmap:core";
const CAP_JUKE = "https://semiotic.systems/jmap/juke";
const CAP_REVIEW = "https://semiotic.systems/jmap/juke/review";

let session, accountId, maxGet;
const albums = new Map();       // id -> Album
let albumList = [];             // albums, by artist then title
let artistList = [];            // album artists, by name, each with their albums
const tracks = new Map();       // id -> Track, filled as needed

// ---- DOM ----

// h builds an element.  attrs may hold event handlers (onclick and so
// on), "class", or any attribute; children are nodes, strings, or arrays
// of either, and null or false are skipped.
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "class") el.className = v;
    else el.setAttribute(k, v);
  }
  const add = c => {
    if (c == null || c === false) return;
    if (Array.isArray(c)) c.forEach(add);
    else el.append(c instanceof Node ? c : String(c));
  };
  children.forEach(add);
  return el;
}

const main = document.getElementById("main");
function show(...nodes) { main.replaceChildren(...nodes.flat(Infinity).filter(n => n != null && n !== false)); }
function status(text) { document.getElementById("status").textContent = text || ""; }

function fold(s) { return (s || "").toLocaleLowerCase(); }

// An album's release date for reading: "1991-02-00" is "1991-02", and
// a year alone is the year.
function releasedText(released) { return released.replace(/(-00)+$/, ""); }
function plural(n, noun) { return `${n} ${noun}${n === 1 ? "" : "s"}`; }
function day(date) { return date ? date.slice(0, 10) : ""; }
function orNone(s, none) { return s ? s : h("span", { class: "none" }, none); }

// ---- JMAP ----

// jmap makes one request of several calls, each [method, args]; an
// argument given as ["#", index, name, path] becomes a result reference
// to the earlier call at that index, sent as "#" plus the argument name.  It returns the responses' arguments, and
// throws on any method error.  The review tab's calls add its capability
// with jmapUsing; the rest of the page does without, so the albums it
// loads at the start aren't burdened with review properties.
function jmap(...calls) { return jmapUsing([CAP_CORE, CAP_JUKE], ...calls); }

async function jmapUsing(using, ...calls) {
  const methodCalls = calls.map(([name, args], i) => {
    const a = { accountId, ...args };
    for (const [k, v] of Object.entries(a)) {
      if (Array.isArray(v) && v[0] === "#") {
        delete a[k];
        a[`#${k}`] = { resultOf: String(v[1]), name: v[2], path: v[3] };
      }
    }
    return [name, a, String(i)];
  });
  const resp = await fetch(session.apiUrl, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ using, methodCalls }),
  });
  const body = await resp.json();
  if (!resp.ok) throw new Error(`${resp.status}: ${body.detail || body.type || JSON.stringify(body)}`);
  return body.methodResponses.map(([name, args]) => {
    if (name === "error") throw new Error(`${args.type}${args.description ? ": " + args.description : ""}`);
    return args;
  });
}

// getAll fetches every object of a type, paging through its query, since
// a library has more tracks than one get may return.
async function getAll(type) {
  const out = [];
  const limit = Math.min(maxGet, 2000);
  for (let position = 0; ; position += limit) {
    const [q, g] = await jmap(
      [`${type}/query`, { sort: [{ property: "title" }], position, limit, calculateTotal: true }],
      [`${type}/get`, { ids: ["#", 0, `${type}/query`, "/ids"] }],
    );
    out.push(...g.list);
    status(`loading ${type.toLowerCase()}s: ${out.length} of ${q.total}`);
    if (q.ids.length < limit) break;
  }
  return out;
}

// loadTracks makes sure the given tracks are in the cache.
async function loadTracks(ids) {
  const missing = [...new Set(ids)].filter(id => !tracks.has(id));
  for (let i = 0; i < missing.length; i += maxGet) {
    const [g] = await jmap(["Track/get", { ids: missing.slice(i, i + maxGet) }]);
    g.list.forEach(t => tracks.set(t.id, t));
  }
}

async function updateTrack(id, patch) {
  const [r] = await jmap(["Track/set", { update: { [id]: patch } }]);
  if (r.notUpdated && r.notUpdated[id]) {
    const e = r.notUpdated[id];
    throw new Error(`${e.type}${e.description ? ": " + e.description : ""}`);
  }
  Object.assign(tracks.get(id), patch);
}

function audioURL(t) { return blobURL(t.blobId, t.title || "track", ""); }
function imageURL(blobId, type) { return blobURL(blobId, "art", type || ""); }

function blobURL(blobId, name, type) {
  return session.downloadUrl
    .replace("{accountId}", encodeURIComponent(accountId))
    .replace("{blobId}", encodeURIComponent(blobId))
    .replace("{name}", encodeURIComponent(name))
    .replace("{type}", encodeURIComponent(type));
}

// ---- Rating ----

// stars is a five-star rating control.  Clicking the left half of a star
// gives a half star, except the first, since ratings start at one;
// clicking the current rating clears it.
function stars(t) {
  const el = h("span", { class: "stars", title: t.rating ? `${t.rating} stars` : "unrated" });
  const paint = r => el.replaceChildren(...[1, 2, 3, 4, 5].map(n =>
    h("span", { class: r >= n ? "on" : r >= n - 0.5 ? "half" : "" }, "★")));
  paint(t.rating || 0);
  el.addEventListener("click", async ev => {
    const star = ev.target.closest("span:not(.stars)");
    if (!star) return;
    const n = [...el.children].indexOf(star) + 1;
    const box = star.getBoundingClientRect();
    let r = Math.max(1, ev.clientX < box.left + box.width / 2 ? n - 0.5 : n);
    if (r === t.rating) r = null;
    const old = t.rating;
    paint(r || 0);
    try {
      await updateTrack(t.id, { rating: r });
      el.title = r ? `${r} stars` : "unrated";
    } catch (e) {
      paint(old || 0);
      alert(`Couldn't rate "${t.title}": ${e.message}`);
    }
  });
  return el;
}

// ---- Track tables ----

// trackTable shows tracks in order.  Clicking a playable track's button
// plays the list from there.  cols picks optional columns; cols.start
// is the number of the first row, when the list is one page of a longer
// one.
function trackTable(list, cols = {}) {
  return groupedTrackTable([{ tracks: list }], cols);
}

// groupedTrackTable shows titled groups of tracks, such as an album's
// discs, as one table, so their columns line up; a group's title is a
// row across it, as is its header, a node such as an album's art and
// name, and numbering starts over in each group.  Playing from a track
// plays on through the groups that follow.
function groupedTrackTable(groups, cols = {}) {
  const playable = groups.flatMap(g => g.tracks).filter(t => t.blobId);
  const columns = 6 + !!cols.number + !!cols.album + !!cols.added;
  return h("table", {},
    h("thead", {}, h("tr", {},
      cols.number && h("th", { class: "num" }, "#"),
      h("th", {}, ""),
      h("th", {}, "Title"),
      h("th", {}, "Artist"),
      cols.album && h("th", {}, "Album"),
      h("th", {}, "Rating"),
      h("th", { class: "num" }, "Plays"),
      h("th", { class: "hide-narrow" }, "Last played"),
      cols.added && h("th", { class: "hide-narrow" }, "Added"),
    )),
    groups.map(g => h("tbody", {},
      g.header && h("tr", { class: "header" }, h("td", { colspan: columns }, g.header)),
      g.title && h("tr", { class: "group" }, h("td", { colspan: columns }, g.title)),
      g.tracks.map((t, i) => {
      const album = t.albumId && albums.get(t.albumId);
      return h("tr", { class: [t.blobId ? "" : "gap", player.current() === t.id ? "playing" : ""].join(" "), "data-track": t.id },
        cols.number && h("td", { class: "num" }, (cols.start || 0) + i + 1),
        h("td", {}, t.blobId
          ? h("button", {
            class: "play", title: "Play from here",
            onclick: () => player.play(playable.map(p => p.id), playable.indexOf(t)),
          }, "▶")
          : h("span", { class: "no-audio", title: "No audio" }, "⊘")),
        h("td", { class: "title" }, t.title),
        h("td", {}, orNone(t.artist, "no artist")),
        cols.album && h("td", {}, album
          ? h("a", { href: `#album/${album.id}` }, album.title)
          : h("span", { class: "none" }, "no album")),
        h("td", {}, stars(t)),
        h("td", { class: "num plays" }, t.playCount || ""),
        h("td", { class: "date last-played hide-narrow" }, day(t.lastPlayed)),
        cols.added && h("td", { class: "date hide-narrow" }, day(t.dateAdded)),
      );
      }))),
  );
}

// showPlays updates the play count and last-played date in every row
// showing the given track, after a play has been recorded.
function showPlays(t) {
  document.querySelectorAll(`tr[data-track="${t.id}"]`).forEach(tr => {
    tr.querySelector("td.plays").textContent = t.playCount || "";
    tr.querySelector("td.last-played").textContent = day(t.lastPlayed);
  });
}

// ---- Views ----

const albumsView = { q: "", which: "all" };

function renderAlbums() {
  const list = h("tbody");
  const count = h("span", { class: "sub" });
  const fill = () => {
    const words = fold(albumsView.q).split(/\s+/).filter(Boolean);
    const shown = albumList.filter(a => {
      if (albumsView.which === "compilation" && a.artist) return false;
      if (albumsView.which === "multidisc" && a.discs.length < 2) return false;
      const hay = fold(`${a.artist || ""} ${a.title}`);
      return words.every(w => hay.includes(w));
    });
    count.textContent = `${shown.length} of ${albumList.length} albums`;
    list.replaceChildren(...shown.map(a => h("tr", {},
      h("td", {}, a.artist ? h("a", { href: artistHash(a.artist) }, a.artist) : h("span", { class: "none" }, "compilation")),
      h("td", {}, h("a", { href: `#album/${a.id}` }, a.title)),
      h("td", { class: "num" }, a.discs.length > 1 ? `${a.discs.length} discs` : ""),
      h("td", { class: "num" }, a.discs.reduce((n, d) => n + d.trackIds.length, 0)),
    )));
  };
  const search = h("input", {
    type: "search", placeholder: "Filter by artist or title", value: albumsView.q,
    oninput: ev => { albumsView.q = ev.target.value; fill(); },
  });
  const which = h("select", { onchange: ev => { albumsView.which = ev.target.value; fill(); } },
    [["all", "All albums"], ["compilation", "No artist"], ["multidisc", "More than one disc"]]
      .map(([v, label]) => h("option", { value: v, selected: albumsView.which === v || null }, label)));
  fill();
  show(
    h("div", { class: "controls" }, search, which, count),
    h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Artist"), h("th", {}, "Title"), h("th"), h("th", { class: "num" }, "Tracks"))),
      list),
  );
  search.focus();
}

async function renderAlbum(id) {
  const a = albums.get(id);
  if (!a) return show(h("p", { class: "error" }, `No album ${id}.`));
  await loadTracks(a.discs.flatMap(d => d.trackIds));
  const all = a.discs.flatMap(d => d.trackIds.map(t => tracks.get(t)));
  const gaps = all.filter(t => !t.blobId).length;
  const playable = all.filter(t => t.blobId);
  show(
    h("h2", {}, a.title),
    h("div", { class: "sub" },
      a.artist || "Compilation", a.released ? ` · ${releasedText(a.released)}` : "", " · ",
      `${all.length} tracks`, gaps ? `, ${gaps} missing` : "", " · ",
      h("button", { onclick: () => player.play(playable.map(t => t.id), 0), disabled: !playable.length || null }, "Play album")),
    groupedTrackTable(a.discs.map((d, i) => ({
      title: a.discs.length > 1 ? d.title || `Disc ${i + 1}` : null,
      tracks: d.trackIds.map(t => tracks.get(t)),
    })), { number: true }),
  );
}

// ---- Artists ----

// An artist is an album artist, with their albums; compilations, having
// no artist, belong to no one.  Artists have no id, so a page's hash
// carries the name, encoded so a slash in it survives the routing.

function artistHash(name) { return `#artist/${encodeURIComponent(name)}`; }

// artistKey is a name as it sorts: folded, with a leading "The", "A" or
// "An" dropped, so The Beatles sort under B.
function artistKey(name) { return fold(name).trim().replace(/^(the|an?) (?=\S)/, ""); }

// artistsOf groups albums by album artist, in name order, counting
// each artist's tracks.
function artistsOf(list) {
  const byName = new Map();
  for (const a of list) {
    if (!a.artist) continue;
    if (!byName.has(a.artist)) byName.set(a.artist, { name: a.artist, albums: [], tracks: 0 });
    const artist = byName.get(a.artist);
    artist.albums.push(a);
    artist.tracks += a.discs.reduce((n, d) => n + d.trackIds.length, 0);
  }
  return [...byName.values()].sort((x, y) =>
    artistKey(x.name).localeCompare(artistKey(y.name)) || x.name.localeCompare(y.name));
}

const ALBUM_ORDERS = [
  ["oldest", "Oldest first"], ["newest", "Newest first"], ["title", "Title, A to Z"], ["title-desc", "Title, Z to A"],
];

// albumsInOrder sorts an artist's albums: by date with the undated
// last whichever way the dates run, ties and the undated by title.
function albumsInOrder(list, order) {
  const byTitle = (a, b) => fold(a.title).localeCompare(fold(b.title));
  const byDate = dir => (a, b) =>
    (a.released == null) - (b.released == null)
    || (a.released && b.released ? dir * a.released.localeCompare(b.released) : 0)
    || byTitle(a, b);
  const compare = { oldest: byDate(1), newest: byDate(-1), title: byTitle, "title-desc": (a, b) => byTitle(b, a) }[order] || byDate(1);
  return [...list].sort(compare);
}

function shuffled(list) {
  const out = [...list];
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

const artistsView = { q: "" };

function renderArtists() {
  const list = h("tbody");
  const count = h("span", { class: "sub" });
  const fill = () => {
    const words = fold(artistsView.q).split(/\s+/).filter(Boolean);
    const shown = artistList.filter(a => words.every(w => fold(a.name).includes(w)));
    count.textContent = `${shown.length} of ${artistList.length} artists`;
    list.replaceChildren(...shown.map(a => h("tr", {},
      h("td", {}, h("a", { href: artistHash(a.name) }, a.name)),
      h("td", { class: "num" }, a.albums.length),
      h("td", { class: "num" }, a.tracks),
    )));
  };
  const search = h("input", {
    type: "search", placeholder: "Filter by name", value: artistsView.q,
    oninput: ev => { artistsView.q = ev.target.value; fill(); },
  });
  fill();
  show(
    h("div", { class: "controls" }, search, count),
    h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Artist"), h("th", { class: "num" }, "Albums"), h("th", { class: "num" }, "Tracks"))),
      list),
  );
  search.focus();
}

// renderArtist shows an artist's albums whole, each under a header with
// its art, in an order kept across visits, as one table so the columns
// line up from album to album.  Playing a track plays on through the
// rest of the albums as shown.
async function renderArtist(name) {
  const artist = artistList.find(a => a.name === name);
  if (!artist) return show(h("p", { class: "error" }, `No artist named ${name}.`));
  await loadTracks(artist.albums.flatMap(a => a.discs.flatMap(d => d.trackIds)));
  let order = localStorage.getItem("artistAlbumOrder") || "oldest";
  let ordered = [], queue = [];
  const tracksOf = a => a.discs.flatMap(d => d.trackIds.map(t => tracks.get(t)));
  const body = h("div");
  const albumHeader = a => {
    const its = tracksOf(a);
    const first = its.find(t => t.blobId);
    return h("div", { class: "album-head" },
      a.artworkBlobId
        ? h("img", { src: imageURL(a.artworkBlobId, a.artworkType), alt: "", loading: "lazy" })
        : h("div", { class: "no-art" }, "♪"),
      h("div", {},
        h("div", { class: "title" }, h("a", { href: `#album/${a.id}` }, a.title)),
        h("div", { class: "sub" },
          a.released ? `${releasedText(a.released)} · ` : "",
          plural(its.length, "track"), " · ",
          h("button", { onclick: () => player.play(queue, queue.indexOf(first.id)), disabled: !first || null }, "Play album"))));
  };
  const fill = () => {
    ordered = albumsInOrder(artist.albums, order);
    queue = ordered.flatMap(tracksOf).filter(t => t.blobId).map(t => t.id);
    body.replaceChildren(groupedTrackTable(ordered.flatMap(a => a.discs.map((d, i) => ({
      header: i === 0 ? albumHeader(a) : null,
      title: a.discs.length > 1 ? d.title || `Disc ${i + 1}` : null,
      tracks: d.trackIds.map(t => tracks.get(t)),
    }))), { number: true }));
  };
  const select = h("select", { onchange: ev => { order = ev.target.value; localStorage.setItem("artistAlbumOrder", order); fill(); } },
    ALBUM_ORDERS.map(([v, label]) => h("option", { value: v, selected: order === v || null }, label)));
  fill();
  show(
    h("h2", {}, artist.name),
    h("div", { class: "sub" },
      plural(artist.albums.length, "album"), ", ", plural(artist.tracks, "track")),
    h("div", { class: "controls" },
      select,
      h("button", { onclick: () => player.play(queue, 0), disabled: !queue.length || null }, "Play all"),
      h("button", {
        onclick: () => { const ids = shuffled(ordered).flatMap(tracksOf).filter(t => t.blobId).map(t => t.id); player.play(ids, 0); },
        disabled: !queue.length || null,
      }, "Play albums shuffled")),
    body,
  );
}

// The track view's state lives in the URL, so the back button and
// reloading keep the query.
const SORTS = [
  ["title", "Title"], ["artist", "Artist"], ["albumTitle", "Album"],
  ["rating", "Rating"], ["playCount", "Plays"], ["lastPlayed", "Last played"],
];
const PAGE = 100;

// pager steps through total rows, PAGE at a time, from pos; go is called
// with the position to move to.
function pager(pos, total, go, noun = "tracks") {
  const last = Math.min(pos + PAGE, total);
  return h("div", { class: "pager" },
    h("button", { disabled: pos === 0 || null, onclick: () => go(Math.max(0, pos - PAGE)) }, "← Previous"),
    total ? `${pos + 1}–${last} of ${total}` : `No ${noun}`,
    h("button", { disabled: last >= total || null, onclick: () => go(pos + PAGE) }, "Next →"));
}

// posParam reads the page position from the URL's query.
function posParam() {
  return Number(new URLSearchParams(location.hash.split("?")[1] || "").get("pos")) || 0;
}

function tracksParams() {
  const p = new URLSearchParams(location.hash.split("?")[1] || "");
  return {
    q: p.get("q") || "",
    field: p.get("field") || "any",
    audio: p.get("audio") || "any",
    rating: p.get("rating") || "",
    sort: p.get("sort") || "artist",
    desc: p.get("desc") === "1",
    pos: posParam(),
  };
}

function setTracksParams(p) {
  const out = new URLSearchParams();
  for (const [k, v] of Object.entries(p)) {
    if (v === "" || v === false || v === 0) continue;
    out.set(k, v === true ? "1" : v);
  }
  location.hash = `tracks?${out}`;
}

function trackFilter(p) {
  const conds = [];
  if (p.q) {
    const fields = { title: ["titleContains"], artist: ["artistContains"], album: ["albumTitleContains"] };
    const names = fields[p.field] || ["titleContains", "artistContains", "albumTitleContains"];
    conds.push(names.length === 1
      ? { [names[0]]: p.q }
      : { operator: "OR", conditions: names.map(n => ({ [n]: p.q })) });
  }
  if (p.audio === "yes") conds.push({ hasAudio: true });
  if (p.audio === "no") conds.push({ hasAudio: false });
  if (p.rating === "none") conds.push({ hasRating: false });
  else if (p.rating) conds.push({ ratingAtLeast: Number(p.rating) });
  if (!conds.length) return null;
  return conds.length === 1 ? conds[0] : { operator: "AND", conditions: conds };
}

async function renderTracks() {
  const p = tracksParams();
  const sort = [{ property: p.sort, isAscending: !p.desc }];
  if (p.sort === "artist") sort.push({ property: "albumOrder" });
  const [q, g] = await jmap(
    ["Track/query", { filter: trackFilter(p), sort, position: p.pos, limit: PAGE, calculateTotal: true }],
    ["Track/get", { ids: ["#", 0, "Track/query", "/ids"] }],
  );
  g.list.forEach(t => tracks.set(t.id, t));
  const list = q.ids.map(id => tracks.get(id));

  const go = change => setTracksParams({ ...p, pos: 0, ...change });
  const select = (key, options) => h("select", { onchange: ev => go({ [key]: ev.target.value }) },
    options.map(([v, label]) => h("option", { value: v, selected: p[key] === v || null }, label)));
  const search = h("input", { type: "search", placeholder: "Search", value: p.q });
  search.addEventListener("keydown", ev => { if (ev.key === "Enter") go({ q: search.value }); });
  search.addEventListener("search", () => go({ q: search.value }));

  const pages = () => pager(p.pos, q.total, pos => go({ pos }));

  show(
    h("div", { class: "controls" },
      search,
      select("field", [["any", "in any field"], ["title", "in title"], ["artist", "in artist"], ["album", "in album"]]),
      select("audio", [["any", "With or without audio"], ["yes", "With audio"], ["no", "Missing audio"]]),
      select("rating", [["", "Any rating"], ["none", "Unrated"], ...[1, 2, 3, 4, 5].map(n => [String(n), `${n}★ or more`])]),
      "Sort by", select("sort", SORTS),
      h("button", { onclick: () => go({ desc: !p.desc }) }, p.desc ? "↓ Descending" : "↑ Ascending"),
    ),
    pages(),
    trackTable(list, { album: true, added: true }),
    pages(),
  );
  search.focus();
}

function gear() { return h("span", { class: "gear", title: "Smart playlist" }, "⚙"); }

// The Playlists tab lists playlists of both kinds together, by title; a
// smart playlist wears a gear.  Helpers come after the rest, in a section
// that stays as this browser last left it, closed at first.
async function renderPlaylists() {
  const props = ["id", "title", "isHelper"];
  const [p, s] = await jmap(["Playlist/get", { ids: null, properties: props }],
                            ["SmartPlaylist/get", { ids: null, properties: props }]);
  const rows = [
    ...p.list.map(pl => ({ pl, href: `#playlist/${pl.id}` })),
    ...s.list.map(pl => ({ pl, href: `#smart/${pl.id}`, smart: true })),
  ].sort((a, b) => fold(a.pl.title).localeCompare(fold(b.pl.title)));
  const table = rows => h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, "Title"))),
    h("tbody", {}, rows.map(r => h("tr", {},
      h("td", {}, r.smart && gear(), h("a", { href: r.href }, r.pl.title))))));
  const helpers = rows.filter(r => r.pl.isHelper);
  show(
    h("div", { class: "controls" }, h("a", { href: "#smart/new" }, h("button", {}, "New smart playlist"))),
    table(rows.filter(r => !r.pl.isHelper)),
    helpers.length > 0 && h("details", {
      class: "helpers",
      open: localStorage.getItem("helpersOpen") === "yes",
      ontoggle: ev => localStorage.setItem("helpersOpen", ev.target.open ? "yes" : "no"),
    }, h("summary", {}, `Helpers (${helpers.length})`), table(helpers)));
}

// helperBox is the checkbox that marks a playlist of either kind as a
// helper, saved as soon as it's clicked.
function helperBox(typ, pl) {
  return h("label", { class: "helper", title: "Kept for smart playlists' rules to use, and listed apart" },
    h("input", { type: "checkbox", checked: pl.isHelper, onchange: async ev => {
      const want = ev.target.checked;
      const [r] = await jmap([`${typ}/set`, { update: { [pl.id]: { isHelper: want } } }]);
      if (r.notUpdated) {
        ev.target.checked = !want;
        alert(`Couldn't save: ${Object.values(r.notUpdated).map(f => f.type).join("; ")}`);
      } else pl.isHelper = want;
    } }),
    " Helper");
}

// showRule opens a dialog showing what a smart playlist stores: the
// Track/query it stands for, as JSON.
function showRule(pl) {
  const { filter, sort, limit, limitBytes, limitSeconds } = pl;
  const dialog = h("dialog", { onclick: ev => { if (ev.target === dialog) dialog.close(); } },
    h("h2", {}, gear(), pl.title),
    h("pre", {}, JSON.stringify({ filter, sort, limit, limitBytes, limitSeconds }, null, 2)),
    h("button", { onclick: () => dialog.close() }, "Close"));
  dialog.addEventListener("close", () => dialog.remove());
  document.body.append(dialog);
  dialog.showModal();
}

// A smart playlist's page runs its query, a page at a time, in the
// order the rule says.  Play plays the page shown.
async function renderSmart(id) {
  const pos = posParam();
  const [sp] = await jmap(["SmartPlaylist/get", { ids: [id] }]);
  const pl = sp.list[0];
  if (!pl) return show(h("p", { class: "error" }, `No smart playlist ${id}.`));
  const [q, g] = await jmap(
    ["Track/query", { filter: { inSmartPlaylist: id }, sort: pl.sort, position: pos, limit: PAGE, calculateTotal: true }],
    ["Track/get", { ids: ["#", 0, "Track/query", "/ids"] }],
  );
  g.list.forEach(t => tracks.set(t.id, t));
  const list = q.ids.map(tid => tracks.get(tid));
  const playable = list.filter(t => t.blobId);
  const go = pos => { location.hash = `smart/${id}` + (pos ? `?pos=${pos}` : ""); };
  show(
    h("h2", {}, gear(), pl.title),
    h("div", { class: "sub" }, `${q.total} tracks · `,
      h("button", { onclick: () => player.play(playable.map(t => t.id), 0), disabled: !playable.length || null }, "Play"),
      " · ", h("button", { onclick: () => smart.edit(pl) }, "Edit rule"),
      " · ", h("button", { onclick: () => smart.duplicate(pl) }, "Duplicate"),
      " · ", h("button", { onclick: () => showRule(pl) }, "Show rule")),
    pager(pos, q.total, go),
    trackTable(list, { number: true, start: pos, album: true }),
    pager(pos, q.total, go),
  );
}

async function renderPlaylist(id) {
  const [g] = await jmap(["Playlist/get", { ids: [id] }]);
  const pl = g.list[0];
  if (!pl) return show(h("p", { class: "error" }, `No playlist ${id}.`));
  await loadTracks(pl.trackIds);
  const list = pl.trackIds.map(t => tracks.get(t));
  const playable = list.filter(t => t.blobId);
  show(
    h("h2", {}, pl.title),
    h("div", { class: "sub" }, `${list.length} tracks · `,
      h("button", { onclick: () => player.play(playable.map(t => t.id), 0), disabled: !playable.length || null }, "Play"),
      " · ", helperBox("Playlist", pl)),
    trackTable(list, { number: true, album: true }),
  );
}

// ---- Player ----

// The player plays a queue of track ids.  A track that plays to its end
// counts as played: its play count goes up and its last-played date is
// set, on the server and then in any rows showing it.
const player = (() => {
  const audio = document.getElementById("audio");
  let queue = [], index = -1;

  function current() { return queue[index]; }

  function mark() {
    document.querySelectorAll("tr.playing").forEach(tr => tr.classList.remove("playing"));
    const id = current();
    if (id) document.querySelectorAll(`tr[data-track="${id}"]`).forEach(tr => tr.classList.add("playing"));
  }

  function load() {
    const t = tracks.get(current());
    if (!t) {
      audio.removeAttribute("src");
      document.getElementById("now-what").textContent = "Nothing playing";
      document.getElementById("now-where").textContent = "";
      return mark();
    }
    const album = t.albumId && albums.get(t.albumId);
    document.getElementById("now-what").textContent = `${t.title}${t.artist ? " — " + t.artist : ""}`;
    document.getElementById("now-where").textContent = album
      ? `${album.title}${album.artist && album.artist !== t.artist ? " · " + album.artist : ""}`
      : "";
    document.title = `${t.title} · Juke`;
    audio.src = audioURL(t);
    audio.play().catch(e => status(`can't play: ${e.message}`));
    mark();
  }

  function play(ids, start) { queue = ids; index = start; load(); }
  function step(by) {
    if (index + by < 0 || index + by >= queue.length) return;
    index += by;
    load();
  }

  audio.addEventListener("ended", async () => {
    const t = tracks.get(current());
    if (t) {
      try {
        await updateTrack(t.id, { playCount: (t.playCount || 0) + 1, lastPlayed: new Date().toISOString().replace(/\.\d+Z$/, "Z") });
        showPlays(t);
      } catch (e) {
        status(`couldn't record the play: ${e.message}`);
      }
    }
    step(1);
  });
  audio.addEventListener("error", () => { if (audio.src) status("audio failed to load"); });
  document.getElementById("prev").addEventListener("click", () => step(-1));
  document.getElementById("next").addEventListener("click", () => step(1));

  return { play, current, mark };
})();

// ---- Routing ----

async function route() {
  const hash = location.hash.slice(1) || "albums";
  const [path] = hash.split("?");
  const [view, id] = path.split("/");
  const tab = { album: "albums", artist: "artists", playlist: "playlists", smart: "playlists" }[view] || view;
  if (!review.leaving() || !smart.leaving()) return;
  document.querySelectorAll("nav a").forEach(a => a.classList.toggle("current", a.dataset.tab === tab));
  try {
    switch (view) {
      case "albums": renderAlbums(); break;
      case "album": await renderAlbum(id); break;
      case "artists": renderArtists(); break;
      case "artist": await renderArtist(decodeURIComponent(id)); break;
      case "tracks": await renderTracks(); break;
      case "playlists": await renderPlaylists(); break;
      case "playlist": await renderPlaylist(id); break;
      case "smart": await smart.page(id); break;
      case "review": await (id ? review.album(id) : review.list()); break;
      default: show(h("p", { class: "error" }, `Nothing at #${hash}.`));
    }
    player.mark();
  } catch (e) {
    show(h("p", { class: "error" }, e.message));
  }
}

async function start() {
  try {
    session = await (await fetch("/.well-known/jmap")).json();
    accountId = session.primaryAccounts[CAP_JUKE];
    maxGet = session.capabilities[CAP_CORE].maxObjectsInGet;
    for (const a of await getAll("Album")) albums.set(a.id, a);
    albumList = [...albums.values()].sort((a, b) =>
      (a.artist == null) - (b.artist == null)
      || fold(a.artist).localeCompare(fold(b.artist))
      || fold(a.title).localeCompare(fold(b.title)));
    artistList = artistsOf(albumList);
    status("");
  } catch (e) {
    return show(h("p", { class: "error" }, `Couldn't reach the server: ${e.message}`));
  }
  window.addEventListener("hashchange", route);
  route();
}

start();

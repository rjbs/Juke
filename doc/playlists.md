# Playlists

A smart playlist is a saved search: a stored `Track/query`, filter and
sort and limits, with a title.  Its rule language is exactly whatever
`Track/query` can filter on, so there's no separate rule engine to build.
Nothing on the server computes its tracks ahead of time.  A client runs
the query when it wants the list, and the server answers from the live
library.

- **Two types.**  A plain playlist is a `Playlist`: an explicit list of
  track ids the client edits.  A smart playlist is a `SmartPlaylist`,
  which has no track list at all.  Keeping them apart means each type's
  properties are client-set or server-set as a type, which is how JMAP
  and the sync engine expect it; an edit to a plain playlist that changes
  what a smart one matches is a cross-type side effect, the kind the
  protocol already handles; and only smart playlists can reference other
  playlists, so plain ones are leaves of that graph.
- **A saved search, not a materialized list.**  An earlier design cached
  each smart playlist's tracks on the server and reported a change only
  when the list really changed.  Nothing needed that.  The client's sync
  engine already keeps a named query subscription current while online
  and stores its last result, which is exactly a smart playlist; and the
  play queue is a snapshot of tracks, so a refreshed list can never move
  the current track.  Dropping the cache drops the recompute on every
  play, the change-reporting problem, and the same-type side effects a
  computed list would have had.
- **Server-side evaluation, still.**  Every client gets its list from
  `Track/query`, so all of them agree, and nobody reimplements
  date-relative rules or sort-and-limit per platform.  Evaluating the
  definition against the client's local mirror would help only offline,
  and is deliberately not done; see below for what offline gets instead.
- **Deterministic results.**  Sorts break ties by track id.  Random order
  is a `Track/query` sort by a hash of the track id and a seed held in
  the comparator, so the selection is stable and changes only where its
  inputs change.  Reshuffling is an edit that sets a new seed.
- **Time-relative rules** like "played in the last 30 days" are a plain
  comparison against the moment of evaluation, measured in seconds.  There
  is no day boundary, so there is no question of whose midnight.
- **Caps.**  A smart playlist can be limited by count, by total size, or
  by total duration, which are what "the most played 25", "500 MB of the
  good stuff" and "a two-hour drive" need.  A cap is applied after the
  sort, cutting the list where the running total would pass it.
- **Gap tracks are not excluded by magic.**  A rule that should leave out
  tracks with no audio says `hasAudio: true`.  A client that edits rules
  can default that on, but the server adds nothing to a rule.
- **References.**  A rule may draw on a playlist of either kind, with
  `inPlaylist` and `inSmartPlaylist`; a referenced smart playlist's rule
  is compiled inline as a subquery.  `SmartPlaylist/set` compiles a rule
  before storing it, and refuses one that won't run, that names a
  playlist that doesn't exist, or that would close a loop.  Destroying a
  playlist of either kind that a rule names fails with the SetError
  `playlistInUse`.  So a stored rule always runs, and the query compiler
  never meets a dangling reference.
- **Big results** are paged through `Track/query` like any other, so
  playing a smart playlist larger than one page means fetching every page
  before enqueueing.

## What a client does

- **Subscribes to every smart playlist's query**, as a named subscription
  with that playlist's filter, sort and limits.  The engine re-runs each
  one when the Track state moves, which is after every play, so the
  lists are always current while online.  The server's cost is one
  id-only query per smart playlist per sync.
- **Offline, the last result stands.**  Every smart playlist can be
  viewed offline at its last-known contents, because the subscription
  stores them.
- **Keeping a smart playlist available offline** means its subscription's
  current ids feed the set of audio to pin.  When the list changes, the
  tracks that entered are fetched and the tracks that left are released,
  which is already how a plain playlist behaves; released audio stays in
  the cache until space is needed.
- **Refreshing a visible list** is the client's choice.  A sorted list
  can update in place.  A random, capped list is better held until the
  view is reopened, since it is the one that visibly shuffles.
- **Sync order** is Track, then Playlist, then SmartPlaylist: a type syncs
  after everything it is computed from, so a client never shows a smart
  playlist evaluated against tracks it hasn't received.

## The 2014 smart playlists

The 2014 library's smart playlists survive as binary blobs in the
catalog, and `bin/decode-smart-playlists` prints them as the rules they
were.  They are not imported; the ones worth having are recreated by
hand, in `bin/recreate-smart-playlists`, which pushes its rules to a
server over JMAP and can be re-run as they change.  Almost all of them hang off one list, "Regular Music", which is
eighteen "genre is not" rules, so genre on Track came first.  Their
"checked only" flag has no counterpart here and is dropped.

## Playlists of albums

A smart playlist is a list of tracks, and nothing here builds a list of
albums.  The 2014 library didn't either: a program drove iTunes to fill
a plain playlist, "Albumen", with whole albums from "Regularer" that
hadn't been played in a while, least recently played first, one album
per artist, up to a gigabyte.  `bin/albumen` is that program, driving
Juke over JMAP instead; it rewrites the plain playlist each time it is
run, so a client that keeps "Albumen" offline follows it.

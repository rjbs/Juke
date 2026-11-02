# Playlists

A smart playlist is a reified `Track/query`: a stored filter and sort,
evaluated on the server whenever it's needed.  Its rule language is exactly
whatever `Track/query` can filter on, so there's no separate rule engine to
build.

- **Plain playlists** are the degenerate case: an explicit list of track ids
  instead of a filter.
- **Server-side evaluation** keeps every client in agreement without
  reimplementing date-relative rules and sort-and-limit per platform.
  Client-side evaluation would only help offline, which isn't worth the cost.
- **Lazy evaluation with a cache.**  A smart playlist's track list is
  computed when needed and cached.  Its inputs are Track and Album state,
  so the cache holds until either changes.  Every play changes Track state,
  so recomputation must be quick, which a SQL query over the library is.
- **Report real changes only.**  When an input changes, the server
  recomputes and compares with the cached list, and bumps the playlist's
  modseq only if the list differs.  Otherwise every play would make every
  client refetch every smart playlist.
- **Deterministic results.**  Sorts break ties by track id.  Random
  selection orders tracks by a hash of the track id and a seed stored on
  the playlist, so the selection is stable and changes only where inputs
  change; reshuffling is an edit that sets a new seed.
- **Time-relative rules** like "played in the last 30 days" are a plain
  comparison against the moment of evaluation, measured in seconds.  There
  is no day boundary, so there is no question of whose midnight.
- **No cycles.**  A rule may draw on another playlist, so playlists form a
  graph.  `Playlist/set` rejects a change that would close a loop, so
  evaluation can always run in dependency order.
- **Two-phase client sync:** the client syncs all Track changes first, then
  fetches playlists.  It never sees playlist membership computed against
  tracks it hasn't received yet.

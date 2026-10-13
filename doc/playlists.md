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
- **No reactive recomputation.**  Track updates don't cascade into playlist
  updates.  The server can evaluate lazily on request, or recompute on write
  and just advance state.  Which of those is still open.
- **Two-phase client sync:** the client syncs all Track changes first, then
  fetches playlists.  It never sees playlist membership computed against
  tracks it hasn't received yet.

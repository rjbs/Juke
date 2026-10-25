# Running the server

The server is one Go program, `juke`, in `cmd/juke`.  Its database is
built by `juke import` and served by `juke serve`.  See
[import](import.md) for what the import does, and [object
types](types.md) for what the server serves.

## Building and testing

    go build -o var/juke ./cmd/juke
    go test ./...

The importer's test runs only where the sample library is in `sample/`
and the recovery's catalog is at `/Volumes/Video/music/catalog.sqlite`;
elsewhere it skips.

## Importing the sample

    var/juke import --library sample \
      --catalog /Volumes/Video/music/catalog.sqlite \
      --include Undamaged --include Damaged --include Miscellaneous \
      --playlists doc/sample-playlists.json \
      --out var/sample.sqlite

The output must not exist; remove it to import again.  The sample
imports in under a second as 188 tracks with audio, 4 gap tracks,
19 albums and 3 playlists.  `--include` defaults to `Undamaged` alone,
which is what the first real import uses.

## Serving

    var/juke serve --db var/sample.sqlite --library sample

It listens on `localhost:8737`.  The session is at
`/.well-known/jmap`, with one account, `music`, under the capability
`https://semiotic.systems/jmap/juke`.  `--max-objects-in-get` lowers the
per-request object limit, which makes clients page through `/query`;
useful for testing.

There is no authentication yet.  Keep it on localhost until that is
decided for the tailnet.

## What is served

- **Track, Album and Playlist** each have `/get`, `/changes` and
  `/query`.  Albums and playlists can be sorted by title but not
  filtered; Track's filters and sorts are listed in
  [object types](types.md).
- **Track/set** updates rating, play count and last played.  Anything
  else, and any create or destroy, is refused.
- **Blob download** serves each track's original file, with range
  requests.  A blobId is derived from the track id, metadata version and
  recipe, as [storage](storage.md) describes; for now every track is at
  version 1 with the "original" recipe.
- **State strings** carry an epoch minted at import, so a client holding
  state from an earlier import gets `cannotCalculateChanges` and resyncs
  rather than receiving wrong changes.

Checked against Jinx on 2026-10-24: a full sync of the sample through
the `/query` paging path, an incremental sync after another client's
change, a rating written from Jinx, a pinned download, and a full
resync after a fresh import.

## Not yet

- `Album/set` and `Playlist/set` answer `unknownMethod`.
- `/queryChanges` always answers `cannotCalculateChanges`.
- Uploads are refused, and push (the event source) is not implemented.
- Smart playlists, metadata flush and artwork.

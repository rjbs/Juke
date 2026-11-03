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

## Backfilling

    var/juke backfill --db var/sample.sqlite --library sample \
      --catalog /Volumes/Video/music/catalog.sqlite

A database imported before a property existed has it empty.  `backfill`
fills what it can: genre from the tags the import stored, duration by
measuring each file under the library, and either from the catalog when
one is given, for what the tags and files don't say.  It also moves a
track that nothing numbered, which the import put after its album's
numbered tracks, to the number its filename starts with, when that
spot is free (#6); for a file the originals move has renamed, the
move's journal (`--journal`, default the database's name plus
`.originals-journal`) still has the name.  It also notes where each
album track's number came from, where an older import didn't say, for
the review tab; with the catalog the answer is more complete.  Every
track it changes gets one new modseq so clients refetch them once; see
[import](import.md).  It is safe to run again, and to run while the
server is up, which notices the change on its next poll.  With a
catalog it also reports how many measured durations disagree with the
catalog's by more than two seconds, as a check on the measuring.

## Artwork

    var/juke artwork --db var/sample.sqlite --library sample

Finds album art in the files' embedded pictures and in image files
beside them, stores each distinct image once under `artwork/` in the
library directory, and sets each album's and track's art by the rule
in [artwork](artwork.md), leaving alone any chosen by hand on the review
tab.  Like `backfill`, it is safe to run again and while the server is
up.

## Serving

    var/juke serve --db var/sample.sqlite --library sample

It listens on `localhost:8737`, where `/` is a web page, built into the
binary (`internal/web`), that browses albums, tracks and playlists of
both kinds, plays them, and sets ratings, all through the JMAP API.
A smart playlist shows the tracks its rule matches now, and the rule
itself on request; nothing on the page creates or edits one yet.  A track played
to its end there counts as a play.  `--web-dir internal/web` serves the
page from the source tree instead, so edits show on reload.

Its Review tab lists the albums with something wrong, by issue (see
[object types](types.md)), and opens each as a page: the pictures the
album could have, side by side with the tracks carrying each, to choose
one as the album's or give a track its own; the tracks, to drag into
order, with where each one's number came from; and a MusicBrainz lookup,
made from the browser, that matches a release's track list to the
album's by title or length and puts the album in its order.  The page
edits a copy and saves it whole, in one request; art and order chosen
there stay chosen through later runs of `juke artwork` and `backfill`.

The session is at `/.well-known/jmap`, with one account, `music`, under
the capability `https://semiotic.systems/jmap/juke`, and a second,
`https://semiotic.systems/jmap/juke/review`, for the web page's review
tab; see [object types](types.md).  `--max-objects-in-get` lowers the
per-request object limit, which makes clients page through `/query`;
useful for testing.

There is no authentication yet.  Keep it on localhost until that is
decided for the tailnet.

## Deploying

The server runs on a Linux host on the tailnet under systemd, as the
`juke` user; `deploy/juke.service` says how it is installed and where
things live.  An upgrade is `make deploy`: cross-compile, copy the
binary over, install it and restart the service.  A schema change needs
nothing more, since the store applies its migrations on the first open
by a binary that knows them, but take a snapshot of the database first,
because an older binary refuses a newer schema and there is no way
back but the snapshot.

## What is served

- **Track, Album, Playlist and SmartPlaylist** each have `/get`,
  `/changes` and `/query`.  Albums and playlists of both kinds can be
  sorted by title but not filtered; Track's filters, sorts and caps are
  listed in [object types](types.md).
- **SmartPlaylist/set** stores a saved search, refusing a rule that
  `Track/query` would refuse, one naming a playlist that doesn't exist,
  or one that would make a loop; see [playlists](playlists.md).
- **Track/set** updates rating, play count, last played and genre.
  Anything else, and any create or destroy, is refused.
- **Blob download** serves each track's original file, or an image of
  album art ([artwork](artwork.md)), with range
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

## Push

The event source (RFC 8620 §7.3) sends a `state` event whenever a
type's state changes, with `closeafter` and `ping` as the RFC describes.
A connection learns of a change at once when this server commits a
`/set`, and within `PushPollInterval` (five seconds) of a write from
outside it, such as an import.

## Not yet

- `/queryChanges` always answers `cannotCalculateChanges`.
- Uploads are refused.
- Metadata flush.
- Authoring smart playlists anywhere but `bin/recreate-smart-playlists`.

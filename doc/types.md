# Object types

A proposal, deliberately small.  Properties are added when something needs
them, not because the files have them; most of what ID3 tags carry can be
mined from the originals later.

Notation follows RFC 8620: `Id`, `String`, `UnsignedInt`, `UTCDate`,
`A|null`, `A[]`.  **server-set** means a client may not set the property
in `/set`; the server rejects the attempt.  It can still change, as a
side effect of other updates.

## Track

A recording on an album, usually with audio.

| Property     | Type            | Notes |
|--------------|-----------------|-------|
| `id`         | `Id`            | Immutable, server-set.  Opaque, minted at ingest; see [storage](storage.md). |
| `title`      | `String`        | |
| `artist`     | `String\|null`  | |
| `albumId`    | `Id\|null`      | Server-set.  Follows from album membership; null for a track on no album. |
| `rating`     | `Number\|null`  | 1 to 5 in steps of 0.5, or null for unrated. |
| `blobId`     | `Id\|null`      | Server-set.  The served audio.  Null when there is no audio. |
| `playCount`  | `UnsignedInt`   | Default 0. |
| `lastPlayed` | `UTCDate\|null` | |

**A track with no audio** stands for a gap: a track the 2014 catalog
knows an album had, whose file is lost.  It keeps the catalog's title,
artist, rating and play count, and holds its position on the album.  If
the audio is found later, it is attached to the same track, which keeps
its id, its playlist memberships and its history.  Clients show it but
cannot play, download or pin it.

**Ratings:** the 2014 library stores ratings as 0 to 100 in steps of 10.
Every track rating in it was set by hand, none computed from an album
rating.  566 tracks carry a half star, so the scale keeps half steps
rather than rounding them away.

## Album

| Property | Type           | Notes |
|----------|----------------|-------|
| `id`     | `Id`           | Immutable, server-set. |
| `title`  | `String`       | One title per album; disc numbers are not part of it. |
| `artist` | `String\|null` | Null for a various-artists compilation. |
| `discs`  | `Disc[]`       | At least one. |

### Disc

A data type, not an object type: no id, no `/get` of its own.

| Property   | Type           | Notes |
|------------|----------------|-------|
| `trackIds` | `Id[]`         | In play order. |
| `title`    | `String\|null` | For sets that name their discs, like *Mellon Collie and the Infinite Sadness*. |

**Position is structural.**  A track's disc number is its disc's place in
`discs`, and its track number is its place in that disc's `trackIds`.
Track has no number properties, so nothing can disagree with the album.
The tags written into served files are computed from this.

**Album owns membership.**  A track appears on at most one album, at most
once.  Changing an album's discs updates `albumId` on every track that
joined or left it, in the same transaction, so those tracks appear in the
next `Track/changes` too.  Moving a track between albums is one
`Album/set` that updates both albums.  Updating one disc is a patch to
`discs/N/trackIds`.

## Playlist

| Property   | Type     | Notes |
|------------|----------|-------|
| `id`       | `Id`     | Immutable, server-set. |
| `title`    | `String` | |
| `trackIds` | `Id[]`   | In play order.  A track may appear more than once. |

This is the plain playlist only.  Smart playlists, stored `Track/query`
calls (see [playlists](playlists.md)), come later and will need more
properties.

## Sync order

Clients sync Track, then Album, then Playlist.  Even so, a track moved to
a newly created album briefly names an `albumId` the client has not
fetched yet.  A client treats an unknown `albumId` as not synced yet, not
as an error.

## Left out for now

Each of these is a likely addition, kept out until it's needed:

- **Genre,** on both types: on Album for the album's genre, and on Track
  for exceptions, like a skit between two songs.
- **Duration.**  The player gets it from the audio, but lists will want
  it without downloading anything.
- **Year, composer, compilation flag, date added, skip count.**
- **Artwork,** which belongs on Album.
- **Album rating.**  Every album rating in the 2014 library was computed
  from track ratings, so there is nothing to import.
- **Audio digest.**  [Storage](storage.md) makes it a property of the
  track, but it is server-internal until a client needs it.
- **Metadata flush state.**  Edits mark a track dirty, and a later flush
  writes its tags and changes its `blobId`, so a burst of edits costs one
  re-download.  This is server bookkeeping; whether clients see it is
  open.

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
| `blobType`   | `String\|null`  | Server-set.  The audio's media type, such as `audio/mpeg` or `audio/mp4`.  Null when there is no audio. |
| `playCount`  | `UnsignedInt`   | Default 0. |
| `lastPlayed` | `UTCDate\|null` | |
| `dateAdded`  | `UTCDate`       | When the track joined the library. |

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
next `Track/changes` too.  A track that joins an album leaves the one it
was on, which changes as well and appears in the next `Album/changes`; a
track that leaves an album and joins no other is left on no album.

`Album/set` sets `title`, `artist` and `discs`.  As RFC 8620 requires, an
update replaces `discs` whole; a patch can't point into it.  Merging two
albums is one `Album/set` that updates the album to keep, giving it the
other's tracks, and destroys the other.  Only an album with no tracks can
be destroyed; destroying one with tracks fails with the SetError
`albumNotEmpty`, so a slip can't strand its tracks on no album.

## Playlist

| Property   | Type     | Notes |
|------------|----------|-------|
| `id`       | `Id`     | Immutable, server-set. |
| `title`    | `String` | |
| `trackIds` | `Id[]`   | In play order.  A track may appear more than once. |

`Playlist/set` creates, updates and destroys playlists.  Every id in
`trackIds` must name a track.  As RFC 8620 requires, an update replaces
`trackIds` whole; a patch can't point into it.

This is the plain playlist only.  Smart playlists, stored `Track/query`
calls (see [playlists](playlists.md)), come later and will need more
properties.

## Track/query

Filters are named conditions, as in RFC 8621's `Email/query`, combined
with the standard `AND`, `OR` and `NOT` operators.  String matches are
case-insensitive, using the `i;unicode-casemap` collation that JMAP
defaults to.

| Condition | Type | Matches tracks whose... |
|-----------|------|-------------------------|
| `title` | `String` | title equals the value |
| `titleContains` | `String` | title contains the value |
| `artist` | `String` | artist equals the value |
| `artistContains` | `String` | artist contains the value |
| `albumTitle` | `String` | album's title equals the value |
| `albumTitleContains` | `String` | album's title contains the value |
| `ratingAtLeast` | `Number` | rating is at least the value |
| `ratingAtMost` | `Number` | rating is at most the value |
| `hasRating` | `Boolean` | rating is set, or is null |
| `playCountAtLeast` | `UnsignedInt` | play count is at least the value |
| `playCountAtMost` | `UnsignedInt` | play count is at most the value |
| `lastPlayedBefore` | `UTCDate` | last play was before the value |
| `lastPlayedAfter` | `UTCDate` | last play was at or after the value |
| `lastPlayedWithinDays` | `UnsignedInt` | last play was within that many days of today |
| `inPlaylist` | `Id` | track is in that playlist |
| `hasAudio` | `Boolean` | `blobId` is set, or is null |

An unrated track matches neither rating bound, and a track never played
matches neither `lastPlayed` bound; `hasRating` and `playCountAtMost: 0`
find those.  A track on no album never matches an album condition.

The last three exist for smart playlists.  A stored filter with an
absolute date would stop moving, so "played this month" needs
`lastPlayedWithinDays`.  `inPlaylist` lets one playlist draw on another;
see [playlists](playlists.md) for why that graph must stay acyclic.
`hasAudio` lets a playlist leave out gap tracks.

**Sorting** is on the same properties: `title`, `artist`, `albumTitle`,
`rating`, `playCount` and `lastPlayed`, plus `albumOrder`, which sorts by
album title and then by disc and track position.  Nulls sort last in
either direction.  Ties always break by track id, so a sorted, limited
list is stable.  Random order, seeded per playlist, is a smart playlist
feature rather than a `Track/query` sort.

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
- **Year, composer, compilation flag, skip count.**
- **Artwork,** which belongs on Album.
- **Album rating.**  Every album rating in the 2014 library was computed
  from track ratings, so there is nothing to import.
- **Audio digest.**  Deferred entirely; see [storage](storage.md).
- **Metadata flush state.**  Edits mark a track dirty, and a later flush
  writes its tags and changes its `blobId`, so a burst of edits costs one
  re-download.  This is server bookkeeping; whether clients see it is
  open.

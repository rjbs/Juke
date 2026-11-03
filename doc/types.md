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
| `genre`      | `String\|null`  | A free string, as the tags have it.  A third of the library has none. |
| `rating`     | `Number\|null`  | 1 to 5 in steps of 0.5, or null for unrated. |
| `blobId`     | `Id\|null`      | Server-set.  The served audio.  Null when there is no audio. |
| `blobType`   | `String\|null`  | Server-set.  The audio's media type, such as `audio/mpeg` or `audio/mp4`.  Null when there is no audio. |
| `size`       | `UnsignedInt\|null` | Server-set.  The served audio's size in bytes.  Null when there is no audio. |
| `duration`   | `UnsignedInt\|null` | Server-set.  Whole seconds.  Null when unknown; a gap track keeps the catalog's figure. |
| `playCount`  | `UnsignedInt`   | Default 0. |
| `lastPlayed` | `UTCDate\|null` | |
| `dateAdded`  | `UTCDate`       | When the track joined the library. |
| `artworkBlobId` | `Id\|null` | Server-set.  The track's own art, only where it differs from its album's; see [artwork](artwork.md).  Null means show the album's. |
| `artworkType` | `String\|null` | Server-set.  The media type of the track's own art, such as `image/jpeg`.  Null with `artworkBlobId`. |
| `numberSource` | `String\|null` | Server-set, **review only** (see Album).  What gave the track its number: `tags`, `catalog`, `file`, `user`, or `none` for a track placed after the rest because nothing did; see [import](import.md).  Null where an older import didn't say. |

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

**Genre** is per track because that is where the tags and the 2014
rules have it.  An album-level genre with per-track exceptions was
considered and isn't needed.

## Album

| Property | Type           | Notes |
|----------|----------------|-------|
| `id`     | `Id`           | Immutable, server-set. |
| `title`  | `String`       | One title per album; disc numbers are not part of it. |
| `artist` | `String\|null` | Null for a various-artists compilation. |
| `discs`  | `Disc[]`       | At least one. |
| `artworkBlobId` | `Id\|null` | The album's art, an image served as a blob; see [artwork](artwork.md).  Set by the server's rule, or by hand under the review capability, below. |
| `artworkType` | `String\|null` | Server-set.  The art's media type, such as `image/jpeg`, for the download URL's `type`.  Null with `artworkBlobId`. |
| `issues` | `String[]` | Server-set, **review only**.  What might be wrong with the album; see below. |
| `artworkCandidates` | `Candidate[]` | Server-set, **review only**.  The images the album could have: `{blobId, type, trackIds, folder}`, the image file beside its tracks first (`folder` true, no `trackIds`), then each picture its tracks' files carry, with the tracks carrying it, by first carrier in play order. |

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

(The server now stores each track's real number, and a missing track is a
number nothing has, but `trackIds` still lists only the tracks there are,
so a place in it is the true track number only on an album with no
missing tracks.  Null slots for missing tracks are the rest of #40.)

**Album owns membership.**  A track appears on at most one album, at most
once.  Changing an album's discs updates `albumId` on every track that
joined or left it, in the same transaction, so those tracks appear in the
next `Track/changes` too.  A track that joins an album leaves the one it
was on, which changes as well and appears in the next `Album/changes`; a
track that leaves an album and joins no other is left on no album.

`Album/set` sets `title`, `artist`, `discs` and, under the review
capability, `artworkBlobId`.  As RFC 8620 requires, an
update replaces `discs` whole; a patch can't point into it.  Merging two
albums is one `Album/set` that updates the album to keep, giving it the
other's tracks, and destroys the other.  Only an album with no tracks can
be destroyed; destroying one with tracks fails with the SetError
`albumNotEmpty`, so a slip can't strand its tracks on no album.

### Review

The review page (see [server](server.md)) asks for more than a client
that plays music needs, so that is a capability of its own,
`https://semiotic.systems/jmap/juke/review`, in `using` beside the
Juke one.  Without it Album is as the table has it less the two review
properties; asking for one by name is `invalidArguments`, and the
filter and the art setting below are refused.  With it:

`issues` names the album's troubles, in this order:

| Issue | Meaning |
|-------|---------|
| `noArt` | Tracks with audio, but no image. |
| `artOverrides` | Some tracks carry a picture other than the album's, and show it. |
| `artTie` | The rule picked the album's image on a tie; see [artwork](artwork.md). |
| `folderArt` | The image file beside the tracks differs from pictures the tracks carry. |
| `unnumbered` | A track was numbered from nowhere: nothing gave it a number, or another track had the one it gave, so the import put it after the rest. |
| `gaps` | Tracks with no audio. |

The art issues are moot once the album's art has been chosen by hand,
and `unnumbered` once `discs` has been set, since the order is then the
user's.

`Album/query` takes a filter, `{issue: "artTie"}` for one issue or
`{hasIssues: true}` for any (false for none); the sort is by title as
before.

`Album/set` with `artworkBlobId` gives the album that image, which must
be one the server has stored (any `artworkCandidates` entry, or another
album's), or null for none, and clears its tracks' own images, so the
choice is the whole album's; the tracks whose `artworkBlobId` changed
appear in the next `Track/changes`.  `Track/set` with `artworkBlobId`
then gives one track an image of its own again, or null to clear it.
Art chosen either way is left alone by later runs of `juke artwork`.
An image from elsewhere is uploaded first, at the session's `uploadUrl`,
and the blob id that answers is an `artworkBlobId`; see
[server](server.md).

## Playlist

| Property   | Type     | Notes |
|------------|----------|-------|
| `id`       | `Id`     | Immutable, server-set. |
| `title`    | `String` | |
| `trackIds` | `Id[]`   | In play order.  A track may appear more than once. |

`Playlist/set` creates, updates and destroys playlists.  Every id in
`trackIds` must name a track.  As RFC 8620 requires, an update replaces
`trackIds` whole; a patch can't point into it.  Destroying a playlist
that a smart playlist's rule names fails with the SetError
`playlistInUse`.

This is the plain playlist only.  A smart playlist is its own type.

## SmartPlaylist

A saved search: a title and the arguments of a `Track/query`.  It has no
track list; a client gets the tracks by running the query.  See
[playlists](playlists.md) for why.

| Property       | Type                  | Notes |
|----------------|-----------------------|-------|
| `id`           | `Id`                  | Immutable, server-set. |
| `title`        | `String`              | |
| `filter`       | `Filter\|null`        | As `Track/query` takes it.  Null matches every track. |
| `sort`         | `Comparator[]`        | As `Track/query` takes it.  May be empty. |
| `limit`        | `UnsignedInt\|null`   | At most this many tracks. |
| `limitBytes`   | `UnsignedInt\|null`   | At most this much audio, by `size`. |
| `limitSeconds` | `UnsignedInt\|null`   | At most this much playing time, by `duration`. |

`SmartPlaylist/set` compiles the filter and sort before storing them and
refuses what `Track/query` would refuse, so a stored rule always runs.
It also refuses a rule naming a playlist of either kind that does not
exist, and one whose references would form a cycle; both are
`invalidProperties` on `filter`.  A reference must be a real id: a
creation id from the same call is not resolved inside a filter.  A
`random` comparator with no `seed` gets one from the server, which the
`/set` response reports in `created` or `updated`.  Destroying a smart
playlist that another's rule names fails with `playlistInUse`.

`limit` is a cap like the other two, applied before any paging by the
client that runs the query: a client that wants the first ten of a
limited list asks `Track/query` for them and gets the stored limit's
first ten.

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
| `hasArtist` | `Boolean` | artist is set, or is null |
| `albumTitle` | `String` | album's title equals the value |
| `albumTitleContains` | `String` | album's title contains the value |
| `hasAlbum` | `Boolean` | `albumId` is set, or is null |
| `genre` | `String` | genre equals the value |
| `genreContains` | `String` | genre contains the value |
| `hasGenre` | `Boolean` | genre is set, or is null |
| `ratingAtLeast` | `Number` | rating is at least the value |
| `ratingAtMost` | `Number` | rating is at most the value |
| `hasRating` | `Boolean` | rating is set, or is null |
| `playCountAtLeast` | `UnsignedInt` | play count is at least the value |
| `playCountAtMost` | `UnsignedInt` | play count is at most the value |
| `lastPlayedBefore` | `UTCDate` | last play was before the value |
| `lastPlayedAfter` | `UTCDate` | last play was at or after the value |
| `lastPlayedWithinDays` | `UnsignedInt` | last play was within that many days, to the second, of now |
| `durationAtLeast` | `UnsignedInt` | duration is at least that many seconds |
| `durationAtMost` | `UnsignedInt` | duration is at most that many seconds |
| `inPlaylist` | `Id` | track is in that playlist |
| `inSmartPlaylist` | `Id` | track matches that smart playlist's rule |
| `hasAudio` | `Boolean` | `blobId` is set, or is null |

**A condition is true or false of every track**, as RFC 8620 §5.5
requires, so a track whose value is missing simply fails the condition,
and `NOT` of it matches.  An unrated track matches neither rating bound
and does match `NOT {ratingAtLeast: 3}`; a track on no album never
matches an album condition and always matches its negation; "genre is
not Classical" includes the tracks with no genre.  The `has…` conditions
find the missing values directly.

`lastPlayedWithinDays` exists for smart playlists, where a stored filter
with an absolute date would stop moving.  `inPlaylist` and
`inSmartPlaylist` let one playlist draw on another; see
[playlists](playlists.md) for how references are kept valid.  `hasAudio`
lets a playlist leave out gap tracks, and nothing does so implicitly.

**Sorting** is on `title`, `artist`, `albumTitle`, `genre`, `rating`,
`playCount`, `lastPlayed` and `duration`, plus `albumOrder`, which sorts
by album title and then by disc and track position, and `random`, a
comparator carrying a `seed` string, which orders by a hash of the track
id and the seed and is refused without one.  Nulls sort last in either direction, except that a
track never played counts as played before any other, so `lastPlayed`
ascending is "least recently played" and starts with the never-played.
Ties always break by track id, so a sorted, limited list is stable.

**Caps.**  Besides the standard `limit`, the query takes `limitBytes`
and `limitSeconds`.  After sorting, the result is cut where the running
total of `size` or `duration` would pass the cap; a track with no audio
or no known duration counts as zero.  `total` and `position` are of the
capped list.

## Sync order

Clients sync Track, then Album, then Playlist, then SmartPlaylist.  Even
so, a track moved to a newly created album briefly names an `albumId`
the client has not fetched yet.  A client treats an unknown `albumId` as
not synced yet, not as an error.

## Left out for now

Each of these is a likely addition, kept out until it's needed:

- **Year, composer, compilation flag, skip count.**  A `compilation`
  condition on `Track/query`, true for a track whose album has no
  artist, would recreate the 2014 "Compilations" list.
- **Artwork,** which belongs on Album.
- **Album rating.**  Every album rating in the 2014 library was computed
  from track ratings, so there is nothing to import.
- **Audio digest.**  Deferred entirely; see [storage](storage.md).
- **Metadata flush state.**  Edits mark a track dirty, and a later flush
  writes its tags and changes its `blobId`, so a burst of edits costs one
  re-download.  This is server bookkeeping; whether clients see it is
  open.

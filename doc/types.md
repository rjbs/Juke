# Object types

The server speaks JMAP (RFC 8620) over the capability
`https://semiotic.systems/jmap/juke`, which gives an account four
object types: Track, Album, Playlist and SmartPlaylist.  Each has the
standard `/get`, `/changes`, `/query`, `/queryChanges` and `/set`
methods, with the differences from the standard noted under each type.
The set is deliberately small.  Properties are added when something
needs them, not because the files have them; most of what ID3 tags
carry can be mined from the originals later.

Type names follow RFC 8620: `Id`, `String`, `Boolean`, `Number`,
`UnsignedInt`, `UTCDate`, `A|null`, `A[]`.  A property that is "set by
the server" may not be set by a client: a create or update that names
it is rejected with an `invalidProperties` SetError.  Such a property
can still change, as a side effect of other updates, and the object
then appears in the next `/changes`.

A second capability, `https://semiotic.systems/jmap/juke/review`,
is for the review page (see [server](server.md)), which asks for more
than a client that plays music needs.  A request `using` it sees a few
more properties and may do a few more things; these are marked
**review only** below.  Without it, those properties are not returned,
asking for one by name in `/get` is an `invalidArguments` error, and
setting one is an `invalidProperties` SetError.

State strings carry an epoch minted at import.  A `/changes` call with
a state from an earlier import, or from another server, gets
`cannotCalculateChanges`, and the client resyncs rather than receiving
wrong changes.  `/queryChanges` always answers `cannotCalculateChanges`
for every type; a client reruns the query.

## Track

A **Track** is a recording on an album, usually with audio.  It has the
following properties:

- **id**: `Id`
  The id of the track.  This is set by the server and immutable.  It is
  opaque, minted at ingest; see [storage](storage.md).
- **title**: `String`
  The title of the track.
- **artist**: `String|null`
  The track's artist, or null when the tags gave none.
- **albumId**: `Id|null`
  The id of the album the track is on, or null for a track on no album.
  This is set by the server: it follows from the album's `discs`.
- **genre**: `String|null`
  A free string, as the tags have it.  A third of the library has none.
- **rating**: `Number|null`
  1 to 5 in steps of 0.5, or null for unrated.
- **blobId**: `Id|null`
  The id of the served audio, or null when there is no audio.  This is
  set by the server.
- **blobType**: `String|null`
  The audio's media type, such as `audio/mpeg` or `audio/mp4`, or null
  when there is no audio.  This is set by the server.
- **size**: `UnsignedInt|null`
  The served audio's size in bytes, or null when there is no audio.
  This is set by the server.
- **duration**: `UnsignedInt|null`
  The length of the audio in whole seconds, or null when unknown; a gap
  track keeps the catalog's figure.  This is set by the server.
- **playCount**: `UnsignedInt`
  How many times the track has been played.  Default 0.
- **lastPlayed**: `UTCDate|null`
  When the track was last played, or null if never.
- **dateAdded**: `UTCDate`
  When the track joined the library.  This is set by the server and
  immutable.
- **artworkBlobId**: `Id|null`
  The id of the track's own art, an image served as a blob, only where
  it differs from its album's; see [artwork](artwork.md).  Null means
  show the album's.  This is set by the server's rule, or by hand under
  the review capability (see `Track/set`).
- **artworkType**: `String|null`
  The media type of the track's own art, such as `image/jpeg`, or null
  when `artworkBlobId` is null.  This is set by the server.
- **numberSource**: `String|null` (review only)
  What gave the track its number; see [import](import.md).  This is set
  by the server.  One of:
    - `tags`: the disc and track number tags.
    - `catalog`: the 2014 catalog.
    - `file`: the number the file name starts with.
    - `user`: an `Album/set` placed it.
    - `none`: nothing gave it a number, or another track had the one it
      gave, so it was placed after the rest.

    Null where an older import didn't say.

A track has no number properties: its disc number is its disc's place
in its album's `discs`, and its track number is its place in that
disc's `trackIds`, so nothing can disagree with the album.  The tags
written into served files are computed from this.

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

### Track/get

Standard JMAP `/get` method.

### Track/changes

Standard JMAP `/changes` method.  A track whose `albumId` changed
because an album's `discs` were set is included, as is one whose
`artworkBlobId` changed because its album's art was set.

### Track/query

Standard JMAP `/query` method, with these additions.

A **FilterCondition** object has the following properties, any of which
may be given; all must match.  Conditions combine with the standard
`AND`, `OR` and `NOT` FilterOperators.  String matches are
case-insensitive, using the `i;unicode-casemap` collation that JMAP
defaults to.

- **title**: `String`
  The track's title equals the value.
- **titleContains**: `String`
  The track's title contains the value.
- **artist**: `String`
  The track's artist equals the value.
- **artistContains**: `String`
  The track's artist contains the value.
- **hasArtist**: `Boolean`
  If true, the track's artist is set; if false, it is null.
- **albumTitle**: `String`
  The title of the track's album equals the value.
- **albumTitleContains**: `String`
  The title of the track's album contains the value.
- **hasAlbum**: `Boolean`
  If true, the track's `albumId` is set; if false, it is null.
- **genre**: `String`
  The track's genre equals the value.
- **genreContains**: `String`
  The track's genre contains the value.
- **hasGenre**: `Boolean`
  If true, the track's genre is set; if false, it is null.
- **ratingAtLeast**: `Number`
  The track's rating is at least the value.
- **ratingAtMost**: `Number`
  The track's rating is at most the value.
- **hasRating**: `Boolean`
  If true, the track's rating is set; if false, it is null.
- **playCountAtLeast**: `UnsignedInt`
  The track's play count is at least the value.
- **playCountAtMost**: `UnsignedInt`
  The track's play count is at most the value.
- **lastPlayedBefore**: `UTCDate`
  The track's last play was before the value.
- **lastPlayedAfter**: `UTCDate`
  The track's last play was at or after the value.
- **lastPlayedWithinDays**: `UnsignedInt`
  The track's last play was within that many days, to the second, of
  the time of the query.
- **durationAtLeast**: `UnsignedInt`
  The track's duration is at least that many seconds.
- **durationAtMost**: `UnsignedInt`
  The track's duration is at most that many seconds.
- **inPlaylist**: `Id`
  The track is in the Playlist with this id.
- **inSmartPlaylist**: `Id`
  The track matches the rule of the SmartPlaylist with this id.
- **hasAudio**: `Boolean`
  If true, the track's `blobId` is set; if false, it is null.

A condition that is not one of these is an `unsupportedFilter` error.

A condition is true or false of every track, as RFC 8620 §5.5
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

The following properties MUST be supported for sorting:

- `title`
- `artist`
- `albumTitle`
- `genre`
- `rating`
- `playCount`
- `lastPlayed`
- `duration`
- `albumOrder`: by album title, then by disc and track position.
- `random`: by a hash of the track id and a **seed**: `String`
  property that the Comparator object MUST carry.  A `random`
  comparator with no seed is an `unsupportedSort` error.

Any other property is an `unsupportedSort` error.  Nulls sort last in
either direction, except that a track never played counts as played
before any other, so `lastPlayed` ascending is "least recently played"
and starts with the never-played.  Ties always break by track id, so a
sorted, limited list is stable.

The method takes two arguments beyond the standard ones, caps like
`limit`:

- **limitBytes**: `UnsignedInt|null`
  At most this much audio, by `size`.
- **limitSeconds**: `UnsignedInt|null`
  At most this much playing time, by `duration`.

After sorting, the result is cut where the running total of `size` or
`duration` would pass the cap; a track with no audio or no known
duration counts as zero.  The `total` and `position` in the response
are of the capped list.

### Track/queryChanges

Always `cannotCalculateChanges`, as noted above.

### Track/set

Standard JMAP `/set` method, except that only updates are allowed.
Tracks are created and destroyed only by import; a create or destroy is
rejected with a `forbidden` SetError.

An update may set `rating`, `playCount`, `lastPlayed` and `genre`, and,
under the review capability, `artworkBlobId`.  A patch naming any other
property, or giving one of these a value outside its type, is rejected
with an `invalidProperties` SetError listing the offending properties.
A `genre` of only whitespace is stored as null.

Setting `artworkBlobId` gives the track an image of its own, or null to
clear it.  The id must name an image the server has stored (any entry
in an album's `artworkCandidates`, an album's current art, or an
uploaded image; see `Album/set`).  Art chosen either way, cleared
included, is the user's: later runs of `juke artwork` leave it alone,
and the review page counts the track as decided.

## Album

An **Album** is a titled set of tracks in order, on one or more discs.
It has the following properties:

- **id**: `Id`
  The id of the album.  This is set by the server and immutable.
- **title**: `String`
  One title per album; disc numbers are not part of it.
- **artist**: `String|null`
  The album's artist, or null for a various-artists compilation.
- **released**: `String|null`
  When the album came out, as `YYYY-MM-DD` with `00` for a month or day
  not known: `1991-00-00`, `1991-02-00` or `1991-02-19`.  A known day
  needs a known month.  Null when nothing says.  The form sorts as text,
  so a year alone sorts before any date in that year.  The import takes
  it from the tracks' tags (the date most of them give) and the catalog's
  year; see [import](import.md).
- **discs**: `Disc[]`
  The album's discs, in order.  At least one.
- **artworkBlobId**: `Id|null`
  The id of the album's art, an image served as a blob, or null for
  none; see [artwork](artwork.md).  Set by the server's rule, or by
  hand under the review capability (see `Album/set`).
- **artworkType**: `String|null`
  The art's media type, such as `image/jpeg`, for the download URL's
  `type`, or null when `artworkBlobId` is null.  This is set by the
  server.
- **issues**: `String[]` (review only)
  What might be wrong with the album, in this order.  This is set by
  the server.  Each is one of:
    - `noArt`: tracks with audio, but no image.
    - `artOverrides`: some tracks carry a picture other than the album's,
      and show it.
    - `artTie`: the rule picked the album's image on a tie; see
      [artwork](artwork.md).
    - `folderArt`: the image file beside the tracks differs from pictures
      the tracks carry.
    - `unnumbered`: a track was numbered from nowhere: nothing gave it a
      number, or another track had the one it gave, so the import put it
      after the rest.
    - `gaps`: tracks with no audio.

    The art issues are moot once the album's art has been chosen by hand;
    `artTie` and `folderArt` also once every track carrying some other
    picture has had its own art set or cleared by hand, since deciding
    them is accepting the album's; and `unnumbered` once `discs` has been
    set, since the order is then the user's.

- **artworkCandidates**: `Candidate[]` (review only)
  The images the album could have: the image file beside its tracks
  first, then each picture its tracks' files carry, by first carrier in
  play order.  This is set by the server.

A **Disc** object has the following properties:

- **trackIds**: `Id[]`
  The ids of the tracks on the disc, in play order.
- **title**: `String|null`
  The disc's own title, for sets that name their discs, like *Mellon
  Collie and the Infinite Sadness*.

A **Candidate** object has the following properties:

- **blobId**: `Id`
  The id of the image.
- **type**: `String`
  The image's media type.
- **trackIds**: `Id[]`
  The tracks whose files carry this picture; empty for the folder.s
  image file.
- **folder**: `Boolean`
  True for the image file beside the tracks.
- **group**: `UnsignedInt`
  Candidates with the same group look the same to the server, one
  picture encoded several ways; see [artwork](artwork.md).

**Position is structural.**  A track's disc number is its disc's place
in `discs`, and its track number is its place in that disc's
`trackIds`.  (The server now stores each track's real number, and a
missing track is a number nothing has, but `trackIds` still lists only
the tracks there are, so a place in it is the true track number only
on an album with no missing tracks.  Null slots for missing tracks are
the rest of #40.)

**Album owns membership.**  A track appears on at most one album, at
most once.  Changing an album's discs updates `albumId` on every track
that joined or left it, in the same transaction, so those tracks
appear in the next `Track/changes` too.  A track that joins an album
leaves the one it was on, which changes as well and appears in the
next `Album/changes`; a track that leaves an album and joins no other
is left on no album.

### Album/get

Standard JMAP `/get` method.  The two review properties are returned
only under the review capability, as noted above.

### Album/changes

Standard JMAP `/changes` method.

### Album/query

Standard JMAP `/query` method.  The sort is by `title`, by `artist`,
which puts the albums with none last in either direction, as the web
page's album list does, or by `released`, which puts the undated last
in either direction; any other property is an `unsupportedSort` error.
With no sort, albums come in id order.

A filter is accepted only under the review capability; otherwise any
filter is an `unsupportedFilter` error.  A **FilterCondition** object
has the following properties, one of which is given:

- **hasIssues**: `Boolean`
  If true, the album has at least one issue; if false, none.
- **issue**: `String`
  The album has this issue, named as in `issues`.

Both together is the one issue.  FilterOperators are not supported.

### Album/set

Standard JMAP `/set` method.  A create or update may set `title`,
`artist`, `released` and `discs`, and, under the review capability,
`artworkBlobId`.  An album needs a title; a `released` in any shape but
the one above is an `invalidProperties` SetError; and every id in
`discs` must name a track, each once.  As RFC 8620 requires, an update
replaces `discs`
whole; a patch can't point into it.

Merging two albums is two `Album/set` calls in one request: the first
updates the album to keep, giving it the other's tracks, and the second
destroys the other.  They can't be one call, because a `/set` destroys
before it updates.  Only an album with no tracks can be destroyed;
destroying one with tracks is rejected with an `albumNotEmpty` SetError,
so a slip can't strand its tracks on no album.

Setting `artworkBlobId` gives the album that image, which must be one
the server has stored (any `artworkCandidates` entry, or another
album's), or null for none, and clears its tracks' own images, so the
choice is the whole album's; the tracks whose `artworkBlobId` changed
appear in the next `Track/changes`.  Art chosen this way is left alone
by later runs of `juke artwork`.  An image from elsewhere is uploaded
first, at the session's `uploadUrl`, and the blob id that answers is an
`artworkBlobId`; see [server](server.md).

## Playlist

A **Playlist** is a titled list of tracks.  This is the plain playlist
only; a smart playlist is its own type.  It has the following
properties:

- **id**: `Id`
  The id of the playlist.  This is set by the server and immutable.
- **title**: `String`
  The title of the playlist.
- **trackIds**: `Id[]`
  The ids of the tracks in the playlist, in play order.  A track may
  appear more than once.
- **isHelper**: `Boolean` (default: false)
  The playlist is kept for smart playlists' rules to name, not for
  listening.  Clients list helpers apart from other playlists, but
  treat them alike otherwise: they can still be played, added to and
  kept offline.

### Playlist/get

Standard JMAP `/get` method.

### Playlist/changes

Standard JMAP `/changes` method.

### Playlist/query

Standard JMAP `/query` method, except that no filter is supported (any
is an `unsupportedFilter` error) and the only sort property is
`title`.  With no sort, playlists come in id order.

### Playlist/set

Standard JMAP `/set` method.  A create or update may set `title`,
`trackIds` and `isHelper`; a playlist needs a title, and every id in
`trackIds` must name a track.  As RFC 8620 requires, an update
replaces `trackIds` whole; a patch can't point into it.

Destroying a playlist that a smart playlist's rule names is rejected
with a `playlistInUse` SetError.

## SmartPlaylist

A **SmartPlaylist** is a saved search: a title and the arguments of a
`Track/query`.  It has no track list; a client gets the tracks by
running the query.  See [playlists](playlists.md) for why.  It has the
following properties:

- **id**: `Id`
  The id of the smart playlist.  This is set by the server and
  immutable.
- **title**: `String`
  The title of the smart playlist.
- **filter**: `FilterOperator|FilterCondition|null`
  The filter, as `Track/query` takes it.  Null matches every track.
- **sort**: `Comparator[]`
  The sort, as `Track/query` takes it.  May be empty.
- **limit**: `UnsignedInt|null`
  At most this many tracks.
- **limitBytes**: `UnsignedInt|null`
  At most this much audio, by `size`.
- **limitSeconds**: `UnsignedInt|null`
  At most this much playing time, by `duration`.
- **isHelper**: `Boolean` (default: false)
  The smart playlist is kept for other rules to name, as Playlist's
  `isHelper` is.

`limit` is a cap like the other two, applied before any paging by the
client that runs the query: a client that wants the first ten of a
limited list asks `Track/query` for them and gets the stored limit's
first ten.

### SmartPlaylist/get

Standard JMAP `/get` method.

### SmartPlaylist/changes

Standard JMAP `/changes` method.

### SmartPlaylist/query

Standard JMAP `/query` method, except that no filter is supported (any
is an `unsupportedFilter` error) and the only sort property is
`title`.  With no sort, smart playlists come in id order.

### SmartPlaylist/set

Standard JMAP `/set` method.  A create or update may set `title`,
`filter`, `sort`, `limit`, `limitBytes`, `limitSeconds` and
`isHelper`; a smart playlist needs a title.

The server compiles the filter and sort before storing them and
refuses what `Track/query` would refuse, so a stored rule always runs.
It also refuses a rule naming a playlist of either kind that does not
exist, and one whose references would form a cycle.  Each of these is
an `invalidProperties` SetError naming `filter` or `sort`.  A reference
must be a real id: a creation id from the same call is not resolved
inside a filter.

A `random` comparator with no `seed` gets one from the server, which
the response reports in `created` or `updated`.

Destroying a smart playlist that another's rule names is rejected with
a `playlistInUse` SetError.

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
- **Album rating.**  Every album rating in the 2014 library was computed
  from track ratings, so there is nothing to import.
- **Audio digest.**  Deferred entirely; see [storage](storage.md).
- **Metadata flush state.**  Edits mark a track dirty, and a later flush
  writes its tags and changes its `blobId`, so a burst of edits costs one
  re-download.  This is server bookkeeping; whether clients see it is
  open.

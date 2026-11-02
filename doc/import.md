# Import

The import builds a new Juke database from two inputs: the files in the
library tree, and the 2014 iTunes library XML used by the recovery.  It
always builds a fresh database; it never updates one in place.  While
prototyping it runs over and over against the [sample
library](sample-library.md).  The real import runs once, and after that
the database is the source of truth.

Because track ids are minted at import, every import invalidates every
client's mirror.  That is fine while prototyping: a client holding state
the server doesn't recognize resyncs from scratch.

## Where each datum comes from

- **The files' tags win** for everything about the music: title, artist,
  album, positions, and anything added later.
- **The 2014 XML wins** for what the listener did: rating, play count,
  last played and date added.  These are almost never in the tags.
- **A file with no title** in its tags or the XML takes its title, and any
  missing disc and track numbers, from its filename, which iTunes wrote as
  "1-02 Title.mp3", and its artist from the directory above the album.
  Undamaged has 114 such files, from albums that never reached the 2014
  library.
- **A track with no artist** in its tags or the XML takes its album's
  artist, or for a track on no album, the name of the artist directory it
  is filed under, unless that is `_`.
- **"Unknown Artist"** is iTunes's placeholder, not a name.  Wherever it
  appears, in a tag, the XML or a directory, it means no artist.
- **"Various Artists"** (or "Various") means no artist for a track.  For
  an album, as its album artist or its artist directory, it means a
  compilation, like an album under `Compilations`.
- **Genre** comes from the tags, with ID3v2's numbered form expanded
  ("(17)" is Rock, and iTunes's "(36)Game" is Game), or from the XML
  when the tags are silent, which is also where a gap track's comes from.
  A third of the library has none, in the XML as in the tags.
- **Duration** is measured from the audio: an MP4 file's movie header,
  or an MP3's frame count from its Xing header, its bytes over its
  bitrate, or a walk of its frames when it has neither.  A gap track
  takes the XML's figure.
- **Plain playlists** come from the XML too, which is their only source.

The 2014 XML is the only listening history there is.  The current Apple
Music library holds a little more, but the files were set aside for years
after the loss, so not much; and its database is hard to read without
scripting the app.  Plays since 2014 are written off.

**Matching is already done.**  The recovery linked each 2014 track to its
surviving file (`itunes_track_file`), and `move_log` says where each file
went.  Where a file's slot went to a better copy,
`move_plan.displaced_by` names the winner, which inherits the listening
data.

**Date added for tracks the 2014 XML lacks,** such as the partner's
library, comes from the file's modification time.  Import time would make
thousands of tracks look added on one day and swamp any "recently added"
playlist.

## Albums

An album is a directory in the library tree: `Artist/Album/`, or
`Compilations/Album/` for a various-artists album, whose artist is null.
The recovery already did the hard grouping work, and the layout records
its results.

- **`Unknown Album` directories** hold tracks with no album.  They become
  tracks with a null `albumId`, not an album called "Unknown Album".
- **Order** within an album comes from the disc and track number tags,
  falling back to the filename.  Disc numbers become entries in `discs`.
- **A track number is kept as its position,** so a number no file or gap
  has is a missing track.  A track with no number, or whose number an
  earlier track took, goes after the disc's highest number.  A disc also
  keeps the total its tracks declare (TRCK's `n/total`, or the catalog's
  track count), so missing tracks at its end can be told too.  Both are
  for album completeness (#40); until the protocol carries them,
  `trackIds` lists only the tracks there are.
- **An album filed as one directory per disc,** like *Mellon Collie* or
  *Tango N' Vectif*, imports as separate albums for now.  Merging them is a
  later edit.

## Damaged albums

The import takes `Undamaged` and `Damaged` by default.  Each lost track
becomes a gap: a Track with no `blobId`, built from the 2014 catalog's
entry, holding its place on the album.  An album with a gap, or with a
missing track, is incomplete (#40), and incomplete albums are for smart
playlists to leave out, not for the import to.

Damaged was kept out at first, so the library would hold only albums
that could be played through.  Once albums could say whether they were
complete, that stopped being a reason (#41).  What does stay out is
`Redundant`: Damaged's partial copies of albums jcap had whole, moved
there by `bin/set-aside-redundant` so they don't sit beside the whole
album as incomplete duplicates.

## Original tags

Tags Juke doesn't model yet stay in the files, which are never modified.
The import also stores every file's tags as found, in one unindexed
column per track, so adding a property later is a query over stored data
rather than a rescan: `juke backfill` fills a property added since the
import from that column, and from the catalog for what the tags don't
say, matching a track to the catalog by title, album and number.  Genre
was the first (#43).  Duration (#44) is the exception that does open the
files, since length isn't in the tags; the headers it needs are small.

- **Server-side only.**  It is not a JMAP property; clients have no use
  for it.
- **A record of the file as imported.**  It never changes.  After a
  metadata flush, the served file's tags differ from it, by design.
- **Format-aware.**  MP3 files have ID3 frames and M4A files have MP4
  atoms; the column records which.
- **Binary frames become stubs:** frame type, MIME type, size and SHA-256
  instead of the bytes.  That covers embedded album art, so later artwork
  work can see which files have art and whether an album shares one image
  without rescanning.

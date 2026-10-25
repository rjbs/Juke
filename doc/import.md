# Import

The import builds a new Juke database from two inputs: the files in the
library tree, and an XML export of the current Apple Music library.  It
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
- **The Apple Music XML wins** for what the listener did: rating, play
  count and last played.  These are almost never in the tags.  Date added
  and the Favorite flag come from there too, if [object
  types](types.md) gains them; both are proposed, not decided.
- **Plain playlists** come from the XML too, which is their only source.

Use an export of the current library, not the 2014 one.  Ratings and
counts have moved on since 2014.

**Matching XML entries to files** goes by path first.  Where the current
library points at a file in one of the old trees, the recovery's
`move_log` maps that path to the file's place in the new library.  Where
a file's slot went to a better copy, `move_plan.displaced_by` names the
winner, which inherits the listening data.  Anything left is matched by
metadata and duration, as the recovery's `match-tracks` did.

## Albums

An album is a directory in the library tree: `Artist/Album/`, or
`Compilations/Album/` for a various-artists album, whose artist is null.
The recovery already did the hard grouping work, and the layout records
its results.

- **`Unknown Album` directories** hold tracks with no album.  They become
  tracks with a null `albumId`, not an album called "Unknown Album".
- **Order** within an album comes from the disc and track number tags,
  falling back to the filename.  Disc numbers become entries in `discs`.
- **An album filed as one directory per disc,** like *Mellon Collie* or
  *Tango N' Vectif*, imports as separate albums for now.  Merging them is a
  later edit.

## Damaged albums

The first real import takes `Undamaged` only.  The sample includes two
damaged albums so the prototype can import them too.  Each lost track
becomes a gap: a Track with no `blobId`, built from the 2014 catalog's
entry, holding its place on the album.  The 2014 catalog is the source
here because the current library may not list the lost tracks at all.

## Original tags

Tags Juke doesn't model yet stay in the files, which are never modified.
The import also stores every file's tags as found, in one unindexed
column per track, so adding a property later is a query over stored data
rather than a rescan.

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

# Server storage

Storage follows [jiggle](https://github.com/rjbs/jiggle/blob/main/STORAGE.md)'s
split between immutable originals and metadata kept separately.  The
difference: the file a client downloads is a derived blob, original audio
plus current tags, so tag edits reach clients without touching originals.

## Originals

- **Originals are immutable** and stored under opaque, hash-derived names,
  not a human-browsable tree.  Browsing happens through the app, not the file
  tree.
- **Track ids are opaque and minted at ingest.**  They are not derived from
  the audio.  An earlier draft made the id a hash of the audio frames, on the
  theory that a later import of the same file would land on the same Track.
  That is not the import we expect.  The likely second import is a backup
  found years later, holding tracks lost in the interim; its files may be a
  different rip or a different bitrate of the same recording, so the bytes
  would not match anyway.  What that import needs is to identify the artist,
  album and track by metadata and duration, and then decide which copy to
  keep.  The recovery tools in `bin/` are the prototype of that: see
  [recovery](recovery.md).
- **Audio digest is a property of the Track**, a hash of the audio frames
  only, skipping ID3v2 at the front and ID3v1 at the end.  Retagging leaves
  it unchanged.  It catches byte-identical duplicates at ingest and verifies
  that a stored original is still the bytes that were ingested.

## Served blobs

- **blobId = hash(track id, metadata version, recipe version).**  Any change
  to the served bytes, from a metadata edit or a change in how tags are
  written, mints a new blobId.  A metadata edit is an ordinary Track update
  in which `blobId` is one of the changed properties.
- **Blobs are synthesized at request time:** a generated ID3v2 tag followed
  by the original's audio bytes.  Nothing extra is stored, and range requests
  are offset arithmetic.
- **Synthesis must be deterministic,** byte-for-byte reproducible from its
  inputs: fixed frame order, fixed padding, no timestamps.
- **Superseded blobIds return not-found.**  The server keeps no old metadata
  to rebuild them.  A client caught mid-download refetches the Track and gets
  the new blobId.

## Formats other than MP3

The starting library is 42,066 MP3 files, 2,747 M4A files and 4 MP2 files.
No FLAC.

Tag writing is straightforward for MP3.  M4A keeps metadata inside the
container, and Go has no mature library for writing it.  Until that
changes, an edit to an M4A track is kept in the database but never reaches
the served file: the track stays dirty, its `blobId` is unchanged, and clients
go on getting the original tags.  The edit is not lost, only unflushed.

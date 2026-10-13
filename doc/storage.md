# Server storage

Storage follows [jiggle](https://github.com/rjbs/jiggle/blob/main/STORAGE.md)'s
split between immutable originals and metadata kept separately.  The
difference: the file a client downloads is a derived blob, original audio
plus current tags, so tag edits reach clients without touching originals.

## Originals

- **Originals are immutable** and stored under opaque, hash-derived names,
  not a human-browsable tree.  Browsing happens through the app, not the file
  tree.
- **Track id = hash of the audio frames only**, skipping ID3v2 at the front
  and ID3v1 at the end.  Retagging leaves the id unchanged, and identical
  audio with different tags shows up as a duplicate.

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

## Open question: formats other than MP3

Tag synthesis is straightforward for MP3.  M4A and FLAC keep metadata inside
the container, which needs a different approach.  Check what the library
actually contains before deciding whether this matters.

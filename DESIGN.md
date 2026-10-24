# Juke: design notes

Juke is a JMAP server in front of an existing MP3 library on home NAS
storage, plus an iOS player app that syncs from it.  The aim is to replace
Apple Music for managing a personal library, which it has handled steadily
worse over the last decade.

These notes were drafted in October 2026 before any code existed.  They
record decisions, not implementation.  When the code and these notes
disagree, fix one of them.

## Scope

- **Server:** serves track metadata, playlists and audio blobs over JMAP;
  syncs play counts, recently played and ratings; evaluates smart playlists.
- **iOS client:** a self-contained player with its own in-app library.
- **Out of scope:** appearing in the system Music app or its media library.
  Not needed, and third-party apps can't add to it anyway.

## Protocol: JMAP

JMAP carries everything: object sync through state strings and `/changes`,
and file transfer through the standard blob upload and download endpoints.
Writing a JMAP server for new data types is treated as a solved problem.

- **Object types:** Track, Album and Playlist, each its own JMAP type with
  its own state.  See [object types](doc/types.md).
- **Updates are last-write-wins** via plain `/set` updates.  Play counts,
  last played and ratings are almost always written by one device.  When two
  devices do collide, the cost is a count off by one, which isn't worth an
  event-sourcing pipeline.
- **Blobs are immutable.**  RFC 8620 treats blob data as immutable, so a
  blobId must always name the same bytes.  Changing metadata therefore
  changes a Track's `blobId`.  See [storage](doc/storage.md).

## The rest of the design

- [Object types](doc/types.md): Track, Album and Playlist, kept small;
  albums have discs, and own their tracks.
- [Server storage](doc/storage.md): immutable originals, audio-only track
  ids, and blobs synthesized on request from original audio plus current
  tags.
- [Metadata database](doc/database.md): SQLite as the precious part, modseq
  for JMAP state, snapshot backups, and export as an escape hatch.
- [Playlists](doc/playlists.md): smart playlists as stored `Track/query`
  calls, evaluated on the server.
- [iOS client](doc/ios-client.md): a Spotify-shaped model, pinned downloads
  first and a streaming cache later.
- [Sample library](doc/sample-library.md): a 1 GB slice of the real library
  for prototyping, chosen to cover the hard cases.
- [Recovery and reorganization](doc/recovery.md): putting the library back
  together before Juke exists, and the rules for doing it without losing
  anything else.

## Open questions

These are also tracked as Loose Threads (`lt list`).

- [ ] Which audio formats are in the library?  M4A or FLAC would need
      container-aware tag synthesis.
- [ ] Where does "pinned for offline" live?  Local per-device state is
      simplest; a server-side property would show which devices hold what.
- [ ] For smart playlists: lazy evaluation on request, or recompute on
      write?
- [ ] Which properties `Track/query` can filter and sort on.  The first
      property sets are proposed in [object types](doc/types.md).
- [ ] How the initial import reads existing ID3 tags into the database.

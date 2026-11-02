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
  It runs long-term on a Linux host on the home Tailscale network, and is
  developed and tested on macOS.  It is written in Go.  If a move to Rust
  ever makes sense, it would be a rewrite against these specs; the data is
  portable either way.
- **iOS client:** a self-contained player with its own in-app library.
- **Out of scope:** appearing in the system Music app or its media library.
  Not needed, and third-party apps can't add to it anyway.

## Protocol: JMAP

JMAP carries everything: object sync through state strings and `/changes`,
and file transfer through the standard blob upload and download endpoints.
Writing a JMAP server for new data types is treated as a solved problem.

- **Object types:** Track, Album, Playlist and SmartPlaylist, each its own
  JMAP type with its own state.  See [object types](doc/types.md).  They
  live under the capability `https://semiotic.systems/jmap/juke`.
- **Updates are last-write-wins** via plain `/set` updates.  Play counts,
  last played and ratings are almost always written by one device.  When two
  devices do collide, the cost is a count off by one, which isn't worth an
  event-sourcing pipeline.
- **Blobs are immutable.**  RFC 8620 treats blob data as immutable, so a
  blobId must always name the same bytes.  Changing metadata therefore
  changes a Track's `blobId`.  See [storage](doc/storage.md).

## The rest of the design

- [Object types](doc/types.md): Track, Album, Playlist and SmartPlaylist,
  kept small;
  albums have discs, and own their tracks.
- [Server storage](doc/storage.md): immutable originals, audio-only track
  ids, and blobs synthesized on request from original audio plus current
  tags.
- [Metadata database](doc/database.md): SQLite as the precious part, modseq
  for JMAP state, snapshot backups, and export as an escape hatch.
- [Playlists](doc/playlists.md): smart playlists as saved searches, run
  through `Track/query` whenever a client wants the list.
- [iOS client](doc/ios-client.md): a Spotify-shaped model, pinned downloads
  first and a streaming cache later.
- [Running the server](doc/server.md): building, importing the sample, and
  serving it; what works and what doesn't yet.
- [Import](doc/import.md): a fresh database from the files and the 2014
  iTunes XML; tags win for the music, the XML for listening data.
- [Sample library](doc/sample-library.md): a 1 GB slice of the real library
  for prototyping, chosen to cover the hard cases.
- [Recovery and reorganization](doc/recovery.md): putting the library back
  together before Juke exists, and the rules for doing it without losing
  anything else.

## Open questions

None of the questions these notes started with are still open.  Smaller
ones are tracked as Loose Threads (`lt list`).

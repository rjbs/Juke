# Juke

Juke is a [JMAP](https://jmap.io/) server in front of a personal music
library, and a player for macOS and iOS that syncs from it.  The server
holds the tracks, albums, and playlists; a client keeps its own copy, and
keeps it current with JMAP's `/changes`, as a mail client does its
mailboxes.  Audio and artwork travel as JMAP blobs.

Nothing here is mail, so the server defines its own data model: a
capability, `https://semiotic.systems/jmap/juke`, with four object types
under it.

- [Object types](types.md): Track, Album, Playlist, and SmartPlaylist, with
  the properties of each, and where their methods differ from the standard
  ones of [RFC 8620](https://www.rfc-editor.org/rfc/rfc8620).

The server is written in Go, on [Gizmo](https://github.com/rjbs/gizmo), a
library for building JMAP servers.  Its source, and the rest of its design
notes, are [on GitHub](https://github.com/rjbs/Juke).

The player is written in Swift, and does its syncing with
[Jinx](https://semiotic.systems/Jinx/), a library that keeps a local copy of
JMAP data current with a server.

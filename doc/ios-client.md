# iOS client

The app keeps its library in its own sandbox, so it counts as app data in
iPhone Storage, not as Music.  It's a Spotify-shaped model, offline downloads
plus a streaming cache, built in two phases with downloads first.

## Phases

1. **Pinned downloads.**  Make the local file set match a list of blobIds:
   diff, download what's missing, delete what's gone, play from local URLs.
   No eviction handling, and no playing while downloading.
2. **Streaming cache.**  Play before a download finishes.  AVPlayer has to
   fetch the authenticated JMAP blob URL with range requests so seeking
   works.  This likely means an `AVAssetResourceLoaderDelegate` that fills in
   byte ranges as they arrive and writes the finished file to the cache.

## Decisions

- **Pinning is device-local.**  Which tracks are pinned lives only in the
  app's own storage, not on the server, so the server cannot say which
  devices hold what.  Jinx's blob store keeps the pin state.
- **Storage location:** one store in Application Support for both pinned and
  cached files, told apart by a flag, with app-managed LRU eviction under a
  size cap.  Avoid Caches, where the OS purges on its own schedule.
- **Backups:** set do-not-back-up on all audio files.
- **Playback integration:** lock-screen and remote controls come from the
  audio session and `MPNowPlayingInfoCenter`.
- **Album art** is a blob like audio: `artworkBlobId` and `artworkType`
  on Album, and on Track only where the track's own picture differs,
  resolved track first; the type fills the download URL and names the
  cached file.  It comes through the same blob store, so a kept album keeps
  its art, and is decoded at the size it's drawn and kept in memory.
  The playing track's art goes to the now-playing info; see
  [artwork](artwork.md).
- **Sync loop:** Track `/changes` first, then playlists, then the smart
  playlists' queries; apply blobId changes by refetching.  See
  [playlists](playlists.md) for why that order matters.
- **Smart playlists.**  Each one's rule is a Jinx query subscription,
  so its tracks are the server's latest answer while online and the
  last answer while not.  Keeping one offline pins whatever its result
  holds, following the result as it changes.  The app shows one page
  of a result, the server's query limit, and says so when the rule
  matched more; the lists one plays are capped well under that.
- **An artist's albums are ordered** oldest or newest first by
  `Album.released`, the undated last either way, or by title either
  way; the choice is kept in user defaults.  Album headers show the
  date as far as it is known, the year alone when that is all.
- **Shuffle is a three-way setting**, off, tracks or albums, kept in
  the play queue and in user defaults across launches, and shown to the
  system's shuffle control as off, items or collections.  Shuffled
  albums keeps each album whole and in order, finishing the one playing
  before the next.  Every Play button is the same plain play icon that
  plays in the shuffle as set; its menu, a long press on iOS and the
  chevron on macOS, plays shuffled by track or by album and sets the
  shuffle so.  The player bar's shuffle control is a menu of the three.
- **The App owns the connection** (`AppModel`): the server, the open
  library and the player.  The window only shows them, so on macOS
  closing the one window, or reopening it with ⌘0, neither stops
  playback nor resyncs.  Menu commands reach the sidebar through a
  focused scene value, so they are disabled when no library window is
  key.
- **Rules are edited in a sheet** (`SmartPlaylistEditor`), the same
  tree of groups and conditions, sort and limits as the web page's
  editor, with the tracks the draft matches now beneath, re-run as it
  is edited.  The model (`Rules.swift`) reads any stored filter into
  the tree and compiles it back, so a rule written on either editor
  opens in the other.  Unlike a plain playlist's edits, which go to
  the mirror and sync later, a rule is previewed and saved through the
  server directly (`Library+Rules.swift`): the preview is a query the
  mirror doesn't keep, and the server's refusals (a rule that won't
  run, a loop, deleting a playlist another rule names) belong in the
  editor, not in a sync that reverts later.  A save waits for the
  mirror to catch up before the editor closes, so the playlist can be
  shown at once.  So editing needs the server; everything else about
  smart playlists works offline.

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
- **Sync loop:** Track `/changes` first, then playlists; apply blobId changes
  by refetching.  See [playlists](playlists.md) for why that order matters.

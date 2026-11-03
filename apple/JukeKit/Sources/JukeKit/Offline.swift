import Foundation
import Observation

/// What this device keeps available offline: albums and playlists of
/// both kinds, whose tracks' audio and art are downloaded and pinned out
/// of the cache's reach.
///
/// The choice is the device's own and never reaches the server.  It is
/// kept as album and playlist ids; the audio wanted follows from them
/// through the library, so a playlist that gains a track gains a
/// download, a smart playlist whose rule now matches other tracks
/// fetches those and releases the rest, and a track whose audio is
/// replaced has its new audio fetched and its old audio released.  Released audio is unpinned, not
/// deleted: it stays in the cache until the cache needs the room.
@MainActor @Observable
public final class Offline {
  public private(set) var albumIds: Set<String> = []
  public private(set) var playlistIds: Set<String> = []
  public private(set) var smartPlaylistIds: Set<String> = []

  /// Files wanted on the device, audio and art, and how many are here.
  public private(set) var wanted = 0
  public private(set) var present = 0
  /// The last download failure, cleared by the next success.
  public private(set) var failure: String?

  @ObservationIgnored private unowned let library: Library
  @ObservationIgnored private let file: URL
  /// Blob ids this device has pinned, so it knows what to release.
  @ObservationIgnored private var pinned: Set<String> = []
  /// The wanted blob ids in download order: albums and playlists in
  /// title order, and each one's tracks in play order.
  @ObservationIgnored private var order: [String] = []
  @ObservationIgnored private var desired: Set<String> = []
  /// Each wanted blob's media type, where the track says.
  @ObservationIgnored private var types: [String: String] = [:]
  /// Wanted blobs known to be here and pinned.  Observed, since each
  /// album's progress is read from it.
  private var ready: Set<String> = []
  @ObservationIgnored private var worker: Task<Void, Never>?

  struct Saved: Codable {
    var albums: [String] = []
    var playlists: [String] = []
    /// Optional, since a file from before smart playlists has no such key.
    var smartPlaylists: [String]?
    var pinned: [String] = []
  }

  init(library: Library, file: URL) {
    self.library = library
    self.file = file
    if let data = try? Data(contentsOf: file), let saved = try? JSONDecoder().decode(Saved.self, from: data) {
      albumIds = Set(saved.albums)
      playlistIds = Set(saved.playlists)
      smartPlaylistIds = Set(saved.smartPlaylists ?? [])
      pinned = Set(saved.pinned)
    }
  }

  // MARK: - Choosing

  public func isKept(_ album: Album) -> Bool { albumIds.contains(album.id) }
  public func isKept(_ playlist: Playlist) -> Bool { playlist.serverId.map(playlistIds.contains) ?? false }
  public func isKept(_ playlist: SmartPlaylist) -> Bool { smartPlaylistIds.contains(playlist.id) }

  public func keep(_ album: Album, _ keep: Bool) {
    if keep { albumIds.insert(album.id) } else { albumIds.remove(album.id) }
    save()
    reconcile()
  }

  public func keep(_ playlist: Playlist, _ keep: Bool) {
    // One not yet synced has no id to remember it by; KeepButton is
    // disabled for it.  -- claude, 2026-10-25
    guard let id = playlist.serverId else { return }
    if keep { playlistIds.insert(id) } else { playlistIds.remove(id) }
    save()
    reconcile()
  }

  public func keep(_ playlist: SmartPlaylist, _ keep: Bool) {
    if keep { smartPlaylistIds.insert(playlist.id) } else { smartPlaylistIds.remove(playlist.id) }
    save()
    reconcile()
  }

  /// How many of these tracks' audio files are here, of those that have
  /// audio at all.
  public func progress(of tracks: [Track]) -> (present: Int, wanted: Int) {
    let blobs = Set(tracks.compactMap(\.blobId))
    return (blobs.intersection(ready).count, blobs.count)
  }

  // MARK: - Reconciling

  /// Recomputes the audio wanted from what is kept and what the library
  /// now holds, releases what is no longer wanted, and starts fetching
  /// what is missing.  The library calls this whenever its index changes.
  func reconcile() {
    var order: [String] = []
    var seen: Set<String> = []
    var types: [String: String] = [:]
    let albums = albumIds.compactMap { library.albums[$0] }
      .sorted { $0.title.localizedStandardCompare($1.title) == .orderedAscending }
    let playlists = library.playlists.filter(isKept)
    let smartPlaylists = library.smartPlaylists.filter(isKept)
    for tracks in albums.map({ library.tracks(of: $0).flatMap(\.self) }) + playlists.map({ library.tracks(of: $0) })
      + smartPlaylists.map({ library.tracks(of: $0) }) {
      for track in tracks {
        if let art = library.artworkBlobId(for: track), seen.insert(art).inserted { order.append(art) }
        guard let blob = track.blobId else { continue }
        if let type = track.blobType { types[blob] = type }
        if seen.insert(blob).inserted { order.append(blob) }
      }
    }
    self.order = order
    self.types = types
    desired = seen
    ready.formIntersection(desired)
    wanted = desired.count
    present = ready.count

    let released = pinned.subtracting(desired)
    if !released.isEmpty {
      pinned.subtract(released)
      save()
      let blobs = library.blobs
      Task { try? await blobs.unpin(Array(released)) }
    }

    if worker == nil, ready.count < desired.count {
      worker = Task { [weak self] in await self?.work() }
    }
  }

  /// Fetches wanted files one at a time until all of them are here,
  /// waiting and retrying after a failure.
  private func work() async {
    defer { worker = nil }
    let blobs = library.blobs
    while !Task.isCancelled {
      guard let next = order.first(where: { !ready.contains($0) }) else { return }
      // Audio kept on an earlier launch is already here; checking is one
      // row, where a download call also rescans the whole cache.
      // -- claude, 2026-10-25
      if let record = try? await blobs.status(next), record.isComplete, record.isPinned {
        ready.insert(next)
        if pinned.insert(next).inserted { save() }
        present = ready.count
        continue
      }
      do {
        try await blobs.download(next, contentType: types[next], pin: true)
        failure = nil
      } catch {
        failure = error.localizedDescription
        try? await Task.sleep(for: .seconds(30))
        continue
      }
      // The album or playlist may have been released while this file
      // was coming; then the pin just made is undone.  -- claude, 2026-10-25
      if desired.contains(next) {
        ready.insert(next)
        pinned.insert(next)
        save()
      } else {
        try? await blobs.unpin([next])
      }
      present = ready.count
    }
  }

  func stop() {
    worker?.cancel()
    worker = nil
  }

  private func save() {
    let saved = Saved(albums: albumIds.sorted(), playlists: playlistIds.sorted(), smartPlaylists: smartPlaylistIds.sorted(), pinned: pinned.sorted())
    try? JSONEncoder().encode(saved).write(to: file, options: .atomic)
  }
}

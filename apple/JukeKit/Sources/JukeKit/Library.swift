import Foundation
import Jinx
import JinxJSON
import JinxPatch
import JinxStore
import JinxWire
import Observation

public let jukeCapability = "https://semiotic.systems/jmap/juke"

public enum LibraryError: Error, LocalizedError {
  case noAccount
  case noAudio

  public var errorDescription: String? {
    switch self {
    case .noAccount: "The server has no Juke account."
    case .noAudio: "That track has no audio."
    }
  }
}

/// The music library: a Jinx mirror of the server's Tracks, Albums and
/// Playlists, kept in sync in the background, with an in-memory index of
/// all of them for the views.
///
/// The index is built once from the store after the first sync, then
/// kept current from Jinx's change stream, so a sync that changes three
/// tracks costs three decodes, not forty-five thousand.
@MainActor @Observable
public final class Library {
  public enum Phase: Equatable, Sendable {
    /// The first sync, with how many tracks have arrived so far.
    case syncing(fetched: Int, total: Int?)
    case ready
    case failed(String)
  }

  public private(set) var phase: Phase = .syncing(fetched: 0, total: nil)
  public private(set) var tracks: [String: Track] = [:]
  public private(set) var albums: [String: Album] = [:]
  /// Albums by artist, then title; compilations last.
  public private(set) var albumList: [Album] = []
  /// Playlists by title.
  public private(set) var playlists: [Playlist] = []
  /// The last sync failure, if the most recent sync failed.
  public private(set) var syncFailure: String?

  @ObservationIgnored let engine: SyncEngine<GRDBSyncStore>
  @ObservationIgnored let blobs: BlobStore<GRDBSyncStore>
  /// What this device keeps available offline.
  @ObservationIgnored public private(set) var offline: Offline!
  @ObservationIgnored private var ids: [LocalKey: String] = [:]
  @ObservationIgnored private var tasks: [Task<Void, Never>] = []

  init(engine: SyncEngine<GRDBSyncStore>, blobs: BlobStore<GRDBSyncStore>, directory: URL) {
    self.engine = engine
    self.blobs = blobs
    self.offline = Offline(library: self, file: directory.appending(path: "offline.json"))
  }

  /// Opens the library for the server at `server`, keeping its mirror and
  /// cached audio under `directory`.
  ///
  /// The server names the account, and the name is remembered in
  /// `directory`, so once a library has been opened it opens again with
  /// the server unreachable.  Only a first open needs the server.
  public static func open(server: URL, directory: URL) async throws -> Library {
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    let config = URLSessionConfiguration.default
    config.timeoutIntervalForRequest = 15
    let transport = JMAPClient(
      sessionUrl: server.appending(path: ".well-known/jmap"),
      authenticator: NoAuthentication(),
      urlSession: URLSession(configuration: config)
    )
    let accountId = try await account(from: transport, rememberedIn: directory.appending(path: "account"))
    let store = try GRDBSyncStore(path: directory.appending(path: "library.sqlite").path)
    let engine = try SyncEngine(
      configuration: SyncConfiguration(
        accountId: accountId,
        types: [
          TypeDescriptor(name: "Track", capability: jukeCapability, blobProperties: ["blobId"]),
          TypeDescriptor(name: "Album", capability: jukeCapability, syncAfter: ["Track"]),
          TypeDescriptor(name: "Playlist", capability: jukeCapability, syncAfter: ["Track"]),
        ]
      ),
      transport: transport,
      store: store
    )
    let blobs = await engine.makeBlobStore(.init(
      directory: directory.appending(path: "audio"),
      cacheLimit: 2 * 1024 * 1024 * 1024
    ))
    // Earlier versions linked audio here under names with extensions;
    // Jinx now names its files that way itself.  -- claude, 2026-10-25
    try? FileManager.default.removeItem(at: directory.appending(path: "playable"))
    return Library(engine: engine, blobs: blobs, directory: directory)
  }

  private static func account(from transport: JMAPClient, rememberedIn file: URL) async throws -> String {
    do {
      let session = try await transport.fetchSession()
      guard let id = session.primaryAccountId(for: jukeCapability) else { throw LibraryError.noAccount }
      try Data(id.utf8).write(to: file)
      return id
    } catch {
      guard let saved = try? String(contentsOf: file, encoding: .utf8), !saved.isEmpty else { throw error }
      return saved
    }
  }

  /// The directory a server's library lives in, under Application
  /// Support: one per server, so pointing the app at another server
  /// never mixes two libraries.
  public static func directory(for server: URL) throws -> URL {
    let base = try FileManager.default.url(
      for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true
    )
    let name = [server.host() ?? "server", server.port.map(String.init)].compactMap(\.self).joined(separator: "-")
    return base.appending(path: "Juke").appending(path: name)
  }

  /// Builds the index and keeps syncing until `stop`.  With a mirror from
  /// an earlier launch, the library is ready at once and syncs behind;
  /// otherwise the first sync comes first, with progress in `phase`.
  public func start() {
    let engine = engine
    tasks.append(Task { [weak self] in
      // A sync failure is recorded in the engine's status rather than
      // thrown from its run loop, so it's read from there. -- claude, 2026-10-25
      while !Task.isCancelled {
        if let status = try? await engine.currentStatus() {
          self?.syncFailure = status.lastFailure
        }
        try? await Task.sleep(for: .seconds(2))
      }
    })
    tasks.append(Task { [weak self] in
      if (try? await engine.serverState(of: "Track")) != nil {
        do {
          try await self?.loadAll()
        } catch {
          self?.phase = .failed(error.localizedDescription)
          return
        }
        await self?.run()
        return
      }
      let progress = Task { [weak self] in
        while !Task.isCancelled {
          if let status = try? await engine.currentStatus(), let t = status.initialSync["Track"] {
            self?.phase = .syncing(fetched: t.fetched, total: t.total)
          }
          try? await Task.sleep(for: .milliseconds(250))
        }
      }
      do {
        try await engine.sync()
      } catch {
        // A first sync that fails shows whatever arrived, and the run
        // loop keeps retrying. -- claude, 2026-10-25
        self?.syncFailure = error.localizedDescription
      }
      progress.cancel()
      await progress.value
      do {
        try await self?.loadAll()
      } catch {
        self?.phase = .failed(error.localizedDescription)
        return
      }
      await self?.run()
    })
  }

  /// Follows changes into the index and runs the sync loop.  Subscribing
  /// only after loadAll skips the change sets loadAll has already read;
  /// nothing else writes before the views appear.  -- claude, 2026-10-25
  private func run() async {
    let engine = engine
    let stream = await engine.changes()
    tasks.append(Task { [weak self] in
      for await changeSet in stream {
        try? await self?.apply(changeSet)
      }
    })
    phase = .ready
    await engine.run(pollInterval: .seconds(60))
  }

  public func stop() {
    tasks.forEach { $0.cancel() }
    tasks = []
    offline.stop()
  }

  /// Asks for a sync now, as after the app comes to the foreground.
  public func syncNow() {
    Task { await engine.requestSync() }
  }

  // MARK: - Reading

  public func tracks(of album: Album) -> [[Track]] {
    album.discs.map { $0.trackIds.compactMap { tracks[$0] } }
  }

  public func tracks(of playlist: Playlist) -> [Track] {
    playlist.trackIds.compactMap { tracks[$0] }
  }

  // MARK: - Writing

  /// Sets a track's rating: 1 to 5 in half steps, or nil for none.
  public func setRating(_ rating: Double?, on track: Track) async throws {
    try await engine.update(track.key, patch: PatchObject(["rating": rating.map { .number($0) } ?? .null]))
  }

  /// Counts one play of a track, now.
  public func recordPlay(of track: Track) async throws {
    let now = formatUTCDate(Date())
    try await engine.update(track.key) { value in
      guard case .object(var o) = value else { return value }
      o["playCount"] = .number(Double((o["playCount"]?.intValue ?? 0) + 1))
      o["lastPlayed"] = .string(now)
      return .object(o)
    }
  }

  // MARK: - Editing playlists
  //
  // Each edit is made to the mirror and reaches the server on the next
  // sync, so it works offline.  Edits that work on positions take them
  // in the playlist's trackIds, and apply them to the mirror's current
  // value, not the caller's copy, which may be a sync behind.

  /// Makes a playlist, empty or with tracks, and returns its key.
  @discardableResult
  public func createPlaylist(titled title: String, tracks: [Track] = []) async throws -> LocalKey {
    try await engine.create("Playlist", .object([
      "title": .string(title),
      "trackIds": .array(tracks.map { .string($0.id) }),
    ]))
  }

  public func rename(_ playlist: Playlist, to title: String) async throws {
    try await engine.update(playlist.key, patch: PatchObject(["title": .string(title)]))
  }

  public func delete(_ playlist: Playlist) async throws {
    try await engine.destroy(playlist.key)
  }

  /// Adds tracks to the end of a playlist.
  public func add(_ tracks: [Track], to playlist: Playlist) async throws {
    let added = tracks.map(\.id)
    try await editTrackIds(of: playlist) { $0 + added }
  }

  /// Removes the tracks at these positions.
  public func removeTracks(at positions: [Int], from playlist: Playlist) async throws {
    let doomed = Set(positions)
    try await editTrackIds(of: playlist) { ids in
      ids.indices.filter { !doomed.contains($0) }.map { ids[$0] }
    }
  }

  /// Moves the tracks at these positions so they come before the track
  /// now at `destination`, or to the end if it's past the last track:
  /// the meaning SwiftUI's onMove gives its offsets.
  public func moveTracks(at positions: [Int], to destination: Int, in playlist: Playlist) async throws {
    try await editTrackIds(of: playlist) { ids in
      Self.move(ids, from: positions, to: destination)
    }
  }

  nonisolated static func move(_ ids: [String], from positions: [Int], to destination: Int) -> [String] {
    let moving = Set(positions.filter(ids.indices.contains))
    let rest = ids.indices.filter { !moving.contains($0) }.map { ids[$0] }
    let at = min(destination, ids.count) - moving.count { $0 < destination }
    var out = rest
    out.insert(contentsOf: moving.sorted().map { ids[$0] }, at: at)
    return out
  }

  private func editTrackIds(of playlist: Playlist, _ edit: @Sendable @escaping ([String]) -> [String]) async throws {
    try await engine.update(playlist.key) { value in
      guard case .object(var o) = value else { return value }
      let ids = o["trackIds"]?.arrayValue?.compactMap(\.stringValue) ?? []
      o["trackIds"] = .array(edit(ids).map { .string($0) })
      return .object(o)
    }
  }

  // MARK: - Audio

  /// A track's audio as a local file that AVFoundation can play,
  /// downloading it into the cache if it isn't there.
  ///
  /// AVFoundation won't open a local file without an extension, and Jinx
  /// names a cached file by the type it's given, so the track's type is
  /// passed along.  A track synced before the server sent its type has
  /// its first bytes read instead; the second download call only renames
  /// the file. -- claude, 2026-10-25
  public func audioFile(for track: Track) async throws -> URL {
    guard let blobId = track.blobId else { throw LibraryError.noAudio }
    if let type = track.blobType {
      return try await blobs.download(blobId, contentType: type)
    }
    let cached = try await blobs.download(blobId)
    let file = try FileHandle(forReadingFrom: cached)
    defer { try? file.close() }
    let head = try file.read(upToCount: 12) ?? Data()
    return try await blobs.download(blobId, contentType: audioType(of: head) ?? "audio/mpeg")
  }

  // MARK: - Index

  private func loadAll() async throws {
    var tracks: [String: Track] = [:]
    var albums: [String: Album] = [:]
    var playlists: [Playlist] = []
    for object in try await engine.objects(type: "Track") {
      if let t = Track(object) { tracks[t.id] = t; ids[t.key] = t.id }
    }
    for object in try await engine.objects(type: "Album") {
      if let a = Album(object) { albums[a.id] = a; ids[a.key] = a.id }
    }
    for object in try await engine.objects(type: "Playlist") {
      if let p = Playlist(object) { playlists.append(p) }
    }
    self.tracks = tracks
    self.albums = albums
    self.playlists = playlists
    sortAlbums()
    sortPlaylists()
    offline.reconcile()
  }

  private func apply(_ changeSet: ChangeSet) async throws {
    for (type, changes) in changeSet.types {
      let keys = changes.created + changes.updated
      let objects = keys.isEmpty ? [] : try await engine.objects(keys: keys)
      switch type {
      case "Track":
        for o in objects { if let t = Track(o) { tracks[t.id] = t; ids[t.key] = t.id } }
        for k in changes.destroyed { if let id = ids.removeValue(forKey: k) { tracks[id] = nil } }
      case "Album":
        for o in objects { if let a = Album(o) { albums[a.id] = a; ids[a.key] = a.id } }
        for k in changes.destroyed { if let id = ids.removeValue(forKey: k) { albums[id] = nil } }
        sortAlbums()
      case "Playlist":
        var byKey = Dictionary(uniqueKeysWithValues: playlists.map { ($0.key, $0) })
        for o in objects { if let p = Playlist(o) { byKey[p.key] = p } }
        for k in changes.destroyed { byKey[k] = nil }
        playlists = Array(byKey.values)
        sortPlaylists()
      default:
        break
      }
    }
    offline.reconcile()
  }

  private func sortAlbums() {
    albumList = albums.values.sorted { a, b in
      if (a.artist == nil) != (b.artist == nil) { return b.artist == nil }
      let byArtist = (a.artist ?? "").localizedStandardCompare(b.artist ?? "")
      if byArtist != .orderedSame { return byArtist == .orderedAscending }
      return a.title.localizedStandardCompare(b.title) == .orderedAscending
    }
  }

  private func sortPlaylists() {
    playlists.sort { $0.title.localizedStandardCompare($1.title) == .orderedAscending }
  }
}

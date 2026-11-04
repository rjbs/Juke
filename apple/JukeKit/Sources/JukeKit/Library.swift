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
/// Playlists of both kinds, kept in sync in the background, with an
/// in-memory index of all of them for the views.  Each smart playlist's
/// rule runs as a query subscription, so its tracks are the server's
/// latest answer while online and the last one while not.
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
  /// Albums by artist (ignoring a leading article), then title;
  /// compilations last.
  public private(set) var albumList: [Album] = []
  /// Album artists by name, ignoring a leading article.
  public private(set) var artistList: [Artist] = []
  /// Goes up whenever any track changes, for views that sort or filter
  /// all of them.
  public private(set) var trackRevision = 0
  /// Playlists by title.
  public private(set) var playlists: [Playlist] = []
  /// Smart playlists by title.
  public private(set) var smartPlaylists: [SmartPlaylist] = []
  /// Each smart playlist's last result, by playlist id.  A playlist
  /// whose rule has never run has none.
  public private(set) var smartResults: [String: SmartPlaylistResult] = [:]
  /// The last sync failure, if the most recent sync failed.
  public private(set) var syncFailure: String?

  @ObservationIgnored let engine: SyncEngine<GRDBSyncStore>
  @ObservationIgnored let blobs: BlobStore<GRDBSyncStore>
  @ObservationIgnored let accountId: String
  /// The server, for the few calls made outside the mirror: running and
  /// saving a rule being edited.  See Library+Rules.swift.
  @ObservationIgnored let transport: JMAPClient
  @ObservationIgnored var session: Session?
  /// What this device keeps available offline.
  @ObservationIgnored public private(set) var offline: Offline!
  /// Album art as images, decoded once per size and kept.
  @ObservationIgnored public private(set) var images: ArtworkCache!
  @ObservationIgnored private var ids: [LocalKey: String] = [:]
  @ObservationIgnored private var tasks: [Task<Void, Never>] = []

  init(engine: SyncEngine<GRDBSyncStore>, blobs: BlobStore<GRDBSyncStore>, accountId: String, transport: JMAPClient, directory: URL) {
    self.engine = engine
    self.blobs = blobs
    self.accountId = accountId
    self.transport = transport
    self.offline = Offline(library: self, file: directory.appending(path: "offline.json"))
    self.images = ArtworkCache(library: self)
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
    try? Data(server.absoluteString.utf8).write(to: directory.appending(path: "server"))
    let store = try GRDBSyncStore(path: directory.appending(path: "library.sqlite").path)
    let engine = try SyncEngine(
      configuration: SyncConfiguration(
        accountId: accountId,
        types: [
          TypeDescriptor(name: "Track", capability: jukeCapability, blobProperties: ["blobId", "artworkBlobId"]),
          TypeDescriptor(name: "Album", capability: jukeCapability, syncAfter: ["Track"], blobProperties: ["artworkBlobId"]),
          TypeDescriptor(name: "Playlist", capability: jukeCapability, syncAfter: ["Track"]),
          TypeDescriptor(name: "SmartPlaylist", capability: jukeCapability, syncAfter: ["Track", "Playlist"]),
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
    return Library(engine: engine, blobs: blobs, accountId: accountId, transport: transport, directory: directory)
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

  /// A smart playlist's tracks as the server last answered, in the
  /// rule's order; none until its rule has run once.
  public func tracks(of playlist: SmartPlaylist) -> [Track] {
    (smartResults[playlist.id]?.trackIds ?? []).compactMap { tracks[$0] }
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

  // MARK: - Artwork

  /// The art to show for a track: its own, where its file's picture
  /// differs from its album's, else its album's.
  public func artwork(for track: Track) -> Artwork? {
    track.artwork ?? track.albumId.flatMap { albums[$0] }?.artwork
  }

  /// An image as a local file, downloading it into the cache if it isn't
  /// there.  Its type names the cached file, as a track's does; ImageIO
  /// reads the bytes for what they are either way.
  func artworkFile(_ art: Artwork) async throws -> URL {
    try await blobs.download(art.blobId, contentType: art.type)
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
    var smartPlaylists: [SmartPlaylist] = []
    for object in try await engine.objects(type: "SmartPlaylist") {
      if let s = SmartPlaylist(object) { smartPlaylists.append(s) }
    }
    self.tracks = tracks
    trackRevision += 1
    self.albums = albums
    self.playlists = playlists
    self.smartPlaylists = smartPlaylists
    sortAlbums()
    sortPlaylists()
    try await declareSubscriptions()
    for playlist in smartPlaylists { try await loadResult(of: playlist.subscriptionId) }
    offline.reconcile()
  }

  /// Keeps one query subscription per smart playlist: declares each
  /// one's rule, which costs nothing for a rule already declared, and
  /// drops the subscriptions of playlists that are gone.
  private func declareSubscriptions() async throws {
    let wanted = Set(smartPlaylists.map(\.subscriptionId))
    for playlist in smartPlaylists {
      try await engine.subscribe(playlist.subscription(accountId: accountId))
    }
    for subscription in try await engine.subscriptions() where !wanted.contains(subscription.id) {
      guard let id = SmartPlaylist.playlistId(ofSubscription: subscription.id) else { continue }
      try await engine.unsubscribe(subscription.id)
      smartResults[id] = nil
    }
  }

  /// Reads a smart playlist's result from its subscription, if it has
  /// run.
  private func loadResult(of subscriptionId: String) async throws {
    guard let id = SmartPlaylist.playlistId(ofSubscription: subscriptionId),
          let result = try await engine.result(of: subscriptionId) else { return }
    smartResults[id] = SmartPlaylistResult(trackIds: result.ids, total: result.total)
  }

  private func apply(_ changeSet: ChangeSet) async throws {
    for (type, changes) in changeSet.types {
      let keys = changes.created + changes.updated
      let objects = keys.isEmpty ? [] : try await engine.objects(keys: keys)
      switch type {
      case "Track":
        for o in objects { if let t = Track(o) { tracks[t.id] = t; ids[t.key] = t.id } }
        for k in changes.destroyed { if let id = ids.removeValue(forKey: k) { tracks[id] = nil } }
        trackRevision += 1
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
      case "SmartPlaylist":
        var byKey = Dictionary(uniqueKeysWithValues: smartPlaylists.map { ($0.key, $0) })
        for o in objects { if let s = SmartPlaylist(o) { byKey[s.key] = s } }
        for k in changes.destroyed { byKey[k] = nil }
        smartPlaylists = Array(byKey.values)
        sortPlaylists()
        try await declareSubscriptions()
        // A rule that names another smart playlist is unchanged when
        // that one's rule changes, and no track has moved either, so
        // the engine wouldn't run it again on its own.  -- claude, 2026-11-02
        try await engine.refreshSubscriptions(of: "Track")
      default:
        break
      }
    }
    for id in changeSet.subscriptions {
      try await loadResult(of: id)
    }
    offline.reconcile()
  }

  private func sortAlbums() {
    albumList = albums.values.sorted { a, b in
      if (a.artist == nil) != (b.artist == nil) { return b.artist == nil }
      let (x, y) = (sortKey(a.artist ?? "", droppingArticle: true), sortKey(b.artist ?? "", droppingArticle: true))
      if x != y { return x < y }
      return a.title.localizedStandardCompare(b.title) == .orderedAscending
    }
    artistList = Self.artists(of: albums.values)
  }

  private func sortPlaylists() {
    playlists.sort { $0.title.localizedStandardCompare($1.title) == .orderedAscending }
    smartPlaylists.sort { $0.title.localizedStandardCompare($1.title) == .orderedAscending }
  }
}

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
  @ObservationIgnored let playable: URL
  /// What this device keeps available offline.
  @ObservationIgnored public private(set) var offline: Offline!
  @ObservationIgnored private var ids: [LocalKey: String] = [:]
  @ObservationIgnored private var tasks: [Task<Void, Never>] = []

  init(engine: SyncEngine<GRDBSyncStore>, blobs: BlobStore<GRDBSyncStore>, directory: URL) {
    self.engine = engine
    self.blobs = blobs
    self.playable = directory.appending(path: "playable")
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
    try FileManager.default.createDirectory(at: directory.appending(path: "playable"), withIntermediateDirectories: true)
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

  // MARK: - Audio

  /// A track's audio as a local file that AVFoundation can play,
  /// downloading it into the cache if it isn't there.
  ///
  /// The cache names files by blob id alone, and AVFoundation won't open a
  /// local file without an extension, whatever type it's told.  So this
  /// returns a symlink to the cached file whose extension follows the
  /// file's first bytes; the cache's own record of the type is the
  /// application/octet-stream that Jinx asked for.  A link left dangling
  /// by eviction is remade on the next call. -- claude, 2026-10-25
  public func audioFile(for track: Track) async throws -> URL {
    guard let blobId = track.blobId else { throw LibraryError.noAudio }
    let cached = try await blobs.download(blobId)
    let file = try FileHandle(forReadingFrom: cached)
    defer { try? file.close() }
    let head = try file.read(upToCount: 12) ?? Data()
    let ext = audioType(of: head) == "audio/mp4" ? "m4a" : "mp3"
    let link = playable.appending(path: "\(blobId).\(ext)")
    let fm = FileManager.default
    if (try? fm.destinationOfSymbolicLink(atPath: link.path)) != cached.path {
      try? fm.removeItem(at: link)
      try fm.createSymbolicLink(at: link, withDestinationURL: cached)
    }
    return link
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
      if let p = Playlist(object) { playlists.append(p); ids[p.key] = p.id }
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
        var byId = Dictionary(uniqueKeysWithValues: playlists.map { ($0.id, $0) })
        for o in objects { if let p = Playlist(o) { byId[p.id] = p; ids[p.key] = p.id } }
        for k in changes.destroyed { if let id = ids.removeValue(forKey: k) { byId[id] = nil } }
        playlists = Array(byId.values)
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

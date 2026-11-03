import Foundation
import Jinx
import JinxJSON
import JinxStore

// The Juke object types, as doc/types.md defines them, decoded from the
// JSON that Jinx mirrors.  An object that fails to decode is left out
// rather than shown half-formed; the server makes every required
// property present.

/// A recording, usually on an album.  A track with no `blobId` is a gap:
/// a track an album had whose audio is lost.  It is shown but can't be
/// played.
public struct Track: Identifiable, Hashable, Sendable {
  public var key: LocalKey
  public var id: String
  public var title: String
  public var artist: String?
  public var albumId: String?
  public var rating: Double?
  public var blobId: String?
  /// The audio's media type.  Nil in a mirror synced before the server
  /// sent it, until the track next changes.
  public var blobType: String?
  public var playCount: Int
  public var lastPlayed: Date?
  public var dateAdded: Date?
  /// The track's own art, only where it differs from its album's; see
  /// `Library.artwork(for:)` for the art to show.
  public var artwork: Artwork?

  public var isPlayable: Bool { blobId != nil }

  public init?(_ object: SyncedObject) {
    self.init(key: object.key, id: object.id, value: object.value)
  }

  public init?(key: LocalKey, id: String?, value v: JSONValue) {
    guard let id, let title = v["title"]?.stringValue else { return nil }
    self.key = key
    self.id = id
    self.title = title
    self.artist = v["artist"]?.stringValue
    self.albumId = v["albumId"]?.stringValue
    self.rating = v["rating"]?.doubleValue
    self.blobId = v["blobId"]?.stringValue
    self.blobType = v["blobType"]?.stringValue
    self.playCount = v["playCount"]?.intValue ?? 0
    self.lastPlayed = v["lastPlayed"]?.stringValue.flatMap(parseUTCDate)
    self.dateAdded = v["dateAdded"]?.stringValue.flatMap(parseUTCDate)
    self.artwork = Artwork(v)
  }
}

/// An image of album art: a blob, and its media type, which the
/// download URL's `type` wants and which names the cached file.
public struct Artwork: Hashable, Sendable {
  public var blobId: String
  /// Nil in a mirror synced before the server sent it.
  public var type: String?

  init?(_ v: JSONValue) {
    guard let blobId = v["artworkBlobId"]?.stringValue else { return nil }
    self.blobId = blobId
    self.type = v["artworkType"]?.stringValue
  }
}

/// One disc of an album: its tracks in play order, and a title for sets
/// that name their discs.
public struct Disc: Hashable, Sendable {
  public var title: String?
  public var trackIds: [String]
}

public struct Album: Identifiable, Hashable, Sendable {
  public var key: LocalKey
  public var id: String
  public var title: String
  /// Nil for a compilation.
  public var artist: String?
  public var discs: [Disc]
  /// The album's art.
  public var artwork: Artwork?

  public var trackIds: [String] { discs.flatMap(\.trackIds) }

  public init?(_ object: SyncedObject) {
    self.init(key: object.key, id: object.id, value: object.value)
  }

  public init?(key: LocalKey, id: String?, value v: JSONValue) {
    guard let id, let title = v["title"]?.stringValue,
          let discs = v["discs"]?.arrayValue else { return nil }
    self.key = key
    self.id = id
    self.title = title
    self.artist = v["artist"]?.stringValue
    self.artwork = Artwork(v)
    self.discs = discs.map { d in
      Disc(title: d["title"]?.stringValue, trackIds: d["trackIds"]?.arrayValue?.compactMap(\.stringValue) ?? [])
    }
  }
}

/// A plain playlist.  A track may appear on it more than once.
///
/// A playlist made on this device has no server id until it syncs, so
/// it's identified by its local key.
public struct Playlist: Identifiable, Hashable, Sendable {
  public var key: LocalKey
  /// The server's id, or nil for a playlist not yet synced.
  public var serverId: String?
  public var title: String
  public var trackIds: [String]

  public var id: LocalKey { key }

  public init?(_ object: SyncedObject) {
    self.init(key: object.key, id: object.id, value: object.value)
  }

  public init?(key: LocalKey, id: String?, value v: JSONValue) {
    guard let title = v["title"]?.stringValue else { return nil }
    self.key = key
    self.serverId = id
    self.title = title
    self.trackIds = v["trackIds"]?.arrayValue?.compactMap(\.stringValue) ?? []
  }
}

/// A smart playlist: a saved search, as doc/playlists.md describes.  It
/// has no track list of its own; the library runs its rule as a Jinx
/// query subscription and keeps what the server answers.  Rules are
/// read here, never written.
public struct SmartPlaylist: Identifiable, Hashable, Sendable {
  public var key: LocalKey
  public var id: String
  public var title: String
  /// The rule, as `Track/query` takes it; nil matches every track.
  public var filter: JSONValue?
  public var sort: [JSONValue]
  public var limit: Int?
  public var limitBytes: Int?
  public var limitSeconds: Int?

  public init?(_ object: SyncedObject) {
    self.init(key: object.key, id: object.id, value: object.value)
  }

  public init?(key: LocalKey, id: String?, value v: JSONValue) {
    guard let id, let title = v["title"]?.stringValue else { return nil }
    self.key = key
    self.id = id
    self.title = title
    self.filter = v["filter"].flatMap { $0 == .null ? nil : $0 }
    self.sort = v["sort"]?.arrayValue ?? []
    self.limit = v["limit"]?.intValue
    self.limitBytes = v["limitBytes"]?.intValue
    self.limitSeconds = v["limitSeconds"]?.intValue
  }

  static let subscriptionPrefix = "smart:"

  /// The id of the subscription that runs this playlist's rule.
  public var subscriptionId: String { Self.subscriptionPrefix + id }

  /// The playlist id a subscription id names, if it is one of these.
  static func playlistId(ofSubscription id: String) -> String? {
    id.hasPrefix(subscriptionPrefix) ? String(id.dropFirst(subscriptionPrefix.count)) : nil
  }

  /// The rule as a query subscription: the filter, sort and caps as the
  /// server stores them.  The rule is copied rather than named through
  /// `inSmartPlaylist`, so that any change to it redefines the
  /// subscription and the engine runs it afresh.  -- claude, 2026-11-02
  func subscription(accountId: String) -> QuerySubscription {
    var arguments: [String: JSONValue] = [:]
    if let limitBytes { arguments["limitBytes"] = .number(Double(limitBytes)) }
    if let limitSeconds { arguments["limitSeconds"] = .number(Double(limitSeconds)) }
    return QuerySubscription(
      id: subscriptionId, accountId: accountId, type: "Track",
      filter: filter, sort: sort, limit: limit, arguments: arguments
    )
  }
}

/// What the server last answered for a smart playlist's rule.
public struct SmartPlaylistResult: Hashable, Sendable {
  /// The matching tracks, in the rule's order.
  public var trackIds: [String]
  /// How many tracks the rule matched in all.  More than `trackIds`
  /// holds when the server capped its answer at one page.
  public var total: Int?
}

func parseUTCDate(_ s: String) -> Date? {
  try? Date(s, strategy: .iso8601)
}

func formatUTCDate(_ d: Date) -> String {
  d.formatted(.iso8601)
}

/// The media type of audio that starts with `head`, from its first dozen
/// bytes: MPEG audio (MP3 or MP2), with or without an ID3 tag before it,
/// or MPEG-4 audio (M4A).  Nil for anything else.
func audioType(of head: Data) -> String? {
  let b = [UInt8](head.prefix(12))
  if b.starts(with: Array("ID3".utf8)) { return "audio/mpeg" }
  if b.count >= 2, b[0] == 0xFF, b[1] & 0xE0 == 0xE0 { return "audio/mpeg" }
  if b.count >= 8, Array(b[4..<8]) == Array("ftyp".utf8) { return "audio/mp4" }
  return nil
}

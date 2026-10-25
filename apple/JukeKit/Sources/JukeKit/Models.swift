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
  public var playCount: Int
  public var lastPlayed: Date?
  public var dateAdded: Date?

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
    self.playCount = v["playCount"]?.intValue ?? 0
    self.lastPlayed = v["lastPlayed"]?.stringValue.flatMap(parseUTCDate)
    self.dateAdded = v["dateAdded"]?.stringValue.flatMap(parseUTCDate)
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
    self.discs = discs.map { d in
      Disc(title: d["title"]?.stringValue, trackIds: d["trackIds"]?.arrayValue?.compactMap(\.stringValue) ?? [])
    }
  }
}

/// A plain playlist.  A track may appear on it more than once.
public struct Playlist: Identifiable, Hashable, Sendable {
  public var key: LocalKey
  public var id: String
  public var title: String
  public var trackIds: [String]

  public init?(_ object: SyncedObject) {
    self.init(key: object.key, id: object.id, value: object.value)
  }

  public init?(key: LocalKey, id: String?, value v: JSONValue) {
    guard let id, let title = v["title"]?.stringValue else { return nil }
    self.key = key
    self.id = id
    self.title = title
    self.trackIds = v["trackIds"]?.arrayValue?.compactMap(\.stringValue) ?? []
  }
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

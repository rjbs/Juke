import Foundation

/// A name as it sorts: case and accents folded away, and for an artist,
/// a leading "The", "A" or "An" dropped, so The Beatles sort under B.
public func sortKey(_ name: String, droppingArticle: Bool = false) -> String {
  var key = name.folding(options: [.caseInsensitive, .diacriticInsensitive, .widthInsensitive], locale: nil)
    .trimmingCharacters(in: .whitespaces)
  if droppingArticle {
    for article in ["the ", "a ", "an "] where key.hasPrefix(article) && key.count > article.count {
      key = String(key.dropFirst(article.count)).trimmingCharacters(in: .whitespaces)
      break
    }
  }
  return key
}

/// An album artist, with their albums.  Compilations, having no artist,
/// belong to no one.
public struct Artist: Identifiable, Hashable, Sendable {
  public var name: String
  /// Their albums' ids, by title.
  public var albumIds: [String]
  public var id: String { name }
}

/// The ways the song list can be sorted.
public enum SongOrder: String, CaseIterable, Identifiable, Sendable {
  case title = "Title"
  case artist = "Artist"
  case recentlyAdded = "Recently Added"
  case recentlyPlayed = "Recently Played"
  case rating = "Rating"

  public var id: Self { self }
}

extension Library {
  /// The artists of these albums, by name, each with their albums by
  /// title.
  nonisolated public static func artists(of albums: some Sequence<Album>) -> [Artist] {
    var byName: [String: [Album]] = [:]
    for album in albums {
      if let artist = album.artist { byName[artist, default: []].append(album) }
    }
    return byName.map { name, albums in
      Artist(name: name, albumIds: albums.sorted { sortKey($0.title) < sortKey($1.title) }.map(\.id))
    }
    .sorted { (sortKey($0.name, droppingArticle: true), $0.name) < (sortKey($1.name, droppingArticle: true), $1.name) }
  }

  /// Tracks in the given order.  Ties, and tracks with nothing to sort
  /// on (never played, unrated), fall back to title order.
  nonisolated public static func sorted(_ tracks: [Track], by order: SongOrder) -> [Track] {
    // Keys are computed once per track, not once per comparison: with
    // tens of thousands of tracks, folding strings inside the comparator
    // takes seconds.  -- claude, 2026-10-25
    typealias Keyed = (track: Track, title: String, artist: String)
    let keyed: [Keyed] = tracks.map { ($0, sortKey($0.title), sortKey($0.artist ?? "", droppingArticle: true)) }
    func byTitle(_ a: Keyed, _ b: Keyed) -> Bool {
      (a.title, a.track.id) < (b.title, b.track.id)
    }
    func descending<T: Comparable>(_ value: @escaping (Track) -> T?) -> (Keyed, Keyed) -> Bool {
      { a, b in
        switch (value(a.track), value(b.track)) {
        case let (x?, y?) where x != y: x > y
        case (_?, nil): true
        case (nil, _?): false
        default: byTitle(a, b)
        }
      }
    }
    let sorted: [Keyed] = switch order {
    case .title: keyed.sorted(by: byTitle)
    case .artist:
      // A track with no artist goes last, not first.
      keyed.sorted {
        ($0.track.artist == nil ? 1 : 0, $0.artist, $0.title, $0.track.id) < ($1.track.artist == nil ? 1 : 0, $1.artist, $1.title, $1.track.id)
      }
    case .recentlyAdded: keyed.sorted(by: descending(\.dateAdded))
    case .recentlyPlayed: keyed.sorted(by: descending(\.lastPlayed))
    case .rating: keyed.sorted(by: descending(\.rating))
    }
    return sorted.map(\.track)
  }
}

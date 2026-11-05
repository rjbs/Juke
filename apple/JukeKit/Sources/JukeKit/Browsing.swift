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

/// A genre as the tracks name it, gathered without regard to case or
/// accents, so "Rock", "rock" and "ROCK" are one genre, called by
/// whichever spelling the most tracks use.
public struct Genre: Identifiable, Hashable, Sendable {
  public var name: String
  /// What its spellings have in common: each one's `sortKey`.
  public var key: String
  public var trackCount: Int
  public var id: String { key }
}

/// The ways an artist's albums can be ordered.  By date, the undated
/// come last whichever way the dates run.
public enum AlbumOrder: String, CaseIterable, Identifiable, Sendable {
  case oldestFirst = "Oldest First"
  case newestFirst = "Newest First"
  case titleAscending = "Title, A to Z"
  case titleDescending = "Title, Z to A"

  public var id: String { rawValue }

  public func sorted(_ albums: [Album]) -> [Album] {
    switch self {
    case .oldestFirst: albums.sorted { byDate($0, $1, ascending: true) }
    case .newestFirst: albums.sorted { byDate($0, $1, ascending: false) }
    case .titleAscending: albums.sorted { byTitle($0, $1) }
    case .titleDescending: albums.sorted { byTitle($1, $0) }
    }
  }

  private func byTitle(_ a: Album, _ b: Album) -> Bool {
    a.title.localizedStandardCompare(b.title) == .orderedAscending
  }

  private func byDate(_ a: Album, _ b: Album, ascending: Bool) -> Bool {
    switch (a.released, b.released) {
    case (nil, nil): byTitle(a, b)
    case (nil, _): false
    case (_, nil): true
    case (let x?, let y?): x == y ? byTitle(a, b) : (ascending ? x < y : x > y)
    }
  }
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

  /// The genres these tracks name, by name.  Tracks with no genre are
  /// in none.
  nonisolated public static func genres(of tracks: some Sequence<Track>) -> [Genre] {
    // A library has tens of thousands of tracks but only a hundred or so
    // spellings of genre, so spellings are counted first and each one is
    // folded once.  -- claude, 2026-11-05
    var spellings: [String: Int] = [:]
    for track in tracks {
      if let genre = track.genre { spellings[genre, default: 0] += 1 }
    }
    var byKey: [String: [(spelling: String, count: Int)]] = [:]
    for (spelling, count) in spellings {
      let key = sortKey(spelling)
      if !key.isEmpty { byKey[key, default: []].append((spelling, count)) }
    }
    return byKey.map { key, spellings in
      // The most used spelling, and of equally used ones, the first in
      // code point order, so the name doesn't change from run to run.
      let name = spellings.max { ($0.count, $1.spelling) < ($1.count, $0.spelling) }!.spelling
      return Genre(name: name, key: key, trackCount: spellings.reduce(0) { $0 + $1.count })
    }
    .sorted { ($0.key, $0.name) < ($1.key, $1.name) }
  }

  /// A genre's tracks among these: those on albums first, the albums in
  /// the given order and each one's tracks in its own order, then the
  /// tracks on no album, by title.  Only the tracks in the genre are
  /// included, so an album of mixed genres contributes only some.
  nonisolated public static func tracks(of genre: Genre, among tracks: some Collection<Track>, albums: [Album]) -> [Track] {
    var keys: [String: String] = [:]
    let matching = tracks.filter { track in
      guard let spelling = track.genre else { return false }
      if keys[spelling] == nil { keys[spelling] = sortKey(spelling) }
      return keys[spelling] == genre.key
    }
    var loose = Dictionary(uniqueKeysWithValues: matching.map { ($0.id, $0) })
    var ordered: [Track] = []
    let albumIds = Set(matching.compactMap(\.albumId))
    for album in albums where albumIds.contains(album.id) {
      for id in album.discs.flatMap(\.trackIds) {
        if let track = loose.removeValue(forKey: id) { ordered.append(track) }
      }
    }
    return ordered + sorted(Array(loose.values), by: .title)
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

/// A stretch of consecutive items that share a key, as a list of tracks
/// shown by album has one per album it passes through.
public struct Run<Element, Key: Equatable> {
  /// Where the run begins in the whole list.
  public var start: Int
  /// The key the run's items share, or nil for an item with none, which
  /// is a run of its own.
  public var key: Key?
  public var elements: [Element]
}

/// Splits items into runs of consecutive ones with the same key, in
/// order: the same key after a different one starts a new run, so the
/// runs read in the list's own order.
public func runs<Element, Key: Equatable>(of items: [Element], by key: (Element) -> Key?) -> [Run<Element, Key>] {
  var out: [Run<Element, Key>] = []
  for (i, item) in items.enumerated() {
    let k = key(item)
    if let k, out.last?.key == k {
      out[out.count - 1].elements.append(item)
    } else {
      out.append(Run(start: i, key: k, elements: [item]))
    }
  }
  return out
}

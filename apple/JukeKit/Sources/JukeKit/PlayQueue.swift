import Foundation

/// The tracks the player goes through, in the order it plays them, and
/// where it is among them; and the shuffle and repeat settings that
/// decide what comes next.
///
/// This is only bookkeeping, with no audio, so it can be tested alone.
/// Randomness comes from a generator the caller gives, for the same
/// reason, and so does `discs`, which tells an album's track order for
/// shuffling by album.
public struct PlayQueue: Sendable {
  public enum Repeat: Sendable, CaseIterable {
    case off, all, one
  }

  /// How the queue is ordered: as queued, tracks at random, or albums
  /// at random with each album whole and in order.
  public enum Shuffle: String, Sendable, CaseIterable {
    case off, tracks, albums
  }

  /// A track in the queue.  The same track can be queued twice, so each
  /// entry has its own id, which shuffling and unshuffling go by.
  public struct Entry: Identifiable, Hashable, Sendable {
    public let id: Int
    public let track: Track
    /// Where it was queued from, such as an album's or playlist's
    /// title, for showing what came from where.
    public let source: String?
  }

  /// An album's track ids in order, or nil for an album not known.
  public typealias Discs = (String) -> [String]?

  /// The entries in play order.
  public private(set) var entries: [Entry] = []
  /// The playing entry's place in `entries`.
  public private(set) var index: Int?
  public var repeatMode: Repeat = .off
  public private(set) var shuffle: Shuffle = .off
  public var isShuffled: Bool { shuffle != .off }

  /// While shuffled, the order to go back to when shuffle is turned off.
  private var unshuffled: [Entry]?
  private var nextID = 0

  public init() {}

  public var current: Track? { index.map { entries[$0].track } }
  /// The entries after the current one, in the order they'll play.
  public var upcoming: ArraySlice<Entry> { entries[((index ?? -1) + 1)...] }

  /// The upcoming entries in runs that share a source, each with the
  /// entries' places in `entries`, for a list headed by where each run
  /// came from.
  public var upcomingBySource: [(source: String?, entries: [(index: Int, entry: Entry)])] {
    var runs: [(source: String?, entries: [(index: Int, entry: Entry)])] = []
    for (i, entry) in zip(upcoming.indices, upcoming) {
      if let last = runs.last, last.source == entry.source {
        runs[runs.count - 1].entries.append((i, entry))
      } else {
        runs.append((entry.source, [(i, entry)]))
      }
    }
    return runs
  }

  /// Replaces the queue with `tracks`, starting at the one at `position`.
  /// Tracks with no audio are dropped.  The chosen track plays first
  /// whatever the shuffle; after it, shuffled tracks come at random, and
  /// shuffled albums finish the chosen track's album, then the others.
  public mutating func start<G: RandomNumberGenerator>(
    _ tracks: [Track], at position: Int, from source: String? = nil, discs: Discs = { _ in nil }, using rng: inout G
  ) {
    guard tracks.indices.contains(position), tracks[position].isPlayable else { return }
    entries = makeEntries(tracks.filter(\.isPlayable), from: source)
    let first = tracks[..<position].filter(\.isPlayable).count
    if isShuffled {
      unshuffled = entries
      var rest = entries
      let chosen = rest.remove(at: first)
      entries = [chosen] + arranged(rest, after: chosen, discs: discs, using: &rng)
      index = 0
    } else {
      index = first
    }
  }

  /// Sets the shuffle.  Turning it on, or changing it, rearranges every
  /// entry after the current one; turning it off puts them back as they
  /// were queued.
  public mutating func setShuffle<G: RandomNumberGenerator>(_ mode: Shuffle, discs: Discs = { _ in nil }, using rng: inout G) {
    if mode == shuffle { return }
    shuffle = mode
    if mode == .off {
      guard let original = unshuffled else { return }
      let playing = index.map { entries[$0].id }
      entries = original
      unshuffled = nil
      index = playing.flatMap { id in entries.firstIndex { $0.id == id } }
    } else {
      unshuffled = unshuffled ?? entries
      let split = (index ?? -1) + 1
      let current = index.map { entries[$0] }
      entries = Array(entries[..<split]) + arranged(Array(entries[split...]), after: current, discs: discs, using: &rng)
    }
  }

  /// Puts tracks right after the current one, or starts with them if
  /// nothing is playing.  Returns true if they're to be played now.
  public mutating func playNext(_ tracks: [Track], from source: String? = nil) -> Bool {
    let new = makeEntries(tracks.filter(\.isPlayable), from: source)
    guard !new.isEmpty else { return false }
    guard let i = index else {
      entries = new
      unshuffled = isShuffled ? new : nil
      index = 0
      return true
    }
    let playing = entries[i].id
    entries.insert(contentsOf: new, at: i + 1)
    if let at = unshuffled?.firstIndex(where: { $0.id == playing }) {
      unshuffled!.insert(contentsOf: new, at: at + 1)
    }
    return false
  }

  /// Puts tracks at the end, or starts with them if nothing is playing.
  /// Returns true if they're to be played now.
  public mutating func add(_ tracks: [Track], from source: String? = nil) -> Bool {
    let new = makeEntries(tracks.filter(\.isPlayable), from: source)
    guard !new.isEmpty else { return false }
    guard index != nil else { return playNext(tracks, from: source) }
    entries += new
    unshuffled? += new
    return false
  }

  /// Where to go after the current entry.  At the natural end of a track,
  /// repeat-one plays it again; skipping moves on regardless.  With
  /// repeat-all, the end of the queue wraps to its start.  Nil means
  /// stop.
  public func following(skipping: Bool) -> Int? {
    guard let i = index else { return nil }
    if repeatMode == .one && !skipping { return i }
    if i + 1 < entries.count { return i + 1 }
    return repeatMode == .all ? 0 : nil
  }

  /// Where going back from the current entry leads: the one before, or
  /// with repeat-all the last from the first.
  public func preceding() -> Int? {
    guard let i = index else { return nil }
    if i > 0 { return i - 1 }
    return repeatMode == .all && entries.count > 1 ? entries.count - 1 : nil
  }

  public mutating func move(to i: Int) {
    guard entries.indices.contains(i) else { return }
    index = i
  }

  /// The tracks grouped by album, the groups in random order and each in
  /// its album's order: a shuffle that keeps albums whole.  Tracks on no
  /// album, or on one `discs` doesn't know, stand alone.
  public static func albumShuffled<G: RandomNumberGenerator>(
    _ tracks: [Track], discs: Discs, using rng: inout G
  ) -> [Track] {
    albumShuffled(tracks, track: { $0 }, first: nil, discs: discs, using: &rng)
  }

  /// The entries after `current`, in the order the shuffle wants them.
  private func arranged<G: RandomNumberGenerator>(_ rest: [Entry], after current: Entry?, discs: Discs, using rng: inout G) -> [Entry] {
    switch shuffle {
    case .off: rest
    case .tracks: rest.shuffled(using: &rng)
    case .albums: Self.albumShuffled(rest, track: \.track, first: current?.track.albumId, discs: discs, using: &rng)
    }
  }

  /// Groups items by their track's album, orders each group as its album
  /// goes, and shuffles the groups, the album `first` (the one playing,
  /// so it finishes) ahead of the rest.
  private static func albumShuffled<T, G: RandomNumberGenerator>(
    _ items: [T], track: (T) -> Track, first: String?, discs: Discs, using rng: inout G
  ) -> [T] {
    var groups: [[T]] = []
    var groupOf: [String: Int] = [:]
    for item in items {
      if let album = track(item).albumId, discs(album) != nil {
        if let g = groupOf[album] {
          groups[g].append(item)
        } else {
          groupOf[album] = groups.count
          groups.append([item])
        }
      } else {
        groups.append([item])
      }
    }
    var ordered = groups.map { group -> [T] in
      guard let album = track(group[0]).albumId, let order = discs(album) else { return group }
      let rank = Dictionary(order.enumerated().map { ($1, $0) }, uniquingKeysWith: { a, _ in a })
      return group.sorted { (rank[track($0).id] ?? .max) < (rank[track($1).id] ?? .max) }
    }
    ordered.shuffle(using: &rng)
    if let first, let g = ordered.firstIndex(where: { track($0[0]).albumId == first && discs(first) != nil }) {
      ordered.insert(ordered.remove(at: g), at: 0)
    }
    return ordered.flatMap(\.self)
  }

  private mutating func makeEntries(_ tracks: [Track], from source: String?) -> [Entry] {
    tracks.map { track in
      defer { nextID += 1 }
      return Entry(id: nextID, track: track, source: source)
    }
  }
}

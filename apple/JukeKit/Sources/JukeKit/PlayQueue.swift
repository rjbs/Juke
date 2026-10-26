import Foundation

/// The tracks the player goes through, in the order it plays them, and
/// where it is among them; and the shuffle and repeat settings that
/// decide what comes next.
///
/// This is only bookkeeping, with no audio, so it can be tested alone.
/// Randomness comes from a generator the caller gives, for the same
/// reason.
public struct PlayQueue: Sendable {
  public enum Repeat: Sendable, CaseIterable {
    case off, all, one
  }

  /// A track in the queue.  The same track can be queued twice, so each
  /// entry has its own id, which shuffling and unshuffling go by.
  public struct Entry: Identifiable, Hashable, Sendable {
    public let id: Int
    public let track: Track
  }

  /// The entries in play order.
  public private(set) var entries: [Entry] = []
  /// The playing entry's place in `entries`.
  public private(set) var index: Int?
  public var repeatMode: Repeat = .off
  public var isShuffled: Bool { unshuffled != nil }

  /// While shuffled, the order to go back to when shuffle is turned off.
  private var unshuffled: [Entry]?
  private var nextID = 0

  public init() {}

  public var current: Track? { index.map { entries[$0].track } }
  /// The entries after the current one, in the order they'll play.
  public var upcoming: ArraySlice<Entry> { entries[((index ?? -1) + 1)...] }

  /// Replaces the queue with `tracks`, starting at the one at `position`.
  /// Tracks with no audio are dropped.  If shuffle is on, the rest are
  /// shuffled; the chosen track still plays first.
  public mutating func start<G: RandomNumberGenerator>(_ tracks: [Track], at position: Int, using rng: inout G) {
    guard tracks.indices.contains(position), tracks[position].isPlayable else { return }
    entries = makeEntries(tracks.filter(\.isPlayable))
    let first = tracks[..<position].filter(\.isPlayable).count
    if isShuffled {
      unshuffled = entries
      var rest = entries
      let chosen = rest.remove(at: first)
      entries = [chosen] + rest.shuffled(using: &rng)
      index = 0
    } else {
      index = first
    }
  }

  /// Turns shuffle on, shuffling every entry after the current one, or
  /// off, putting them back as they were.
  public mutating func setShuffle<G: RandomNumberGenerator>(_ on: Bool, using rng: inout G) {
    if on == isShuffled { return }
    if on {
      unshuffled = entries
      let split = (index ?? -1) + 1
      entries = Array(entries[..<split]) + entries[split...].shuffled(using: &rng)
    } else if let original = unshuffled {
      let playing = index.map { entries[$0].id }
      entries = original
      unshuffled = nil
      index = playing.flatMap { id in entries.firstIndex { $0.id == id } }
    }
  }

  /// Puts tracks right after the current one, or starts with them if
  /// nothing is playing.  Returns true if they're to be played now.
  public mutating func playNext(_ tracks: [Track]) -> Bool {
    let new = makeEntries(tracks.filter(\.isPlayable))
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
  public mutating func add(_ tracks: [Track]) -> Bool {
    let new = makeEntries(tracks.filter(\.isPlayable))
    guard !new.isEmpty else { return false }
    guard index != nil else { return playNext(tracks) }
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
  /// album, or on one `discs` doesn't know, stand alone.  `discs` gives
  /// an album's track ids in order.
  public static func albumShuffled<G: RandomNumberGenerator>(
    _ tracks: [Track], discs: (String) -> [String]?, using rng: inout G
  ) -> [Track] {
    var groups: [[Track]] = []
    var groupOf: [String: Int] = [:]
    for track in tracks {
      if let album = track.albumId, discs(album) != nil {
        if let g = groupOf[album] {
          groups[g].append(track)
        } else {
          groupOf[album] = groups.count
          groups.append([track])
        }
      } else {
        groups.append([track])
      }
    }
    let ordered = groups.map { group -> [Track] in
      guard let album = group[0].albumId, let order = discs(album) else { return group }
      let rank = Dictionary(order.enumerated().map { ($1, $0) }, uniquingKeysWith: { a, _ in a })
      return group.sorted { (rank[$0.id] ?? .max) < (rank[$1.id] ?? .max) }
    }
    return ordered.shuffled(using: &rng).flatMap(\.self)
  }

  private mutating func makeEntries(_ tracks: [Track]) -> [Entry] {
    tracks.map { track in
      defer { nextID += 1 }
      return Entry(id: nextID, track: track)
    }
  }
}

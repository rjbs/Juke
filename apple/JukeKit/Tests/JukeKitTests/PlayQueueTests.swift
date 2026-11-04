import Foundation
import JinxJSON
import JinxStore
import Testing
@testable import JukeKit

/// A seeded generator (SplitMix64), so shuffles in tests are repeatable.
struct SeededGenerator: RandomNumberGenerator {
  var state: UInt64
  mutating func next() -> UInt64 {
    state &+= 0x9E37_79B9_7F4A_7C15
    var z = state
    z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE4_E5B9
    z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
    return z ^ (z >> 31)
  }
}

/// Tracks named by single letters: an uppercase letter is a gap, with no
/// audio.  `albums` puts letters on albums: "ab:A cd:B" puts a and b on
/// album A, c and d on B.
func letterTracks(_ letters: String, albums: String = "") -> [Track] {
  var albumOf: [Character: String] = [:]
  for spec in albums.split(separator: " ") {
    let parts = spec.split(separator: ":")
    for c in parts[0] { albumOf[c] = String(parts[1]) }
  }
  return letters.enumerated().map { i, c in
    var v: [String: JSONValue] = ["title": .string(String(c))]
    if c.isLowercase { v["blobId"] = .string("G\(c)") }
    if let album = albumOf[c] { v["albumId"] = .string(album) }
    return Track(key: LocalKey(Int64(i)), id: String(c), value: .object(v))!
  }
}

func letters(_ entries: some Sequence<PlayQueue.Entry>) -> String {
  entries.map(\.track.title).joined()
}

/// Album orders as letters, "A=abc B=d", as a `discs` closure.
func discs(_ orders: String) -> PlayQueue.Discs {
  var order: [String: [String]] = [:]
  for spec in orders.split(separator: " ") {
    let parts = spec.split(separator: "=")
    order[String(parts[0])] = parts[1].map(String.init)
  }
  return { order[$0] }
}

/// A queue started on `letters` at `position`, in a shuffle or not.
/// `albums` puts letters on albums and `orders` says each album's order.
func queue(_ s: String, at position: Int = 0, shuffle: PlayQueue.Shuffle = .off, albums: String = "", orders: String = "",
           repeat mode: PlayQueue.Repeat = .off, seed: UInt64 = 1) -> PlayQueue {
  var q = PlayQueue()
  var rng = SeededGenerator(state: seed)
  q.setShuffle(shuffle, using: &rng)
  q.repeatMode = mode
  q.start(letterTracks(s, albums: albums), at: position, discs: discs(orders), using: &rng)
  return q
}

// checkStart starts a queue and checks the order and what's playing.
func checkStart(_ s: String, at position: Int, order: String, current: String?,
                sourceLocation: SourceLocation = #_sourceLocation) {
  let q = queue(s, at: position)
  #expect(letters(q.entries) == order, sourceLocation: sourceLocation)
  #expect(q.current?.title == current, sourceLocation: sourceLocation)
}

@Test func starting() {
  checkStart("abcd", at: 2, order: "abcd", current: "c")
  checkStart("aBcd", at: 2, order: "acd", current: "c")
  checkStart("aBcd", at: 1, order: "", current: nil)
}

// Shuffling leaves what's played and playing alone and shuffles the rest;
// unshuffling puts everything back, still playing the same track.
@Test func shuffling() {
  var q = queue("abcdefgh", at: 2)
  var rng = SeededGenerator(state: 7)
  q.setShuffle(.tracks, using: &rng)
  #expect(letters(q.entries.prefix(3)) == "abc")
  #expect(q.current?.title == "c")
  #expect(letters(q.upcoming) != "defgh")
  #expect(letters(q.upcoming).sorted() == Array("defgh"))

  q.move(to: 5)
  let playing = q.current?.title
  q.setShuffle(.off, using: &rng)
  #expect(letters(q.entries) == "abcdefgh")
  #expect(q.current?.title == playing)
}

// Starting while shuffled shuffles all but the chosen track, which plays
// first.
@Test func startingShuffled() {
  let q = queue("abcdefgh", at: 3, shuffle: .tracks)
  #expect(q.current?.title == "d")
  #expect(q.index == 0)
  #expect(letters(q.entries).sorted() == Array("abcdefgh"))
}

// checkAlbumStart starts a queue shuffled by album and checks that the
// chosen track plays first, that the rest of its album follows in
// order, and that the whole order is one of those allowed.
func checkAlbumStart(_ s: String, at position: Int, albums: String, orders: String, prefix: String, anyOf allowed: [String],
                     sourceLocation: SourceLocation = #_sourceLocation) {
  for seed: UInt64 in 1...6 {
    let q = queue(s, at: position, shuffle: .albums, albums: albums, orders: orders, seed: seed)
    let out = letters(q.entries)
    #expect(q.index == 0, sourceLocation: sourceLocation)
    #expect(out.hasPrefix(prefix), "\(out) from seed \(seed)", sourceLocation: sourceLocation)
    #expect(allowed.contains(out), "\(out) from seed \(seed)", sourceLocation: sourceLocation)
  }
}

@Test func startingShuffledByAlbum() {
  // From a's album, the album finishes first, then the others whole.
  checkAlbumStart("cazbd", at: 1, albums: "abc:A d:B", orders: "A=abc B=d", prefix: "abc", anyOf: ["abczd", "abcdz"])
  // From b, mid-album: b, then the rest of A in A's order.
  checkAlbumStart("cazbd", at: 3, albums: "abc:A d:B", orders: "A=abc B=d", prefix: "bac", anyOf: ["baczd", "bacdz"])
  // From a loner, every album is whole, in any order.
  checkAlbumStart("cazbd", at: 2, albums: "abc:A d:B", orders: "A=abc B=d", prefix: "z", anyOf: ["zabcd", "zdabc"])
}

// Switching to albums mid-queue keeps what's played, finishes the
// current album, then plays the others whole; switching between
// shuffles and back off restores the queued order.
@Test func switchingShuffles() {
  var q = queue("abcdef", at: 2, albums: "ab:A cd:B ef:C", orders: "A=ab B=cd C=fe")
  var rng = SeededGenerator(state: 2)
  q.setShuffle(.albums, discs: discs("A=ab B=cd C=fe"), using: &rng)
  #expect(letters(q.entries) == "abcdfe")
  #expect(q.current?.title == "c")
  q.setShuffle(.tracks, using: &rng)
  #expect(letters(q.entries.prefix(3)) == "abc")
  #expect(letters(q.upcoming).sorted() == Array("def"))
  q.setShuffle(.off, using: &rng)
  #expect(letters(q.entries) == "abcdef")
  #expect(q.current?.title == "c")
}

// checkInsert queues "xy" with play next or add, on a queue of "abcd"
// playing c, and checks the order before and after unshuffling.
func checkInsert(next: Bool, shuffled: Bool, order: String, unshuffled: String,
                 sourceLocation: SourceLocation = #_sourceLocation) {
  var q = queue("abcd", at: 2, shuffle: shuffled ? .tracks : .off)
  var rng = SeededGenerator(state: 3)
  let xy = letterTracks("xy")
  let startNow = next ? q.playNext(xy) : q.add(xy)
  #expect(!startNow, sourceLocation: sourceLocation)
  if !shuffled || next {
    #expect(letters(q.entries) == order, sourceLocation: sourceLocation)
  } else {
    #expect(letters(q.entries).suffix(2) == "xy", sourceLocation: sourceLocation)
  }
  q.setShuffle(.off, using: &rng)
  #expect(letters(q.entries) == unshuffled, sourceLocation: sourceLocation)
  #expect(q.current?.title == "c", sourceLocation: sourceLocation)
}

@Test func inserting() {
  checkInsert(next: true, shuffled: false, order: "abcxyd", unshuffled: "abcxyd")
  checkInsert(next: false, shuffled: false, order: "abcdxy", unshuffled: "abcdxy")
  checkInsert(next: false, shuffled: true, order: "", unshuffled: "abcdxy")

  // Shuffled, the current track is first, so play next puts xy second.
  var q = queue("abcd", at: 2, shuffle: .tracks)
  _ = q.playNext(letterTracks("xy"))
  #expect(letters(q.entries.prefix(3)) == "cxy")
}

@Test func insertingIntoNothing() {
  var q = PlayQueue()
  let started = q.add(letterTracks("xy"))
  #expect(started)
  #expect(letters(q.entries) == "xy")
  #expect(q.current?.title == "x")
  var gaps = PlayQueue()
  let startedGaps = gaps.add(letterTracks("X"))
  #expect(!startedGaps)
}

// checkFollowing checks where the queue goes after the entry at `at`.
func checkFollowing(_ s: String, at position: Int, repeat mode: PlayQueue.Repeat, skipping: Bool, _ expected: Int?,
                    sourceLocation: SourceLocation = #_sourceLocation) {
  let q = queue(s, at: position, repeat: mode)
  #expect(q.following(skipping: skipping) == expected, sourceLocation: sourceLocation)
}

// checkPreceding checks where going back from the entry at `at` leads.
func checkPreceding(_ s: String, at position: Int, repeat mode: PlayQueue.Repeat, _ expected: Int?,
                    sourceLocation: SourceLocation = #_sourceLocation) {
  let q = queue(s, at: position, repeat: mode)
  #expect(q.preceding() == expected, sourceLocation: sourceLocation)
}

@Test func repeating() {
  checkFollowing("abc", at: 1, repeat: .off, skipping: false, 2)
  checkFollowing("abc", at: 2, repeat: .off, skipping: false, nil)
  checkFollowing("abc", at: 2, repeat: .all, skipping: false, 0)
  checkFollowing("abc", at: 1, repeat: .one, skipping: false, 1)
  checkFollowing("abc", at: 1, repeat: .one, skipping: true, 2)
  checkFollowing("abc", at: 2, repeat: .one, skipping: true, nil)

  checkPreceding("abc", at: 1, repeat: .off, 0)
  checkPreceding("abc", at: 0, repeat: .off, nil)
  checkPreceding("abc", at: 0, repeat: .all, 2)
  checkPreceding("a", at: 0, repeat: .all, nil)
}

// checkAlbumShuffle shuffles tracks by album and checks that each album's
// tracks stay together and in album order, and that none are lost.
// Album orders are given as letters, like "A=abc".
func checkAlbumShuffle(_ s: String, albums: String, orders: [String: String], seed: UInt64,
                       sourceLocation: SourceLocation = #_sourceLocation) {
  var rng = SeededGenerator(state: seed)
  let shuffled = PlayQueue.albumShuffled(letterTracks(s, albums: albums), discs: { orders[$0].map { $0.map(String.init) } }, using: &rng)
  let out = shuffled.map(\.title).joined()
  #expect(out.sorted() == s.sorted(), "lost tracks: \(out)", sourceLocation: sourceLocation)
  for (album, order) in orders {
    let mine = shuffled.enumerated().filter { $0.element.albumId == album }
    let positions = mine.map(\.offset)
    #expect(positions == Array((positions.first ?? 0)..<((positions.first ?? 0) + positions.count)),
            "\(album) split up in \(out)", sourceLocation: sourceLocation)
    let titles = mine.map(\.element.title).joined()
    #expect(titles == order.filter { titles.contains($0) }, "\(album) out of order in \(out)", sourceLocation: sourceLocation)
  }
}

@Test func albumShuffling() {
  // A playlist that has tracks of two albums out of order, and a loner.
  for seed: UInt64 in 1...5 {
    checkAlbumShuffle("cazbd", albums: "abc:A d:B", orders: ["A": "abc", "B": "d"], seed: seed)
  }
}

// Upcoming entries are grouped in runs by where they came from, and keep
// their places in the queue for jumping to.
@Test func sources() {
  var q = PlayQueue()
  var rng = SeededGenerator(state: 1)
  q.start(letterTracks("abc"), at: 0, from: "Album", using: &rng)
  _ = q.add(letterTracks("x"), from: "Queued")
  _ = q.playNext(letterTracks("y"), from: "Queued")
  let runs = q.upcomingBySource.map { run in
    "\(run.source ?? "-"): " + run.entries.map { "\($0.index)\($0.entry.track.title)" }.joined(separator: " ")
  }
  #expect(runs == ["Queued: 1y", "Album: 2b 3c", "Queued: 4x"])
}

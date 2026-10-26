import Foundation
import JinxJSON
import JinxStore
import Testing
@testable import JukeKit

// checkSortKey checks how a name sorts, as a title and as an artist.
func checkSortKey(_ name: String, title: String, artist: String,
                  sourceLocation: SourceLocation = #_sourceLocation) {
  #expect(sortKey(name) == title, sourceLocation: sourceLocation)
  #expect(sortKey(name, droppingArticle: true) == artist, sourceLocation: sourceLocation)
}

@Test func sortKeys() {
  checkSortKey("The Beatles", title: "the beatles", artist: "beatles")
  checkSortKey("A Tribe Called Quest", title: "a tribe called quest", artist: "tribe called quest")
  checkSortKey("An Pierlé", title: "an pierle", artist: "pierle")
  checkSortKey("Björk", title: "bjork", artist: "bjork")
  checkSortKey("Theatre of Tragedy", title: "theatre of tragedy", artist: "theatre of tragedy")
  checkSortKey("The The", title: "the the", artist: "the")
  checkSortKey("The", title: "the", artist: "the")
  checkSortKey("A", title: "a", artist: "a")
}

func album(_ id: String, _ title: String, by artist: String?) -> Album {
  let v: JSONValue = .object([
    "title": .string(title),
    "artist": artist.map(JSONValue.string) ?? .null,
    "discs": .array([.object(["trackIds": .array([])])]),
  ])
  return Album(key: LocalKey(Int64(id.hashValue & 0xFFFF)), id: id, value: v)!
}

// checkArtists groups albums by artist and checks the artists' order and
// each one's albums, as "Artist: title, title".
func checkArtists(_ albums: [Album], _ expected: [String], sourceLocation: SourceLocation = #_sourceLocation) {
  let titles = Dictionary(uniqueKeysWithValues: albums.map { ($0.id, $0.title) })
  let got = Library.artists(of: albums).map { a in
    "\(a.name): " + a.albumIds.map { titles[$0]! }.joined(separator: ", ")
  }
  #expect(got == expected, sourceLocation: sourceLocation)
}

@Test func artistGrouping() {
  checkArtists([
    album("1", "Revolver", by: "The Beatles"),
    album("2", "Abbey Road", by: "The Beatles"),
    album("3", "Homogenic", by: "Björk"),
    album("4", "The Low End Theory", by: "A Tribe Called Quest"),
    album("5", "Now That's What I Call Music", by: nil),
    album("6", "Amnesiac", by: "Radiohead"),
  ], [
    "The Beatles: Abbey Road, Revolver",
    "Björk: Homogenic",
    "Radiohead: Amnesiac",
    "A Tribe Called Quest: The Low End Theory",
  ])
}

/// A track for sorting: title, artist, and optionally when it was added
/// and played (days into 2014) and its rating.
func song(_ title: String, _ artist: String?, added: Int? = nil, played: Int? = nil, rating: Double? = nil) -> Track {
  func day(_ d: Int) -> JSONValue { .string(String(format: "2014-01-%02dT00:00:00Z", d)) }
  var v: [String: JSONValue] = ["title": .string(title), "blobId": .string("G\(title)")]
  if let artist { v["artist"] = .string(artist) }
  if let added { v["dateAdded"] = day(added) }
  if let played { v["lastPlayed"] = day(played) }
  if let rating { v["rating"] = .number(rating) }
  return Track(key: LocalKey(1), id: "T\(title)", value: .object(v))!
}

let songs = [
  song("Yellow", "Coldplay", added: 3, played: 9, rating: 3),
  song("ånother", "The Who", added: 5, rating: 5),
  song("Bones", "Radiohead", added: 1, played: 20),
  song("Creep", "Radiohead", added: 4, played: 2, rating: 5),
  song("Zebra", nil, added: 2),
]

// checkOrder sorts the sample songs and checks the titles in order.
func checkOrder(_ order: SongOrder, _ expected: [String], sourceLocation: SourceLocation = #_sourceLocation) {
  #expect(Library.sorted(songs, by: order).map(\.title) == expected, "\(order)", sourceLocation: sourceLocation)
}

@Test func songOrders() {
  checkOrder(.title, ["ånother", "Bones", "Creep", "Yellow", "Zebra"])
  checkOrder(.artist, ["Yellow", "Bones", "Creep", "ånother", "Zebra"])
  checkOrder(.recentlyAdded, ["ånother", "Creep", "Yellow", "Zebra", "Bones"])
  // Never played sorts last, by title.
  checkOrder(.recentlyPlayed, ["Bones", "Yellow", "Creep", "ånother", "Zebra"])
  // Tied ratings break by title; unrated last.
  checkOrder(.rating, ["ånother", "Creep", "Yellow", "Bones", "Zebra"])
}

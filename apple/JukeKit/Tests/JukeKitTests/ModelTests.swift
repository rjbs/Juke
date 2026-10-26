import Foundation
import JinxJSON
import JinxStore
import Testing
@testable import JukeKit

// checkTrack decodes a Track from JSON and checks the properties the
// views use; nil expectations mean "absent".
func checkTrack(
  _ json: String, title: String?, artist: String? = nil, rating: Double? = nil,
  playable: Bool = true, blobType: String? = nil, playCount: Int = 0, lastPlayed: String? = nil,
  sourceLocation: SourceLocation = #_sourceLocation
) throws {
  let track = Track(key: LocalKey(1), id: "T1", value: try JSONValue(parsing: json))
  guard let title else {
    #expect(track == nil, "decoded \(json)", sourceLocation: sourceLocation)
    return
  }
  let t = try #require(track, sourceLocation: sourceLocation)
  #expect(t.title == title, sourceLocation: sourceLocation)
  #expect(t.artist == artist, sourceLocation: sourceLocation)
  #expect(t.rating == rating, sourceLocation: sourceLocation)
  #expect(t.isPlayable == playable, sourceLocation: sourceLocation)
  #expect(t.blobType == blobType, sourceLocation: sourceLocation)
  #expect(t.playCount == playCount, sourceLocation: sourceLocation)
  #expect(t.lastPlayed.map(formatUTCDate) == lastPlayed, sourceLocation: sourceLocation)
}

// checkAlbum decodes an Album and checks its artist and disc layout,
// given as each disc's track ids.
func checkAlbum(
  _ json: String, artist: String?, discs: [[String]], titles: [String?],
  sourceLocation: SourceLocation = #_sourceLocation
) throws {
  let a = try #require(Album(key: LocalKey(2), id: "A1", value: try JSONValue(parsing: json)), sourceLocation: sourceLocation)
  #expect(a.artist == artist, sourceLocation: sourceLocation)
  #expect(a.discs.map(\.trackIds) == discs, sourceLocation: sourceLocation)
  #expect(a.discs.map(\.title) == titles, sourceLocation: sourceLocation)
}

@Test func tracks() throws {
  try checkTrack(#"{"title":"Whip It","artist":"Devo","rating":4.5,"blobId":"G1","blobType":"audio/mpeg","playCount":3,"lastPlayed":"2014-06-10T08:00:00Z"}"#,
                 title: "Whip It", artist: "Devo", rating: 4.5, blobType: "audio/mpeg", playCount: 3, lastPlayed: "2014-06-10T08:00:00Z")
  try checkTrack(#"{"title":"Lost Track","artist":null,"rating":null,"blobId":null,"playCount":0,"lastPlayed":null}"#,
                 title: "Lost Track", playable: false)
  try checkTrack(#"{"artist":"Nobody"}"#, title: nil)
}

@Test func albums() throws {
  try checkAlbum(#"{"title":"Live Killers","artist":"Queen","discs":[{"trackIds":["T1","T2"]},{"trackIds":["T3"],"title":"Encore"}]}"#,
                 artist: "Queen", discs: [["T1", "T2"], ["T3"]], titles: [nil, "Encore"])
  try checkAlbum(#"{"title":"Mix","artist":null,"discs":[{"trackIds":[]}]}"#,
                 artist: nil, discs: [[]], titles: [nil])
}

// checkAudioType checks the type sniffed from a file's first bytes.
func checkAudioType(_ bytes: [UInt8], _ expected: String?, sourceLocation: SourceLocation = #_sourceLocation) {
  #expect(audioType(of: Data(bytes)) == expected, sourceLocation: sourceLocation)
}

@Test func sniffing() {
  checkAudioType(Array("ID3\u{3}\0\0".utf8), "audio/mpeg")
  checkAudioType([0xFF, 0xFB, 0x90, 0x64], "audio/mpeg")
  checkAudioType([0xFF, 0xFD, 0x90, 0x64], "audio/mpeg")
  checkAudioType([0, 0, 0, 0x20] + Array("ftypM4A ".utf8), "audio/mp4")
  checkAudioType(Array("RIFF....WAVE".utf8), nil)
  checkAudioType([], nil)
}

// checkMove moves the entries at some positions of a list of letters, as
// SwiftUI's onMove would ask, and checks the result.
func checkMove(_ from: [Int], to destination: Int, _ expected: String,
               sourceLocation: SourceLocation = #_sourceLocation) {
  let ids = "abcde".map(String.init)
  let moved = Library.move(ids, from: from, to: destination).joined()
  #expect(moved == expected, "move \(from) to \(destination)", sourceLocation: sourceLocation)
}

@Test func moving() {
  checkMove([0], to: 2, "bacde")
  checkMove([4], to: 0, "eabcd")
  checkMove([1], to: 5, "acdeb")
  checkMove([0, 2], to: 4, "bdace")
  checkMove([3, 1], to: 0, "bdace")
  checkMove([2], to: 2, "abcde")
  checkMove([2], to: 3, "abcde")
}

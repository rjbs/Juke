import Foundation
import JinxJSON
import JinxStore
import Testing
@testable import JukeKit

// An artist's albums in each order: by date with the undated last
// whichever way the dates run, ties and the undated by title.

/// An album with a title and a release date, or none.
private func album(_ title: String, _ released: String?) -> Album {
  var v: [String: JSONValue] = ["title": .string(title), "discs": .array([])]
  if let released { v["released"] = .string(released) }
  return Album(key: LocalKey(Int64(title.hashValue & 0xffff)), id: title, value: .object(v))!
}

/// Checks the titles `order` puts a set of albums in.
private func orders(_ order: AlbumOrder, _ albums: [Album], as titles: [String], sourceLocation: SourceLocation = #_sourceLocation) {
  #expect(order.sorted(albums).map(\.title) == titles, sourceLocation: sourceLocation)
}

@Test func albumOrders() {
  let albums = [
    album("Rain Dogs", "1985-00-00"), album("Alice", "2002-05-07"), album("Blood Money", "2002-05-07"),
    album("Bootleg", nil), album("Closing Time", "1973-00-00"), album("Another Bootleg", nil),
  ]
  orders(.oldestFirst, albums, as: ["Closing Time", "Rain Dogs", "Alice", "Blood Money", "Another Bootleg", "Bootleg"])
  orders(.newestFirst, albums, as: ["Alice", "Blood Money", "Rain Dogs", "Closing Time", "Another Bootleg", "Bootleg"])
  orders(.titleAscending, albums, as: ["Alice", "Another Bootleg", "Blood Money", "Bootleg", "Closing Time", "Rain Dogs"])
  orders(.titleDescending, albums, as: ["Rain Dogs", "Closing Time", "Bootleg", "Blood Money", "Another Bootleg", "Alice"])
  #expect(album("x", "1991-02-00").releasedText == "1991-02")
  #expect(album("x", "1991-00-00").releasedText == "1991")
  #expect(album("x", nil).releasedText == nil)
}

import Foundation
import JinxJSON
import JinxStore
import Testing
@testable import JukeKit

// The rule editor's model: a stored filter reads into the tree and
// compiles back to the same filter, and the shapes the web editor
// writes read back as the same rows.  No server needed.

/// A stored smart playlist with this filter, and nothing else.
private func stored(_ filter: JSONValue?, sort: [JSONValue] = [], limit: Int? = nil) throws -> SmartPlaylist {
  var v: [String: JSONValue] = ["title": "T", "sort": .array(sort)]
  if let filter { v["filter"] = filter }
  if let limit { v["limit"] = .number(Double(limit)) }
  return try #require(SmartPlaylist(key: LocalKey(7), id: "S1", value: .object(v)))
}

/// Reads a filter into a draft and checks it compiles back to itself.
private func roundTrips(_ filter: JSONValue?, sourceLocation: SourceLocation = #_sourceLocation) throws -> SmartPlaylistDraft {
  let draft = try SmartPlaylistDraft(try stored(filter))
  let back = try draft.filter()
  #expect(back.isJMAPEquivalent(to: filter ?? .null), "\(back) from \(String(describing: filter))", sourceLocation: sourceLocation)
  return draft
}

/// Checks that a filter reads as one row with this field and operator.
private func readsAsRow(_ filter: JSONValue, _ field: RuleField, _ op: RuleOperator, sourceLocation: SourceLocation = #_sourceLocation) throws -> RuleRow {
  let draft = try roundTrips(filter, sourceLocation: sourceLocation)
  #expect(draft.root.items.count == 1, sourceLocation: sourceLocation)
  let row = try #require(draft.root.items.first?.row, sourceLocation: sourceLocation)
  #expect(row.field == field && row.operator == op, "\(row.field) \(row.operator)", sourceLocation: sourceLocation)
  return row
}

@Test func plainConditionsRoundTrip() throws {
  _ = try readsAsRow(["genre": "Rock"], .genre, .equals)
  _ = try readsAsRow(["titleContains": "love"], .title, .contains)
  _ = try readsAsRow(["hasGenre": false], .genre, .isUnset)
  _ = try readsAsRow(["ratingAtLeast": 4], .rating, .atLeast)
  _ = try readsAsRow(["playCountAtMost": 2], .playCount, .atMost)
  _ = try readsAsRow(["hasAudio": true], .audio, .present)
  _ = try readsAsRow(["lastPlayedWithinDays": 30], .lastPlayed, .within)
  _ = try readsAsRow(["lastPlayedBefore": "2020-01-01T00:00:00Z"], .lastPlayed, .before)
  _ = try readsAsRow(["inPlaylist": "P1"], .playlist, .inAny)
  _ = try readsAsRow(["inSmartPlaylist": "S2"], .playlist, .inAny)
}

@Test func negationsReadAsNegatedOperators() throws {
  _ = try readsAsRow(["operator": "NOT", "conditions": [["genre": "Classical"]]], .genre, .notEquals)
  _ = try readsAsRow(["operator": "NOT", "conditions": [["artistContains": "Beatles"]]], .artist, .notContains)
  _ = try readsAsRow(["operator": "NOT", "conditions": [["lastPlayedWithinDays": 7]]], .lastPlayed, .notWithin)
}

@Test func listsReadAsOneRow() throws {
  let any = try readsAsRow(["operator": "OR", "conditions": [["genre": "Rock"], ["genre": "Pop"]]], .genre, .anyOf)
  #expect(any.strings == ["Rock", "Pop"])
  let none = try readsAsRow(["operator": "NOT", "conditions": [["artist": "A"], ["artist": "B"], ["artist": "C"]]], .artist, .noneOf)
  #expect(none.strings == ["A", "B", "C"])
  let lists = try readsAsRow(["operator": "AND", "conditions": [["inPlaylist": "P1"], ["inSmartPlaylist": "S2"]]], .playlist, .inAll)
  #expect(lists.lists == [RuleList(id: "P1", smart: false), RuleList(id: "S2", smart: true)])
  _ = try readsAsRow(["operator": "NOT", "conditions": [["inSmartPlaylist": "S2"]]], .playlist, .inNone)
}

@Test func equalNumberBoundsReadAsIs() throws {
  let row = try readsAsRow(["ratingAtLeast": 3, "ratingAtMost": 3], .rating, .equals)
  #expect(row.number == 3)
}

@Test func lengthIsEnteredInMinutes() throws {
  let row = try readsAsRow(["durationAtLeast": 150], .duration, .atLeast)
  #expect(row.number == 2.5)
}

@Test func groupsNest() throws {
  let draft = try roundTrips([
    "operator": "AND", "conditions": [
      ["hasAudio": true],
      ["operator": "OR", "conditions": [["ratingAtLeast": 4], ["playCountAtLeast": 10]]],
      ["operator": "NOT", "conditions": [["genre": "Spoken"], ["genre": "Comedy"]]],
    ],
  ])
  #expect(draft.root.operator == .all && draft.root.items.count == 3)
  guard case .group(let inner) = draft.root.items[1] else { Issue.record("no inner group"); return }
  #expect(inner.operator == .any && inner.items.count == 2)
}

@Test func emptyAndSingleRootsStoreBare() throws {
  let empty = try roundTrips(nil)
  #expect(empty.root.items.isEmpty)
  var draft = SmartPlaylistDraft(title: "x")
  draft.root.items = []
  #expect(try draft.filter() == .null)
  draft.root.items = [.row(RuleRow(field: .audio, operator: .present))]
  #expect(try draft.filter() == ["hasAudio": true])
  draft.root.operator = .none
  #expect(try draft.filter() == ["operator": "NOT", "conditions": [["hasAudio": true]]])
}

@Test func unfinishedRowsRefuseToCompile() throws {
  var draft = SmartPlaylistDraft(title: "x")
  #expect(throws: RuleError.unfinished(.genre, .equals)) { try draft.filter() }
  draft.root.items = [.row(RuleRow(field: .rating, operator: .atLeast))]
  #expect(throws: RuleError.unfinished(.rating, .atLeast)) { try draft.filter() }
  draft.title = " "
  #expect(throws: RuleError.noTitle) { try draft.properties() }
}

@Test func unknownConditionsAreRefused() throws {
  #expect(throws: RuleError.unknownCondition("compilation")) { try SmartPlaylistDraft(try stored(["compilation": true])) }
  #expect(throws: RuleError.unknownOperator("XOR")) { try SmartPlaylistDraft(try stored(["operator": "XOR", "conditions": []])) }
}

@Test func limitsSaveAsTheSmallest() throws {
  var draft = SmartPlaylistDraft(title: "x")
  draft.root.items = []
  draft.limits = [RuleLimit(unit: .megabytes, value: 50), RuleLimit(unit: .megabytes, value: 20), RuleLimit(unit: .tracks, value: 7), RuleLimit(unit: .minutes)]
  let p = try draft.properties()
  #expect(p["limit"] == 7)
  #expect(p["limitBytes"] == .number(20 * 1024 * 1024))
  #expect(p["limitSeconds"] == .null)
  let args = try draft.queryArguments(position: 5, pageSize: 100)
  #expect(args["limit"] == 2)
  #expect(args["limitBytes"] == .number(20 * 1024 * 1024))
  #expect(args["limitSeconds"] == nil)
}

@Test func sortReadsBackWithSeed() throws {
  let playlist = try stored(nil, sort: [["property": "lastPlayed", "isAscending": false], ["property": "random", "seed": "abc"]], limit: 25)
  let draft = try SmartPlaylistDraft(playlist)
  #expect(draft.sort.map(\.property) == [.lastPlayed, .random])
  #expect(draft.sort[0].ascending == false)
  #expect(draft.sort[1].seed == "abc")
  #expect(draft.limits.map(\.unit) == [.tracks] && draft.limits.map(\.value) == [25])
  #expect(playlist.matches(try draft.properties()))
  #expect(draft.isEquivalent(to: draft))
}

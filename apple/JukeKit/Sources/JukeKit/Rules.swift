import Foundation
import JinxJSON

// A smart playlist's rule as an editor holds it: a tree of groups and
// conditions, a sort, and limits, compiled to the properties that
// `SmartPlaylist/set` takes and read back from a stored playlist.  The
// vocabulary is doc/types.md's `Track/query`, and the tree is the web
// page's (internal/web/smart.js), so a rule written in either reads
// back in the other.  -- claude, 2026-11-03

/// What a condition row is about.  Its kind decides the operators it
/// offers and the input it takes; the condition names are the server's.
public enum RuleField: String, CaseIterable, Identifiable, Hashable, Sendable {
  case title, artist, album, genre, rating, playCount, duration, lastPlayed, playlist, audio

  public var id: String { rawValue }

  public enum Kind: Sendable { case string, number, duration, date, playlist, audio }

  public var kind: Kind {
    switch self {
    case .title, .artist, .album, .genre: .string
    case .rating, .playCount: .number
    case .duration: .duration
    case .lastPlayed: .date
    case .playlist: .playlist
    case .audio: .audio
    }
  }

  public var label: String {
    switch self {
    case .title: "Title"
    case .artist: "Artist"
    case .album: "Album"
    case .genre: "Genre"
    case .rating: "Rating"
    case .playCount: "Play count"
    case .duration: "Length"
    case .lastPlayed: "Last played"
    case .playlist: "Playlist"
    case .audio: "Audio"
    }
  }

  /// The server's condition for equality, for a string field.
  var equals: String? {
    switch self {
    case .title: "title"
    case .artist: "artist"
    case .album: "albumTitle"
    case .genre: "genre"
    default: nil
    }
  }

  var contains: String? { equals.map { $0 + "Contains" } }

  /// The server's `has…` condition, for a field a track may lack.
  var has: String? {
    switch self {
    case .artist: "hasArtist"
    case .album: "hasAlbum"
    case .genre: "hasGenre"
    case .rating: "hasRating"
    default: nil
    }
  }

  var atLeast: String? {
    switch self {
    case .rating: "ratingAtLeast"
    case .playCount: "playCountAtLeast"
    case .duration: "durationAtLeast"
    default: nil
    }
  }

  var atMost: String? { atLeast.map { String($0.dropLast("AtLeast".count)) + "AtMost" } }

  /// The operators this field offers, in menu order.
  public var operators: [RuleOperator] {
    switch kind {
    case .string: [.equals, .notEquals, .contains, .notContains, .anyOf, .noneOf] + (has != nil ? [.isSet, .isUnset] : [])
    case .number: [.atLeast, .atMost, .equals] + (has != nil ? [.isSet, .isUnset] : [])
    case .duration: [.atLeast, .atMost]
    case .date: [.within, .notWithin, .before, .after]
    case .playlist: [.inAny, .inAll, .inNone]
    case .audio: [.present, .missing]
    }
  }

  /// A number field's step and bounds, and what its unit is called.
  public var numberStep: Double { self == .rating ? 0.5 : 1 }
  public var numberRange: ClosedRange<Double> { self == .rating ? 0...5 : 0...1_000_000 }
  public var unit: String? {
    switch self {
    case .rating: "stars"
    case .playCount: "plays"
    case .duration: "minutes"
    default: nil
    }
  }
}

/// How a row compares its field to its value.
public enum RuleOperator: String, CaseIterable, Identifiable, Hashable, Sendable {
  case equals, notEquals, contains, notContains, anyOf, noneOf, isSet, isUnset
  case atLeast, atMost
  case before, after, within, notWithin
  case inAny, inAll, inNone
  case present, missing

  public var id: String { rawValue }

  public var label: String {
    switch self {
    case .equals: "is"
    case .notEquals: "is not"
    case .contains: "contains"
    case .notContains: "doesn't contain"
    case .anyOf: "is any of"
    case .noneOf: "is none of"
    case .isSet: "is set"
    case .isUnset: "is not set"
    case .atLeast: "is at least"
    case .atMost: "is at most"
    case .before: "was before"
    case .after: "was on or after"
    case .within: "was within the last"
    case .notWithin: "was not within the last"
    case .inAny: "is in any of"
    case .inAll: "is in all of"
    case .inNone: "is in none of"
    case .present: "is present"
    case .missing: "is missing"
    }
  }

  /// The operator this one is the `NOT` of, if it is one.  A negated
  /// operator matches a track whose value is missing too, as the server
  /// defines `NOT`.
  public var negated: RuleOperator? {
    switch self {
    case .notEquals: .equals
    case .notContains: .contains
    case .noneOf: .anyOf
    case .notWithin: .within
    case .inNone: .inAny
    default: nil
    }
  }

  var negation: RuleOperator? {
    Self.allCases.first { $0.negated == self }
  }

  /// What kind of value the operator takes.
  public enum Input: Sendable { case none, text, number, list, date, playlists }

  public var input: Input {
    switch self {
    case .isSet, .isUnset, .present, .missing: .none
    case .equals, .notEquals, .contains, .notContains: .text
    case .anyOf, .noneOf: .list
    case .atLeast, .atMost, .within, .notWithin: .number
    case .before, .after: .date
    case .inAny, .inAll, .inNone: .playlists
    }
  }
}

/// A playlist of either kind, as a rule names it.
public struct RuleList: Hashable, Sendable, Identifiable {
  public var id: String
  public var smart: Bool

  public init(id: String, smart: Bool) {
    self.id = id
    self.smart = smart
  }

  var condition: JSONValue { .object([smart ? "inSmartPlaylist" : "inPlaylist": .string(id)]) }
}

/// One condition: a field, an operator, and the value the operator
/// takes, each kind kept in its own slot so an input can bind to it.
public struct RuleRow: Identifiable, Hashable, Sendable {
  public var id = UUID()
  public var field: RuleField
  public var `operator`: RuleOperator
  public var text = ""
  public var number: Double? = nil
  public var date = Date()
  public var strings: [String] = []
  public var lists: [RuleList] = []

  public init(field: RuleField = .genre, operator: RuleOperator = .equals) {
    self.field = field
    self.`operator` = `operator`
  }

  /// Changes the field, keeping the operator if the new field offers it
  /// and taking the field's first otherwise.
  public mutating func change(to field: RuleField) {
    self.field = field
    if !field.operators.contains(`operator`) { `operator` = field.operators[0] }
  }

  /// The number as compiled: a length is entered in minutes and sent in
  /// seconds; an equals on a number field is both bounds at once.
  var compiledNumber: Double? {
    number.map { field == .duration ? ($0 * 60).rounded() : $0 }
  }

  /// A whole number, where the server wants one, as JSON.
  private func numberValue() throws -> JSONValue {
    guard let n = compiledNumber else { throw RuleError.unfinished(field, `operator`) }
    return .number(n)
  }

  private func textValue() throws -> String {
    let t = text.trimmingCharacters(in: .whitespaces)
    if t.isEmpty { throw RuleError.unfinished(field, `operator`) }
    return t
  }

  /// The row as a `Track/query` filter condition.
  public func compiled() throws -> JSONValue {
    func not(_ c: JSONValue) -> JSONValue { .object(["operator": "NOT", "conditions": .array([c])]) }
    func one(_ name: String?, _ value: JSONValue) -> JSONValue { .object([name ?? "": value]) }
    switch `operator` {
    case .equals:
      if field.kind == .number {
        let n = try numberValue()
        return .object([field.atLeast ?? "": n, field.atMost ?? "": n])
      }
      return one(field.equals, .string(try textValue()))
    case .notEquals: return not(one(field.equals, .string(try textValue())))
    case .contains: return one(field.contains, .string(try textValue()))
    case .notContains: return not(one(field.contains, .string(try textValue())))
    case .anyOf, .noneOf:
      if strings.isEmpty { throw RuleError.unfinished(field, `operator`) }
      let conditions = strings.map { one(field.equals, .string($0)) }
      return .object(["operator": `operator` == .anyOf ? "OR" : "NOT", "conditions": .array(conditions)])
    case .isSet: return one(field.has, true)
    case .isUnset: return one(field.has, false)
    case .atLeast: return one(field.atLeast, try numberValue())
    case .atMost: return one(field.atMost, try numberValue())
    case .before: return .object(["lastPlayedBefore": .string(formatUTCDate(date))])
    case .after: return .object(["lastPlayedAfter": .string(formatUTCDate(date))])
    case .within: return .object(["lastPlayedWithinDays": try numberValue()])
    case .notWithin: return not(.object(["lastPlayedWithinDays": try numberValue()]))
    case .inAny, .inAll:
      if lists.isEmpty { throw RuleError.unfinished(field, `operator`) }
      if lists.count == 1 { return lists[0].condition }
      return .object(["operator": `operator` == .inAny ? "OR" : "AND", "conditions": .array(lists.map(\.condition))])
    case .inNone:
      if lists.isEmpty { throw RuleError.unfinished(field, `operator`) }
      return .object(["operator": "NOT", "conditions": .array(lists.map(\.condition))])
    case .present: return .object(["hasAudio": true])
    case .missing: return .object(["hasAudio": false])
    }
  }
}

/// A group of nodes, all, any or none of which must match.
public struct RuleGroup: Identifiable, Hashable, Sendable {
  public enum Operator: String, CaseIterable, Identifiable, Hashable, Sendable {
    case all, any, none

    public var id: String { rawValue }

    public var label: String {
      switch self {
      case .all: "All of these"
      case .any: "Any of these"
      case .none: "None of these"
      }
    }

    var jmap: String {
      switch self {
      case .all: "AND"
      case .any: "OR"
      case .none: "NOT"
      }
    }

    init?(jmap: String) {
      switch jmap {
      case "AND": self = .all
      case "OR": self = .any
      case "NOT": self = .none
      default: return nil
      }
    }
  }

  public var id = UUID()
  public var `operator`: Operator
  public var items: [RuleNode]

  public init(operator: Operator = .all, items: [RuleNode] = []) {
    self.`operator` = `operator`
    self.items = items
  }

  public func compiled() throws -> JSONValue {
    .object(["operator": .string(`operator`.jmap), "conditions": .array(try items.map { try $0.compiled() })])
  }
}

public indirect enum RuleNode: Identifiable, Hashable, Sendable {
  case group(RuleGroup)
  case row(RuleRow)

  public var id: UUID {
    switch self {
    case .group(let g): g.id
    case .row(let r): r.id
    }
  }

  public func compiled() throws -> JSONValue {
    switch self {
    case .group(let g): try g.compiled()
    case .row(let r): try r.compiled()
    }
  }

  var row: RuleRow? {
    if case .row(let r) = self { return r }
    return nil
  }
}

public enum RuleError: Error, LocalizedError, Equatable {
  /// A row with no value yet.
  case unfinished(RuleField, RuleOperator)
  /// A stored rule holds something this editor doesn't know.
  case unknownCondition(String)
  case unknownOperator(String)
  case unknownSort(String)
  case noTitle

  public var errorDescription: String? {
    switch self {
    case .unfinished(let f, let o): "“\(f.label) \(o.label)” needs a value."
    case .unknownCondition(let c): "The rule has a condition this editor doesn't know: \(c)."
    case .unknownOperator(let o): "The rule has an operator this editor doesn't know: \(o)."
    case .unknownSort(let s): "The rule sorts on something this editor doesn't know: \(s)."
    case .noTitle: "The playlist needs a title."
    }
  }
}

/// One comparator of the rule's sort.
public struct RuleSort: Identifiable, Hashable, Sendable {
  public enum Property: String, CaseIterable, Identifiable, Hashable, Sendable {
    case title, artist, albumTitle, albumOrder, genre, rating, playCount, lastPlayed, duration, random

    public var id: String { rawValue }

    public var label: String {
      switch self {
      case .title: "Title"
      case .artist: "Artist"
      case .albumTitle: "Album"
      case .albumOrder: "Album order"
      case .genre: "Genre"
      case .rating: "Rating"
      case .playCount: "Play count"
      case .lastPlayed: "Last played"
      case .duration: "Length"
      case .random: "Random"
      }
    }
  }

  public var id = UUID()
  public var property: Property
  public var ascending = true
  /// What a random sort shuffles by; a new one reshuffles.
  public var seed: String

  public init(property: Property = .albumOrder, ascending: Bool = true, seed: String = UUID().uuidString) {
    self.property = property
    self.ascending = ascending
    self.seed = seed
  }

  public var compiled: JSONValue {
    var out: [String: JSONValue] = ["property": .string(property.rawValue)]
    if !ascending { out["isAscending"] = false }
    if property == .random { out["seed"] = .string(seed) }
    return .object(out)
  }

  init(parsing v: JSONValue) throws {
    let name = v["property"]?.stringValue ?? ""
    guard let property = Property(rawValue: name) else { throw RuleError.unknownSort(name) }
    self.property = property
    self.ascending = v["isAscending"]?.boolValue ?? true
    self.seed = v["seed"]?.stringValue ?? UUID().uuidString
  }
}

/// "At most so many" tracks, megabytes or minutes.  The server has one
/// of each; two rows of one unit are saved as the smaller.
public struct RuleLimit: Identifiable, Hashable, Sendable {
  public enum Unit: String, CaseIterable, Identifiable, Hashable, Sendable {
    case tracks, megabytes, minutes

    public var id: String { rawValue }

    public var label: String {
      switch self {
      case .tracks: "tracks"
      case .megabytes: "MB"
      case .minutes: "minutes"
      }
    }

    var property: String {
      switch self {
      case .tracks: "limit"
      case .megabytes: "limitBytes"
      case .minutes: "limitSeconds"
      }
    }

    var scale: Double {
      switch self {
      case .tracks: 1
      case .megabytes: 1024 * 1024
      case .minutes: 60
      }
    }
  }

  public var id = UUID()
  public var unit: Unit
  public var value: Double?

  public init(unit: Unit, value: Double? = nil) {
    self.unit = unit
    self.value = value
  }
}

/// A smart playlist as the editor has it, compiled to what
/// `SmartPlaylist/set` takes.
public struct SmartPlaylistDraft: Hashable, Sendable {
  public var title: String
  public var root: RuleGroup
  public var sort: [RuleSort]
  public var limits: [RuleLimit]
  public var isHelper: Bool

  /// A blank rule, with one row to fill in.
  public init(title: String = "") {
    self.title = title
    self.root = RuleGroup(items: [.row(RuleRow())])
    self.sort = []
    self.limits = []
    self.isHelper = false
  }

  /// A stored playlist's rule, read back into the tree.  Throws for a
  /// rule holding something this editor doesn't know.
  public init(_ playlist: SmartPlaylist, title: String? = nil) throws {
    self.title = title ?? playlist.title
    self.root = try Self.parse(playlist.filter)
    self.sort = try playlist.sort.map(RuleSort.init(parsing:))
    var limits: [RuleLimit] = []
    for unit in RuleLimit.Unit.allCases {
      let stored: Int? = switch unit {
      case .tracks: playlist.limit
      case .megabytes: playlist.limitBytes
      case .minutes: playlist.limitSeconds
      }
      if let stored { limits.append(RuleLimit(unit: unit, value: Double(stored) / unit.scale)) }
    }
    self.limits = limits
    self.isHelper = playlist.isHelper
  }

  /// The filter as stored: a root group of one condition is stored as
  /// that condition, and no condition at all as null, matching
  /// everything.
  public func filter() throws -> JSONValue {
    if root.items.isEmpty { return .null }
    if root.items.count == 1, root.operator != .none { return try root.items[0].compiled() }
    return try root.compiled()
  }

  /// The limits as the server's three properties, each the smallest of
  /// its rows or null.
  public var limitProperties: [String: JSONValue] {
    var out: [String: JSONValue] = [:]
    for unit in RuleLimit.Unit.allCases {
      let values = limits.filter { $0.unit == unit }.compactMap(\.value).filter { $0 > 0 }.map { ($0 * unit.scale).rounded() }
      out[unit.property] = values.min().map { .number($0) } ?? .null
    }
    return out
  }

  /// The playlist's properties, for `SmartPlaylist/set`.
  public func properties() throws -> JSONValue {
    let title = title.trimmingCharacters(in: .whitespaces)
    if title.isEmpty { throw RuleError.noTitle }
    var out: [String: JSONValue] = [
      "title": .string(title),
      "filter": try filter(),
      "sort": .array(sort.map(\.compiled)),
      "isHelper": .bool(isHelper),
    ]
    out.merge(limitProperties) { _, new in new }
    return .object(out)
  }

  /// The arguments of a `Track/query` that shows the rule's tracks from
  /// `position`, up to `pageSize` of them: within the rule's own track
  /// limit, since the query's limit is a page size, and with the other
  /// limits applied by the server.
  public func queryArguments(position: Int, pageSize: Int) throws -> [String: JSONValue] {
    let limits = limitProperties
    var limit = pageSize
    if let cap = limits["limit"]?.intValue { limit = max(0, min(pageSize, cap - position)) }
    var out: [String: JSONValue] = [
      "filter": try filter(),
      "sort": .array(sort.map(\.compiled)),
      "position": .number(Double(position)),
      "limit": .number(Double(limit)),
      "calculateTotal": true,
    ]
    for name in ["limitBytes", "limitSeconds"] where limits[name] != .null { out[name] = limits[name] }
    return out
  }

  /// Whether two drafts would save the same playlist.
  public func isEquivalent(to other: SmartPlaylistDraft) -> Bool {
    guard let a = try? properties(), let b = try? other.properties() else { return false }
    return a.isJMAPEquivalent(to: b)
  }

  /// The rule's own track limit, if any.
  public var trackLimit: Int? { limitProperties["limit"]?.intValue }

  // MARK: - Reading a stored filter

  static func parse(_ filter: JSONValue?) throws -> RuleGroup {
    guard let filter, filter != .null else { return RuleGroup() }
    switch try parseNode(filter) {
    case .group(let g): return g
    case .row(let r): return RuleGroup(items: [.row(r)])
    }
  }

  /// Reads one filter into a node.  A `NOT` of one negatable row is that
  /// row negated; several "is" rows on one string field, any or none of
  /// them, are one row with a list, and so are playlist memberships, all
  /// of them too; both bounds of one number, equal, are "is".
  static func parseNode(_ f: JSONValue) throws -> RuleNode {
    if let op = f["operator"]?.stringValue {
      guard let group = RuleGroup.Operator(jmap: op) else { throw RuleError.unknownOperator(op) }
      let items = try (f["conditions"]?.arrayValue ?? []).map(parseNode)
      let rows = items.compactMap(\.row)
      let alike = rows.count == items.count && !rows.isEmpty
      if group == .none, rows.count == 1, let negation = rows[0].operator.negation {
        var r = rows[0]
        r.operator = negation
        return .row(r)
      }
      if group != .all, alike, let field = rows.first?.field, field.kind == .string,
         rows.allSatisfy({ $0.field == field && $0.operator == .equals }) {
        var r = RuleRow(field: field, operator: group == .any ? .anyOf : .noneOf)
        r.strings = rows.map(\.text)
        return .row(r)
      }
      if alike, rows.allSatisfy({ $0.field == .playlist && $0.operator == .inAny && $0.lists.count == 1 }) {
        var r = RuleRow(field: .playlist, operator: group == .all ? .inAll : group == .any ? .inAny : .inNone)
        r.lists = rows.map { $0.lists[0] }
        return .row(r)
      }
      return .group(RuleGroup(operator: group, items: items))
    }
    guard let members = f.objectValue else { throw RuleError.unknownCondition(f.description) }
    let keys = members.keys.sorted()
    if keys.count == 2, let field = RuleField.allCases.first(where: { $0.kind == .number && keys == [$0.atLeast!, $0.atMost!].sorted() }),
       members[field.atLeast!] == members[field.atMost!] {
      var r = RuleRow(field: field, operator: .equals)
      r.number = members[field.atLeast!]?.doubleValue
      return .row(r)
    }
    let rows = try keys.map { try parseCondition($0, members[$0]!) }
    return rows.count == 1 ? rows[0] : .group(RuleGroup(items: rows))
  }

  static func parseCondition(_ name: String, _ value: JSONValue) throws -> RuleNode {
    func text(_ field: RuleField, _ op: RuleOperator) -> RuleNode {
      var r = RuleRow(field: field, operator: op)
      r.text = value.stringValue ?? ""
      return .row(r)
    }
    func number(_ field: RuleField, _ op: RuleOperator) -> RuleNode {
      var r = RuleRow(field: field, operator: op)
      r.number = value.doubleValue.map { field == .duration ? $0 / 60 : $0 }
      return .row(r)
    }
    func flag(_ field: RuleField, _ yes: RuleOperator, _ no: RuleOperator) -> RuleNode {
      .row(RuleRow(field: field, operator: value.boolValue == false ? no : yes))
    }
    func date(_ op: RuleOperator) -> RuleNode {
      var r = RuleRow(field: .lastPlayed, operator: op)
      r.date = value.stringValue.flatMap(parseUTCDate) ?? Date()
      return .row(r)
    }
    func list(smart: Bool) -> RuleNode {
      var r = RuleRow(field: .playlist, operator: .inAny)
      r.lists = [RuleList(id: value.stringValue ?? "", smart: smart)]
      return .row(r)
    }
    for field in RuleField.allCases {
      if name == field.equals { return text(field, .equals) }
      if name == field.contains { return text(field, .contains) }
      if name == field.has { return flag(field, .isSet, .isUnset) }
      if name == field.atLeast { return number(field, .atLeast) }
      if name == field.atMost { return number(field, .atMost) }
    }
    switch name {
    case "lastPlayedBefore": return date(.before)
    case "lastPlayedAfter": return date(.after)
    case "lastPlayedWithinDays": return number(.lastPlayed, .within)
    case "hasAudio": return flag(.audio, .present, .missing)
    case "inPlaylist": return list(smart: false)
    case "inSmartPlaylist": return list(smart: true)
    default: throw RuleError.unknownCondition(name)
    }
  }
}

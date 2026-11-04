import Foundation
import Jinx
import JinxJSON
import JinxWire

/// A refusal from the server: a method error, or a `SetError` on the one
/// object a call was about.
public struct ServerRefusal: Error, LocalizedError, Hashable {
  public var type: String
  public var detail: String?

  public var errorDescription: String? {
    [type, detail].compactMap(\.self).joined(separator: ": ")
  }

  init(_ error: MethodError) {
    self.type = error.type
    self.detail = error.description
  }

  init?(setError json: JSONValue?) {
    guard let json, let type = json["type"]?.stringValue else { return nil }
    self.type = type
    self.detail = json["description"]?.stringValue
  }
}

/// What a rule being edited matches now: one page of track ids, and how
/// many there are in all.
public struct RulePreview: Hashable, Sendable {
  public var trackIds: [String]
  public var total: Int
}

// Editing a smart playlist goes to the server directly, not through the
// mirror: the preview is a query the mirror doesn't keep, and a saved
// rule is one the server has checked, so its refusals (a rule that
// won't run, a loop, a playlist in use) come straight back to the
// editor instead of surfacing later as a reverted sync.  The mirror
// picks the change up on the sync asked for right after.  So, unlike a
// plain playlist's edits, these need the server.  -- claude, 2026-11-03
extension Library {
  /// One method call to the server, with the account filled in; the
  /// response's arguments, or the method error as a refusal.
  func call(_ method: String, _ arguments: [String: JSONValue]) async throws -> JSONValue {
    let session: Session
    if let known = self.session {
      session = known
    } else {
      session = try await transport.fetchSession()
      self.session = session
    }
    var args = arguments
    args["accountId"] = .string(accountId)
    let request = Request(
      using: [Session.coreCapability, jukeCapability],
      methodCalls: [Invocation(name: method, arguments: .object(args), callId: "0")]
    )
    let response = try await transport.send(request, to: session.apiUrl)
    guard let first = response.response(to: "0") else { throw ServerRefusal(MethodError(type: "serverFail", description: "no response")) }
    do {
      return try first.result()
    } catch {
      throw ServerRefusal(error)
    }
  }

  /// Runs a draft's rule for one page of its tracks.  Throws for a
  /// draft that isn't finished, and for a rule the server refuses.
  public func preview(_ draft: SmartPlaylistDraft, position: Int = 0, pageSize: Int = 100) async throws -> RulePreview {
    let args = try draft.queryArguments(position: position, pageSize: pageSize)
    let result = try await call("Track/query", args)
    let ids = result["ids"]?.arrayValue?.compactMap(\.stringValue) ?? []
    var total = result["total"]?.intValue ?? ids.count
    if let cap = draft.trackLimit { total = min(total, cap) }
    return RulePreview(trackIds: ids, total: total)
  }

  /// Saves a draft as a new smart playlist, or over an existing one, and
  /// returns the playlist's id once the library has it, so the caller can
  /// show it at once: a saved change at once, a new playlist when the
  /// mirror brings it.  Throws for a draft that isn't finished and for a
  /// rule the server refuses.
  @discardableResult
  public func save(_ draft: SmartPlaylistDraft, as id: String? = nil) async throws -> String {
    let properties = try draft.properties()
    let result: JSONValue
    let savedId: String
    if let id {
      result = try await call("SmartPlaylist/set", ["update": .object([id: properties])])
      if let refusal = ServerRefusal(setError: result["notUpdated"]?[id]) { throw refusal }
      // The server has what was sent, plus any seed it chose, so the
      // library takes that now rather than waiting on the mirror, which
      // can be busy for seconds running rules.  -- claude, 2026-11-04
      if let current = smartPlaylists.first(where: { $0.id == id }),
         case .object(var value) = properties {
        if case .object(let filled)? = result["updated"]?[id] { value.merge(filled) { _, new in new } }
        if let saved = SmartPlaylist(key: current.key, id: id, value: .object(value)) {
          try await adopt([saved])
          syncNow()
          return id
        }
      }
      savedId = id
    } else {
      result = try await call("SmartPlaylist/set", ["create": .object(["s": properties])])
      if let refusal = ServerRefusal(setError: result["notCreated"]?["s"]) { throw refusal }
      guard let created = result["created"]?["s"]?["id"]?.stringValue else {
        throw ServerRefusal(MethodError(type: "serverFail", description: "created nothing"))
      }
      savedId = created
    }
    await syncUntil { library in
      library.smartPlaylists.contains { $0.id == savedId && $0.matches(properties) }
    }
    return savedId
  }

  /// Destroys a smart playlist on the server.  Throws `playlistInUse`
  /// for one another rule names.
  public func delete(_ playlist: SmartPlaylist) async throws {
    let result = try await call("SmartPlaylist/set", ["destroy": .array([.string(playlist.id)])])
    if let refusal = ServerRefusal(setError: result["notDestroyed"]?[playlist.id]) { throw refusal }
    await syncUntil { library in !library.smartPlaylists.contains { $0.id == playlist.id } }
  }

  /// Asks for a sync and waits, a few seconds at most, for the mirror
  /// to show a change made on the server, so a view opened right after
  /// finds it.  The push connection usually brings it sooner.
  private func syncUntil(_ done: @MainActor (Library) -> Bool) async {
    syncNow()
    for _ in 0..<50 {
      if done(self) { return }
      try? await Task.sleep(for: .milliseconds(100))
    }
  }
}

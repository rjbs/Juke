import AVFoundation
import Foundation
import Testing
@testable import JukeKit

// These run against a live server, named by JUKE_TEST_SERVER (such as
// http://localhost:8737), and are skipped without one.
let testServer = ProcessInfo.processInfo.environment["JUKE_TEST_SERVER"].flatMap(URL.init(string:))

// openLibrary syncs a fresh mirror of the test server into a temporary
// directory and waits for its index.
@MainActor
func openLibrary() async throws -> Library {
  let dir = FileManager.default.temporaryDirectory.appending(path: "jukekit-\(UUID())")
  let server = try #require(testServer)
  let library = try await Library.open(server: server, directory: dir)
  library.start()
  while case .syncing = library.phase { try await Task.sleep(for: .milliseconds(50)) }
  #expect(library.phase == .ready)
  return library
}

// checkPlayable fetches a track's audio through the cache and checks
// that it has an audio file's extension, that AVFoundation can play
// it, and that it is the given length or longer.
func checkPlayable(_ library: Library, _ track: Track, atLeast seconds: Double) async throws {
  let url = try await library.audioFile(for: track)
  let asset = AVURLAsset(url: url)
  let (playable, duration) = try await asset.load(.isPlayable, .duration)
  #expect(["mp3", "mp4", "m4a"].contains(url.pathExtension), "\(url.lastPathComponent)")
  #expect(playable, "\(track.title) at \(url.path)")
  #expect(duration.seconds >= seconds, "\(track.title): \(duration.seconds) s")
}

@Test(.enabled(if: testServer != nil))
@MainActor
func syncAndPlay() async throws {
  let library = try await openLibrary()
  #expect(!library.albumList.isEmpty)
  let album = try #require(library.albumList.first { library.tracks(of: $0).joined().contains(where: \.isPlayable) })
  let playable = library.tracks(of: album).joined().first { $0.isPlayable }
  let track = try #require(playable)
  try await checkPlayable(library, track, atLeast: 1)
}

// eventually waits, by default up to five seconds, for a condition.
@MainActor
func eventually(_ what: String, within seconds: Double = 5, _ condition: () async throws -> Bool) async throws {
  for _ in 0..<Int(seconds * 20) {
    if try await condition() { return }
    try await Task.sleep(for: .milliseconds(50))
  }
  Issue.record("never: \(what)")
}

// checkWrite applies a write to a track and checks that the index shows
// the result and that the write reaches the server.
@MainActor
func checkWrite(_ library: Library, _ track: Track, _ what: String,
                write: () async throws -> Void, shows: @escaping (Track) -> Bool) async throws {
  try await write()
  try await eventually("\(what) in the index") { library.tracks[track.id].map(shows) ?? false }
  try await eventually("\(what) sent to the server") {
    try await library.engine.object(key: track.key)?.pending == nil
  }
}

@Test(.enabled(if: testServer != nil))
@MainActor
func writes() async throws {
  let library = try await openLibrary()
  let candidate = library.tracks.values.first { $0.isPlayable }
  let track = try #require(candidate)
  let rating: Double = track.rating == 2.5 ? 3.5 : 2.5
  try await checkWrite(library, track, "rating \(rating)",
                       write: { try await library.setRating(rating, on: track) },
                       shows: { $0.rating == rating })
  try await checkWrite(library, track, "a play",
                       write: { try await library.recordPlay(of: track) },
                       shows: { $0.playCount == track.playCount + 1 && $0.lastPlayed != nil })
}

// The player itself, not just the audio file: plays an album from its
// second playable track and checks that the audio is really advancing,
// that the queue is the album's playable tracks, and that next moves on.
@Test(.enabled(if: testServer != nil))
@MainActor
func player() async throws {
  let library = try await openLibrary()
  let found = library.albumList.first { library.tracks(of: $0).joined().filter(\.isPlayable).count >= 3 }
  let album = try #require(found)
  let tracks = Array(library.tracks(of: album).joined())
  let start = try #require(tracks.indices.filter { tracks[$0].isPlayable }.dropFirst().first)

  let player = Player(library: library)
  player.play(tracks, at: start)
  #expect(player.current == tracks[start])
  #expect(player.queue == tracks.filter(\.isPlayable))
  try await eventually("audio advancing") { player.player.currentTime().seconds > 0.5 }
  #expect(player.isPlaying)
  #expect(player.failure == nil)
  try await eventually("duration known") { (player.duration ?? 0) > 1 }
  try await eventually("elapsed reported") { player.elapsed > 0.5 }

  // Seeking to the middle carries on from there, and elapsed follows.
  let middle = player.duration! / 2
  player.seek(to: middle)
  try await eventually("playing from the middle") { player.player.currentTime().seconds > middle + 0.25 }
  try await eventually("elapsed follows the seek") { player.elapsed > middle }

  player.next()
  #expect(player.index == 2)
  try await eventually("next track advancing") {
    player.player.currentTime().seconds > 0.5 && player.player.currentItem != nil
  }
  player.togglePause()
  #expect(!player.isPlaying)
}

// A library opened once opens again with its server unreachable: the
// account is remembered, the index comes from the mirror, and the
// failure to sync is reported rather than fatal.
@Test(.enabled(if: testServer != nil))
@MainActor
func offlineStart() async throws {
  let dir = FileManager.default.temporaryDirectory.appending(path: "jukekit-\(UUID())")
  let online = try await Library.open(server: try #require(testServer), directory: dir)
  online.start()
  while case .syncing = online.phase { try await Task.sleep(for: .milliseconds(50)) }
  let count = online.tracks.count
  let cached = try #require(online.tracks.values.first { $0.isPlayable })
  _ = try await online.audioFile(for: cached)
  online.stop()

  let nowhere = URL(string: "http://127.0.0.1:9")!
  let offline = try await Library.open(server: nowhere, directory: dir)
  offline.start()
  try await eventually("ready offline") { offline.phase == .ready }
  #expect(offline.tracks.count == count)
  _ = try await offline.audioFile(for: cached)
  try await eventually("a sync failure reported") { offline.syncFailure != nil }
  offline.stop()

  let fresh = FileManager.default.temporaryDirectory.appending(path: "jukekit-\(UUID())")
  await #expect(throws: (any Error).self) { try await Library.open(server: nowhere, directory: fresh) }
}

// checkPinned checks whether every playable track's audio is in the
// cache, complete and pinned.
func checkPinned(_ library: Library, _ tracks: [Track], _ pinned: Bool,
                 sourceLocation: SourceLocation = #_sourceLocation) async throws {
  for blob in tracks.compactMap(\.blobId) {
    let record = try await library.blobs.status(blob)
    #expect((record?.isPinned ?? false) == pinned, "\(blob)", sourceLocation: sourceLocation)
    if pinned { #expect(record?.isComplete == true, "\(blob)", sourceLocation: sourceLocation) }
  }
}

// Keeping an album offline downloads and pins its audio, survives a
// relaunch, and releasing it unpins the audio again.
@Test(.enabled(if: testServer != nil))
@MainActor
func keepOffline() async throws {
  let dir = FileManager.default.temporaryDirectory.appending(path: "jukekit-\(UUID())")
  let server = try #require(testServer)
  var library = try await Library.open(server: server, directory: dir)
  library.start()
  while case .syncing = library.phase { try await Task.sleep(for: .milliseconds(50)) }

  let found = library.albumList.first { (3...6).contains(library.tracks(of: $0).joined().filter(\.isPlayable).count) }
  let album = try #require(found)
  let tracks = Array(library.tracks(of: album).joined())
  library.offline.keep(album, true)
  #expect(library.offline.isKept(album))
  try await eventually("album downloaded") {
    let p = library.offline.progress(of: tracks)
    return p.present == p.wanted
  }
  #expect(library.offline.present == library.offline.wanted)
  try await checkPinned(library, tracks, true)
  library.stop()

  library = try await Library.open(server: server, directory: dir)
  library.start()
  try await eventually("ready again") { library.phase == .ready }
  #expect(library.offline.isKept(album))
  try await eventually("kept audio recognised") {
    let p = library.offline.progress(of: tracks)
    return p.present == p.wanted
  }

  library.offline.keep(album, false)
  #expect(library.offline.wanted == 0)
  try await eventually("audio released") {
    for blob in tracks.compactMap(\.blobId) where try await library.blobs.status(blob)?.isPinned == true { return false }
    return true
  }
  library.stop()
}

// checkServerPlaylist opens a fresh mirror of the server and checks that
// the playlist with this server id has the given title and tracks.
@MainActor
func checkServerPlaylist(_ id: String, title: String, tracks: [Track],
                         sourceLocation: SourceLocation = #_sourceLocation) async throws {
  let fresh = try await openLibrary()
  defer { fresh.stop() }
  let found = fresh.playlists.first { $0.serverId == id }
  let playlist = try #require(found, "playlist \(id) on the server", sourceLocation: sourceLocation)
  #expect(playlist.title == title, sourceLocation: sourceLocation)
  #expect(playlist.trackIds == tracks.map(\.id), sourceLocation: sourceLocation)
}

// A playlist made, filled, reordered, trimmed, renamed and deleted here
// reaches the server at each step.  A track appears twice, so removal
// has to go by position.
@Test(.enabled(if: testServer != nil))
@MainActor
func playlistEditing() async throws {
  let library = try await openLibrary()
  defer { library.stop() }
  let album = try #require(library.albumList.first { library.tracks(of: $0).joined().count >= 3 })
  let t = Array(library.tracks(of: album).joined().prefix(3))
  let title = "JukeKit test \(UUID())"

  let key = try await library.createPlaylist(titled: title, tracks: [t[0], t[1]])
  var playlist: Playlist { library.playlists.first { $0.key == key }! }
  try await eventually("the playlist in the index") { library.playlists.contains { $0.key == key } }
  try await eventually("the playlist on the server") { playlist.serverId != nil }
  let id = try #require(playlist.serverId)

  try await library.add([t[2], t[0]], to: playlist)
  try await library.moveTracks(at: [2], to: 0, in: playlist)
  try await library.removeTracks(at: [3], from: playlist)
  try await library.rename(playlist, to: title + " renamed")
  // Each edit reaches the index through the change stream, a moment
  // after the write returns.
  try await eventually("the edits in the index") { playlist.trackIds == [t[2], t[0], t[1]].map(\.id) }
  try await eventually("edits sent") { try await library.engine.object(key: key)?.pending == nil }
  try await checkServerPlaylist(id, title: title + " renamed", tracks: [t[2], t[0], t[1]])

  try await library.delete(playlist)
  try await eventually("gone from the index") { !library.playlists.contains { $0.key == key } }
  try await eventually("destroy sent") { try await library.engine.object(key: key) == nil }
  let fresh = try await openLibrary()
  #expect(!fresh.playlists.contains { $0.serverId == id })
  fresh.stop()
}

// A write from one device reaches another by push, well inside the
// 60-second poll the library otherwise relies on.
@Test(.enabled(if: testServer != nil))
@MainActor
func pushedChanges() async throws {
  let writer = try await openLibrary()
  let reader = try await openLibrary()
  defer { writer.stop(); reader.stop() }
  let candidate = writer.tracks.values.first { $0.isPlayable }
  let track = try #require(candidate)
  let rating: Double = track.rating == 1.5 ? 4.5 : 1.5

  try await writer.setRating(rating, on: track)
  try await eventually("the rating on the other device") { reader.tracks[track.id]?.rating == rating }
}

// jmap sends one method call straight to the test server, outside the
// library: how a test makes and destroys smart playlists, which the app
// only reads.  Returns the response's arguments.
@MainActor
func jmap(_ method: String, _ arguments: [String: Any]) async throws -> [String: Any] {
  let server = try #require(testServer)
  let (sessionData, _) = try await URLSession.shared.data(from: server.appending(path: ".well-known/jmap"))
  let session = try #require(try JSONSerialization.jsonObject(with: sessionData) as? [String: Any])
  let accountId = try #require((session["primaryAccounts"] as? [String: Any])?[jukeCapability] as? String)
  let apiUrl = try #require((session["apiUrl"] as? String).flatMap(URL.init(string:)))
  var request = URLRequest(url: apiUrl)
  request.httpMethod = "POST"
  request.setValue("application/json", forHTTPHeaderField: "Content-Type")
  var args = arguments
  args["accountId"] = accountId
  request.httpBody = try JSONSerialization.data(withJSONObject: [
    "using": ["urn:ietf:params:jmap:core", jukeCapability],
    "methodCalls": [[method, args, "0"]],
  ])
  let (data, _) = try await URLSession.shared.data(for: request)
  let body = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
  let first = try #require((body["methodResponses"] as? [[Any]])?.first)
  #expect(first[0] as? String == method, "\(first)")
  return try #require(first[1] as? [String: Any])
}

// A smart playlist on the server appears in the library with the tracks
// its rule matches, in the rule's order; a change to the rule changes
// the list with no track having moved; it can be kept offline; and it
// goes when destroyed.
@Test(.enabled(if: testServer != nil))
@MainActor
func smartPlaylists() async throws {
  let library = try await openLibrary()
  defer { library.stop() }
  let found = library.albumList.first { (2...8).contains(library.tracks(of: $0).joined().filter(\.isPlayable).count) }
  let album = try #require(found)
  let title = "JukeKit smart \(UUID())"
  let created = try await jmap("SmartPlaylist/set", ["create": ["s": [
    "title": title,
    "filter": ["operator": "AND", "conditions": [["hasAudio": true], ["albumTitle": album.title]]],
    "sort": [["property": "title"]],
  ]]])
  let id = try #require(((created["created"] as? [String: Any])?["s"] as? [String: Any])?["id"] as? String)

  try await eventually("the smart playlist in the index") { library.smartPlaylists.contains { $0.id == id } }
  let playlist = try #require(library.smartPlaylists.first { $0.id == id })
  #expect(playlist.title == title)
  // The rule matches by title, and a title can belong to more than one
  // album, as "Greatest Hits" does.
  let sameTitle = library.albumList.filter { $0.title.lowercased() == album.title.lowercased() }
  let expected = Set(sameTitle.flatMap { library.tracks(of: $0).joined() }.filter(\.isPlayable).map(\.id))
  try await eventually("its tracks") { Set(library.tracks(of: playlist).map(\.id)) == expected }
  let titles = library.tracks(of: playlist).map { $0.title.lowercased() }
  #expect(titles == titles.sorted(), "not in title order: \(titles)")

  // The rule changes the moment its first result has landed, which is
  // when a pass may still be in flight with the old rule; and a second
  // library, opened after the change, sees only the new rule.
  _ = try await jmap("SmartPlaylist/set", ["update": [id: ["limit": 2]]])
  try await eventually("the cap applied") { library.tracks(of: playlist).count == 2 }
  let other = try await openLibrary()
  defer { other.stop() }
  try await eventually("the cap in the other library") { other.tracks(of: playlist).count == 2 }

  library.offline.keep(playlist, true)
  #expect(library.offline.isKept(playlist))
  let kept = library.tracks(of: playlist)
  try await eventually("its audio downloaded") {
    let p = library.offline.progress(of: kept)
    return p.present == p.wanted
  }
  try await checkPinned(library, kept, true)
  library.offline.keep(playlist, false)
  try await eventually("its audio released") {
    for blob in kept.compactMap(\.blobId) where try await library.blobs.status(blob)?.isPinned == true { return false }
    return true
  }

  _ = try await jmap("SmartPlaylist/set", ["destroy": [id]])
  try await eventually("gone from the index") { !library.smartPlaylists.contains { $0.id == id } }
  try await eventually("its subscription dropped") {
    try await library.engine.subscriptions().allSatisfy { $0.id != playlist.subscriptionId }
  }
}

// A smart playlist without a limit of its own gets every track its rule
// matches, however many: the engine pages the query past the server's
// cap on one answer (5,000 ids).  This proves it only when the test
// server's library is bigger than the cap; the trial library is.
@Test(.enabled(if: testServer != nil))
@MainActor
func smartPlaylistPastTheCap() async throws {
  let created = try await jmap("SmartPlaylist/set", ["create": ["s": [
    "title": "JukeKit paging \(UUID())", "filter": ["hasAudio": true], "sort": [["property": "title"]],
  ]]])
  let id = try #require(((created["created"] as? [String: Any])?["s"] as? [String: Any])?["id"] as? String)
  let library = try await openLibrary()
  defer { library.stop() }
  let playable = library.tracks.values.filter(\.isPlayable).count
  try await eventually("the rule's result", within: 60) { library.smartResults[id] != nil }
  let result = try #require(library.smartResults[id])
  #expect(result.total == playable)
  #expect(result.trackIds.count == playable, "got \(result.trackIds.count) of \(playable)")
  #expect(Set(result.trackIds).count == result.trackIds.count, "a track repeated across pages")
  _ = try await jmap("SmartPlaylist/set", ["destroy": [id]])
}

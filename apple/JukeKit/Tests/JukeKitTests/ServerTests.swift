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
// that AVFoundation can play it and finds it the given length or longer.
func checkPlayable(_ library: Library, _ track: Track, atLeast seconds: Double) async throws {
  let url = try await library.audioFile(for: track)
  let asset = AVURLAsset(url: url)
  let (playable, duration) = try await asset.load(.isPlayable, .duration)
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

// eventually waits up to five seconds for a condition.
@MainActor
func eventually(_ what: String, _ condition: () async throws -> Bool) async throws {
  for _ in 0..<100 {
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

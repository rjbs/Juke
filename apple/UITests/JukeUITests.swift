import XCTest

// These drive the iOS app in the simulator against a live server: by
// default the scratch server (bin/scratch-server, on localhost:8738), or
// the one named by JUKE_TEST_SERVER, which xcodebuild passes through when
// it's given as TEST_RUNNER_JUKE_TEST_SERVER.  They play audio and record
// plays, so don't point them at a library you care about.
//
// Each test saves screenshots as attachments; bin/ui-test exports them.

let testServer = ProcessInfo.processInfo.environment["JUKE_TEST_SERVER"] ?? "http://localhost:8738"

/// The first sync of a full library in the simulator takes a while.
let syncTimeout: TimeInterval = 180

@MainActor
final class JukeUITests: XCTestCase {
  var app: XCUIApplication!

  override func setUp() async throws {
    continueAfterFailure = false
    app = XCUIApplication()
    // A launch argument "-key value" lands in UserDefaults' argument
    // domain, which @AppStorage reads.  -- claude, 2026-10-25
    app.launchArguments = ["-serverURL", testServer]
    app.launch()
    XCTAssertTrue(app.navigationBars["Albums"].waitForExistence(timeout: syncTimeout), "the album list never appeared")
  }

  func testPlayFromAlbum() throws {
    try checkPlays(album: "Richard D. James Album", track: "Milkman")
  }

  // MARK: - Helpers

  /// Finds an album by searching, opens it, taps a track, and checks the
  /// player bar shows that track playing.
  func checkPlays(album: String, track: String, file: StaticString = #filePath, line: UInt = #line) throws {
    openAlbum(album, file: file, line: line)
    let row = app.buttons.containing(.staticText, identifier: track).firstMatch
    XCTAssertTrue(row.waitForExistence(timeout: 10), "no row for \(track)", file: file, line: line)
    row.tap()

    let title = app.staticTexts["player.title"]
    XCTAssertTrue(title.waitForExistence(timeout: 30), "the player bar never appeared", file: file, line: line)
    XCTAssertEqual(title.label, track, file: file, line: line)
    XCTAssertFalse(app.staticTexts["player.failure"].exists, "the player reported a failure", file: file, line: line)
    XCTAssertTrue(
      app.buttons["player.playPause"].waitFor(label: "Pause", timeout: 30),
      "the player never started", file: file, line: line
    )
    snapshot("playing \(track)")
  }

  func openAlbum(_ album: String, file: StaticString = #filePath, line: UInt = #line) {
    let search = app.searchFields.firstMatch
    if !search.exists { app.swipeDown() }
    XCTAssertTrue(search.waitForExistence(timeout: 5), "no search field", file: file, line: line)
    search.tap()
    search.typeText(album)
    let cell = app.buttons.containing(.staticText, identifier: album).firstMatch
    XCTAssertTrue(cell.waitForExistence(timeout: 10), "no album \(album)", file: file, line: line)
    cell.tap()
    XCTAssertTrue(app.navigationBars[album].waitForExistence(timeout: 10), "\(album) never opened", file: file, line: line)
    snapshot(album)
  }

  func snapshot(_ name: String) {
    let shot = XCTAttachment(screenshot: app.screenshot())
    shot.name = name
    shot.lifetime = .keepAlways
    add(shot)
  }
}

extension XCUIElement {
  /// Waits for the element's label to become the given one.
  func waitFor(label: String, timeout: TimeInterval) -> Bool {
    let matches = NSPredicate(format: "label == %@", label)
    let expectation = XCTNSPredicateExpectation(predicate: matches, object: self)
    return XCTWaiter().wait(for: [expectation], timeout: timeout) == .completed
  }
}

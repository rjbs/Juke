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

  func testPlayerBarLeavesLastTrackVisible() throws {
    try checkLastTrackClear(album: "Symphonic Star Trek", last: "Borg")
  }

  func testEditPlaylist() throws {
    let title = "UI Test \(Int(Date().timeIntervalSince1970))"
    createPlaylist(title)

    showSidebar()
    sidebarRow("Albums").tap()
    openAlbum("Anti EP [WAP54CD]")
    app.buttons["album.addToPlaylist"].tap()
    app.buttons[title].tap()
    let lost = app.buttons.containing(.staticText, identifier: "Lost").firstMatch
    lost.press(forDuration: 1)
    app.buttons.matching(NSPredicate(format: "label == %@ AND identifier != %@", "Add to Playlist", "album.addToPlaylist")).firstMatch.tap()
    app.buttons[title].tap()

    openPlaylist(title)
    checkPlaylistRows(["Lost", "Djarum", "Flutter", "Lost"])
    snapshot("playlist filled")

    // Swiping away the second "Lost" must leave the first.
    let last = app.cells.containing(.staticText, identifier: "4").firstMatch
    last.swipeLeft()
    app.buttons["Delete"].tap()
    checkPlaylistRows(["Lost", "Djarum", "Flutter"])

    let renamed = title + " Renamed"
    app.buttons["playlist.menu"].tap()
    app.buttons["Rename…"].tap()
    let field = app.alerts.textFields.firstMatch
    field.clearAndType(renamed)
    XCTAssertEqual(field.value as? String, renamed)
    confirmAlert("Rename")
    XCTAssertTrue(app.navigationBars[renamed].waitForExistence(timeout: 5), "never renamed")

    app.buttons["playlist.menu"].tap()
    app.buttons["Delete Playlist…"].tap()
    app.buttons["Delete Playlist"].firstMatch.tap()
    XCTAssertTrue(app.staticTexts["No Such Playlist"].waitForExistence(timeout: 5), "never deleted")
    showSidebar()
    XCTAssertFalse(sidebarRow(renamed).exists, "still in the sidebar")
  }

  func testQueueControls() throws {
    openAlbum("Anti EP [WAP54CD]")
    contextMenu(on: "Flutter", choose: "Play Next")
    checkPlaying("Flutter")
    contextMenu(on: "Lost", choose: "Add to Queue")
    contextMenu(on: "Djarum", choose: "Play Next")
    snapshot("queued")
    app.buttons["Next"].tap()
    checkPlaying("Djarum")
    app.buttons["Next"].tap()
    checkPlaying("Lost")

    let shuffle = app.buttons["player.shuffle"]
    XCTAssertEqual(shuffle.value as? String, "off")
    shuffle.tap()
    XCTAssertEqual(shuffle.value as? String, "on")
    let repeatButton = app.buttons["player.repeat"]
    for want in ["all", "one", "off"] {
      repeatButton.tap()
      XCTAssertEqual(repeatButton.value as? String, want)
    }
    // Let the repeat symbol finish changing before the screenshot.
    Thread.sleep(forTimeInterval: 1)
    snapshot("shuffled")
  }

  func testArtistBrowser() throws {
    showSidebar()
    sidebarRow("Artists").tap()
    XCTAssertTrue(app.navigationBars["Artists"].waitForExistence(timeout: 5), "no artist list")
    search(for: "Autechre")
    snapshot("artists")
    app.buttons.containing(.staticText, identifier: "Autechre").firstMatch.tap()
    XCTAssertTrue(app.navigationBars["Autechre"].waitForExistence(timeout: 5), "no artist page")
    snapshot("Autechre")
    let album = app.buttons.containing(.staticText, identifier: "Anti EP [WAP54CD]").firstMatch
    XCTAssertTrue(album.waitForExistence(timeout: 5), "Anti EP isn't on the artist page")
    album.tap()
    XCTAssertTrue(app.navigationBars["Anti EP [WAP54CD]"].waitForExistence(timeout: 5), "the album never opened")
  }

  func testSongBrowser() throws {
    showSidebar()
    sidebarRow("Songs").tap()
    XCTAssertTrue(app.navigationBars["Songs"].waitForExistence(timeout: 5), "no song list")
    search(for: "djarum")
    let row = app.buttons.containing(.staticText, identifier: "Djarum").firstMatch
    XCTAssertTrue(row.waitForExistence(timeout: 10), "searching found no Djarum")
    snapshot("found")
    row.tap()
    checkPlaying("Djarum")

    // With the search cleared, sorting by rating puts five-star tracks
    // first.
    showSidebar()
    sidebarRow("Songs").tap()
    app.buttons["songs.sort"].tap()
    app.buttons["Rating"].tap()
    let first = app.cells.firstMatch
    XCTAssertTrue(first.waitForExistence(timeout: 10), "no songs")
    XCTAssertTrue(first.descendants(matching: .any)["5 stars"].waitForExistence(timeout: 5),
                  "the first song by rating isn't five stars")
    snapshot("by rating")
  }

  func testNowPlaying() throws {
    try checkPlays(album: "Richard D. James Album", track: "Milkman")
    contextMenu(on: "Fingerbib", choose: "Play Next")

    app.staticTexts["player.title"].tap()
    let title = app.staticTexts["nowPlaying.title"]
    XCTAssertTrue(title.waitForExistence(timeout: 5), "the now-playing screen never opened")
    XCTAssertEqual(title.label, "Milkman")
    XCTAssertEqual(app.staticTexts["nowPlaying.album"].label, "Richard D. James Album")
    let elapsed = app.staticTexts["nowPlaying.elapsed"]
    XCTAssertTrue(elapsed.waitFor(label: "0:01", timeout: 10) || elapsed.label != "0:00", "time isn't passing")

    // Seeking halfway puts elapsed and remaining about level.
    app.sliders["nowPlaying.seek"].adjust(toNormalizedSliderPosition: 0.5)
    let (played, left) = (Self.seconds(elapsed.label), Self.seconds(app.staticTexts["nowPlaying.remaining"].label))
    XCTAssertLessThan(abs(played - left), 0.2 * Double(played + left), "after seeking halfway: \(played)s played, \(left)s left")

    // Up next: the queued track first, under its own heading, then the
    // rest of the album.
    XCTAssertTrue(app.staticTexts["Queued"].exists, "no Queued heading")
    XCTAssertTrue(app.staticTexts["Inkey$"].exists, "the rest of the album isn't listed")
    snapshot("now playing")
    let upNext = app.buttons.matching(identifier: "nowPlaying.upNext").containing(.staticText, identifier: "Fingerbib").firstMatch
    upNext.tap()
    XCTAssertTrue(title.waitFor(label: "Fingerbib", timeout: 30), "jumping to Fingerbib didn't play it")

    app.buttons["Done"].tap()
    XCTAssertFalse(title.waitForExistence(timeout: 2), "the now-playing screen stayed open")
  }

  // MARK: - Helpers

  /// Opens an album, starts it playing so the player bar shows, scrolls to
  /// the end, and checks the last track sits wholly above the bar.
  func checkLastTrackClear(album: String, last: String, file: StaticString = #filePath, line: UInt = #line) throws {
    openAlbum(album, file: file, line: line)
    app.navigationBars[album].buttons["Play"].tap()
    let bar = app.otherElements["player"]
    XCTAssertTrue(bar.waitForExistence(timeout: 30), "the player bar never appeared", file: file, line: line)

    let row = app.buttons.containing(.staticText, identifier: last).firstMatch
    for _ in 0..<10 where !row.exists || row.frame.maxY > app.frame.maxY {
      app.swipeUp()
    }
    // One more, to be sure the list is at its end.
    app.swipeUp()
    snapshot("end of \(album)")
    XCTAssertTrue(row.exists, "never found \(last)", file: file, line: line)
    XCTAssertLessThanOrEqual(
      row.frame.maxY, bar.frame.minY,
      "\(last) ends at \(row.frame.maxY), below the bar's top at \(bar.frame.minY)", file: file, line: line
    )
  }

  /// "m:ss" or "-m:ss" as seconds.
  static func seconds(_ time: String) -> Double {
    let parts = time.trimmingCharacters(in: CharacterSet(charactersIn: "-")).split(separator: ":").compactMap { Double($0) }
    return parts.reduce(0) { $0 * 60 + $1 }
  }

  func search(for text: String) {
    let field = app.searchFields.firstMatch
    if !field.exists { app.swipeDown() }
    XCTAssertTrue(field.waitForExistence(timeout: 5), "no search field")
    field.tap()
    field.typeText(text)
  }

  /// Taps an alert's button until the alert goes.  A tap made while the
  /// keyboard is still settling can be swallowed.
  func confirmAlert(_ button: String, file: StaticString = #filePath, line: UInt = #line) {
    let alert = app.alerts.firstMatch
    for _ in 0..<3 where alert.exists {
      alert.buttons[button].tap()
      _ = alert.waitForNonExistence(timeout: 2)
    }
    XCTAssertFalse(alert.exists, "the alert stayed up", file: file, line: line)
  }

  /// Long-presses the row for a track and picks from its context menu.
  func contextMenu(on track: String, choose item: String, file: StaticString = #filePath, line: UInt = #line) {
    let row = app.buttons.containing(.staticText, identifier: track).firstMatch
    XCTAssertTrue(row.waitForExistence(timeout: 10), "no row for \(track)", file: file, line: line)
    row.press(forDuration: 1)
    let choice = app.buttons[item]
    XCTAssertTrue(choice.waitForExistence(timeout: 5), "no \(item) in the menu", file: file, line: line)
    choice.tap()
  }

  func checkPlaying(_ track: String, file: StaticString = #filePath, line: UInt = #line) {
    XCTAssertTrue(app.staticTexts["player.title"].waitFor(label: track, timeout: 30),
                  "\(track) never started; playing \(app.staticTexts["player.title"].label)", file: file, line: line)
  }

  /// Goes back to the sidebar, on an iPhone the root of the stack.
  func showSidebar() {
    // An active search hides the navigation bar, back button and all,
    // and the first tap on its close button may only put the keyboard
    // away.
    let close = app.buttons["close"]
    let back = app.navigationBars.buttons["BackButton"]
    for _ in 0..<6 where !app.buttons["playlist.new"].waitForExistence(timeout: 1) {
      if close.exists, close.isHittable {
        close.tap()
      } else if back.exists, back.isHittable {
        back.tap()
      }
    }
    XCTAssertTrue(app.buttons["playlist.new"].waitForExistence(timeout: 5), "no sidebar")
  }

  /// A row of the sidebar, scrolled to if it's below the fold, as a
  /// playlist can be once there are many.
  func sidebarRow(_ title: String) -> XCUIElement {
    let row = app.cells.containing(.staticText, identifier: title).firstMatch
    for _ in 0..<8 where !row.isHittable {
      app.collectionViews.firstMatch.swipeUp()
    }
    return row
  }

  func createPlaylist(_ title: String, file: StaticString = #filePath, line: UInt = #line) {
    showSidebar()
    app.buttons["playlist.new"].tap()
    app.alerts.textFields.firstMatch.typeText(title)
    confirmAlert("Create")
    XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 5), "\(title) never opened", file: file, line: line)
  }

  func openPlaylist(_ title: String, file: StaticString = #filePath, line: UInt = #line) {
    showSidebar()
    sidebarRow(title).tap()
    XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 5), "\(title) never opened", file: file, line: line)
  }

  /// Checks the track titles down a short playlist, all on screen.
  func checkPlaylistRows(_ want: [String], file: StaticString = #filePath, line: UInt = #line) {
    let rows = {
      self.app.cells.allElementsBoundByIndex.compactMap { cell in
        // Each row reads: its number, then its title.
        cell.staticTexts.allElementsBoundByIndex.dropFirst().first?.label
      }
    }
    let matches = NSPredicate { _, _ in rows() == want }
    let done = XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: matches, object: nil)], timeout: 5)
    XCTAssertEqual(done, .completed, "rows are \(rows()), want \(want)", file: file, line: line)
  }

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

  /// Replaces a text field's contents.
  func clearAndType(_ text: String) {
    // A tap on a field with text in it brings up the editing callout,
    // which then eats the next tap, so a field that already has the
    // keyboard, as an alert's does, isn't tapped.  The cursor is then at
    // the end, after the text the deletes remove.
    if !((value(forKey: "hasKeyboardFocus") as? Bool) ?? false) {
      tap()
    }
    let existing = value as? String ?? ""
    typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: existing.count) + text)
  }
}

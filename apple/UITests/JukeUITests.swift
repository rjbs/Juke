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
    await destroyLeftoverPlaylists()
    app = XCUIApplication()
    // A launch argument "-key value" lands in UserDefaults' argument
    // domain, which @AppStorage reads.  -- claude, 2026-10-25
    app.launchArguments = ["-serverURL", testServer]
    app.launch()
    XCTAssertTrue(app.navigationBars["Albums"].waitForExistence(timeout: syncTimeout), "the album list never appeared")
  }

  /// A test that fails partway leaves the playlist it made on the scratch
  /// server, where the next run finds two in the sidebar.  Whatever a
  /// test makes is named for it, so the leftovers can be told apart and
  /// destroyed, of either kind, before the next test launches the app.
  /// Errors are ignored: with the server down there is nothing to clean,
  /// and the launch that follows will say so.
  ///
  /// This runs before a test rather than after, because an async
  /// tearDown never returned after an async test had failed and been
  /// halted.  -- claude, 2026-11-02
  func destroyLeftoverPlaylists() async {
    for type in ["Playlist", "SmartPlaylist"] {
      guard let all = try? await jmap("\(type)/get", ["ids": NSNull()]) else { continue }
      let leftovers = (all["list"] as? [[String: Any]] ?? [])
        .filter { ($0["title"] as? String).map(Self.isTestPlaylist) ?? false }
        .compactMap { $0["id"] as? String }
      if !leftovers.isEmpty {
        _ = try? await jmap("\(type)/set", ["destroy": leftovers])
      }
    }
  }

  static func isTestPlaylist(_ title: String) -> Bool {
    title.hasPrefix("UI Test ") || title.hasPrefix("UI Smart ")
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

  func testSmartPlaylist() async throws {
    let title = "UI Smart \(Int(Date().timeIntervalSince1970))"
    let id = try await jmap("SmartPlaylist/set", ["create": ["s": [
      "title": title,
      "filter": ["operator": "AND", "conditions": [["hasAudio": true], ["ratingAtLeast": 3]]],
      "sort": [["property": "title"]],
      "limit": 3,
    ]]])
    let created = try XCTUnwrap(((id["created"] as? [String: Any])?["s"] as? [String: Any])?["id"] as? String)

    openPlaylist(title)
    // The first cell is the summary; the rows follow, each reading its
    // number, then its title.
    let rows = { self.app.cells.allElementsBoundByIndex.dropFirst().compactMap { $0.staticTexts.allElementsBoundByIndex.dropFirst().first?.label } }
    let threeRows = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in rows().count == 3 }, object: nil)
    XCTAssertEqual(XCTWaiter().wait(for: [threeRows], timeout: 10), .completed, "rows are \(rows())")
    XCTAssertTrue(app.staticTexts["3 tracks"].exists, "no summary line")
    snapshot("smart playlist")

    let first = try XCTUnwrap(rows().first)
    app.buttons.containing(.staticText, identifier: first).firstMatch.tap()
    checkPlaying(first)

    app.buttons["Keep Offline"].tap()
    XCTAssertTrue(app.staticTexts["Available offline"].waitForExistence(timeout: 60), "never downloaded")
    snapshot("kept offline")

    _ = try await jmap("SmartPlaylist/set", ["destroy": [created]])
    XCTAssertTrue(app.staticTexts["No Such Playlist"].waitForExistence(timeout: 10), "never noticed the destroy")
  }

  /// A smart playlist is made in the editor: titled, its one blank
  /// condition dropped so the rule matches everything, the preview's
  /// count read, and the saved playlist opened with the same count;
  /// then deleted from its menu.
  func testSmartPlaylistEditor() throws {
    let title = "UI Smart Edit \(Int(Date().timeIntervalSince1970))"
    showSidebar()
    app.buttons["smartPlaylist.new"].tap()
    let field = app.textFields["smartPlaylist.title"]
    XCTAssertTrue(field.waitForExistence(timeout: 5), "no editor")
    field.tap()
    field.typeText(title)
    app.buttons["rule.remove"].firstMatch.tap()

    let summary = app.staticTexts["smartPlaylist.previewSummary"]
    let counted = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label MATCHES %@", "[0-9,]+ tracks"), object: summary)
    XCTAssertEqual(XCTWaiter().wait(for: [counted], timeout: 15), .completed, "the preview says \(summary.label)")
    let count = summary.label
    snapshot("editor")

    app.buttons["smartPlaylist.save"].tap()
    XCTAssertTrue(app.navigationBars[title].waitForExistence(timeout: 15), "\(title) never opened")
    XCTAssertTrue(app.staticTexts[count].waitForExistence(timeout: 15), "the playlist doesn't say \(count)")
    snapshot("saved")

    app.buttons["smartPlaylist.menu"].tap()
    app.buttons["Delete Smart Playlist…"].tap()
    app.buttons["Delete Smart Playlist"].tap()
    XCTAssertTrue(app.staticTexts["No Such Playlist"].waitForExistence(timeout: 10), "never deleted")
  }

  // MARK: - Helpers

  /// Sends one method call straight to the server, which is how a test
  /// makes and destroys smart playlists; the app only reads them.
  func jmap(_ method: String, _ arguments: [String: Any]) async throws -> [String: Any] {
    let server = try XCTUnwrap(URL(string: testServer))
    let capability = "https://semiotic.systems/jmap/juke"
    let (sessionData, _) = try await URLSession.shared.data(from: server.appending(path: ".well-known/jmap"))
    let session = try XCTUnwrap(try JSONSerialization.jsonObject(with: sessionData) as? [String: Any])
    let accountId = try XCTUnwrap((session["primaryAccounts"] as? [String: Any])?[capability] as? String)
    let apiUrl = try XCTUnwrap((session["apiUrl"] as? String).flatMap(URL.init(string:)))
    var request = URLRequest(url: apiUrl)
    request.httpMethod = "POST"
    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
    var args = arguments
    args["accountId"] = accountId
    request.httpBody = try JSONSerialization.data(withJSONObject: [
      "using": ["urn:ietf:params:jmap:core", capability],
      "methodCalls": [[method, args, "0"]],
    ])
    let (data, _) = try await URLSession.shared.data(for: request)
    let body = try XCTUnwrap(try JSONSerialization.jsonObject(with: data) as? [String: Any])
    let first = try XCTUnwrap((body["methodResponses"] as? [[Any]])?.first)
    XCTAssertEqual(first[0] as? String, method, "\(first)")
    return try XCTUnwrap(first[1] as? [String: Any])
  }


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
    let row = trackRow(track)
    XCTAssertTrue(row.waitForExistence(timeout: 10), "no row for \(track)", file: file, line: line)
    row.press(forDuration: 1)
    let choice = app.buttons[item]
    XCTAssertTrue(choice.waitForExistence(timeout: 5), "no \(item) in the menu", file: file, line: line)
    choice.tap()
  }

  /// A track's row, scrolled to if it's below the fold: a List builds
  /// its rows lazily, so one far enough down doesn't exist until it's
  /// near.
  func trackRow(_ track: String) -> XCUIElement {
    let row = app.buttons.containing(.staticText, identifier: track).firstMatch
    for _ in 0..<8 where !row.exists {
      app.swipeUp()
    }
    return row
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
    let row = trackRow(track)
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

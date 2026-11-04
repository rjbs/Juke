import JukeKit
import SwiftUI

/// The app's one connection: the server named in settings, the library
/// opened on it, and the player over that.  The App owns it, not a
/// window, so on macOS the window can close and open again while the
/// music plays on.
@MainActor @Observable
public final class AppModel {
  public static let defaultServer = "https://juke.tail0324d.ts.net"

  /// The server's URL, as the user typed it.
  public private(set) var serverURL: String
  public private(set) var library: Library?
  public private(set) var player: Player?
  /// Why the last attempt to open the library failed, if it did.
  public private(set) var openFailure: String?
  @ObservationIgnored private var opening: Task<Void, Never>?

  public init() {
    serverURL = UserDefaults.standard.string(forKey: "serverURL") ?? Self.defaultServer
  }

  /// Opens the library, if nothing has tried yet.
  public func openIfNeeded() {
    if opening == nil { reopen() }
  }

  /// Connects to a server, remembering it for next time.
  public func connect(to url: String) {
    serverURL = url
    UserDefaults.standard.set(url, forKey: "serverURL")
    reopen()
  }

  /// Closes the library, if one is open, and opens the server's again.
  public func reopen() {
    opening?.cancel()
    opening = Task { await open() }
  }

  private func open() async {
    library?.stop()
    library = nil
    player = nil
    openFailure = nil
    guard let url = URL(string: serverURL), url.host() != nil else {
      openFailure = "“\(serverURL)” isn't a server URL."
      return
    }
    do {
      let opened = try await Library.open(server: url, directory: Library.directory(for: url))
      if Task.isCancelled { return }
      opened.start()
      library = opened
      player = Player(library: opened)
    } catch {
      if Task.isCancelled { return }
      openFailure = error.localizedDescription
    }
  }
}

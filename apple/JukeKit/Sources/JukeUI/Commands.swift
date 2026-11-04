import SwiftUI

/// What the menu bar can ask the library window to do.  The sidebar
/// publishes these as a focused scene value while it is shown, so the
/// commands act on the key window and are disabled without one.
struct LibraryActions {
  var newPlaylist: () -> Void
  var newSmartPlaylist: () -> Void
}

private struct LibraryActionsKey: FocusedValueKey {
  typealias Value = LibraryActions
}

extension FocusedValues {
  var libraryActions: LibraryActions? {
    get { self[LibraryActionsKey.self] }
    set { self[LibraryActionsKey.self] = newValue }
  }
}

/// The app's menu commands.  File → New Playlist is ⌘N and New Smart
/// Playlist is ⌥⌘N, as in Music; there is no New Window, since the
/// library has one.
public struct JukeCommands: Commands {
  @FocusedValue(\.libraryActions) private var actions

  public init() {}

  public var body: some Commands {
    CommandGroup(replacing: .newItem) {
      Button("New Playlist") { actions?.newPlaylist() }
        .keyboardShortcut("n")
        .disabled(actions == nil)
      Button("New Smart Playlist") { actions?.newSmartPlaylist() }
        .keyboardShortcut("n", modifiers: [.command, .option])
        .disabled(actions == nil)
    }
  }
}

import JukeUI
import SwiftUI

@main
struct JukeApp: App {
  @State private var model = AppModel()

  var body: some Scene {
    #if os(macOS)
    // One window, as Music has: Window → Juke, or ⌘0, brings it back
    // after it is closed, and the music plays on meanwhile.
    Window("Juke", id: "main") {
      JukeRootView(model: model)
    }
    .keyboardShortcut("0")
    .commands { JukeCommands() }

    Settings {
      JukeSettingsView(model: model)
    }
    #else
    WindowGroup {
      JukeRootView(model: model)
    }
    .commands { JukeCommands() }
    #endif
  }
}

import JukeKit
import SwiftUI

/// The whole app: connects to the server named in settings, then shows the
/// library.  Both the iOS and macOS apps are this view in a window.
public struct JukeRootView: View {
  @AppStorage("serverURL") private var serverURL = "https://juke.tail0324d.ts.net"
  @State private var library: Library?
  @State private var player: Player?
  @State private var openFailure: String?
  @State private var showingSettings = false

  public init() {}

  public var body: some View {
    Group {
      if let library, let player {
        LibraryView(library: library, player: player) { showingSettings = true }
      } else if let openFailure {
        ContentUnavailableView {
          Label("Can't reach the server", systemImage: "wifi.exclamationmark")
        } description: {
          Text(openFailure)
        } actions: {
          Button("Try Again") { Task { await open() } }
          Button("Settings…") { showingSettings = true }
        }
      } else {
        ProgressView("Connecting to \(serverURL)…")
      }
    }
    .task(id: serverURL) { await open() }
    .sheet(isPresented: $showingSettings) {
      SettingsView(serverURL: $serverURL)
    }
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
      opened.start()
      library = opened
      player = Player(library: opened)
    } catch {
      openFailure = error.localizedDescription
    }
  }
}

struct SettingsView: View {
  @Binding var serverURL: String
  @State private var draft = ""
  @Environment(\.dismiss) private var dismiss

  var body: some View {
    NavigationStack {
      Form {
        TextField("Server", text: $draft, prompt: Text("https://juke.tail0324d.ts.net"))
          .autocorrectionDisabled()
          #if os(iOS)
          .textInputAutocapitalization(.never)
          .keyboardType(.URL)
          #endif
      }
      .navigationTitle("Settings")
      .toolbar {
        ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
        ToolbarItem(placement: .confirmationAction) {
          Button("Done") {
            serverURL = draft.trimmingCharacters(in: .whitespaces)
            dismiss()
          }
        }
      }
    }
    .onAppear { draft = serverURL }
    #if os(macOS)
    .frame(minWidth: 400, minHeight: 120)
    #endif
  }
}

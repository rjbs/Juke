import JukeKit
import SwiftUI

/// The whole app in a window: the library once it is open, the failure
/// if it isn't, and the connection attempt in between.
public struct JukeRootView: View {
  let model: AppModel
  @State private var showingSettings = false

  public init(model: AppModel) {
    self.model = model
  }

  public var body: some View {
    Group {
      if let library = model.library, let player = model.player {
        LibraryView(library: library, player: player) { showingSettings = true }
      } else if let openFailure = model.openFailure {
        ContentUnavailableView {
          Label("Can't reach the server", systemImage: "wifi.exclamationmark")
        } description: {
          Text(openFailure)
        } actions: {
          Button("Try Again") { model.reopen() }
          #if os(macOS)
          SettingsLink { Text("Settings…") }
          #else
          Button("Settings…") { showingSettings = true }
          #endif
        }
      } else {
        ProgressView("Connecting to \(model.serverURL)…")
      }
    }
    .task { model.openIfNeeded() }
    #if os(iOS)
    .sheet(isPresented: $showingSettings) {
      SettingsView(model: model)
    }
    #endif
  }
}

#if os(iOS)
/// The iOS settings sheet: the server, with Cancel and Done.
struct SettingsView: View {
  let model: AppModel
  @State private var draft = ""
  @Environment(\.dismiss) private var dismiss

  var body: some View {
    NavigationStack {
      Form {
        TextField("Server", text: $draft, prompt: Text(AppModel.defaultServer))
          .autocorrectionDisabled()
          .textInputAutocapitalization(.never)
          .keyboardType(.URL)
      }
      .navigationTitle("Settings")
      .toolbar {
        ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
        ToolbarItem(placement: .confirmationAction) {
          Button("Done") {
            model.connect(to: draft.trimmingCharacters(in: .whitespaces))
            dismiss()
          }
        }
      }
    }
    .onAppear { draft = model.serverURL }
  }
}
#endif

#if os(macOS)
/// The macOS Settings window: the server as it is, with an Edit… button
/// that opens a sheet with Cancel and OK, the way System Settings edits
/// the local hostname.  Applying the field as it is typed would
/// reconnect on every keystroke.
public struct JukeSettingsView: View {
  let model: AppModel
  @State private var editing = false

  public init(model: AppModel) {
    self.model = model
  }

  public var body: some View {
    Form {
      LabeledContent("Server") {
        HStack {
          Text(model.serverURL)
            .textSelection(.enabled)
          Button("Edit…") { editing = true }
        }
      }
      if let failure = model.openFailure {
        Label(failure, systemImage: "wifi.exclamationmark")
          .foregroundStyle(.secondary)
      }
    }
    .formStyle(.grouped)
    .frame(width: 520)
    .sheet(isPresented: $editing) {
      ServerEditor(model: model)
    }
  }
}

/// The sheet that changes the server.  OK connects to it.
private struct ServerEditor: View {
  let model: AppModel
  @State private var draft = ""
  @Environment(\.dismiss) private var dismiss

  private var trimmed: String { draft.trimmingCharacters(in: .whitespaces) }

  var body: some View {
    VStack(alignment: .leading, spacing: 12) {
      Text("Server")
        .font(.headline)
      TextField("Server", text: $draft, prompt: Text(AppModel.defaultServer))
        .textFieldStyle(.roundedBorder)
        .autocorrectionDisabled()
        .frame(width: 400)
      HStack {
        Spacer()
        Button("Cancel", role: .cancel) { dismiss() }
          .keyboardShortcut(.cancelAction)
        Button("OK") {
          model.connect(to: trimmed)
          dismiss()
        }
        .keyboardShortcut(.defaultAction)
        .disabled(trimmed.isEmpty)
      }
    }
    .padding(20)
    .onAppear { draft = model.serverURL }
  }
}
#endif

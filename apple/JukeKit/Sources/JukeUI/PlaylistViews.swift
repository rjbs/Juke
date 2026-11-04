import JukeKit
import SwiftUI

/// A playlist's tracks, which can be played, removed and reordered, with
/// the playlist itself renamed or deleted from the toolbar.
///
/// It looks the playlist up by key each time, rather than holding a copy,
/// so it shows its own edits and notices when the playlist is deleted.
struct PlaylistView: View {
  let library: Library
  let player: Player
  let key: Playlist.ID

  @State private var renaming = false
  @State private var newTitle = ""
  @State private var confirmingDelete = false

  /// A track on the playlist and its position in trackIds.  Only tracks
  /// the index knows are shown, so the two can differ.
  private struct Entry: Identifiable {
    let position: Int
    let track: Track
    var id: Int { position }
  }

  var body: some View {
    if let playlist = library.playlists.first(where: { $0.key == key }) {
      list(of: playlist)
    } else {
      ContentUnavailableView("No Such Playlist", systemImage: "music.note.list",
                             description: Text("This playlist has been deleted."))
    }
  }

  private func list(of playlist: Playlist) -> some View {
    let entries = playlist.trackIds.enumerated().compactMap { i, id in
      library.tracks[id].map { Entry(position: i, track: $0) }
    }
    let tracks = entries.map(\.track)
    return List {
      if library.offline.isKept(playlist) {
        OfflineStatus(offline: library.offline, kept: true, tracks: tracks)
          .font(.caption)
          .foregroundStyle(.secondary)
      }
      ForEach(Array(entries.enumerated()), id: \.element.id) { i, entry in
        TrackRow(library: library, player: player, track: entry.track, number: i + 1, showArtist: true,
                 remove: { edit { try await library.removeTracks(at: [entry.position], from: playlist) } }) {
          player.play(tracks, at: i, from: playlist.title)
        }
      }
      .onDelete { offsets in
        let positions = offsets.map { entries[$0].position }
        edit { try await library.removeTracks(at: positions, from: playlist) }
      }
      .onMove { offsets, destination in
        let positions = offsets.map { entries[$0].position }
        let to = destination < entries.count ? entries[destination].position : playlist.trackIds.count
        edit { try await library.moveTracks(at: positions, to: to, in: playlist) }
      }
    }
    .navigationTitle(playlist.title)
    .toolbar {
      #if os(iOS)
      ToolbarItem { EditButton() }
      #endif
      ToolbarItem {
        PlayMenu(player: player, source: playlist.title) { library.tracks(of: playlist) }
          .disabled(!tracks.contains(where: \.isPlayable))
      }
      ToolbarItem {
        KeepButton(kept: library.offline.isKept(playlist)) { library.offline.keep(playlist, $0) }
          .disabled(playlist.serverId == nil)
      }
      ToolbarItem {
        Menu("Playlist", systemImage: "ellipsis.circle") {
          Button("Rename…", systemImage: "pencil") {
            newTitle = playlist.title
            renaming = true
          }
          Toggle("Helper Playlist", systemImage: "wrench.adjustable", isOn: Binding(
            get: { playlist.isHelper },
            set: { want in edit { try await library.setHelper(playlist, want) } }
          ))
          .accessibilityIdentifier("playlist.isHelper")
          Button("Delete Playlist…", systemImage: "trash", role: .destructive) { confirmingDelete = true }
        }
        .accessibilityIdentifier("playlist.menu")
      }
    }
    .alert("Rename Playlist", isPresented: $renaming) {
      TextField("Title", text: $newTitle)
      Button("Rename") {
        let title = newTitle.trimmingCharacters(in: .whitespaces)
        if !title.isEmpty { edit { try await library.rename(playlist, to: title) } }
      }
      Button("Cancel", role: .cancel) {}
    }
    .confirmationDialog("Delete “\(playlist.title)”?", isPresented: $confirmingDelete, titleVisibility: .visible) {
      Button("Delete Playlist", role: .destructive) { edit { try await library.delete(playlist) } }
    }
  }

  private func edit(_ body: @escaping @MainActor () async throws -> Void) {
    Task { try? await body() }
  }
}

/// A menu of playlists to add some tracks to.  Helpers come after the
/// rest, past a divider, since adding to one is how a hand-kept
/// exclusion list is kept.
struct AddToPlaylistMenu: View {
  let library: Library
  let tracks: [Track]

  var body: some View {
    let helpers = library.playlists.filter(\.isHelper)
    Menu("Add to Playlist", systemImage: "text.badge.plus") {
      ForEach(library.playlists.filter { !$0.isHelper }) { button(for: $0) }
      if !helpers.isEmpty {
        Divider()
        ForEach(helpers) { button(for: $0) }
      }
    }
    .disabled(library.playlists.isEmpty || tracks.isEmpty)
  }

  private func button(for playlist: Playlist) -> some View {
    Button(playlist.title) {
      Task { try? await library.add(tracks, to: playlist) }
    }
  }
}

/// The prompt that makes a new, empty playlist, asking for its title.
/// `made` gets the new playlist's key, to show it.
///
/// This hangs on a view that is always in the window, not on the
/// button that raises it: on macOS a toolbar button pushed into the
/// overflow menu is only a menu item there, and an alert attached to
/// it never shows.  -- claude, 2026-11-03
struct NewPlaylistPrompt: ViewModifier {
  let library: Library
  @Binding var isPresented: Bool
  let made: (Playlist.ID) -> Void

  @State private var title = ""

  func body(content: Content) -> some View {
    content
      .onChange(of: isPresented) { if isPresented { title = "" } }
      .alert("New Playlist", isPresented: $isPresented) {
        TextField("Title", text: $title)
        Button("Create") {
          let title = title.trimmingCharacters(in: .whitespaces)
          guard !title.isEmpty else { return }
          Task {
            if let key = try? await library.createPlaylist(titled: title) { made(key) }
          }
        }
        Button("Cancel", role: .cancel) {}
      }
  }
}

extension View {
  /// Asks for a title and makes a new playlist while `isPresented`.
  func newPlaylistPrompt(isPresented: Binding<Bool>, library: Library, made: @escaping (Playlist.ID) -> Void) -> some View {
    modifier(NewPlaylistPrompt(library: library, isPresented: isPresented, made: made))
  }
}

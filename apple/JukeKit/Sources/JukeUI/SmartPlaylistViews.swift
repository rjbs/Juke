import JukeKit
import SwiftUI

/// A smart playlist's tracks: what the server last said its rule
/// matched, in the rule's order.  Its tracks can be played, added to a
/// plain playlist, and kept offline; the rule is edited, copied or
/// deleted from the toolbar menu, the editing in a sheet the sidebar
/// owns, so that what it saves can be shown.
///
/// It looks the playlist up by id each time, rather than holding a copy,
/// so it follows the rule and its result as they change and notices
/// when the playlist is destroyed.
struct SmartPlaylistView: View {
  let library: Library
  let player: Player
  let id: String
  /// Opens the editor on something; the sidebar provides it, so a view
  /// pushed from the Offline tab has it too.
  @Environment(SmartPlaylistEditorHost.self) private var editor: SmartPlaylistEditorHost?

  @State private var confirmingDelete = false
  @State private var deleteFailure: String?
  /// Kept per playlist, on this device.
  @AppStorage private var style: TrackListStyle

  init(library: Library, player: Player, id: String) {
    self.library = library
    self.player = player
    self.id = id
    _style = AppStorage(wrappedValue: .tracks, "trackListStyle.smart.\(id)")
  }

  var body: some View {
    if let playlist = library.smartPlaylists.first(where: { $0.id == id }) {
      list(of: playlist)
    } else {
      ContentUnavailableView("No Such Playlist", systemImage: "gearshape",
                             description: Text("This smart playlist has been deleted."))
    }
  }

  private func list(of playlist: SmartPlaylist) -> some View {
    let tracks = library.tracks(of: playlist)
    let playable = tracks.filter(\.isPlayable)
    return List {
      Section {
        VStack(alignment: .leading, spacing: 4) {
          Text(summary(of: playlist, showing: tracks.count))
            .font(.caption)
            .foregroundStyle(.secondary)
          OfflineStatus(offline: library.offline, kept: library.offline.isKept(playlist), tracks: tracks)
            .font(.caption)
            .foregroundStyle(.secondary)
        }
      }
      switch style {
      case .tracks:
        ForEach(Array(tracks.enumerated()), id: \.element.id) { i, track in
          TrackRow(library: library, player: player, track: track, number: i + 1, showArtist: true, showAlbum: true) {
            player.play(tracks, at: i, from: playlist.title)
          }
        }
      case .albums:
        AlbumRunsView(library: library, player: player,
                      entries: tracks.enumerated().map { AlbumEntry(track: $1, index: $0) }) {
          player.play(tracks, at: $0, from: playlist.title)
        }
      }
    }
    .navigationTitle(playlist.title)
    .toolbar {
      #if os(macOS)
      ToolbarItem { TrackListStylePicker(style: $style).pickerStyle(.segmented) }
      #endif
      ToolbarItem {
        PlayMenu(player: player, source: playlist.title) { tracks }
          .disabled(playable.isEmpty)
      }
      ToolbarItem {
        KeepButton(kept: library.offline.isKept(playlist)) { library.offline.keep(playlist, $0) }
      }
      ToolbarItem {
        Menu("Playlist", systemImage: "ellipsis.circle") {
          #if os(iOS)
          TrackListStylePicker(style: $style).pickerStyle(.inline)
          #endif
          if let editor {
            Button("Edit Rule…", systemImage: "pencil") { editor.edit(.rule(playlist)) }
            Button("Duplicate…", systemImage: "doc.on.doc") { editor.edit(.copy(playlist)) }
            Button("Delete Smart Playlist…", systemImage: "trash", role: .destructive) { confirmingDelete = true }
          }
        }
        .accessibilityIdentifier("smartPlaylist.menu")
      }
    }
    .confirmationDialog("Delete “\(playlist.title)”?", isPresented: $confirmingDelete, titleVisibility: .visible) {
      Button("Delete Smart Playlist", role: .destructive) {
        Task {
          do { try await library.delete(playlist) } catch { deleteFailure = error.localizedDescription }
        }
      }
    }
    .alert("Couldn't Delete", isPresented: Binding(get: { deleteFailure != nil }, set: { if !$0 { deleteFailure = nil } })) {
      Button("OK") {}
    } message: {
      Text(deleteFailure ?? "")
    }
  }

  /// How many tracks the rule matches: all of them, or the first page
  /// of more than the server answers at once, or nothing yet, for a
  /// rule that hasn't run since the library was opened.  A rule with a
  /// limit of its own is never cut by the server: its total counts
  /// everything the filter matched before the limit, so only a rule
  /// without one can have been cut.  -- claude, 2026-11-02
  private func summary(of playlist: SmartPlaylist, showing count: Int) -> String {
    guard let result = library.smartResults[playlist.id] else {
      return "Waiting for the server to run this playlist's rule."
    }
    if playlist.limit == nil, let total = result.total, total > result.trackIds.count {
      return "The first \(count.formatted()) of \(total.formatted()) tracks"
    }
    return "\(count.formatted()) tracks"
  }
}

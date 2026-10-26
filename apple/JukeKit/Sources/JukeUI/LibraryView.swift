import JukeKit
import SwiftUI

/// The library once connected: a progress view during the first sync,
/// then a sidebar of albums and playlists with the player along the
/// bottom.  On an iPhone the split view collapses into a stack.
struct LibraryView: View {
  let library: Library
  let player: Player
  let showSettings: () -> Void

  enum SidebarItem: Hashable {
    case albums
    case offline
    case playlist(String)
  }

  @State private var selection: SidebarItem? = .albums

  var body: some View {
    switch library.phase {
    case .syncing(let fetched, let total):
      VStack(spacing: 12) {
        if let total, total > 0 {
          ProgressView(value: Double(fetched), total: Double(total))
            .frame(maxWidth: 320)
          Text("Syncing the library: \(fetched.formatted()) of \(total.formatted()) tracks")
        } else {
          ProgressView()
          Text("Syncing the library…")
        }
      }
      .padding()
    case .failed(let message):
      ContentUnavailableView("Couldn't open the library", systemImage: "exclamationmark.triangle", description: Text(message))
    case .ready:
      split
    }
  }

  private var columns: some View {
    NavigationSplitView {
      List(selection: $selection) {
        Label("Albums", systemImage: "square.stack").tag(SidebarItem.albums)
        Label("Offline", systemImage: "arrow.down.circle").tag(SidebarItem.offline)
        if !library.playlists.isEmpty {
          Section("Playlists") {
            ForEach(library.playlists) { p in
              Label(p.title, systemImage: "music.note.list").tag(SidebarItem.playlist(p.id))
            }
          }
        }
      }
      .navigationTitle("Juke")
      .toolbar {
        ToolbarItem {
          Button("Settings", systemImage: "gear", action: showSettings)
        }
      }
      .safeAreaInset(edge: .bottom) {
        VStack(alignment: .leading, spacing: 4) {
          if let failure = library.syncFailure {
            Label("Can't reach the server", systemImage: "exclamationmark.icloud")
              .help(failure)
          }
          let offline = library.offline!
          if offline.present < offline.wanted {
            Label("Downloading \(offline.present) of \(offline.wanted)", systemImage: "arrow.down.circle")
              .help(offline.failure ?? "")
          }
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .padding(8)
      }
    } detail: {
      NavigationStack {
        switch selection {
        case .albums, nil:
          AlbumListView(library: library, player: player)
        case .offline:
          OfflineView(library: library, player: player)
        case .playlist(let id):
          if let playlist = library.playlists.first(where: { $0.id == id }) {
            PlaylistView(library: library, player: player, playlist: playlist)
          }
        }
      }
      .id(selection)
    }
  }

  // The bar sits below the split view, not in a safe-area inset on it:
  // when the split view collapses into a stack, as on an iPhone, the inset
  // doesn't reach the lists in it, which then scroll under the bar and
  // hide their last rows.  -- claude, 2026-10-25
  private var split: some View {
    VStack(spacing: 0) {
      columns
      PlayerBar(player: player)
    }
  }
}

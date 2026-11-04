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
    case artists
    case songs
    case offline
    case playlist(Playlist.ID)
    case smartPlaylist(String)
  }

  @State private var selection: SidebarItem? = .albums
  /// The smart playlist editor, shown as a sheet while it has a subject.
  @State private var editor = SmartPlaylistEditorHost()
  /// Whether the new-playlist prompt is up.
  @State private var namingPlaylist = false

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
        Label("Artists", systemImage: "music.mic").tag(SidebarItem.artists)
        Label("Songs", systemImage: "music.note").tag(SidebarItem.songs)
        Label("Offline", systemImage: "arrow.down.circle").tag(SidebarItem.offline)
        let rows = PlaylistRow.rows(of: library)
        if !rows.isEmpty {
          Section("Playlists") {
            ForEach(rows) { row in
              switch row {
              case .plain(let p):
                Label(p.title, systemImage: row.icon).tag(SidebarItem.playlist(p.id))
              case .smart(let s):
                Label(s.title, systemImage: row.icon).tag(SidebarItem.smartPlaylist(s.id))
              }
            }
          }
        }
      }
      .navigationTitle("Juke")
      // On macOS these are menu items instead: Settings… under the app
      // menu, the two New items under File.  Toolbar buttons would spill
      // into the overflow menu in a narrow sidebar.
      #if os(iOS)
      .toolbar {
        ToolbarItem {
          Button("Settings", systemImage: "gear", action: showSettings)
        }
        ToolbarItem {
          Button("New Playlist", systemImage: "plus") { namingPlaylist = true }
            .accessibilityIdentifier("playlist.new")
        }
        ToolbarItem {
          Button("New Smart Playlist", systemImage: "gearshape.2") { editor.edit(.new) }
            .accessibilityIdentifier("smartPlaylist.new")
        }
      }
      #endif
      .focusedSceneValue(\.libraryActions, LibraryActions(
        newPlaylist: { if editor.editing == nil { namingPlaylist = true } },
        newSmartPlaylist: { if !namingPlaylist { editor.edit(.new) } }
      ))
      .sheet(item: Bindable(editor).editing) { editing in
        SmartPlaylistEditor(library: library, editing: editing, window: editor.windowSize) { selection = .smartPlaylist($0) }
      }
      .newPlaylistPrompt(isPresented: $namingPlaylist, library: library) { selection = .playlist($0) }
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
        case .artists:
          ArtistListView(library: library, player: player)
        case .songs:
          SongListView(library: library, player: player)
        case .offline:
          OfflineView(library: library, player: player)
        case .playlist(let key):
          PlaylistView(library: library, player: player, key: key)
        case .smartPlaylist(let id):
          SmartPlaylistView(library: library, player: player, id: id)
        }
      }
      .id(selection)
      .environment(editor)
    }
  }

  /// A playlist of either kind, for a list that shows them together by
  /// title, a smart playlist wearing a gear.
  enum PlaylistRow: Identifiable {
    case plain(Playlist)
    case smart(SmartPlaylist)

    var id: String {
      switch self {
      case .plain(let p): "playlist:\(p.key)"
      case .smart(let s): "smart:\(s.id)"
      }
    }

    var title: String {
      switch self {
      case .plain(let p): p.title
      case .smart(let s): s.title
      }
    }

    /// The symbol that tells the kinds apart wherever they are listed.
    var icon: String {
      switch self {
      case .plain: "music.note.list"
      case .smart: "gearshape"
      }
    }

    @MainActor func isKept(_ offline: Offline) -> Bool {
      switch self {
      case .plain(let p): offline.isKept(p)
      case .smart(let s): offline.isKept(s)
      }
    }

    @MainActor func tracks(of library: Library) -> [Track] {
      switch self {
      case .plain(let p): library.tracks(of: p)
      case .smart(let s): library.tracks(of: s)
      }
    }

    @MainActor static func rows(of library: Library) -> [PlaylistRow] {
      (library.playlists.map(PlaylistRow.plain) + library.smartPlaylists.map(PlaylistRow.smart))
        .sorted { $0.title.localizedStandardCompare($1.title) == .orderedAscending }
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

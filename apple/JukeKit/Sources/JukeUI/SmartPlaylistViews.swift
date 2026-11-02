import JukeKit
import SwiftUI

/// A smart playlist's tracks: what the server last said its rule
/// matched, in the rule's order.  The list is read-only; the rule is
/// authored elsewhere.  Its tracks can be played, added to a plain
/// playlist, and kept offline.
///
/// It looks the playlist up by id each time, rather than holding a copy,
/// so it follows the rule and its result as they change and notices
/// when the playlist is destroyed.
struct SmartPlaylistView: View {
  let library: Library
  let player: Player
  let id: String

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
      ForEach(Array(tracks.enumerated()), id: \.element.id) { i, track in
        TrackRow(library: library, player: player, track: track, number: i + 1, showArtist: true, showAlbum: true) {
          player.play(tracks, at: i, from: playlist.title)
        }
      }
    }
    .navigationTitle(playlist.title)
    .toolbar {
      ToolbarItem {
        Button("Play", systemImage: "play.fill") {
          if let first = tracks.firstIndex(where: \.isPlayable) { player.play(tracks, at: first, from: playlist.title) }
        }
        .disabled(playable.isEmpty)
      }
      ToolbarItem {
        AddToPlaylistMenu(library: library, tracks: playable)
          .accessibilityIdentifier("smartPlaylist.addToPlaylist")
      }
      ToolbarItem {
        KeepButton(kept: library.offline.isKept(playlist)) { library.offline.keep(playlist, $0) }
      }
      ToolbarItem {
        Menu("Playlist", systemImage: "ellipsis.circle") {
          Button("Shuffle by Album", systemImage: "square.stack") {
            player.playShuffledByAlbum(tracks, from: playlist.title)
          }
          .disabled(playable.isEmpty)
        }
        .accessibilityIdentifier("smartPlaylist.menu")
      }
    }
  }

  /// How many tracks the rule matches: all of them, or the first page
  /// of more than the server answers at once, or nothing yet, for a
  /// rule that hasn't run since the library was opened.
  private func summary(of playlist: SmartPlaylist, showing count: Int) -> String {
    guard let result = library.smartResults[playlist.id] else {
      return "Waiting for the server to run this playlist's rule."
    }
    if let total = result.total, total > result.trackIds.count {
      return "The first \(count.formatted()) of \(total.formatted()) tracks"
    }
    return "\(count.formatted()) tracks"
  }
}

import JukeKit
import SwiftUI

/// Where a kept album or playlist's downloads stand: nothing if it isn't
/// kept, a progress count while its audio arrives, and a check when all
/// of it is here.
struct OfflineStatus: View {
  let offline: Offline
  let kept: Bool
  let tracks: [Track]

  var body: some View {
    if kept {
      let p = offline.progress(of: tracks)
      if p.present < p.wanted {
        Label("Downloading \(p.present) of \(p.wanted)", systemImage: "arrow.down.circle")
      } else {
        Label("Available offline", systemImage: "checkmark.circle")
      }
    }
  }
}

/// The toolbar button that keeps an album or playlist offline or lets it
/// go.
struct KeepButton: View {
  let kept: Bool
  let toggle: (Bool) -> Void

  var body: some View {
    Button(kept ? "Remove Download" : "Keep Offline",
           systemImage: kept ? "arrow.down.circle.fill" : "arrow.down.circle") {
      toggle(!kept)
    }
  }
}

/// Everything kept offline on this device.
struct OfflineView: View {
  let library: Library
  let player: Player

  var body: some View {
    let offline = library.offline!
    let albums = library.albumList.filter(offline.isKept)
    let playlists = library.playlists.filter(offline.isKept)
    List {
      if albums.isEmpty && playlists.isEmpty {
        Text("Nothing is kept offline yet.  Use “Keep Offline” on an album or playlist.")
          .foregroundStyle(.secondary)
      }
      if !albums.isEmpty {
        Section("Albums") {
          ForEach(albums) { album in
            NavigationLink(value: album.id) {
              VStack(alignment: .leading) {
                Text(album.title)
                OfflineStatus(offline: offline, kept: true, tracks: library.tracks(of: album).flatMap(\.self))
                  .font(.caption)
                  .foregroundStyle(.secondary)
              }
            }
          }
        }
      }
      if !playlists.isEmpty {
        Section("Playlists") {
          ForEach(playlists) { playlist in
            NavigationLink {
              PlaylistView(library: library, player: player, playlist: playlist)
            } label: {
              VStack(alignment: .leading) {
                Text(playlist.title)
                OfflineStatus(offline: offline, kept: true, tracks: library.tracks(of: playlist))
                  .font(.caption)
                  .foregroundStyle(.secondary)
              }
            }
          }
        }
      }
    }
    .navigationTitle("Offline")
    .navigationDestination(for: String.self) { id in
      if let album = library.albums[id] {
        AlbumView(library: library, player: player, album: album)
      }
    }
  }
}

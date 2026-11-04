import JukeKit
import SwiftUI

struct AlbumListView: View {
  let library: Library
  let player: Player
  @State private var search = ""
  @State private var searching = false

  private var shown: [Album] {
    let words = search.split(separator: " ").map(String.init)
    guard !words.isEmpty else { return library.albumList }
    return library.albumList.filter { a in
      let hay = "\(a.artist ?? "") \(a.title)"
      return words.allSatisfy { hay.localizedStandardContains($0) }
    }
  }

  var body: some View {
    List(shown) { album in
      NavigationLink(value: album.id) {
        HStack(spacing: 12) {
          ArtworkView(library: library, artwork: album.artwork, size: 44)
          VStack(alignment: .leading) {
            Text(album.title)
            HStack(spacing: 4) {
              if library.offline.isKept(album) {
                Image(systemName: "arrow.down.circle.fill").accessibilityLabel("Kept offline")
              }
              Text(album.artist ?? "Compilation")
            }
            .font(.caption)
            .foregroundStyle(.secondary)
          }
        }
      }
    }
    .findable(text: $search, isPresented: $searching, prompt: "Artist or title")
    .navigationTitle("Albums")
    .toolbar {
      ToolbarItem {
        PlayMenu(player: player, source: "All Albums") { library.albumList.flatMap { library.tracks(of: $0).joined() } }
      }
    }
  }
}

struct AlbumView: View {
  let library: Library
  let player: Player
  let album: Album

  var body: some View {
    let all = library.tracks(of: album).flatMap(\.self)
    List {
      AlbumSection(library: library, player: player, album: album,
                   entries: all.enumerated().map { AlbumEntry(track: $1, index: $0) }, linked: false) {
        player.play(all, at: $0, from: album.title)
      }
    }
    .navigationTitle(album.title)
    .toolbar {
      ToolbarItem {
        PlayMenu(player: player, source: album.title) { all }
          .disabled(!all.contains(where: \.isPlayable))
      }
      ToolbarItem {
        AddToPlaylistMenu(library: library, tracks: all.filter(\.isPlayable))
          .accessibilityIdentifier("album.addToPlaylist")
      }
      ToolbarItem {
        KeepButton(kept: library.offline.isKept(album)) { library.offline.keep(album, $0) }
      }
    }
  }
}

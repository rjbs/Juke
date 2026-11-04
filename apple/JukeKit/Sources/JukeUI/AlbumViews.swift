import JukeKit
import SwiftUI

struct AlbumListView: View {
  let library: Library
  let player: Player
  @State private var search = ""

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
    .searchable(text: $search, prompt: "Artist or title")
    .navigationTitle("Albums")
    .toolbar {
      ToolbarItem {
        PlayMenu(player: player, source: "All Albums") { library.albumList.flatMap { library.tracks(of: $0).joined() } }
      }
    }
    .navigationDestination(for: String.self) { id in
      if let album = library.albums[id] {
        AlbumView(library: library, player: player, album: album)
      }
    }
  }
}

struct AlbumView: View {
  let library: Library
  let player: Player
  let album: Album

  var body: some View {
    let discs = library.tracks(of: album)
    let all = discs.flatMap(\.self)
    List {
      Section {
        HStack(alignment: .top, spacing: 12) {
          ArtworkView(library: library, artwork: album.artwork, size: 120, cornerRadius: 8)
          VStack(alignment: .leading, spacing: 4) {
            Text(album.artist ?? "Compilation").foregroundStyle(.secondary)
            let missing = all.count(where: { !$0.isPlayable })
            Text("\(all.count) tracks" + (missing > 0 ? ", \(missing) missing" : ""))
              .font(.caption)
              .foregroundStyle(.secondary)
            OfflineStatus(offline: library.offline, kept: library.offline.isKept(album), tracks: all)
              .font(.caption)
              .foregroundStyle(.secondary)
          }
        }
      }
      ForEach(Array(discs.enumerated()), id: \.offset) { d, tracks in
        Section(discs.count > 1 ? (album.discs[d].title ?? "Disc \(d + 1)") : "") {
          ForEach(Array(tracks.enumerated()), id: \.offset) { i, track in
            // Positions count across discs, since the queue is the
            // whole album. -- claude, 2026-10-25
            let position = discs[..<d].reduce(0) { $0 + $1.count } + i
            TrackRow(library: library, player: player, track: track, number: i + 1,
                     showArtist: track.artist != album.artist) {
              player.play(all, at: position, from: album.title)
            }
          }
        }
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

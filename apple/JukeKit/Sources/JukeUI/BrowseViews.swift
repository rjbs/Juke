import JukeKit
import SwiftUI

/// Album artists, searchable, each leading to their albums.
struct ArtistListView: View {
  let library: Library
  let player: Player
  @State private var search = ""

  private var shown: [Artist] {
    let words = search.split(separator: " ").map(String.init)
    guard !words.isEmpty else { return library.artistList }
    return library.artistList.filter { a in words.allSatisfy { a.name.localizedStandardContains($0) } }
  }

  var body: some View {
    List(shown) { artist in
      NavigationLink(value: artist) {
        VStack(alignment: .leading) {
          Text(artist.name)
          Text(artist.albumIds.count == 1 ? "1 album" : "\(artist.albumIds.count) albums")
            .font(.caption)
            .foregroundStyle(.secondary)
        }
      }
    }
    .searchable(text: $search, prompt: "Artist")
    .navigationTitle("Artists")
    .navigationDestination(for: Artist.self) { artist in
      ArtistView(library: library, player: player, artist: artist)
    }
    .navigationDestination(for: String.self) { id in
      if let album = library.albums[id] {
        AlbumView(library: library, player: player, album: album)
      }
    }
  }
}

/// One artist's albums.
struct ArtistView: View {
  let library: Library
  let player: Player
  let artist: Artist

  var body: some View {
    let albums = artist.albumIds.compactMap { library.albums[$0] }
    List(albums) { album in
      NavigationLink(value: album.id) {
        VStack(alignment: .leading) {
          Text(album.title)
          HStack(spacing: 4) {
            if library.offline.isKept(album) {
              Image(systemName: "arrow.down.circle.fill").accessibilityLabel("Kept offline")
            }
            let count = album.discs.reduce(0) { $0 + $1.trackIds.count }
            Text(count == 1 ? "1 track" : "\(count) tracks")
          }
          .font(.caption)
          .foregroundStyle(.secondary)
        }
      }
    }
    .navigationTitle(artist.name)
    .toolbar {
      ToolbarItem {
        Button("Shuffle by Album", systemImage: "shuffle") {
          player.playShuffledByAlbum(albums.flatMap { library.tracks(of: $0).joined() }, from: artist.name)
        }
      }
    }
  }
}

/// Every track, searchable by title and artist, in a chosen order.
/// Tapping one plays the list as shown, from there.
struct SongListView: View {
  let library: Library
  let player: Player

  @State private var order: SongOrder = .title
  @State private var search = ""
  @State private var model = SongListModel()

  var body: some View {
    let query = SongListModel.Query(order: order, search: search, revision: library.trackRevision)
    List(model.shown) { track in
      TrackRow(library: library, player: player, track: track, number: nil, showArtist: true, showAlbum: true) {
        if let i = model.shown.firstIndex(of: track) { player.play(model.shown, at: i, from: "Songs") }
      }
    }
    .overlay {
      if !model.ready {
        ProgressView()
      } else if model.shown.isEmpty {
        ContentUnavailableView.search(text: search)
      }
    }
    .searchable(text: $search, prompt: "Title or artist")
    .navigationTitle("Songs")
    .toolbar {
      ToolbarItem {
        Menu("Sort", systemImage: "arrow.up.arrow.down") {
          Picker("Sort", selection: $order) {
            ForEach(SongOrder.allCases) { Text($0.rawValue).tag($0) }
          }
        }
        .accessibilityIdentifier("songs.sort")
      }
    }
    .onAppear { model.update(query, tracks: library.tracks.values) }
    .onChange(of: query) { model.update(query, tracks: library.tracks.values) }
  }
}

/// The song list's contents, filtered and sorted off the main actor.
///
/// This owns its work rather than using .task(id:), because SwiftUI
/// cancelled that task when the list first appeared and never started
/// it again, leaving the list empty.  Here only a newer query cancels
/// the work.  -- claude, 2026-10-25
@MainActor @Observable
final class SongListModel {
  struct Query: Equatable {
    var order: SongOrder
    var search: String
    var revision: Int
  }

  private(set) var shown: [Track] = []
  private(set) var ready = false
  @ObservationIgnored private var query: Query?
  @ObservationIgnored private var work: Task<Void, Never>?

  func update(_ query: Query, tracks: some Collection<Track>) {
    guard query != self.query else { return }
    let typing = query.search != self.query?.search
    self.query = query
    work?.cancel()
    let all = Array(tracks)
    let words = query.search.split(separator: " ").map(String.init)
    work = Task {
      // Wait for typing to pause before sorting for it.
      if typing && !words.isEmpty {
        try? await Task.sleep(for: .milliseconds(250))
      }
      guard !Task.isCancelled else { return }
      let result = await Self.filterAndSort(all, words: words, order: query.order)
      guard !Task.isCancelled else { return }
      shown = result
      ready = true
    }
  }

  @concurrent nonisolated private static func filterAndSort(_ all: [Track], words: [String], order: SongOrder) async -> [Track] {
    let matching = words.isEmpty ? all : all.filter { t in
      let hay = "\(t.title) \(t.artist ?? "")"
      return words.allSatisfy { hay.localizedStandardContains($0) }
    }
    return Library.sorted(matching, by: order)
  }
}

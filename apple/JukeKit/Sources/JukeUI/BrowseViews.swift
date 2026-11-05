import JukeKit
import SwiftUI

/// Album artists, searchable, each leading to their albums.
struct ArtistListView: View {
  let library: Library
  let player: Player
  @State private var search = ""
  @State private var searching = false

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
    .findable(text: $search, isPresented: $searching, prompt: "Artist")
    .navigationTitle("Artists")
  }
}

/// One artist's albums, each whole: a header with its art and title,
/// leading to the album's own page, and its tracks beneath, as Music
/// shows an artist.  Playing a track queues the whole discography from
/// there, so shuffling by album from here plays the albums in random
/// order.
struct ArtistView: View {
  let library: Library
  let player: Player
  let artist: Artist
  /// How the albums are ordered, kept across launches.
  @AppStorage("artistAlbumOrder") private var order = AlbumOrder.oldestFirst

  /// An album with its tracks, and where its first track falls in the
  /// artist's whole list.
  private struct Shown: Identifiable {
    let album: Album
    let tracks: [Track]
    let offset: Int
    var id: String { album.id }
  }

  private var shown: [Shown] {
    var offset = 0
    return order.sorted(artist.albumIds.compactMap { library.albums[$0] }).map { album in
      let tracks = library.tracks(of: album).flatMap(\.self)
      defer { offset += tracks.count }
      return Shown(album: album, tracks: tracks, offset: offset)
    }
  }

  var body: some View {
    let shown = shown
    let all = shown.flatMap(\.tracks)
    List {
      ForEach(shown) { entry in
        AlbumSection(library: library, player: player, album: entry.album,
                     entries: entry.tracks.enumerated().map { AlbumEntry(track: $1, index: entry.offset + $0) },
                     linked: true) {
          player.play(all, at: $0, from: artist.name)
        }
      }
    }
    .navigationTitle(artist.name)
    .navigationSubtitle(summary(albums: shown.count, tracks: all.count))
    .toolbar {
      ToolbarItem {
        PlayMenu(player: player, source: artist.name) { all }
          .disabled(!all.contains(where: \.isPlayable))
      }
      ToolbarItem {
        Menu("Sort", systemImage: "arrow.up.arrow.down") {
          Picker("Sort", selection: $order) {
            ForEach(AlbumOrder.allCases) { Text($0.rawValue).tag($0) }
          }
          .pickerStyle(.inline)
        }
        .accessibilityIdentifier("artist.sort")
      }
    }
  }

  private func summary(albums: Int, tracks: Int) -> String {
    (albums == 1 ? "1 album" : "\(albums) albums") + ", " + (tracks == 1 ? "1 track" : "\(tracks) tracks")
  }
}

/// The genres the tracks name, searchable, each leading to its tracks.
/// Tracks with no genre aren't listed here.
struct GenreListView: View {
  let library: Library
  let player: Player
  @State private var search = ""
  @State private var searching = false

  private var shown: [Genre] {
    let words = search.split(separator: " ").map(String.init)
    guard !words.isEmpty else { return library.genreList }
    return library.genreList.filter { g in words.allSatisfy { g.name.localizedStandardContains($0) } }
  }

  var body: some View {
    List(shown) { genre in
      NavigationLink(value: genre) {
        VStack(alignment: .leading) {
          Text(genre.name)
          Text(genre.trackCount == 1 ? "1 track" : "\(genre.trackCount.formatted()) tracks")
            .font(.caption)
            .foregroundStyle(.secondary)
        }
      }
    }
    .findable(text: $search, isPresented: $searching, prompt: "Genre")
    .navigationTitle("Genres")
  }
}

/// One genre's tracks, by album in the album list's order, shown either
/// as one list or under their albums' headings.  An album of mixed
/// genres shows only its tracks in this one.
struct GenreView: View {
  let library: Library
  let player: Player
  let genre: Genre
  /// Kept for all genres alike, on this device.
  @AppStorage("trackListStyle.genre") private var style = TrackListStyle.albums

  var body: some View {
    let tracks = library.tracks(of: genre)
    List {
      switch style {
      case .tracks:
        ForEach(Array(tracks.enumerated()), id: \.element.id) { i, track in
          TrackRow(library: library, player: player, track: track, number: i + 1, showArtist: true, showAlbum: true) {
            player.play(tracks, at: i, from: genre.name)
          }
        }
      case .albums:
        AlbumRunsView(library: library, player: player,
                      entries: tracks.enumerated().map { AlbumEntry(track: $1, index: $0) }) {
          player.play(tracks, at: $0, from: genre.name)
        }
      }
    }
    .navigationTitle(genre.name)
    .navigationSubtitle(tracks.count == 1 ? "1 track" : "\(tracks.count.formatted()) tracks")
    .toolbar {
      #if os(macOS)
      ToolbarItem { TrackListStylePicker(style: $style).pickerStyle(.segmented) }
      #endif
      ToolbarItem {
        PlayMenu(player: player, source: genre.name) { tracks }
          .disabled(!tracks.contains(where: \.isPlayable))
      }
      #if os(iOS)
      ToolbarItem {
        Menu("Genre", systemImage: "ellipsis.circle") {
          TrackListStylePicker(style: $style).pickerStyle(.inline)
        }
      }
      #endif
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
  @State private var searching = false
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
    .findable(text: $search, isPresented: $searching, prompt: "Title or artist")
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

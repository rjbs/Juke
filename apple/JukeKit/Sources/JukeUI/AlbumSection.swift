import JukeKit
import SwiftUI

/// One track in an album section, and where it falls in the list being
/// played, so that tapping it plays the whole list from there.
struct AlbumEntry {
  let track: Track
  let index: Int
  /// Removes the track from the list it's in, for a list that allows it.
  var remove: (() -> Void)? = nil
}

/// Part of a list of tracks given over to one album: a heading with its
/// art, artist and counts, then its tracks numbered as on the album, with
/// a track's artist shown only where it isn't the album's.
///
/// The album page shows one, of the whole album; an artist's page shows
/// one per album, each whole and leading to the album's page; a playlist
/// shown by album has one per run of tracks from the same album, which
/// may be only some of it.
struct AlbumSection: View {
  let library: Library
  let player: Player
  let album: Album
  let entries: [AlbumEntry]
  /// Whether the heading names the album and leads to its page, as it
  /// must everywhere but the album's own page.
  let linked: Bool
  /// How big the heading's art is.
  var artSize: CGFloat = 120
  /// Plays the list from this index.
  let play: (Int) -> Void

  var body: some View {
    Section {
      if linked {
        NavigationLink(value: album.id) { heading }
      } else {
        heading
      }
      ForEach(rows) { row in
        switch row {
        case .disc(_, let title):
          Text(title)
            .font(.caption)
            .foregroundStyle(.secondary)
        case .track(let entry, let number):
          TrackRow(library: library, player: player, track: entry.track, number: number,
                   showArtist: entry.track.artist != album.artist, remove: entry.remove) {
            play(entry.index)
          }
        }
      }
    }
  }

  /// A row under the heading: a track, or the title of the disc the
  /// tracks after it are on, for an album of more than one.
  ///
  /// Each row's id is unique across the whole list, not just this
  /// section: macOS's List showed every section with the first one's
  /// rows when each section's rows were numbered from zero.
  /// -- claude, 2026-11-04
  private enum Row: Identifiable {
    case disc(at: Int, title: String)
    case track(AlbumEntry, number: Int?)

    var id: String {
      switch self {
      case .disc(let at, _): "d\(at)"
      case .track(let entry, _): "t\(entry.index)"
      }
    }
  }

  private var rows: [Row] {
    var out: [Row] = []
    var disc: Int?
    for entry in entries {
      let place = album.place(of: entry.track.id)
      if album.discs.count > 1, let place, place.disc != disc {
        out.append(.disc(at: entry.index, title: album.discs[place.disc].title ?? "Disc \(place.disc + 1)"))
      }
      disc = place?.disc
      out.append(.track(entry, number: place?.number))
    }
    return out
  }

  private var heading: some View {
    let tracks = entries.map(\.track)
    let whole = album.trackIds.count
    let missing = tracks.count(where: { !$0.isPlayable })
    let count = tracks.count == whole
      ? (whole == 1 ? "1 track" : "\(whole) tracks")
      : "\(tracks.count) of \(whole) tracks"
    return HStack(alignment: .top, spacing: 12) {
      ArtworkView(library: library, artwork: album.artwork, seed: album.id, size: artSize, cornerRadius: artSize / 15)
      VStack(alignment: .leading, spacing: 4) {
        if linked {
          Text(album.title).font(.title3.weight(.semibold))
        }
        Text(album.artist ?? "Compilation").foregroundStyle(.secondary)
        Text((album.releasedText.map { $0 + " · " } ?? "") + count + (missing > 0 ? ", \(missing) missing" : ""))
          .font(.caption)
          .foregroundStyle(.secondary)
        OfflineStatus(offline: library.offline, kept: library.offline.isKept(album), tracks: library.tracks(of: album).flatMap(\.self))
          .font(.caption)
          .foregroundStyle(.secondary)
      }
    }
    .padding(.vertical, 4)
  }
}

/// How a playlist's tracks are shown: one list, or by album.
enum TrackListStyle: String {
  case tracks, albums
}

/// The control that switches a playlist between its two styles.
struct TrackListStylePicker: View {
  @Binding var style: TrackListStyle

  var body: some View {
    Picker("View", selection: $style) {
      Label("Tracks", systemImage: "list.bullet").tag(TrackListStyle.tracks)
      Label("Albums", systemImage: "square.stack").tag(TrackListStyle.albums)
    }
    .accessibilityIdentifier("trackList.style")
  }
}

/// A playlist's tracks shown by album: a section for each run of tracks
/// from one album.  The runs are of consecutive tracks, not each album's
/// tracks gathered up, so that the list reads in the order it plays.
struct AlbumRunsView: View {
  let library: Library
  let player: Player
  let entries: [AlbumEntry]
  let play: (Int) -> Void

  var body: some View {
    // A track whose album the index doesn't know is a run of its own,
    // with no heading.
    let runs = runs(of: entries) { $0.track.albumId.flatMap { library.albums[$0]?.id } }
    ForEach(runs, id: \.start) { run in
      if let album = run.key.flatMap({ library.albums[$0] }) {
        AlbumSection(library: library, player: player, album: album, entries: run.elements, linked: true, play: play)
      } else {
        Section {
          ForEach(run.elements, id: \.index) { entry in
            TrackRow(library: library, player: player, track: entry.track, number: nil, showArtist: true,
                     remove: entry.remove) {
              play(entry.index)
            }
          }
        }
      }
    }
  }
}

extension View {
  /// Where a link to an album, by its id, or to an artist or genre leads.  Set
  /// once, on the stack, so every view in it can link to any of them.
  func libraryDestinations(library: Library, player: Player) -> some View {
    navigationDestination(for: String.self) { id in
      if let album = library.albums[id] {
        AlbumView(library: library, player: player, album: album)
      }
    }
    .navigationDestination(for: Artist.self) { artist in
      ArtistView(library: library, player: player, artist: artist)
    }
    .navigationDestination(for: Genre.self) { genre in
      GenreView(library: library, player: player, genre: genre)
    }
  }
}

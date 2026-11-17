import JukeKit
import SwiftUI

/// One track in a list.  Tapping a playable track plays the list from
/// there; its context menu sets the rating, adds it to a playlist, and may
/// remove it from the list.  A gap is shown dimmed and
/// does nothing.
struct TrackRow: View {
  let library: Library
  let player: Player
  let track: Track
  /// Its place in the list, or nil where that means nothing.
  let number: Int?
  let showArtist: Bool
  /// Whether to name the album, for a list drawn from many.
  var showAlbum = false
  /// Removes the track from the list it's in, for a list that allows it.
  var remove: (() -> Void)? = nil
  let play: () -> Void
  @Environment(\.navigator) private var navigator
  @Environment(\.destination) private var destination

  var body: some View {
    Button(action: play) {
      HStack(alignment: .firstTextBaseline) {
        if let number {
          Text("\(number)")
            .monospacedDigit()
            .foregroundStyle(.secondary)
            .frame(minWidth: 24, alignment: .trailing)
        }
        VStack(alignment: .leading) {
          Text(track.title)
          let album = showAlbum ? track.albumId.flatMap { library.albums[$0]?.title } : nil
          let byline = [showArtist ? track.artist : nil, album].compactMap(\.self).joined(separator: " — ")
          if !byline.isEmpty {
            Text(byline).font(.caption).foregroundStyle(.secondary)
          }
          if !track.isPlayable {
            Text("Missing").font(.caption).foregroundStyle(.secondary)
          }
        }
        Spacer()
        if player.current?.id == track.id {
          Image(systemName: player.isPlaying ? "speaker.wave.2.fill" : "speaker.fill")
            .foregroundStyle(.tint)
        }
        RatingView(rating: track.rating)
      }
      .contentShape(Rectangle())
    }
    .buttonStyle(.plain)
    .disabled(!track.isPlayable)
    .opacity(track.isPlayable ? 1 : 0.5)
    .contextMenu {
      Picker("Rating", selection: Binding(
        get: { track.rating ?? 0 },
        set: { r in Task { try? await library.setRating(r == 0 ? nil : r, on: track) } }
      )) {
        Text("None").tag(0.0)
        ForEach(Array(stride(from: 0.5, through: 5, by: 0.5)), id: \.self) { r in
          Text(Self.stars(r)).tag(r)
        }
      }
      if track.isPlayable {
        Button("Play Next", systemImage: "text.line.first.and.arrowtriangle.forward") { player.playNext([track]) }
        Button("Add to Queue", systemImage: "text.line.last.and.arrowtriangle.forward") { player.addToQueue([track]) }
      }
      Section { goTo }
      AddToPlaylistMenu(library: library, tracks: [track])
      if let remove {
        Button("Remove from Playlist", systemImage: "minus.circle", role: .destructive, action: remove)
      }
    }
  }

  /// Links to the track's artist and album, but not to the page it's on.
  /// The artist is the track's own, if they have albums, so a song on a
  /// compilation leads to its singer; otherwise it's the album's.
  @ViewBuilder private var goTo: some View {
    let album = track.albumId.flatMap { library.albums[$0] }
    let artist = [track.artist, album?.artist].lazy.compactMap { $0.flatMap(library.artist(named:)) }.first
    if let artist, destination != .artist(artist.name) {
      Button("Go to Artist", systemImage: "music.mic") { navigator.goToArtist(artist) }
    }
    if let album, destination != .album(album.id) {
      Button("Go to Album", systemImage: "square.stack") { navigator.goToAlbum(album.id) }
    }
  }

  static func stars(_ r: Double) -> String {
    String(repeating: "★", count: Int(r)) + (r.truncatingRemainder(dividingBy: 1) != 0 ? "½" : "")
  }
}

struct RatingView: View {
  let rating: Double?

  var body: some View {
    if let rating {
      HStack(spacing: 1) {
        ForEach(1...5, id: \.self) { n in
          Image(systemName: rating >= Double(n) ? "star.fill"
                : rating >= Double(n) - 0.5 ? "star.leadinghalf.filled" : "star")
        }
      }
      .font(.caption2)
      .foregroundStyle(.orange)
      .accessibilityElement(children: .ignore)
      .accessibilityLabel("\(rating.formatted()) stars")
    }
  }
}

/// What's playing, with the transport buttons.  Hidden until something
/// has been played.
struct PlayerBar: View {
  let player: Player
  @State private var showingNowPlaying = false

  var body: some View {
    if player.current != nil || player.failure != nil {
      HStack(spacing: 16) {
        if let track = player.current {
          ArtworkView(library: player.library, artwork: player.library.artwork(for: track), seed: track.albumId, size: 40, cornerRadius: 4)
            .onTapGesture { showingNowPlaying = true }
        }
        // Tapping what's playing opens the whole player.  A gesture, not
        // a Button, so the texts stay separate for accessibility, and for
        // the UI tests that read them.  -- claude, 2026-10-25
        VStack(alignment: .leading) {
          if let failure = player.failure {
            Text(failure).foregroundStyle(.red).lineLimit(2).accessibilityIdentifier("player.failure")
          } else if let track = player.current {
            Text(track.title).lineLimit(1).accessibilityIdentifier("player.title")
            if let artist = track.artist {
              Text(artist).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
          }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
        .onTapGesture { showingNowPlaying = true }
        .accessibilityAction(named: "Show Now Playing") { showingNowPlaying = true }
        #if os(macOS)
        if player.current != nil {
          SeekBar(player: player, layout: .inline)
            .frame(minWidth: 200, maxWidth: 480)
        }
        #endif
        if player.isLoading { ProgressView().controlSize(.small) }
        Button("Previous", systemImage: "backward.fill") { player.previous() }
          .disabled(player.current == nil)
        Button(player.isPlaying ? "Pause" : "Play", systemImage: player.isPlaying ? "pause.fill" : "play.fill") {
          player.togglePause()
        }
        .disabled(player.current == nil || player.isLoading)
        .keyboardShortcut(.space, modifiers: [])
        .accessibilityIdentifier("player.playPause")
        Button("Next", systemImage: "forward.fill") { player.next() }
          .disabled(!player.hasNext)
        ShuffleButton(player: player)
        RepeatButton(player: player)
      }
      .labelStyle(.iconOnly)
      .buttonStyle(.borderless)
      .padding(.horizontal)
      .padding(.vertical, 10)
      .background(.bar)
      .accessibilityElement(children: .contain)
      .accessibilityIdentifier("player")
      .sheet(isPresented: $showingNowPlaying) { NowPlayingView(player: player) }
    }
  }
}

/// Chooses the shuffle from a menu: ordered, tracks or albums; tinted
/// while it's on, with the album icon for albums.
struct ShuffleButton: View {
  let player: Player

  var body: some View {
    Menu {
      Picker("Shuffle", selection: Binding(get: { player.shuffle }, set: { player.setShuffle($0) })) {
        Label("Ordered", systemImage: "list.bullet").tag(PlayQueue.Shuffle.off)
        Label("Shuffle Tracks", systemImage: "shuffle").tag(PlayQueue.Shuffle.tracks)
        Label("Shuffle Albums", systemImage: "square.stack").tag(PlayQueue.Shuffle.albums)
      }
      .pickerStyle(.inline)
    } label: {
      Label("Shuffle", systemImage: player.shuffle == .albums ? "square.stack" : "shuffle")
    }
    .menuIndicator(.hidden)
    .foregroundStyle(player.shuffle == .off ? AnyShapeStyle(.secondary) : AnyShapeStyle(.tint))
    .accessibilityValue(player.shuffle.rawValue)
    .accessibilityIdentifier("player.shuffle")
  }
}

/// The Play button for a list of tracks: plays them in order, in the
/// shuffle as set.  Its menu, a long press on iOS and the chevron on
/// macOS, plays them shuffled by track or by album, setting the shuffle
/// so.  `tracks` is a closure because some lists are the whole library.
struct PlayMenu: View {
  let player: Player
  let source: String?
  let tracks: () -> [Track]

  var body: some View {
    Menu {
      Button("Play Shuffled", systemImage: "shuffle") { player.playShuffled(tracks(), from: source) }
      Button("Play Shuffled by Album", systemImage: "square.stack") { player.playShuffledByAlbum(tracks(), from: source) }
    } label: {
      Label("Play", systemImage: "play.fill")
    } primaryAction: {
      let all = tracks()
      if let first = all.firstIndex(where: \.isPlayable) { player.play(all, at: first, from: source) }
    }
    .accessibilityIdentifier("play")
  }
}

/// Steps repeat from off, to all, to one; tinted while it's on.
struct RepeatButton: View {
  let player: Player

  var body: some View {
    Button("Repeat", systemImage: player.repeatMode == .one ? "repeat.1" : "repeat") { player.cycleRepeat() }
      .foregroundStyle(player.repeatMode == .off ? AnyShapeStyle(.secondary) : AnyShapeStyle(.tint))
      .accessibilityValue(value)
      .accessibilityIdentifier("player.repeat")
  }

  private var value: String {
    switch player.repeatMode {
    case .off: "off"
    case .all: "all"
    case .one: "one"
    }
  }
}

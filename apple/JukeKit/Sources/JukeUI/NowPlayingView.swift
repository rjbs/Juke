import JukeKit
import SwiftUI

/// The whole player: what's playing, a seek bar, the transport, and
/// what's coming up, grouped by where it came from.
struct NowPlayingView: View {
  let player: Player
  @Environment(\.dismiss) private var dismiss

  var body: some View {
    NavigationStack {
      List {
        Section {
          VStack(spacing: 16) {
            artwork
            titles
            SeekBar(player: player, layout: .stacked)
            transport
          }
          .frame(maxWidth: .infinity)
          .listRowBackground(Color.clear)
        }
        ForEach(Array(player.playQueue.upcomingBySource.enumerated()), id: \.offset) { _, run in
          Section(run.source ?? "Up Next") {
            ForEach(run.entries, id: \.entry.id) { item in
              Button {
                player.jump(to: item.index)
              } label: {
                VStack(alignment: .leading) {
                  Text(item.entry.track.title)
                  if let artist = item.entry.track.artist {
                    Text(artist).font(.caption).foregroundStyle(.secondary)
                  }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
              }
              .buttonStyle(.plain)
              .accessibilityIdentifier("nowPlaying.upNext")
            }
          }
        }
      }
      .navigationTitle("Now Playing")
      #if os(iOS)
      .navigationBarTitleDisplayMode(.inline)
      #endif
      .toolbar {
        ToolbarItem(placement: .confirmationAction) {
          Button("Done") { dismiss() }
        }
      }
    }
    #if os(macOS)
    .frame(minWidth: 420, minHeight: 640)
    #endif
  }

  private var artwork: some View {
    ArtworkView(library: player.library, artwork: player.current.flatMap { player.library.artwork(for: $0) }, seed: player.current?.albumId,
                maxWidth: 260, cornerRadius: 12)
  }

  private var titles: some View {
    VStack(spacing: 4) {
      Text(player.current?.title ?? "Nothing playing")
        .font(.title3.bold())
        .multilineTextAlignment(.center)
        .accessibilityIdentifier("nowPlaying.title")
      if let artist = player.current?.artist {
        Text(artist).foregroundStyle(.secondary)
      }
      if let album = player.currentAlbum {
        Text(album.title).font(.callout).foregroundStyle(.secondary)
          .accessibilityIdentifier("nowPlaying.album")
      }
    }
  }

  private var transport: some View {
    HStack(spacing: 28) {
      ShuffleButton(player: player)
      Button("Previous", systemImage: "backward.fill") { player.previous() }
        .disabled(player.current == nil)
      Button(player.isPlaying ? "Pause" : "Play", systemImage: player.isPlaying ? "pause.fill" : "play.fill") {
        player.togglePause()
      }
      .font(.largeTitle)
      .disabled(player.current == nil || player.isLoading)
      Button("Next", systemImage: "forward.fill") { player.next() }
        .disabled(!player.hasNext)
      RepeatButton(player: player)
    }
    .font(.title2)
    .labelStyle(.iconOnly)
    .buttonStyle(.borderless)
  }
}

/// A seek bar with the time gone and the time left, under the bar or on
/// either side of it.
struct SeekBar: View {
  enum Layout {
    /// The times beneath the bar, for the whole player.
    case stacked
    /// The times beside it, for the player bar.
    case inline
  }

  let player: Player
  let layout: Layout

  /// The bar's drag.  The times follow the drag; playback moves when
  /// it's released, or has rested a moment, so scrubbing doesn't stutter
  /// through the audio.
  @State private var scrubber: Scrubber

  init(player: Player, layout: Layout) {
    self.player = player
    self.layout = layout
    _scrubber = State(initialValue: Scrubber { player.seek(to: $0) })
  }

  /// Where the UI tests find it: in the whole player, or in the bar.
  private var id: String { layout == .stacked ? "nowPlaying" : "player" }

  var body: some View {
    let duration = player.duration ?? 0
    let shown = scrubber.target ?? min(player.elapsed, duration)
    let slider = Slider(
      value: Binding(get: { shown }, set: { scrubber.moved(to: $0) }),
      in: 0...max(duration, 1)
    ) { editing in
      if !editing { scrubber.released() }
    }
    .disabled(player.duration == nil)
    .accessibilityIdentifier("\(id).seek")
    let elapsed = Text(Self.time(shown)).accessibilityIdentifier("\(id).elapsed")
    let remaining = Text("-" + Self.time(max(duration - shown, 0))).accessibilityIdentifier("\(id).remaining")
    switch layout {
    case .stacked:
      VStack(spacing: 2) {
        slider
        HStack {
          elapsed
          Spacer()
          remaining
        }
        .font(.caption.monospacedDigit())
        .foregroundStyle(.secondary)
      }
    case .inline:
      HStack(spacing: 8) {
        elapsed.frame(minWidth: 36, alignment: .trailing)
        slider.controlSize(.small)
        remaining.frame(minWidth: 40, alignment: .leading)
      }
      .font(.caption.monospacedDigit())
      .foregroundStyle(.secondary)
    }
  }

  /// Seconds as m:ss, or h:mm:ss for an hour or more.
  static func time(_ seconds: Double) -> String {
    let s = Int(seconds.rounded(.down))
    return s >= 3600
      ? String(format: "%d:%02d:%02d", s / 3600, s / 60 % 60, s % 60)
      : String(format: "%d:%02d", s / 60, s % 60)
  }
}

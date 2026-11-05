import JukeKit
import SwiftUI

/// The whole player: what's playing, a seek bar, the transport, and
/// what's coming up, grouped by where it came from.
struct NowPlayingView: View {
  let player: Player
  @Environment(\.dismiss) private var dismiss

  /// The seek bar's drag.  The times follow the drag; playback moves when
  /// it's released, or has rested a moment, so scrubbing doesn't stutter
  /// through the audio.
  @State private var scrubber: Scrubber

  init(player: Player) {
    self.player = player
    _scrubber = State(initialValue: Scrubber { player.seek(to: $0) })
  }

  var body: some View {
    NavigationStack {
      List {
        Section {
          VStack(spacing: 16) {
            artwork
            titles
            seekBar
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

  private var seekBar: some View {
    let duration = player.duration ?? 0
    let shown = scrubber.target ?? min(player.elapsed, duration)
    return VStack(spacing: 2) {
      Slider(
        value: Binding(get: { shown }, set: { scrubber.moved(to: $0) }),
        in: 0...max(duration, 1)
      ) { editing in
        if !editing { scrubber.released() }
      }
      .disabled(player.duration == nil)
      .accessibilityIdentifier("nowPlaying.seek")
      HStack {
        Text(Self.time(shown)).accessibilityIdentifier("nowPlaying.elapsed")
        Spacer()
        Text("-" + Self.time(max(duration - shown, 0))).accessibilityIdentifier("nowPlaying.remaining")
      }
      .font(.caption.monospacedDigit())
      .foregroundStyle(.secondary)
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

  /// Seconds as m:ss, or h:mm:ss for an hour or more.
  static func time(_ seconds: Double) -> String {
    let s = Int(seconds.rounded(.down))
    return s >= 3600
      ? String(format: "%d:%02d:%02d", s / 3600, s / 60 % 60, s % 60)
      : String(format: "%d:%02d", s / 60, s % 60)
  }
}

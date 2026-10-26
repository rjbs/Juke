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
  let number: Int
  let showArtist: Bool
  /// Removes the track from the list it's in, for a list that allows it.
  var remove: (() -> Void)? = nil
  let play: () -> Void

  var body: some View {
    Button(action: play) {
      HStack(alignment: .firstTextBaseline) {
        Text("\(number)")
          .monospacedDigit()
          .foregroundStyle(.secondary)
          .frame(minWidth: 24, alignment: .trailing)
        VStack(alignment: .leading) {
          Text(track.title)
          if showArtist, let artist = track.artist {
            Text(artist).font(.caption).foregroundStyle(.secondary)
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
      AddToPlaylistMenu(library: library, tracks: [track])
      if let remove {
        Button("Remove from Playlist", systemImage: "minus.circle", role: .destructive, action: remove)
      }
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
      .accessibilityLabel("\(rating.formatted()) stars")
    }
  }
}

/// What's playing, with the transport buttons.  Hidden until something
/// has been played.
struct PlayerBar: View {
  let player: Player

  var body: some View {
    if player.current != nil || player.failure != nil {
      HStack(spacing: 16) {
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
        Spacer()
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
      }
      .labelStyle(.iconOnly)
      .buttonStyle(.borderless)
      .padding(.horizontal)
      .padding(.vertical, 10)
      .background(.bar)
      .accessibilityElement(children: .contain)
      .accessibilityIdentifier("player")
    }
  }
}

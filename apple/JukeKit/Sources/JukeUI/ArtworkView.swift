import JukeKit
import SwiftUI

/// An album's or track's art, or a placeholder where there is none or
/// it hasn't come yet.  Square: `size` points on a side, or with no size,
/// as wide as it's given up to `maxWidth`.
///
/// The image is asked for at the size it's drawn, in pixels, so a list
/// of thumbnails decodes thumbnails, not covers.
///
/// What was loaded is kept with the art it was loaded for: a view whose
/// art changes (the player bar, as tracks change) must not go on showing
/// the old image, nor skip loading the new one because it has one.
/// -- claude, 2026-11-04
struct ArtworkView: View {
  let library: Library
  let artwork: Artwork?
  /// What picks the color drawn when there's no art: the album's id,
  /// usually.  With none, the placeholder is plain grey.
  var seed: String? = nil
  var size: CGFloat? = nil
  var maxWidth: CGFloat = 260
  var cornerRadius: CGFloat = 6
  @Environment(\.displayScale) private var scale
  @State private var loaded: (artwork: Artwork, image: CGImage)?

  private var maxPixels: Int { Int((size ?? maxWidth) * scale) }

  var body: some View {
    let shown = artwork.flatMap { art in
      loaded.flatMap { $0.artwork == art ? $0.image : nil }
        ?? library.images.cached(art, maxPixels: maxPixels)
    }
    Group {
      if let shown {
        Color.clear.overlay {
          Image(decorative: shown, scale: 1).resizable().aspectRatio(contentMode: .fill)
        }
      } else {
        placeholder
      }
    }
    .modifier(Square(size: size, maxWidth: maxWidth))
    .clipShape(RoundedRectangle(cornerRadius: cornerRadius))
    .accessibilityHidden(true)
    .task(id: artwork) {
      guard let artwork, shown == nil else { return }
      if let image = await library.images.image(artwork, maxPixels: maxPixels) {
        loaded = (artwork, image)
      }
    }
  }

  @ViewBuilder private var placeholder: some View {
    let note = Image(systemName: "music.note").font(.system(size: (size ?? maxWidth) / 4))
    if let seed {
      let swatch = PlaceholderSwatch.for(seed)
      Rectangle().fill(Color(red: swatch.red, green: swatch.green, blue: swatch.blue)).overlay {
        note.foregroundStyle(swatch.inkIsLight ? Color.white.opacity(0.82) : Color.black.opacity(0.62))
      }
    } else {
      Rectangle().fill(.quaternary).overlay { note.foregroundStyle(.secondary) }
    }
  }

  private struct Square: ViewModifier {
    let size: CGFloat?
    let maxWidth: CGFloat
    func body(content: Content) -> some View {
      if let size {
        content.frame(width: size, height: size)
      } else {
        content.aspectRatio(1, contentMode: .fit).frame(maxWidth: maxWidth)
      }
    }
  }
}

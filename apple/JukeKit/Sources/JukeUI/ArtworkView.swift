import JukeKit
import SwiftUI

/// An album's or track's art, or a placeholder where there is none or
/// it hasn't come yet.  Square: `size` points on a side, or with no size,
/// as wide as it's given up to `maxWidth`.
///
/// The image is asked for at the size it's drawn, in pixels, so a list
/// of thumbnails decodes thumbnails, not covers.
struct ArtworkView: View {
  let library: Library
  let artwork: Artwork?
  var size: CGFloat? = nil
  var maxWidth: CGFloat = 260
  var cornerRadius: CGFloat = 6
  @Environment(\.displayScale) private var scale
  @State private var image: CGImage?

  private var maxPixels: Int { Int((size ?? maxWidth) * scale) }

  var body: some View {
    let shown = artwork.flatMap { image ?? library.images.cached($0, maxPixels: maxPixels) }
    Group {
      if let shown {
        Color.clear.overlay {
          Image(decorative: shown, scale: 1).resizable().aspectRatio(contentMode: .fill)
        }
      } else {
        Rectangle().fill(.quaternary).overlay {
          Image(systemName: "music.note")
            .font(.system(size: (size ?? maxWidth) / 4))
            .foregroundStyle(.secondary)
        }
      }
    }
    .modifier(Square(size: size, maxWidth: maxWidth))
    .clipShape(RoundedRectangle(cornerRadius: cornerRadius))
    .accessibilityHidden(true)
    .task(id: artwork) {
      guard let artwork, shown == nil else { return }
      image = await library.images.image(artwork, maxPixels: maxPixels)
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

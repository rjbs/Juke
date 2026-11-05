import Foundation
#if canImport(UIKit)
import UIKit
#else
import AppKit
#endif

/// The color of a cover drawn where there's no art, picked from a fixed
/// palette by a hash of something stable, usually the album's id, so an
/// album always gets the same one.
///
/// The palette is muted and mostly dark, so the mark drawn on it is
/// usually light; `inkIsLight` says which of white or black contrasts
/// more with it.
public struct PlaceholderSwatch: Hashable, Sendable {
  /// The swatch, as six hex digits.
  public let hex: String
  public let red: Double
  public let green: Double
  public let blue: Double
  public let inkIsLight: Bool

  init(hex: String) {
    let v = UInt32(hex, radix: 16)!
    self.hex = hex
    red = Double(v >> 16 & 0xff) / 255
    green = Double(v >> 8 & 0xff) / 255
    blue = Double(v & 0xff) / 255
    let lum = [red, green, blue]
      .map { $0 <= 0.03928 ? $0 / 12.92 : pow(($0 + 0.055) / 1.055, 2.4) }
    let l = 0.2126 * lum[0] + 0.7152 * lum[1] + 0.0722 * lum[2]
    inkIsLight = 1.05 / (l + 0.05) >= (l + 0.05) / 0.05
  }

  /// The swatch for `seed`.
  ///
  /// This hashes with FNV-1a rather than `hashValue`, which is seeded
  /// anew on every launch: a cover's color has to stay put between runs,
  /// and match anywhere else that draws these.  -- claude, 2026-11-04
  public static func `for`(_ seed: String) -> PlaceholderSwatch {
    var h: UInt32 = 0x811c_9dc5
    for b in seed.utf8 {
      h ^= UInt32(b)
      h = h &* 0x0100_0193
    }
    return palette[Int(h % UInt32(palette.count))]
  }

  /// The "ed_colors" swatches, in order.  Reordering or adding to them
  /// recolors every placeholder.
  static let palette: [PlaceholderSwatch] = """
    002029 072d4b 17181a 1c3e70 202226 225422 252e34 280830 2d864c 306a10
    321313 36200b 383631 3a5369 3c4f85 3c6c0e 3e4346 44332b 452020 487ab1
    4d1045 4f6229 521a38 565c49 576f23 580000 5c1613 5e3b18 5e463a 5f1a04
    5f544f 603c9d 635a82 64741a 652020 663104 682e74 696865 6b0e06 6b5539
    6c2670 6d1818 6d3909 6d6d6d 704444 745264 751c01 798b97 7a5843 7c0000
    7d4814 7e1a1a 7e271b 834a8f 8c3030 902d0f 930000 953f19 961818 969080
    973200 983308 994220 9b3603 9c5539 9d74a7 9d8f9d 9e672e 9f50b3 a03e0f
    a72828 a9460c a95c5c ad9db1 b94615 bc590a c44100
    """.split(whereSeparator: \.isWhitespace).map { PlaceholderSwatch(hex: String($0)) }
}

extension PlaceholderSwatch {
  /// The placeholder cover as an image `pixels` on a side: the swatch,
  /// with a music note in the middle a quarter as tall, as the app draws
  /// it.  This is for places that want a picture, not a view, like the
  /// system's now-playing display.
  public func image(pixels: Int) -> CGImage? {
    let side = CGFloat(pixels)
    let rect = CGRect(x: 0, y: 0, width: side, height: side)
    #if canImport(UIKit)
    let fill = UIColor(red: red, green: green, blue: blue, alpha: 1)
    let ink = inkIsLight ? UIColor.white.withAlphaComponent(0.82) : UIColor.black.withAlphaComponent(0.62)
    let config = UIImage.SymbolConfiguration(pointSize: side / 4)
      .applying(UIImage.SymbolConfiguration(paletteColors: [ink]))
    let format = UIGraphicsImageRendererFormat()
    format.scale = 1
    return UIGraphicsImageRenderer(size: rect.size, format: format).image { context in
      fill.setFill()
      context.fill(rect)
      if let note = UIImage(systemName: "music.note", withConfiguration: config) {
        note.draw(in: Self.centered(note.size, in: rect))
      }
    }.cgImage
    #else
    guard let rep = NSBitmapImageRep(
      bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels, bitsPerSample: 8,
      samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB,
      bytesPerRow: 0, bitsPerPixel: 0
    ) else { return nil }
    let fill = NSColor(red: red, green: green, blue: blue, alpha: 1)
    let ink = inkIsLight ? NSColor.white.withAlphaComponent(0.82) : NSColor.black.withAlphaComponent(0.62)
    let config = NSImage.SymbolConfiguration(pointSize: side / 4, weight: .regular)
      .applying(NSImage.SymbolConfiguration(paletteColors: [ink]))
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    fill.setFill()
    rect.fill()
    if let note = NSImage(systemSymbolName: "music.note", accessibilityDescription: nil)?
      .withSymbolConfiguration(config) {
      note.draw(in: Self.centered(note.size, in: rect))
    }
    NSGraphicsContext.restoreGraphicsState()
    return rep.cgImage
    #endif
  }

  private static func centered(_ size: CGSize, in rect: CGRect) -> CGRect {
    CGRect(x: rect.midX - size.width / 2, y: rect.midY - size.height / 2, width: size.width, height: size.height)
  }
}

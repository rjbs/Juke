import Foundation

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

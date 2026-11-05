import CoreGraphics
import Testing
@testable import JukeKit

// The swatch a seed gets, and which ink goes on it.  The expected hex
// values were worked out independently, so a change to the hash or the
// palette's order shows up here.

/// Checks the swatch for `seed` and whether its ink is light.
private func swatch(_ seed: String, is hex: String, lightInk: Bool, sourceLocation: SourceLocation = #_sourceLocation) {
  let s = PlaceholderSwatch.for(seed)
  #expect(s.hex == hex, sourceLocation: sourceLocation)
  #expect(s.inkIsLight == lightInk, sourceLocation: sourceLocation)
}

@Test func placeholderSwatches() {
  swatch("Aag2en26qdexrb2iw", is: "4f6229", lightInk: true)
  swatch("A22cufbz5igc44hy5", is: "521a38", lightInk: true)
  swatch("Adcxvyazrfds5vojm", is: "ad9db1", lightInk: false)
  swatch("", is: "961818", lightInk: true)
}

@Test func placeholderPalette() {
  #expect(PlaceholderSwatch.palette.count == 77)
  #expect(Set(PlaceholderSwatch.palette).count == 77)
}

/// The red, green and blue at a pixel of `image`, 0 to 255, with the
/// origin at the top left.
private func pixel(_ image: CGImage, _ x: Int, _ y: Int) -> [Int] {
  var bytes = [UInt8](repeating: 0, count: 4)
  let context = CGContext(
    data: &bytes, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4,
    space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue
  )!
  context.draw(image, in: CGRect(x: -x, y: y - image.height + 1, width: image.width, height: image.height))
  return bytes[0..<3].map(Int.init)
}

/// Checks a seed's placeholder image: its size, the swatch in a corner,
/// and the note's ink somewhere along the middle column.
private func placeholderImage(_ seed: String, pixels: Int, corner: [Int], lightInk: Bool, sourceLocation: SourceLocation = #_sourceLocation) {
  guard let image = PlaceholderSwatch.for(seed).image(pixels: pixels) else {
    Issue.record("no image", sourceLocation: sourceLocation)
    return
  }
  #expect(image.width == pixels && image.height == pixels, sourceLocation: sourceLocation)
  #expect(zip(pixel(image, 2, 2), corner).allSatisfy { abs($0 - $1) <= 3 }, sourceLocation: sourceLocation)
  let column = (pixels / 3..<pixels * 2 / 3).map { pixel(image, pixels / 2 - pixels / 40, $0).reduce(0, +) / 3 }
  let cornerLevel = corner.reduce(0, +) / 3
  let inked = lightInk ? column.contains { $0 > cornerLevel + 80 } : column.contains { $0 < cornerLevel - 60 }
  #expect(inked, sourceLocation: sourceLocation)
}

@Test func placeholderImages() {
  placeholderImage("Aag2en26qdexrb2iw", pixels: 600, corner: [0x4f, 0x62, 0x29], lightInk: true)
  placeholderImage("Adcxvyazrfds5vojm", pixels: 300, corner: [0xad, 0x9d, 0xb1], lightInk: false)
}

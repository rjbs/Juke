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

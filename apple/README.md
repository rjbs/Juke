# Juke for iOS and macOS

One SwiftUI app for both platforms, nearly all of it in the `JukeKit`
package so the two share everything they can:

- **JukeKit** keeps the library: a [Jinx](../../Jinx) mirror of the
  server's Tracks, Albums and Playlists, an in-memory index kept current
  from Jinx's change stream, rating and play-count writes, and a player
  that plays from Jinx's blob cache and reports to the system's
  now-playing display and remote commands.
- **JukeUI** is the views, shared by both platforms; on an iPhone the
  split view collapses into a stack.
- **App/** is the entry point, and `project.yml` describes the app target
  for [XcodeGen](https://github.com/yonaskolb/XcodeGen).
- **Art/icon.png** is the app's artwork at full size, on a transparent
  background.  `Art/make-icons` (run from `apple/`, needs ImageMagick)
  sets it on an opaque Basalt square and writes the PNGs in
  `App/Assets.xcassets/AppIcon.appiconset`: one 1024-pixel image for
  iOS, and the sizes macOS still wants listed one by one.

Jinx is a sibling checkout, at `../../Jinx` from `JukeKit`.

## Building

    cd apple
    xcodegen                   # writes Juke.xcodeproj, which isn't committed
    open Juke.xcodeproj

or from the command line:

    xcodebuild -project Juke.xcodeproj -scheme Juke -destination platform=macOS -derivedDataPath build build

Jinx needs iOS 27, so the simulator needs an iOS 27 runtime.

The app connects to `http://localhost:8737` until told otherwise in its
settings; see [the server](../doc/server.md) for running one.

## Testing

    cd apple/JukeKit
    swift test

The tests that need a server run only when `JUKE_TEST_SERVER` names one,
such as `http://localhost:8737`.  They sync a fresh mirror, play a track's
audio through the cache, and write a rating and a play, so point them at
a scratch database.

# Juke for iOS and macOS

One SwiftUI app for both platforms, nearly all of it in the `JukeKit`
package so the two share everything they can:

- **JukeKit** keeps the library: a [Jinx](../../Jinx) mirror of the
  server's Tracks, Albums and Playlists of both kinds, an in-memory index
  kept current from Jinx's change stream, each smart playlist's rule run
  as a query subscription, rating and play-count writes, and a player
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

The tests that need a server run only when `JUKE_TEST_SERVER` names one.
They sync a fresh mirror, play a track's audio through the cache, and
write a rating and a play, so point them at the scratch server (see UI
tests, below): `JUKE_TEST_SERVER=http://localhost:8738`.

## Releasing to TestFlight

This needs a paid Apple Developer Program membership on the team named in
`project.yml`; a free personal team can't upload.

Once, in [App Store Connect](https://appstoreconnect.apple.com):

1. **Apps → + → New App.**  Platform iOS, name "Juke" (or whatever is
   free: App Store names are global, even for an app that never leaves
   TestFlight), primary language English, bundle ID
   `systems.semiotic.juke`, and any SKU, such as `juke`.  If the bundle ID
   isn't offered, build once from Xcode with the paid team selected,
   which registers it.
2. **TestFlight → Internal Testing → +.**  Make a group and add yourself.
   Internal testers get every build without Apple's review.
3. Install the TestFlight app on each device, signed in with the same
   Apple ID.

Then, for each build, from a clean working tree:

    cd apple
    bin/release

That archives the app, signs it for distribution, and uploads it.  The
build number is the number of commits on HEAD, so commit before
releasing; the version shown to testers is `MARKETING_VERSION` in
`project.yml`.  App Store Connect takes some minutes to process an
upload, then TestFlight offers it.  `bin/release --no-upload` stops
short, leaving an `.ipa` under `build/release`.

## UI tests

`UITests/` drives the iOS app in the simulator: it launches it against a
server, searches, opens albums, plays tracks, and saves screenshots along
the way.  They play audio and record plays, so they run against a scratch
copy of the library rather than the one you listen to:

    bin/scratch-server --fresh     # from the repo root: var/scratch.sqlite on :8738
    cd apple
    bin/ui-test                    # every UI test
    bin/ui-test testPlayFromAlbum  # some of them

`bin/ui-test` prints how each test went, keeps xcodebuild's full output in
`build/ui-test/xcodebuild.log`, and exports the screenshots to
`build/ui-test/screenshots`, named by test and step.  It uses a simulator
called "Juke iPhone", making one if there isn't one.  The first run in a
fresh simulator syncs the whole library first, which takes a minute.

The app is pointed at the server with a launch argument, `-serverURL`,
which lands in the same `UserDefaults` key the settings screen writes.

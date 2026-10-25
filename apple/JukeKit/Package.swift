// swift-tools-version: 6.2
import PackageDescription

// JukeKit is everything the Juke apps share: the library mirror over
// Jinx and the player (JukeKit), and the SwiftUI views (JukeUI).  The
// apps themselves are little more than an entry point.
let package = Package(
  name: "JukeKit",
  platforms: [
    .macOS("27.0"),
    .iOS("27.0"),
  ],
  products: [
    .library(name: "JukeKit", targets: ["JukeKit"]),
    .library(name: "JukeUI", targets: ["JukeUI"]),
  ],
  dependencies: [
    .package(path: "../../../Jinx"),
  ],
  targets: [
    .target(
      name: "JukeKit",
      dependencies: [
        .product(name: "Jinx", package: "Jinx"),
        .product(name: "JinxJSON", package: "Jinx"),
        .product(name: "JinxPatch", package: "Jinx"),
        .product(name: "JinxStore", package: "Jinx"),
        .product(name: "JinxWire", package: "Jinx"),
      ]
    ),
    .target(name: "JukeUI", dependencies: ["JukeKit"]),
    .testTarget(name: "JukeKitTests", dependencies: ["JukeKit"]),
  ]
)

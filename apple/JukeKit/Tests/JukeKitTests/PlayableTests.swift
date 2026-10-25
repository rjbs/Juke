import Foundation
@testable import JukeKit
import Testing

enum PlayableName {
  /// A hard link to a file the cache still holds.
  case linked
  /// A file whose cached original has been evicted.
  case alone
  /// A symlink, as earlier versions made.
  case symlink
}

// checkSweep makes a playable directory holding the given names, sweeps
// it, and checks which names are left.
func checkSweep(_ names: [String: PlayableName], left: Set<String>,
                sourceLocation: SourceLocation = #_sourceLocation) throws {
  let fm = FileManager.default
  let root = fm.temporaryDirectory.appending(path: "juke-sweep-\(UUID())")
  defer { try? fm.removeItem(at: root) }
  let cache = root.appending(path: "audio")
  let playable = root.appending(path: "playable")
  try fm.createDirectory(at: cache, withIntermediateDirectories: true)
  try fm.createDirectory(at: playable, withIntermediateDirectories: true)

  for (name, kind) in names {
    let cached = cache.appending(path: name)
    try Data("audio".utf8).write(to: cached)
    let link = playable.appending(path: "\(name).mp3")
    switch kind {
    case .linked:
      try fm.linkItem(at: cached, to: link)
    case .alone:
      try fm.linkItem(at: cached, to: link)
      try fm.removeItem(at: cached)
    case .symlink:
      try fm.createSymbolicLink(at: link, withDestinationURL: cached)
    }
  }

  Library.sweepPlayable(playable)
  let found = try fm.contentsOfDirectory(atPath: playable.path).map { ($0 as NSString).deletingPathExtension }
  #expect(Set(found) == left, sourceLocation: sourceLocation)
}

@Test func sweep() throws {
  try checkSweep([:], left: [])
  try checkSweep(["kept": .linked], left: ["kept"])
  try checkSweep(["kept": .linked, "evicted": .alone, "old": .symlink], left: ["kept"])
}

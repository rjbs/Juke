import Foundation
import Testing
@testable import JukeKit

// Other servers' copies: listed by the server each remembers, or its
// directory's name, with the bytes under it; removed whole.

/// A copies directory holding one directory per entry: its name, the
/// server file's contents if any, and how many bytes of audio.
private func copiesDirectory(_ entries: [(name: String, server: String?, bytes: Int)]) throws -> URL {
  let base = FileManager.default.temporaryDirectory.appending(path: "copies-\(UUID().uuidString)")
  for entry in entries {
    let dir = base.appending(path: entry.name)
    try FileManager.default.createDirectory(at: dir.appending(path: "audio"), withIntermediateDirectories: true)
    if let server = entry.server {
      try Data(server.utf8).write(to: dir.appending(path: "server"))
    }
    try Data(repeating: 0, count: entry.bytes).write(to: dir.appending(path: "audio/one.mp3"))
  }
  try Data("not a copy".utf8).write(to: base.appending(path: "stray.txt"))
  return base
}

/// Lists a copies directory and checks what each entry says.
private func lists(_ entries: [(name: String, server: String?, bytes: Int)], as expected: [(server: String, atLeast: Int)], sourceLocation: SourceLocation = #_sourceLocation) throws -> (URL, [LibraryCopy]) {
  let base = try copiesDirectory(entries)
  let copies = try Library.copies(under: base)
  #expect(copies.map(\.server) == expected.map(\.server), sourceLocation: sourceLocation)
  for (copy, want) in zip(copies, expected) {
    #expect(copy.size >= Int64(want.atLeast), "\(copy.server) is \(copy.size) bytes", sourceLocation: sourceLocation)
  }
  return (base, copies)
}

@Test func copiesListByServerThenDirectoryName() throws {
  let (base, _) = try lists(
    [("localhost-8738", "http://localhost:8738", 10), ("juke.example", nil, 5000), ("empty", "", 0)],
    as: [("empty", 0), ("http://localhost:8738", 10), ("juke.example", 5000)]
  )
  try? FileManager.default.removeItem(at: base)
}

@Test func removingACopyDeletesItsDirectory() throws {
  let (base, copies) = try lists([("a", "https://a", 1), ("b", "https://b", 1)], as: [("https://a", 1), ("https://b", 1)])
  try Library.remove(copies[0])
  #expect(try Library.copies(under: base).map(\.server) == ["https://b"])
  try? FileManager.default.removeItem(at: base)
}

import Foundation

/// A server's library as kept on this device: the directory that
/// `Library.directory(for:)` names, holding its mirror, its pins and
/// its audio.  Listed so the copies of servers no longer in use can be
/// seen, with their size, and removed.
public struct LibraryCopy: Identifiable, Hashable, Sendable {
  public var directory: URL
  /// The server's URL as remembered when the library was opened, or the
  /// directory's name for a copy made before that was remembered.
  public var server: String
  /// Bytes on disk, audio mostly.
  public var size: Int64

  public var id: String { directory.lastPathComponent }
}

extension Library {
  /// The directory every server's copy lives under.
  nonisolated static func copiesDirectory() throws -> URL {
    let base = try FileManager.default.url(
      for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true
    )
    return base.appending(path: "Juke")
  }

  /// The directory a server's library lives in, under Application
  /// Support: one per server, so pointing the app at another server
  /// never mixes two libraries.
  public nonisolated static func directory(for server: URL) throws -> URL {
    let name = [server.host() ?? "server", server.port.map(String.init)].compactMap(\.self).joined(separator: "-")
    return try copiesDirectory().appending(path: name)
  }

  /// Every server's copy on this device.  Sizes are walked, so this
  /// belongs off the main thread.
  public nonisolated static func copies() throws -> [LibraryCopy] {
    try copies(under: copiesDirectory())
  }

  /// The copies under a directory, for `copies` and its test.
  nonisolated static func copies(under base: URL) throws -> [LibraryCopy] {
    let fm = FileManager.default
    guard let entries = try? fm.contentsOfDirectory(at: base, includingPropertiesForKeys: [.isDirectoryKey]) else { return [] }
    var copies: [LibraryCopy] = []
    for dir in entries where (try? dir.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) == true {
      let server = (try? String(contentsOf: dir.appending(path: "server"), encoding: .utf8))
        .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
        .flatMap { $0.isEmpty ? nil : $0 }
      copies.append(LibraryCopy(directory: dir, server: server ?? dir.lastPathComponent, size: size(of: dir)))
    }
    return copies.sorted { $0.server < $1.server }
  }

  /// Deletes a copy: its mirror, pins and audio.  Not for the copy in
  /// use; the server itself is untouched.
  public nonisolated static func remove(_ copy: LibraryCopy) throws {
    try FileManager.default.removeItem(at: copy.directory)
  }

  private nonisolated static func size(of dir: URL) -> Int64 {
    let keys: Set<URLResourceKey> = [.totalFileAllocatedSizeKey, .fileSizeKey]
    guard let walk = FileManager.default.enumerator(at: dir, includingPropertiesForKeys: Array(keys)) else { return 0 }
    var total: Int64 = 0
    for case let file as URL in walk {
      guard let values = try? file.resourceValues(forKeys: keys) else { continue }
      total += Int64(values.totalFileAllocatedSize ?? values.fileSize ?? 0)
    }
    return total
  }
}

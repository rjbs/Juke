import CoreGraphics
import Foundation
import ImageIO

/// Album art as images ready to draw: decoded from the blob cache at the
/// size they're shown, and kept in memory so a list scrolls without
/// decoding the same cover twice.
@MainActor
public final class ArtworkCache {
  private unowned let library: Library
  private let images = NSCache<NSString, CGImage>()
  private var loading: [String: Task<CGImage?, Never>] = [:]

  init(library: Library) {
    self.library = library
    images.countLimit = 500
  }

  /// The image if it's decoded already, to draw without waiting.
  public func cached(_ blobId: String, maxPixels: Int) -> CGImage? {
    images.object(forKey: key(blobId, maxPixels))
  }

  /// The image, fetched and decoded if need be, at most `maxPixels` on
  /// a side.  Nil when it can't be had, such as offline with nothing
  /// cached.  Asking twice for one image while it's coming waits once.
  public func image(_ blobId: String, maxPixels: Int) async -> CGImage? {
    let k = key(blobId, maxPixels)
    if let image = images.object(forKey: k) { return image }
    if let task = loading[k as String] { return await task.value }
    let task = Task<CGImage?, Never> { [library] in
      guard let url = try? await library.artworkFile(blobId) else { return nil }
      return await Self.decode(url, maxPixels: maxPixels)
    }
    loading[k as String] = task
    let image = await task.value
    loading[k as String] = nil
    if let image { images.setObject(image, forKey: k) }
    return image
  }

  private func key(_ blobId: String, _ maxPixels: Int) -> NSString {
    "\(blobId)@\(maxPixels)" as NSString
  }

  /// Decodes an image file scaled down to fit `maxPixels`, off the main
  /// actor, since a full-size cover takes real time to decode.
  private nonisolated static func decode(_ url: URL, maxPixels: Int) async -> CGImage? {
    await Task.detached(priority: .userInitiated) {
      guard let source = CGImageSourceCreateWithURL(url as CFURL, nil) else { return nil }
      let options: [CFString: Any] = [
        kCGImageSourceCreateThumbnailFromImageAlways: true,
        kCGImageSourceCreateThumbnailWithTransform: true,
        kCGImageSourceShouldCacheImmediately: true,
        kCGImageSourceThumbnailMaxPixelSize: maxPixels,
      ]
      return CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary)
    }.value
  }
}

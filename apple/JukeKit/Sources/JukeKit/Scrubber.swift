import Foundation
import Observation

/// A seek bar's drag, kept apart from playback.  The bar follows the
/// finger, and playback follows the bar when the finger lifts, or once it
/// has rested for a moment.
///
/// Seeking on rest, not just on release, is what makes this robust: a
/// slider inside a scrolling list can have its touch cancelled by the
/// list, and SwiftUI's Slider doesn't always report the end of editing
/// for a cancelled touch.  Waiting only for that report left the bar
/// frozen where it was dragged, with playback carrying on where it was,
/// until a later tap on the bar fired the stale seek.  -- claude, 2026-11-02
@MainActor @Observable
public final class Scrubber {
  /// Where the bar has been dragged to, while a drag is unresolved.
  public private(set) var target: Double?

  @ObservationIgnored private let seek: (Double) -> Void
  @ObservationIgnored private let rest: Duration
  @ObservationIgnored private var resting: Task<Void, Never>?

  /// `seek` moves playback; it's called with where the bar came to rest
  /// for `rest`, or where it was released.
  public init(rest: Duration = .milliseconds(150), seek: @escaping (Double) -> Void) {
    self.rest = rest
    self.seek = seek
  }

  /// The bar has moved to `value`.
  public func moved(to value: Double) {
    target = value
    resting?.cancel()
    resting = Task { [weak self, rest] in
      guard (try? await Task.sleep(for: rest)) != nil else { return }
      self?.commit()
    }
  }

  /// The drag has ended.  This is also what a tap on the bar sends, so it
  /// seeks only if there's an unresolved drag.
  public func released() {
    commit()
  }

  private func commit() {
    resting?.cancel()
    resting = nil
    guard let target else { return }
    self.target = nil
    seek(target)
  }
}

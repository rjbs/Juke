import Foundation
import Testing
@testable import JukeKit

@MainActor
struct ScrubberTests {
  enum Step {
    case move(Double)
    case release
    /// Long enough for a moved bar to count as resting.
    case rest
  }

  static let rest = Duration.milliseconds(20)

  /// Drives a scrubber through `steps` and checks the seeks it asked for.
  func checkScrubbing(_ steps: [Step], seeks expected: [Double], _ comment: Comment) async {
    var seeks: [Double] = []
    let scrubber = Scrubber(rest: Self.rest) { seeks.append($0) }
    for step in steps {
      switch step {
      case .move(let value): scrubber.moved(to: value)
      case .release: scrubber.released()
      case .rest: try? await Task.sleep(for: Self.rest * 5)
      }
    }
    #expect(seeks == expected, comment)
  }

  @Test func scrubbing() async {
    await checkScrubbing([.move(10), .move(20), .move(30), .release], seeks: [30],
                         "a drag seeks where it was released")
    await checkScrubbing([.move(10), .move(20), .move(30), .rest], seeks: [30],
                         "a drag whose end is never reported still seeks")
    await checkScrubbing([.move(30), .rest, .release], seeks: [30],
                         "a late release after the bar has rested doesn't seek again")
    await checkScrubbing([.move(10), .rest, .move(20), .release], seeks: [10, 20],
                         "a pause mid-drag seeks, and the rest of the drag seeks again")
    await checkScrubbing([.release], seeks: [],
                         "a tap with no drag doesn't seek")
    await checkScrubbing([.move(10), .move(20), .release, .rest], seeks: [20],
                         "the rest timer is cancelled by a release")
  }
}

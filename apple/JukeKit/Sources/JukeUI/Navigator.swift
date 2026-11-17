import JukeKit
import SwiftUI

/// Jumps from anywhere in the library to an artist's or an album's page,
/// switching the sidebar to Artists or Albums and opening the page there,
/// so Back leads to that list, not to wherever the jump started.
struct Navigator {
  var goToArtist: (Artist) -> Void = { _ in }
  var goToAlbum: (Album.ID) -> Void = { _ in }
}

/// The page a view is, for a link that would lead back to it.
enum Destination: Equatable {
  case artist(String)
  case album(Album.ID)
}

extension EnvironmentValues {
  @Entry var navigator = Navigator()
  /// The page this view is in, if it's an artist's or an album's.
  @Entry var destination: Destination? = nil
}

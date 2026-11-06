import JinxInspector
import JukeKit
import SwiftUI

/// Jinx's inspector over the open library's sync engine: its store, the
/// requests made while the window is open, and the journal of changes.
/// It is a diagnostic tool, reached from the Window menu.
public struct JukeInspectorView: View {
  let model: AppModel

  public init(model: AppModel) {
    self.model = model
  }

  public var body: some View {
    if let library = model.library {
      // A reopened library has a new engine, and the inspector's request
      // log is tapped into the old one, so build it afresh.
      // -- claude, 2026-11-05
      JinxInspectorView(engine: library.inspecting)
        .id(ObjectIdentifier(library))
        .frame(minWidth: 700, minHeight: 600)
    } else {
      ContentUnavailableView("No Library Open", systemImage: "cylinder.split.1x2")
    }
  }
}

import JukeKit
import SwiftUI

/// The settings section listing other servers' libraries kept on this
/// device, each with its size and a Remove Copy… button.  Shown only
/// when there are any.
struct CopiesSection: View {
  let model: AppModel

  var body: some View {
    if !model.otherCopies.isEmpty {
      Section("Other Libraries") {
        ForEach(model.otherCopies) { copy in
          CopyRow(model: model, copy: copy)
        }
        if let failure = model.removeFailure {
          Label(failure, systemImage: "exclamationmark.triangle")
            .foregroundStyle(.secondary)
        }
      }
    }
  }
}

private struct CopyRow: View {
  let model: AppModel
  let copy: LibraryCopy
  @State private var confirming = false

  private var size: String { copy.size.formatted(.byteCount(style: .file)) }

  var body: some View {
    LabeledContent(copy.server) {
      HStack {
        Text(size)
          .foregroundStyle(.secondary)
        Button("Remove Copy…") { confirming = true }
      }
    }
    .confirmationDialog("Remove the copy of \(copy.server)?", isPresented: $confirming, titleVisibility: .visible) {
      Button("Remove Copy", role: .destructive) { model.remove(copy) }
    } message: {
      Text("Its synced library and \(size) of audio are deleted from this device.  The server is not affected.")
    }
  }
}

import JukeKit
import SwiftUI

/// What the smart playlist editor is opened on: a blank rule, a stored
/// playlist's rule, or a copy of one as the start of a new playlist.
public enum SmartPlaylistEditing: Identifiable, Hashable {
  case new
  case rule(SmartPlaylist)
  case copy(SmartPlaylist)

  public var id: String {
    switch self {
    case .new: "new"
    case .rule(let p): "rule:\(p.id)"
    case .copy(let p): "copy:\(p.id)"
    }
  }
}

/// Where the editor is presented from: the sidebar keeps one of these,
/// shows the editor as a sheet while `editing` is set, and puts it in
/// the detail column's environment, so a smart playlist's page can open
/// the editor wherever it was pushed from.
@MainActor @Observable
final class SmartPlaylistEditorHost {
  var editing: SmartPlaylistEditing?
  /// The content size of the window the editor was opened from, so the
  /// sheet can be sized to fit inside it.
  var windowSize: CGSize?

  func edit(_ subject: SmartPlaylistEditing) {
    #if os(macOS)
    windowSize = NSApp.keyWindow?.contentView?.frame.size
    #endif
    editing = subject
  }
}

/// The editor's state: the draft, what the server last said it matches,
/// and what it was opened on, for telling a change.  The preview re-runs
/// shortly after each edit; the server's refusals are the validation.
@MainActor @Observable
final class SmartPlaylistEditorModel {
  let library: Library
  let editing: SmartPlaylistEditing
  var draft: SmartPlaylistDraft
  /// Why the stored rule couldn't be read into the tree, if it couldn't.
  let unreadable: String?
  /// The id being edited, or nil for a new playlist.
  let id: String?
  /// The draft as opened, for a stored rule, to tell a change.
  private let original: SmartPlaylistDraft?
  var preview: RulePreview?
  var previewError: String?
  var running = false
  var saving = false
  var saveError: String?
  /// The playlists a rule may name, by title, with the one being edited
  /// left out.
  let lists: [ListChoice]
  /// Values the library has, to offer for a string field.
  let suggestions: [RuleField: [String]]
  @ObservationIgnored private var previewTask: Task<Void, Never>?
  @ObservationIgnored private var previewSeq = 0

  struct ListChoice: Identifiable, Hashable {
    var list: RuleList
    var title: String
    var id: String { list.id }
  }

  init(library: Library, editing: SmartPlaylistEditing) {
    self.library = library
    self.editing = editing
    var unreadable: String?
    var draft: SmartPlaylistDraft
    var id: String?
    var original: SmartPlaylistDraft?
    switch editing {
    case .new:
      draft = SmartPlaylistDraft()
    case .rule(let p):
      id = p.id
      do {
        draft = try SmartPlaylistDraft(p)
        original = draft
      } catch {
        draft = SmartPlaylistDraft(title: p.title)
        unreadable = error.localizedDescription
      }
    case .copy(let p):
      do {
        draft = try SmartPlaylistDraft(p, title: "\(p.title) copy")
      } catch {
        draft = SmartPlaylistDraft(title: "\(p.title) copy")
        unreadable = error.localizedDescription
      }
    }
    self.draft = draft
    self.id = id
    self.original = original
    self.unreadable = unreadable
    let plain = library.playlists.compactMap { p in
      p.serverId.map { ListChoice(list: RuleList(id: $0, smart: false), title: p.title) }
    }
    let editingId = id
    let smart = library.smartPlaylists.filter { $0.id != editingId }.map {
      ListChoice(list: RuleList(id: $0.id, smart: true), title: $0.title)
    }
    lists = (plain + smart).sorted { $0.title.localizedStandardCompare($1.title) == .orderedAscending }
    func distinct(_ values: some Sequence<String?>) -> [String] {
      Array(Set(values.compactMap(\.self))).sorted { $0.localizedStandardCompare($1) == .orderedAscending }
    }
    suggestions = [
      .artist: distinct(library.albumList.map(\.artist)),
      .album: distinct(library.albumList.map(\.title)),
      .genre: distinct(library.tracks.values.map(\.genre)),
    ]
  }

  /// Why the draft can't be saved as it stands, if it can't.
  var problem: String? {
    if let unreadable { return unreadable }
    do { _ = try draft.properties() } catch { return error.localizedDescription }
    return previewError
  }

  var isDirty: Bool {
    guard let original else { return true }
    return !draft.isEquivalent(to: original)
  }

  var canSave: Bool { problem == nil && isDirty && !saving }

  /// A playlist's title, for a chip.
  func title(of list: RuleList) -> String {
    lists.first { $0.list == list }?.title ?? "a deleted playlist"
  }

  /// Runs the preview again, after a pause for more typing.
  func changed(delay: Duration = .milliseconds(400)) {
    previewTask?.cancel()
    previewTask = Task { [weak self] in
      try? await Task.sleep(for: delay)
      guard !Task.isCancelled else { return }
      await self?.runPreview()
    }
  }

  func runPreview() async {
    previewSeq += 1
    let seq = previewSeq
    if unreadable != nil { return }
    do {
      _ = try draft.properties()
    } catch {
      preview = nil
      previewError = nil
      return
    }
    running = true
    do {
      let result = try await library.preview(draft, pageSize: 50)
      guard seq == previewSeq else { return }
      preview = result
      previewError = nil
    } catch {
      guard seq == previewSeq else { return }
      preview = nil
      previewError = error.localizedDescription
    }
    running = false
  }

  /// Saves, and returns the playlist's id, or nil with `saveError` set.
  func save() async -> String? {
    saving = true
    defer { saving = false }
    do {
      return try await library.save(draft, as: id)
    } catch {
      saveError = error.localizedDescription
      return nil
    }
  }
}

/// The smart playlist editor, in a sheet: the rule as a tree of groups
/// and conditions, the sort and limits beneath, and beneath that the
/// tracks it matches now.  `saved` gets the playlist's id.
struct SmartPlaylistEditor: View {
  @State private var model: SmartPlaylistEditorModel
  let saved: (String) -> Void
  /// The content size of the window the sheet is shown in, if known.
  let window: CGSize?
  @Environment(\.dismiss) private var dismiss

  init(library: Library, editing: SmartPlaylistEditing, window: CGSize? = nil, saved: @escaping (String) -> Void) {
    _model = State(initialValue: SmartPlaylistEditorModel(library: library, editing: editing))
    self.window = window
    self.saved = saved
  }

  /// The size to ask for on macOS: roomy, but inside the window it is
  /// shown over, which a sheet would otherwise hang out of.
  private var sheetSize: CGSize {
    let window = window ?? CGSize(width: 1200, height: 900)
    return CGSize(
      width: min(840, max(640, window.width - 80)),
      height: min(780, max(420, window.height - 60))
    )
  }

  var body: some View {
    @Bindable var model = model
    NavigationStack {
      Form {
        Section {
          TextField("Title", text: $model.draft.title)
            .accessibilityIdentifier("smartPlaylist.title")
        }
        Section("Match") {
          if let unreadable = model.unreadable {
            Label(unreadable, systemImage: "exclamationmark.triangle")
              .foregroundStyle(.secondary)
          } else {
            GroupEditor(group: $model.draft.root, model: model, depth: 0, remove: nil)
          }
        }
        Section("Order") {
          SortEditor(sort: $model.draft.sort)
        }
        Section("Limits") {
          LimitsEditor(limits: $model.draft.limits)
        }
        Section("Preview") {
          PreviewList(model: model)
        }
      }
      .formStyle(.grouped)
      .navigationTitle(model.id == nil ? "New Smart Playlist" : "Edit Smart Playlist")
      #if os(iOS)
      .navigationBarTitleDisplayMode(.inline)
      #endif
      .safeAreaInset(edge: .bottom) { bar }
      .onChange(of: model.draft) { model.changed() }
      .task { await model.runPreview() }
      .alert("Couldn't Save", isPresented: Binding(get: { model.saveError != nil }, set: { if !$0 { model.saveError = nil } })) {
        Button("OK") {}
      } message: {
        Text(model.saveError ?? "")
      }
    }
    #if os(macOS)
    .frame(minWidth: 640, idealWidth: sheetSize.width, minHeight: 420, idealHeight: sheetSize.height)
    #endif
  }

  private var bar: some View {
    HStack {
      Button("Cancel", role: .cancel) { dismiss() }
        .keyboardShortcut(.cancelAction)
      Spacer()
      Group {
        if let problem = model.problem {
          Text(problem).foregroundStyle(.secondary)
        } else if model.isDirty {
          Text("Unsaved changes")
        } else {
          Text("Nothing to save").foregroundStyle(.secondary)
        }
      }
      .font(.caption)
      .lineLimit(2)
      Button(model.id == nil ? "Create" : "Save") {
        Task {
          if let id = await model.save() {
            saved(id)
            dismiss()
          }
        }
      }
      .keyboardShortcut(.defaultAction)
      .disabled(!model.canSave)
      .accessibilityIdentifier("smartPlaylist.save")
    }
    .padding()
    .background(.bar)
  }
}

// MARK: - The tree

extension Binding where Value == RuleNode {
  /// The node as a group, for a binding into a group's items.  Setting
  /// it replaces the node, whatever it was.
  var group: Binding<RuleGroup> {
    Binding<RuleGroup>(
      get: { if case .group(let g) = wrappedValue { g } else { RuleGroup() } },
      set: { wrappedValue = .group($0) }
    )
  }

  var row: Binding<RuleRow> {
    Binding<RuleRow>(
      get: { if case .row(let r) = wrappedValue { r } else { RuleRow() } },
      set: { wrappedValue = .row($0) }
    )
  }
}

/// A group: which of its items must match, the items, and buttons to
/// add more.  `remove` takes the group out of its parent; the root has
/// none.
struct GroupEditor: View {
  @Binding var group: RuleGroup
  let model: SmartPlaylistEditorModel
  let depth: Int
  let remove: (() -> Void)?

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      HStack {
        Picker("Group", selection: $group.operator) {
          ForEach(RuleGroup.Operator.allCases) { Text($0.label).tag($0) }
        }
        .labelsHidden()
        .pickerStyle(.menu)
        .fixedSize()
        Spacer()
        if let remove {
          RemoveButton(action: remove)
        }
      }
      ForEach($group.items) { $item in
        switch item {
        case .group:
          GroupEditor(group: $item.group, model: model, depth: depth + 1) { group.items.removeAll { $0.id == item.id } }
            .padding(.leading, 16)
            .overlay(alignment: .leading) { Rectangle().fill(.quaternary).frame(width: 2) }
        case .row:
          RowEditor(row: $item.row, model: model) { group.items.removeAll { $0.id == item.id } }
        }
      }
      HStack {
        Button("Condition", systemImage: "plus") { group.items.append(.row(RuleRow())) }
          .accessibilityIdentifier("rule.addCondition")
        Button("Group", systemImage: "plus") { group.items.append(.group(RuleGroup(items: [.row(RuleRow())]))) }
      }
      .buttonStyle(.bordered)
      .controlSize(.small)
    }
  }
}

struct RemoveButton: View {
  let action: () -> Void

  var body: some View {
    Button("Remove", systemImage: "xmark", action: action)
      .labelStyle(.iconOnly)
      .buttonStyle(.borderless)
      .foregroundStyle(.secondary)
      .accessibilityIdentifier("rule.remove")
  }
}

/// One condition: the field, the operator, and the value the operator
/// takes.
struct RowEditor: View {
  @Binding var row: RuleRow
  let model: SmartPlaylistEditorModel
  let remove: () -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 4) {
      HStack(alignment: .firstTextBaseline) {
        Picker("Field", selection: Binding(get: { row.field }, set: { row.change(to: $0) })) {
          ForEach(RuleField.allCases) { Text($0.label).tag($0) }
        }
        .labelsHidden()
        .pickerStyle(.menu)
        .fixedSize()
        Picker("Operator", selection: $row.operator) {
          ForEach(row.field.operators) { Text($0.label).tag($0) }
        }
        .labelsHidden()
        .pickerStyle(.menu)
        .fixedSize()
        value
        Spacer(minLength: 0)
        RemoveButton(action: remove)
      }
      if let hint {
        Text(hint).font(.caption).foregroundStyle(.secondary)
      }
    }
  }

  /// What a negated operator also matches, as the server's `NOT` has it.
  private var hint: String? {
    guard row.operator.negated != nil else { return nil }
    switch row.field {
    case .artist, .album, .genre, .rating: return "Also tracks with no \(row.field.label.lowercased())."
    case .lastPlayed: return "Also tracks never played."
    default: return nil
    }
  }

  @ViewBuilder private var value: some View {
    switch row.operator.input {
    case .none:
      EmptyView()
    case .text:
      TextField("Value", text: $row.text)
        .textFieldStyle(.roundedBorder)
        .frame(minWidth: 120, idealWidth: 220)
      SuggestionMenu(values: model.suggestions[row.field] ?? []) { row.text = $0 }
    case .number:
      TextField("0", value: $row.number, format: .number)
        .textFieldStyle(.roundedBorder)
        .frame(width: 72)
        #if os(iOS)
        .keyboardType(.decimalPad)
        #endif
      if let unit = row.operator == .within || row.operator == .notWithin ? "days" : row.field.unit {
        Text(unit).foregroundStyle(.secondary)
      }
    case .date:
      DatePicker("Date", selection: $row.date, displayedComponents: .date)
        .labelsHidden()
    case .list:
      ChipList(
        chips: row.strings.map { Chip(id: $0, title: $0) },
        remove: { id in row.strings.removeAll { $0 == id } },
        suggestions: (model.suggestions[row.field] ?? []).filter { !row.strings.contains($0) },
        typed: { row.strings.append($0) },
        chosen: { row.strings.append($0) }
      )
    case .playlists:
      let unchosen = model.lists.filter { !row.lists.contains($0.list) }
      ChipList(
        chips: row.lists.map { Chip(id: $0.id, title: model.title(of: $0), smart: $0.smart) },
        remove: { id in row.lists.removeAll { $0.id == id } },
        suggestions: unchosen.map(\.title),
        typed: nil,
        chosen: { title in if let c = unchosen.first(where: { $0.title == title }) { row.lists.append(c.list) } }
      )
    }
  }
}

/// A menu of values the library has, to put in a text field.
struct SuggestionMenu: View {
  let values: [String]
  let choose: (String) -> Void

  var body: some View {
    if !values.isEmpty {
      Menu("Choose", systemImage: "chevron.down.circle") {
        ForEach(values, id: \.self) { v in Button(v) { choose(v) } }
      }
      .labelStyle(.iconOnly)
      .menuStyle(.borderlessButton)
      .fixedSize()
    }
  }
}

struct Chip: Identifiable, Hashable {
  var id: String
  var title: String
  var smart = false
}

/// A list of chosen values, each removable, with a way to add one: typed
/// in, where `typed` is given, and chosen from `suggestions`.
struct ChipList: View {
  let chips: [Chip]
  let remove: (String) -> Void
  let suggestions: [String]
  let typed: ((String) -> Void)?
  let chosen: (String) -> Void

  @State private var entry = ""

  var body: some View {
    VStack(alignment: .leading, spacing: 4) {
      ForEach(chips) { chip in
        HStack(spacing: 4) {
          if chip.smart { Image(systemName: "gearshape").foregroundStyle(.secondary) }
          Text(chip.title)
          RemoveButton { remove(chip.id) }
        }
      }
      HStack {
        if let typed {
          TextField("Add", text: $entry)
            .textFieldStyle(.roundedBorder)
            .frame(minWidth: 120, idealWidth: 220)
            .onSubmit {
              let v = entry.trimmingCharacters(in: .whitespaces)
              if !v.isEmpty { typed(v) }
              entry = ""
            }
        }
        if !suggestions.isEmpty {
          if typed == nil {
            Menu("Add Playlist", systemImage: "plus") {
              ForEach(suggestions, id: \.self) { s in Button(s) { chosen(s) } }
            }
            .fixedSize()
          } else {
            SuggestionMenu(values: suggestions, choose: chosen)
          }
        }
      }
    }
  }
}

// MARK: - Order and limits

struct SortEditor: View {
  @Binding var sort: [RuleSort]

  var body: some View {
    ForEach($sort) { $c in
      HStack {
        Picker("Property", selection: $c.property) {
          ForEach(RuleSort.Property.allCases) { Text($0.label).tag($0) }
        }
        .labelsHidden()
        .pickerStyle(.menu)
        .fixedSize()
        if c.property == .random {
          Button("Reshuffle") { c.seed = UUID().uuidString }
            .controlSize(.small)
        } else {
          Picker("Direction", selection: $c.ascending) {
            Text(c.property == .lastPlayed ? "least recent first" : "ascending").tag(true)
            Text(c.property == .lastPlayed ? "most recent first" : "descending").tag(false)
          }
          .labelsHidden()
          .pickerStyle(.menu)
          .fixedSize()
        }
        Spacer()
        if let i = sort.firstIndex(where: { $0.id == c.id }), i > 0 {
          Button("Sort by this earlier", systemImage: "arrow.up") { sort.swapAt(i, i - 1) }
            .labelStyle(.iconOnly)
            .buttonStyle(.borderless)
        }
        RemoveButton { sort.removeAll { $0.id == c.id } }
      }
    }
    Button("Sort", systemImage: "plus") { sort.append(RuleSort()) }
      .buttonStyle(.bordered)
      .controlSize(.small)
    if sort.isEmpty {
      Text("No order: tracks come as the server finds them.")
        .font(.caption)
        .foregroundStyle(.secondary)
    }
  }
}

struct LimitsEditor: View {
  @Binding var limits: [RuleLimit]

  var body: some View {
    ForEach($limits) { $l in
      HStack {
        Text("At most")
        TextField("0", value: $l.value, format: .number)
          .textFieldStyle(.roundedBorder)
          .frame(width: 72)
          #if os(iOS)
          .keyboardType(.decimalPad)
          #endif
        Picker("Unit", selection: $l.unit) {
          ForEach(RuleLimit.Unit.allCases) { Text($0.label).tag($0) }
        }
        .labelsHidden()
        .pickerStyle(.menu)
        .fixedSize()
        Spacer()
        RemoveButton { limits.removeAll { $0.id == l.id } }
      }
    }
    Button("Limit", systemImage: "plus") {
      let unit = RuleLimit.Unit.allCases.first { u in !limits.contains { $0.unit == u } } ?? .tracks
      limits.append(RuleLimit(unit: unit))
    }
    .buttonStyle(.bordered)
    .controlSize(.small)
    Text(limits.isEmpty
         ? "No limit: every matching track."
         : "A limit cuts the sorted list; a track with no audio or no known length counts as nothing toward MB and minutes.")
      .font(.caption)
      .foregroundStyle(.secondary)
  }
}

// MARK: - Preview

/// The tracks the draft matches now: a summary line, then the first
/// page of them.
struct PreviewList: View {
  let model: SmartPlaylistEditorModel

  var body: some View {
    Text(summary)
      .font(.caption)
      .foregroundStyle(model.previewError != nil ? AnyShapeStyle(.red) : AnyShapeStyle(.secondary))
      .accessibilityIdentifier("smartPlaylist.previewSummary")
    if let preview = model.preview {
      ForEach(Array(preview.trackIds.enumerated()), id: \.element) { i, id in
        if let track = model.library.tracks[id] {
          HStack(alignment: .firstTextBaseline) {
            Text("\(i + 1)").monospacedDigit().foregroundStyle(.secondary).frame(minWidth: 24, alignment: .trailing)
            VStack(alignment: .leading) {
              Text(track.title)
              let album = track.albumId.flatMap { model.library.albums[$0]?.title }
              let byline = [track.artist, album].compactMap(\.self).joined(separator: " — ")
              if !byline.isEmpty { Text(byline).font(.caption).foregroundStyle(.secondary) }
            }
          }
        }
      }
      if preview.total > preview.trackIds.count {
        Text("and \((preview.total - preview.trackIds.count).formatted()) more")
          .font(.caption)
          .foregroundStyle(.secondary)
      }
    }
  }

  private var summary: String {
    if let error = model.previewError { return error }
    if model.unreadable != nil { return "" }
    if let problem = model.problem { return problem }
    guard let preview = model.preview else { return "Running the rule…" }
    var s = "\(preview.total.formatted()) tracks"
    if model.running { s += " · running…" }
    return s
  }
}

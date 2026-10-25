import AVFoundation
import Foundation
import MediaPlayer
import Observation
import os

/// Plays a queue of tracks.  Each track's audio comes through the
/// library's blob cache: the whole file is fetched before it plays, and
/// the next track is fetched while this one plays.  A track that plays
/// to its end counts as played.
///
/// The system's now-playing display and remote commands (the lock
/// screen, Control Center, a Mac's media keys) follow the player.
@MainActor @Observable
public final class Player {
  public private(set) var queue: [Track] = []
  public private(set) var index: Int?
  public private(set) var isPlaying = false
  /// True while the current track's audio is being fetched.
  public private(set) var isLoading = false
  public private(set) var failure: String?

  public var current: Track? { index.map { queue[$0] } }
  public var hasNext: Bool { index.map { $0 + 1 < queue.count } ?? false }
  public var hasPrevious: Bool { (index ?? 0) > 0 }

  @ObservationIgnored let library: Library
  @ObservationIgnored let player = AVPlayer()
  @ObservationIgnored private var loadTask: Task<Void, Never>?
  @ObservationIgnored private var endObserver: (any NSObjectProtocol)?
  @ObservationIgnored private var statusObserver: NSKeyValueObservation?
  @ObservationIgnored private let log = Logger(subsystem: "systems.semiotic.juke", category: "player")

  public init(library: Library) {
    self.library = library
    #if os(iOS)
    try? AVAudioSession.sharedInstance().setCategory(.playback)
    #endif
    setUpRemoteCommands()
  }

  /// Plays `tracks` from the one at `position`.  Tracks with no audio are
  /// dropped from the queue.  A position, not a track, says where to
  /// start, because a playlist may hold a track more than once.
  public func play(_ tracks: [Track], at position: Int) {
    guard tracks.indices.contains(position), tracks[position].isPlayable else { return }
    queue = tracks.filter(\.isPlayable)
    load(tracks[..<position].filter(\.isPlayable).count)
  }

  public func togglePause() {
    guard index != nil else { return }
    if isPlaying { player.pause() } else { player.play() }
    isPlaying.toggle()
    updateNowPlaying()
  }

  public func next() {
    if let i = index, i + 1 < queue.count { load(i + 1) }
  }

  /// Goes back a track, or to the start of this one if it has played a
  /// few seconds, as players usually do.
  public func previous() {
    guard let i = index else { return }
    if player.currentTime().seconds > 3 || i == 0 {
      player.seek(to: .zero)
    } else {
      load(i - 1)
    }
  }

  private func load(_ i: Int) {
    loadTask?.cancel()
    index = i
    failure = nil
    isLoading = true
    player.pause()
    updateNowPlaying()
    let track = queue[i]
    loadTask = Task { [weak self] in
      guard let self else { return }
      do {
        let url = try await library.audioFile(for: track)
        guard !Task.isCancelled else { return }
        start(url: url, track: track)
        if i + 1 < queue.count {
          _ = try? await library.audioFile(for: queue[i + 1])
        }
      } catch {
        guard !Task.isCancelled else { return }
        isLoading = false
        isPlaying = false
        failure = "Couldn't load “\(track.title)”: \(error.localizedDescription)"
      }
    }
  }

  private func start(url: URL, track: Track) {
    let item = AVPlayerItem(url: url)

    if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
    endObserver = NotificationCenter.default.addObserver(
      forName: AVPlayerItem.didPlayToEndTimeNotification, object: item, queue: .main
    ) { [weak self] _ in
      MainActor.assumeIsolated { self?.finished(track) }
    }

    // AVPlayer fails quietly: a file it can't open just never starts.
    // Watching the item is the only way to hear about it.  -- claude, 2026-10-25
    statusObserver = item.observe(\.status) { [weak self] item, _ in
      guard item.status == .failed else { return }
      let error = item.error
      Task { @MainActor in self?.failed(track, url: url, error: error) }
    }

    player.replaceCurrentItem(with: item)
    player.play()
    isLoading = false
    isPlaying = true
    updateNowPlaying()
  }

  private func failed(_ track: Track, url: URL, error: (any Error)?) {
    guard current?.id == track.id else { return }
    let detail = error.map(Self.describe) ?? "unknown error"
    log.error("can't play \(url.path, privacy: .public): \(detail, privacy: .public)")
    isPlaying = false
    failure = "Couldn't play “\(track.title)”: \(detail)"
    updateNowPlaying()
  }

  /// An error's description with its underlying errors' domains and codes,
  /// which is where AVFoundation puts the useful part.
  static func describe(_ error: any Error) -> String {
    var parts = [error.localizedDescription]
    var next = (error as NSError).userInfo[NSUnderlyingErrorKey] as? NSError
    while let e = next {
      parts.append("\(e.domain) \(e.code)")
      next = e.userInfo[NSUnderlyingErrorKey] as? NSError
    }
    return parts.joined(separator: "; ")
  }

  private func finished(_ track: Track) {
    Task { try? await library.recordPlay(of: track) }
    if hasNext {
      next()
    } else {
      isPlaying = false
      updateNowPlaying()
    }
  }

  // MARK: - System integration

  private func setUpRemoteCommands() {
    let center = MPRemoteCommandCenter.shared()
    center.playCommand.addTarget { [weak self] _ in
      MainActor.assumeIsolated { if self?.isPlaying == false { self?.togglePause() } }
      return .success
    }
    center.pauseCommand.addTarget { [weak self] _ in
      MainActor.assumeIsolated { if self?.isPlaying == true { self?.togglePause() } }
      return .success
    }
    center.togglePlayPauseCommand.addTarget { [weak self] _ in
      MainActor.assumeIsolated { self?.togglePause() }
      return .success
    }
    center.nextTrackCommand.addTarget { [weak self] _ in
      MainActor.assumeIsolated { self?.next() }
      return .success
    }
    center.previousTrackCommand.addTarget { [weak self] _ in
      MainActor.assumeIsolated { self?.previous() }
      return .success
    }
  }

  private func updateNowPlaying() {
    let center = MPNowPlayingInfoCenter.default()
    guard let track = current else {
      center.nowPlayingInfo = nil
      center.playbackState = .stopped
      return
    }
    var info: [String: Any] = [MPMediaItemPropertyTitle: track.title]
    if let artist = track.artist { info[MPMediaItemPropertyArtist] = artist }
    if let album = track.albumId.flatMap({ library.albums[$0] }) { info[MPMediaItemPropertyAlbumTitle] = album.title }
    info[MPNowPlayingInfoPropertyElapsedPlaybackTime] = player.currentTime().seconds
    info[MPNowPlayingInfoPropertyPlaybackRate] = isPlaying ? 1.0 : 0.0
    center.nowPlayingInfo = info
    center.playbackState = isPlaying ? .playing : .paused
  }
}

import AVFoundation
import Foundation
import MediaPlayer
import Observation
import os
#if canImport(UIKit)
import UIKit
#else
import AppKit
#endif

/// Plays a queue of tracks.  Each track's audio comes through the
/// library's blob cache: the whole file is fetched before it plays, and
/// the next track is fetched while this one plays.  A track that plays
/// to its end counts as played.
///
/// The system's now-playing display and remote commands (the lock
/// screen, Control Center, a Mac's media keys) follow the player.
@MainActor @Observable
public final class Player {
  /// What plays, in what order, and the shuffle and repeat settings.
  public private(set) var playQueue = PlayQueue()
  public private(set) var isPlaying = false
  /// True while the current track's audio is being fetched.
  public private(set) var isLoading = false
  public private(set) var failure: String?

  public var queue: [Track] { playQueue.entries.map(\.track) }
  public var index: Int? { playQueue.index }
  public var current: Track? { playQueue.current }
  public var currentAlbum: Album? { current?.albumId.flatMap { library.albums[$0] } }
  public var hasNext: Bool { playQueue.following(skipping: true) != nil }
  public var shuffle: PlayQueue.Shuffle { playQueue.shuffle }
  public var repeatMode: PlayQueue.Repeat { playQueue.repeatMode }

  @ObservationIgnored public let library: Library
  @ObservationIgnored let player = AVPlayer()
  @ObservationIgnored private var loadTask: Task<Void, Never>?
  @ObservationIgnored private var endObserver: (any NSObjectProtocol)?
  /// The current track's length in seconds, once its audio is open.
  public private(set) var duration: Double?
  /// How far into the current track playback is, in seconds, updated a
  /// few times a second.
  public private(set) var elapsed: Double = 0

  /// The source given to tracks queued with play next or add to queue.
  public static let queuedByHand = "Queued"

  @ObservationIgnored private var statusObserver: NSKeyValueObservation?
  /// The current track's art for the lock screen and Control Center, and
  /// which image it is (see `artKey(for:)`), so a stale one is never shown
  /// for a new track.
  @ObservationIgnored private var artwork: (key: String, image: CGImage)?
  @ObservationIgnored private var artworkTask: Task<Void, Never>?
  @ObservationIgnored private var timeObserver: Any?
  /// Whether an interruption under way stopped playback, which should go
  /// on when it ends.  Pausing or playing in the meantime settles it.
  @ObservationIgnored private var resumeAfterInterruption = false
  /// Seeks asked for but not yet done.  Until they are, the player still
  /// reports the old time, which shouldn't drag `elapsed` back after a
  /// seek has set it.  -- claude, 2026-11-02
  @ObservationIgnored private var seeksInFlight = 0
  @ObservationIgnored private let log = Logger(subsystem: "systems.semiotic.juke", category: "player")

  public init(library: Library) {
    self.library = library
    #if os(iOS)
    try? AVAudioSession.sharedInstance().setCategory(.playback)
    watchAudioSession()
    #endif
    var rng = SystemRandomNumberGenerator()
    playQueue.setShuffle(Self.savedShuffle, using: &rng)
    setUpRemoteCommands()
    timeObserver = player.addPeriodicTimeObserver(forInterval: CMTime(seconds: 0.25, preferredTimescale: 600), queue: .main) {
      [weak self] time in
      MainActor.assumeIsolated {
        guard let self, self.seeksInFlight == 0 else { return }
        self.elapsed = time.seconds.isFinite ? time.seconds : 0
      }
    }
  }

  /// Moves playback in the current track to `seconds` from its start.
  public func seek(to seconds: Double) {
    guard current != nil else { return }
    let target = max(0, min(seconds, duration ?? seconds))
    elapsed = target
    seeksInFlight += 1
    player.seek(to: CMTime(seconds: target, preferredTimescale: 600)) { [weak self] _ in
      Task { @MainActor in
        guard let self else { return }
        self.seeksInFlight -= 1
        self.updateNowPlaying()
      }
    }
  }

  /// Plays `tracks` from the one at `position`.  Tracks with no audio are
  /// dropped from the queue.  A position, not a track, says where to
  /// start, because a playlist may hold a track more than once.
  public func play(_ tracks: [Track], at position: Int, from source: String? = nil) {
    guard tracks.indices.contains(position), tracks[position].isPlayable else { return }
    var rng = SystemRandomNumberGenerator()
    playQueue.start(tracks, at: position, from: source, discs: discs, using: &rng)
    if let i = playQueue.index { load(i) }
  }

  /// Turns shuffle to tracks and plays `tracks` from one at random.
  public func playShuffled(_ tracks: [Track], from source: String? = nil) {
    let playable = tracks.indices.filter { tracks[$0].isPlayable }
    guard let position = playable.randomElement() else { return }
    setShuffle(.tracks)
    play(tracks, at: position, from: source)
  }

  /// Turns shuffle to albums and plays `tracks` an album at a time: the
  /// albums in random order, each whole and in order, from the start of
  /// one of them.
  public func playShuffledByAlbum(_ tracks: [Track], from source: String? = nil) {
    var rng = SystemRandomNumberGenerator()
    let ordered = PlayQueue.albumShuffled(tracks.filter(\.isPlayable), discs: discs, using: &rng)
    guard !ordered.isEmpty else { return }
    setShuffle(.albums)
    play(ordered, at: 0, from: source)
  }

  /// An album's track ids in order, for shuffling by album.
  private var discs: PlayQueue.Discs {
    { [library] in library.albums[$0]?.discs.flatMap(\.trackIds) }
  }

  /// The shuffle as last set, kept across launches.
  private static var savedShuffle: PlayQueue.Shuffle {
    UserDefaults.standard.string(forKey: "shuffle").flatMap(PlayQueue.Shuffle.init) ?? .off
  }

  /// Plays tracks after the current one, or now if nothing is playing.
  public func playNext(_ tracks: [Track]) {
    if playQueue.playNext(tracks, from: Self.queuedByHand), let i = playQueue.index { load(i) }
  }

  /// Puts tracks at the end of the queue, or plays them now if nothing
  /// is playing.
  public func addToQueue(_ tracks: [Track]) {
    if playQueue.add(tracks, from: Self.queuedByHand), let i = playQueue.index { load(i) }
  }

  public func setShuffle(_ mode: PlayQueue.Shuffle) {
    var rng = SystemRandomNumberGenerator()
    playQueue.setShuffle(mode, discs: discs, using: &rng)
    UserDefaults.standard.set(mode.rawValue, forKey: "shuffle")
    updateRemoteModes()
  }

  public func setRepeat(_ mode: PlayQueue.Repeat) {
    playQueue.repeatMode = mode
    updateRemoteModes()
  }

  /// Steps repeat from off, to all, to one, and back to off.
  public func cycleRepeat() {
    let all = PlayQueue.Repeat.allCases
    setRepeat(all[(all.firstIndex(of: repeatMode)! + 1) % all.count])
  }

  /// Plays the queue's entry at `i`, as when it's picked from a list of
  /// what's coming up.
  public func jump(to i: Int) {
    guard playQueue.entries.indices.contains(i) else { return }
    load(i)
  }

  public func togglePause() {
    guard index != nil else { return }
    resumeAfterInterruption = false
    if isPlaying { player.pause() } else { player.play() }
    isPlaying.toggle()
    updateNowPlaying()
  }

  public func next() {
    if let i = playQueue.following(skipping: true) { load(i) }
  }

  /// Goes back a track, or to the start of this one if it has played a
  /// few seconds, as players usually do.
  public func previous() {
    guard index != nil else { return }
    if player.currentTime().seconds > 3 {
      player.seek(to: .zero)
    } else if let i = playQueue.preceding() {
      load(i)
    } else {
      player.seek(to: .zero)
    }
  }

  private func load(_ i: Int) {
    loadTask?.cancel()
    playQueue.move(to: i)
    duration = nil
    elapsed = 0
    failure = nil
    isLoading = true
    player.pause()
    let track = playQueue.entries[i].track
    fetchArtwork(for: track)
    updateNowPlaying()
    let following = playQueue.following(skipping: true).map { playQueue.entries[$0].track }
    loadTask = Task { [weak self] in
      guard let self else { return }
      do {
        let url = try await library.audioFile(for: track)
        guard !Task.isCancelled else { return }
        start(url: url, track: track)
        if let following {
          _ = try? await library.audioFile(for: following)
        }
      } catch {
        guard !Task.isCancelled else { return }
        isLoading = false
        isPlaying = false
        failure = "Couldn't load “\(track.title)”: \(error.localizedDescription)"
      }
    }
  }

  /// Which image the now-playing info should show for a track: its art,
  /// or with none, its album's placeholder cover.  Nil for a track with
  /// neither, which shows no image.
  private func artKey(for track: Track) -> String? {
    if let art = library.artwork(for: track) { return art.blobId }
    return track.albumId.map { "placeholder:\($0)" }
  }

  /// Fetches a track's art for the now-playing info, which is set again
  /// once it comes.  A placeholder is drawn at once.
  private func fetchArtwork(for track: Track) {
    artworkTask?.cancel()
    guard let key = artKey(for: track) else {
      artwork = nil
      return
    }
    if artwork?.key == key { return }
    artwork = nil
    guard let art = library.artwork(for: track) else {
      if let albumId = track.albumId, let image = PlaceholderSwatch.for(albumId).image(pixels: 600) {
        artwork = (key, image)
      }
      return
    }
    artworkTask = Task { [weak self] in
      guard let self, let image = await library.images.image(art, maxPixels: 1024), !Task.isCancelled else { return }
      artwork = (key, image)
      updateNowPlaying()
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
      switch item.status {
      case .readyToPlay:
        let seconds = item.duration.seconds
        Task { @MainActor in self?.ready(track, duration: seconds) }
      case .failed:
        let error = item.error
        Task { @MainActor in self?.failed(track, url: url, error: error) }
      default:
        break
      }
    }

    player.replaceCurrentItem(with: item)
    player.play()
    isLoading = false
    isPlaying = true
    updateNowPlaying()
  }

  private func ready(_ track: Track, duration: Double) {
    guard current?.id == track.id, duration.isFinite else { return }
    self.duration = duration
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
    if let i = playQueue.following(skipping: false) {
      if i == index {
        player.seek(to: .zero)
        player.play()
      } else {
        load(i)
      }
    } else {
      isPlaying = false
      updateNowPlaying()
    }
  }

  // MARK: - System integration

  #if os(iOS)
  /// Pauses when the headphones are pulled out or another app's audio
  /// interrupts.  The system pauses the AVPlayer itself then, but nothing
  /// else would tell `isPlaying`, which would go on saying it plays, so
  /// that the Pause button took one tap to do nothing and another to
  /// play.  -- claude, 2026-11-19
  private func watchAudioSession() {
    let center = NotificationCenter.default
    center.addObserver(forName: AVAudioSession.routeChangeNotification, object: nil, queue: .main) { [weak self] note in
      let reason = (note.userInfo?[AVAudioSessionRouteChangeReasonKey] as? UInt)
        .flatMap(AVAudioSession.RouteChangeReason.init)
      guard reason == .oldDeviceUnavailable else { return }
      MainActor.assumeIsolated {
        self?.resumeAfterInterruption = false
        self?.pausedBySystem()
      }
    }
    center.addObserver(forName: AVAudioSession.interruptionNotification, object: nil, queue: .main) { [weak self] note in
      let info = note.userInfo
      let type = (info?[AVAudioSessionInterruptionTypeKey] as? UInt).flatMap(AVAudioSession.InterruptionType.init)
      let options = AVAudioSession.InterruptionOptions(rawValue: info?[AVAudioSessionInterruptionOptionKey] as? UInt ?? 0)
      MainActor.assumeIsolated {
        guard let self else { return }
        switch type {
        case .began:
          self.resumeAfterInterruption = self.isPlaying
          self.pausedBySystem()
        case .ended:
          if self.resumeAfterInterruption && options.contains(.shouldResume) { self.resumedBySystem() }
          self.resumeAfterInterruption = false
        default:
          break
        }
      }
    }
  }

  private func pausedBySystem() {
    guard isPlaying else { return }
    player.pause()
    isPlaying = false
    updateNowPlaying()
  }

  /// Plays again after an interruption, as the system says to when the
  /// interruption was, say, a call, and not another app starting to play.
  private func resumedBySystem() {
    guard !isPlaying, current != nil, player.currentItem != nil else { return }
    player.play()
    isPlaying = true
    updateNowPlaying()
  }
  #endif

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
    center.changePlaybackPositionCommand.addTarget { [weak self] event in
      guard let event = event as? MPChangePlaybackPositionCommandEvent else { return .commandFailed }
      let seconds = event.positionTime
      MainActor.assumeIsolated { self?.seek(to: seconds) }
      return .success
    }
    center.changeShuffleModeCommand.addTarget { [weak self] event in
      guard let event = event as? MPChangeShuffleModeCommandEvent else { return .commandFailed }
      let mode: PlayQueue.Shuffle = switch event.shuffleType {
      case .items: .tracks
      case .collections: .albums
      default: .off
      }
      MainActor.assumeIsolated { self?.setShuffle(mode) }
      return .success
    }
    center.changeRepeatModeCommand.addTarget { [weak self] event in
      guard let event = event as? MPChangeRepeatModeCommandEvent else { return .commandFailed }
      let mode: PlayQueue.Repeat = switch event.repeatType {
      case .one: .one
      case .all: .all
      default: .off
      }
      MainActor.assumeIsolated { self?.setRepeat(mode) }
      return .success
    }
    updateRemoteModes()
  }

  /// Tells the system's shuffle and repeat controls how they're set.
  private func updateRemoteModes() {
    let center = MPRemoteCommandCenter.shared()
    center.changeShuffleModeCommand.currentShuffleType = switch shuffle {
    case .off: .off
    case .tracks: .items
    case .albums: .collections
    }
    center.changeRepeatModeCommand.currentRepeatType = switch repeatMode {
    case .off: .off
    case .all: .all
    case .one: .one
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
    if let duration { info[MPMediaItemPropertyPlaybackDuration] = duration }
    if let artwork, artwork.key == artKey(for: track) {
      let image = artwork.image
      let size = CGSize(width: image.width, height: image.height)
      // The system asks for the image on its own queue, so the closure
      // must not be main-actor isolated, which a closure made here is
      // unless it's @Sendable.  -- claude, 2026-11-02
      info[MPMediaItemPropertyArtwork] = MPMediaItemArtwork(boundsSize: size) { @Sendable _ in
        #if canImport(UIKit)
        UIImage(cgImage: image)
        #else
        NSImage(cgImage: image, size: size)
        #endif
      }
    }
    info[MPNowPlayingInfoPropertyElapsedPlaybackTime] = player.currentTime().seconds
    info[MPNowPlayingInfoPropertyPlaybackRate] = isPlaying ? 1.0 : 0.0
    center.nowPlayingInfo = info
    center.playbackState = isPlaying ? .playing : .paused
  }
}

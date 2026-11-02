package audio

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// Analysis is everything the measuring can find out about an MPEG
// audio file, for when a length looks wrong.  Duration chooses one of
// its figures: a trusted header's, else the estimate for a file that
// is constant-rate throughout, else the walk's.
type Analysis struct {
	Start, End int64 // where the audio begins and ends, tags excluded

	// The first frame.
	Version    string // "1", "2" or "2.5"
	Layer      int
	Bitrate    int // bits per second
	SampleRate int
	Mono       bool

	// A Xing, Info or VBRI header, if the encoder left one, and whether
	// its byte count matches the file, which is what makes it believable.
	Header         string // "Xing", "Info", "VBRI" or ""
	HeaderFrames   int
	HeaderBytes    int64
	HeaderTrusted  bool
	HeaderDuration time.Duration

	// Whether frames sampled across the whole file share the first
	// frame's bitrate, and the length that implies if so.
	ConstantRate bool
	Estimate     time.Duration

	// The walk, done when the file is analysed or when nothing else
	// answers: every frame counted, and the bitrates seen.
	Walked                 bool
	WalkFrames             int
	WalkDuration           time.Duration
	MinBitrate, MaxBitrate int
	Resyncs                int   // runs of junk skipped between frames
	JunkBytes              int64 // bytes skipped as junk, between frames or after the last

	Chosen time.Duration
}

// Analyze measures an MPEG audio file every way, walking its frames
// whether or not the answer needs it.
func Analyze(r io.ReadSeeker) (*Analysis, error) {
	return analyzeMPEG(r, true)
}

// String lays the analysis out one fact per line.
func (a *Analysis) String() string {
	s := fmt.Sprintf("audio bytes %d to %d (%d)\n", a.Start, a.End, a.End-a.Start)
	s += fmt.Sprintf("first frame MPEG-%s layer %d, %d kbps, %d Hz", a.Version, a.Layer, a.Bitrate/1000, a.SampleRate)
	if a.Mono {
		s += ", mono"
	}
	s += "\n"
	switch {
	case a.Header == "":
		s += "no Xing, Info or VBRI header\n"
	case a.HeaderTrusted:
		s += fmt.Sprintf("%s header: %d frames, %d bytes, %v\n", a.Header, a.HeaderFrames, a.HeaderBytes, a.HeaderDuration.Round(time.Millisecond))
	default:
		s += fmt.Sprintf("%s header: %d frames, %d bytes, %v; not trusted, the byte count is wrong\n", a.Header, a.HeaderFrames, a.HeaderBytes, a.HeaderDuration.Round(time.Millisecond))
	}
	rate := "variable"
	if a.ConstantRate {
		rate = "constant"
	}
	s += fmt.Sprintf("sampled frames %s rate; estimate at %d kbps: %v\n", rate, a.Bitrate/1000, a.Estimate.Round(time.Millisecond))
	if a.Walked {
		s += fmt.Sprintf("walk: %d frames, %v, %d to %d kbps, %d resyncs, %d bytes of junk\n",
			a.WalkFrames, a.WalkDuration.Round(time.Millisecond), a.MinBitrate/1000, a.MaxBitrate/1000, a.Resyncs, a.JunkBytes)
	}
	s += fmt.Sprintf("chosen: %v\n", a.Chosen.Round(time.Millisecond))
	return s
}

// analyzeMPEG finds the first frame after any ID3v2 tags, and takes the
// length from a Xing, Info or VBRI header if the encoder left one and
// its byte count matches the file; a header that survived a re-cut of
// the audio declares the old length, and two such were found in the
// library.  Otherwise it samples frames at sixteen points through the
// file; if every one has the first frame's bitrate the file is taken as
// constant-rate, and its audio bytes divided by that rate.  Thirty-two
// frames from the start was not enough: files that open at one rate and
// vary later fooled it.  A variable-rate file with no trusted header is
// the slow case, and the only one that reads the whole file: every
// frame is counted.  With walk set the frames are counted regardless,
// for comparison. -- claude, 2026-11-01
func analyzeMPEG(r io.ReadSeeker, walk bool) (*Analysis, error) {
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	start, err := skipID3v2(r)
	if err != nil {
		return nil, err
	}
	end, err := trimID3v1(r, size)
	if err != nil {
		return nil, err
	}
	first, at, err := firstFrame(r, start, end)
	if err != nil {
		return nil, err
	}

	a := &Analysis{
		Start: at, End: end,
		Version: first.version, Layer: first.layer, Bitrate: first.bitrate, SampleRate: first.rate, Mono: first.mono,
		Estimate: time.Duration(float64(end-at) * 8 / float64(first.bitrate) * float64(time.Second)),
	}

	if _, err := r.Seek(at, io.SeekStart); err != nil {
		return nil, err
	}
	head := make([]byte, min(first.length, int(end-at)))
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, err
	}
	a.Header, a.HeaderFrames, a.HeaderBytes = headerInfo(first, head)
	if a.Header != "" {
		a.HeaderDuration = time.Duration(a.HeaderFrames) * first.duration()
		a.HeaderTrusted = a.HeaderBytes > 0 && (within(a.HeaderBytes, end-at, 0.02) || within(a.HeaderBytes, size, 0.02))
	}

	if !a.HeaderTrusted {
		a.ConstantRate, err = sampledConstant(r, first, at, end)
		if err != nil {
			return nil, err
		}
	}

	switch {
	case a.HeaderTrusted:
		a.Chosen = a.HeaderDuration
	case a.ConstantRate:
		a.Chosen = a.Estimate
	default:
		walk = true
	}
	if !walk {
		return a, nil
	}

	a.Walked = true
	if err := walkFrames(r, at, end, a); err != nil {
		return nil, err
	}
	if !a.HeaderTrusted && !a.ConstantRate {
		a.Chosen = a.WalkDuration
	}
	return a, nil
}

// within reports whether a is within the fraction of b.
func within(a, b int64, fraction float64) bool {
	diff := float64(a - b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= float64(b)*fraction
}

// headerInfo returns the kind, frame count and byte count of a Xing,
// Info or VBRI header in the first frame, or "" and zeros for none.  A
// Xing header's byte count is optional; a VBRI header always has one.
func headerInfo(f *frame, head []byte) (string, int, int64) {
	if n := f.xingAt; n+8 <= len(head) {
		tag := string(head[n : n+4])
		if tag == "Xing" || tag == "Info" {
			flags := binary.BigEndian.Uint32(head[n+4:])
			var frames int
			var bytes int64
			p := n + 8
			if flags&1 != 0 && p+4 <= len(head) {
				frames = int(binary.BigEndian.Uint32(head[p:]))
				p += 4
			}
			if flags&2 != 0 && p+4 <= len(head) {
				bytes = int64(binary.BigEndian.Uint32(head[p:]))
			}
			if frames > 0 {
				return tag, frames, bytes
			}
		}
	}
	if n := f.vbriAt; n+18 <= len(head) && string(head[n:n+4]) == "VBRI" {
		return "VBRI", int(binary.BigEndian.Uint32(head[n+14:])), int64(binary.BigEndian.Uint32(head[n+10:]))
	}
	return "", 0, 0
}

// sampledConstant looks for a frame at each of two dozen points through
// the audio and reports whether every one, and the frame after it, has
// the first frame's bitrate and sample rate.  The points are spaced by
// the golden ratio rather than evenly, so a file whose frames alternate
// with a period can't line every sample up on the same kind; and a
// point where no frame can be found counts against it, since a file
// that stops being audio partway isn't constant-rate either.
func sampledConstant(r io.ReadSeeker, first *frame, at, end int64) (bool, error) {
	const points = 24
	span := end - at
	for i := 1; i <= points; i++ {
		frac := math.Mod(float64(i)*0.6180339887, 1)
		pos := at + int64(float64(span)*frac)
		if pos+int64(first.length) >= end {
			continue // inside the last frame, where nothing follows
		}
		f, where, err := firstFrameWithin(r, pos, end, 16*1024)
		if err != nil {
			if errors.Is(err, ErrUnknown) {
				return false, nil
			}
			return false, err
		}
		next, err := frameAt(r, where+int64(f.length), end)
		if err != nil {
			return false, err
		}
		for _, g := range []*frame{f, next} {
			if g != nil && (g.bitrate != first.bitrate || g.rate != first.rate) {
				return false, nil
			}
		}
	}
	return true, nil
}

// walkFrames counts every frame from at to end, reading sequentially
// through a buffer so the walk costs one pass over the file and little
// else.  Junk between frames is skipped a byte at a time until a header
// that another header follows turns up.
func walkFrames(r io.ReadSeeker, at, end int64, a *Analysis) error {
	if _, err := r.Seek(at, io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(io.LimitReader(r, end-at), 1<<20)
	a.MinBitrate = 0
	inJunk := false
	// A header is believed if the header after it parses too (or it ends
	// the audio), or if the frame before it was believed that way.  So the
	// last real frame before a run of junk still counts, and a stray sync
	// pattern inside the junk doesn't start a chain. -- claude, 2026-11-01
	prevValidated := false
	for {
		head, err := br.Peek(4)
		if err != nil {
			break
		}
		f := parseFrame(head)
		if f == nil {
			if !inJunk {
				a.Resyncs++
				inJunk = true
			}
			prevValidated = false
			br.Discard(1)
			a.JunkBytes++
			continue
		}
		nextParses := false
		if next, err := br.Peek(f.length + 4); err == nil {
			nextParses = parseFrame(next[f.length:]) != nil
		} else if _, err := br.Peek(f.length); err == nil {
			_, err := br.Peek(f.length + 1)
			nextParses = err != nil // the frame ends exactly where the audio does
		}
		if !nextParses && !prevValidated {
			if !inJunk {
				a.Resyncs++
				inJunk = true
			}
			br.Discard(1)
			a.JunkBytes++
			continue
		}
		inJunk = false
		prevValidated = nextParses
		a.WalkFrames++
		a.WalkDuration += f.duration()
		if a.MinBitrate == 0 || f.bitrate < a.MinBitrate {
			a.MinBitrate = f.bitrate
		}
		a.MaxBitrate = max(a.MaxBitrate, f.bitrate)
		if _, err := br.Discard(f.length); err != nil {
			break
		}
	}
	// The last few bytes, too short for a header, and junk at the very
	// end isn't a resync.
	a.JunkBytes += int64(br.Buffered())
	if inJunk {
		a.Resyncs--
	}
	return nil
}

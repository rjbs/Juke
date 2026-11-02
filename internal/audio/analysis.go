package audio

import (
	"fmt"
	"io"
	"time"
)

// Analysis is everything the measuring can find out about an MPEG
// audio file, for when a length looks wrong.  Duration chooses one of
// its figures: the header's, else the estimate for a constant-rate
// file, else the walk's.
type Analysis struct {
	Start, End int64 // where the audio begins and ends, tags excluded

	// The first frame.
	Version    string // "1", "2" or "2.5"
	Layer      int
	Bitrate    int // bits per second
	SampleRate int
	Mono       bool

	Header         string // "Xing", "Info", "VBRI" or ""
	HeaderFrames   int
	HeaderDuration time.Duration

	ConstantRate bool          // the first frames share a bitrate
	Estimate     time.Duration // audio bytes over the first frame's bitrate

	// The walk, done when the file is analysed or when nothing else
	// answers: every frame counted, and the bitrates seen.
	Walked                 bool
	WalkFrames             int
	WalkDuration           time.Duration
	MinBitrate, MaxBitrate int
	Resyncs                int // junk skipped between frames

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
	if a.Header != "" {
		s += fmt.Sprintf("%s header: %d frames, %v\n", a.Header, a.HeaderFrames, a.HeaderDuration.Round(time.Millisecond))
	} else {
		s += "no Xing, Info or VBRI header\n"
	}
	rate := "variable"
	if a.ConstantRate {
		rate = "constant"
	}
	s += fmt.Sprintf("first frames %s rate; estimate at %d kbps: %v\n", rate, a.Bitrate/1000, a.Estimate.Round(time.Millisecond))
	if a.Walked {
		s += fmt.Sprintf("walk: %d frames, %v, %d to %d kbps, %d resyncs\n",
			a.WalkFrames, a.WalkDuration.Round(time.Millisecond), a.MinBitrate/1000, a.MaxBitrate/1000, a.Resyncs)
	}
	s += fmt.Sprintf("chosen: %v\n", a.Chosen.Round(time.Millisecond))
	return s
}

// analyzeMPEG finds the first frame after any ID3v2 tags, and takes the
// length from a Xing, Info or VBRI header if the encoder left one.
// Otherwise it reads a few frames; if they share a bitrate the file is
// taken as constant-rate, and its audio bytes divided by that rate.  A
// variable-rate file with no header is the slow case, and the only one
// that reads the whole file: every frame is counted.  With walk set the
// frames are counted regardless, for comparison.
// -- claude, 2026-11-01
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
	a.Header, a.HeaderFrames = headerFrameCount(first, head)
	if a.Header != "" {
		a.HeaderDuration = time.Duration(a.HeaderFrames) * first.duration()
	}

	a.ConstantRate = true
	pos := at
	for i := 0; i < 32 && a.ConstantRate; i++ {
		f, err := frameAt(r, pos, end)
		if err != nil || f == nil {
			break
		}
		if f.bitrate != first.bitrate {
			a.ConstantRate = false
		}
		pos += int64(f.length)
	}

	switch {
	case a.Header != "":
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
	a.MinBitrate = first.bitrate
	for pos := at; pos+4 <= end; {
		f, err := frameAt(r, pos, end)
		if err != nil {
			return nil, err
		}
		if f == nil {
			// Junk between frames: resync at the next header.
			_, next, err := firstFrame(r, pos+1, end)
			if err != nil {
				break
			}
			a.Resyncs++
			pos = next
			continue
		}
		a.WalkFrames++
		a.WalkDuration += f.duration()
		a.MinBitrate = min(a.MinBitrate, f.bitrate)
		a.MaxBitrate = max(a.MaxBitrate, f.bitrate)
		pos += int64(f.length)
	}
	if a.Header == "" && !a.ConstantRate {
		a.Chosen = a.WalkDuration
	}
	return a, nil
}

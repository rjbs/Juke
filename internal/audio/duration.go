// Package audio reads what a track's audio says about itself that its
// tags don't: for now, how long it plays.  It reads headers, not audio,
// so a library of fifty thousand files can be measured in a minute.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrUnknown is returned when a file's length can't be found in it.
var ErrUnknown = errors.New("audio: duration not found")

// Duration returns how long the audio plays, by content type: audio/mp4
// is measured from its movie header, audio/mpeg from its frames.
func Duration(r io.ReadSeeker, contentType string) (time.Duration, error) {
	switch contentType {
	case "audio/mp4":
		return mp4Duration(r)
	case "audio/mpeg":
		return mpegDuration(r)
	}
	return 0, fmt.Errorf("audio: no duration reader for %s", contentType)
}

// ---- MP4 ----------------------------------------------------------------------

// mp4Duration walks the file's top-level boxes to moov, and moov's to
// mvhd, whose time scale and duration give the length.  moov may be at
// either end of the file, so this seeks rather than reads.
func mp4Duration(r io.ReadSeeker) (time.Duration, error) {
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	moov, moovSize, err := findBox(r, 0, size, "moov")
	if err != nil {
		return 0, err
	}
	mvhd, mvhdSize, err := findBox(r, moov, moov+moovSize, "mvhd")
	if err != nil {
		return 0, err
	}
	if _, err := r.Seek(mvhd, io.SeekStart); err != nil {
		return 0, err
	}
	buf := make([]byte, min(mvhdSize, 32))
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	var scale, length uint64
	switch buf[0] {
	case 0:
		if len(buf) < 20 {
			return 0, ErrUnknown
		}
		scale = uint64(binary.BigEndian.Uint32(buf[12:]))
		length = uint64(binary.BigEndian.Uint32(buf[16:]))
	case 1:
		if len(buf) < 32 {
			return 0, ErrUnknown
		}
		scale = uint64(binary.BigEndian.Uint32(buf[20:]))
		length = binary.BigEndian.Uint64(buf[24:])
	default:
		return 0, ErrUnknown
	}
	if scale == 0 {
		return 0, ErrUnknown
	}
	return time.Duration(float64(length) / float64(scale) * float64(time.Second)), nil
}

// findBox scans the boxes between start and end for one of the given
// type, returning the offset and size of its payload.
func findBox(r io.ReadSeeker, start, end int64, want string) (int64, int64, error) {
	var header [16]byte
	for pos := start; pos+8 <= end; {
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return 0, 0, err
		}
		if _, err := io.ReadFull(r, header[:8]); err != nil {
			return 0, 0, err
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		typ := string(header[4:8])
		headerLen := int64(8)
		switch size {
		case 0:
			size = end - pos
		case 1:
			if _, err := io.ReadFull(r, header[8:16]); err != nil {
				return 0, 0, err
			}
			size = int64(binary.BigEndian.Uint64(header[8:16]))
			headerLen = 16
		}
		if size < headerLen {
			return 0, 0, ErrUnknown
		}
		if typ == want {
			return pos + headerLen, size - headerLen, nil
		}
		pos += size
	}
	return 0, 0, ErrUnknown
}

// ---- MPEG audio -----------------------------------------------------------------

// A frame is what one MPEG audio frame header says.
type frame struct {
	length  int // bytes, header included
	samples int // per frame
	rate    int // samples per second
	bitrate int // bits per second
	version string
	layer   int
	mono    bool
	// Where a Xing or VBRI header would sit, from the start of the frame.
	xingAt, vbriAt int
}

var bitrates = map[[2]int][14]int{
	{1, 1}: {32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448},
	{1, 2}: {32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384},
	{1, 3}: {32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320},
	{2, 1}: {32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256},
	{2, 2}: {8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
	{2, 3}: {8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
}

var sampleRates = map[int][3]int{1: {44100, 48000, 32000}, 2: {22050, 24000, 16000}, 25: {11025, 12000, 8000}}

// parseFrame reads a four-byte frame header, or returns nil if it isn't one.
func parseFrame(h []byte) *frame {
	if len(h) < 4 || h[0] != 0xFF || h[1]&0xE0 != 0xE0 {
		return nil
	}
	version := map[byte]int{0: 25, 2: 2, 3: 1}[h[1]>>3&3]
	layer := 4 - int(h[1]>>1&3)
	if version == 0 || layer == 4 {
		return nil
	}
	bitrateIndex := int(h[2] >> 4)
	rateIndex := int(h[2] >> 2 & 3)
	if bitrateIndex == 0 || bitrateIndex == 15 || rateIndex == 3 {
		return nil
	}
	mono := h[3]>>6&3 == 3
	padding := int(h[2] >> 1 & 1)

	family := version
	if family == 25 {
		family = 2
	}
	f := &frame{
		bitrate: bitrates[[2]int{family, layer}][bitrateIndex-1] * 1000,
		rate:    sampleRates[version][rateIndex],
		version: map[int]string{1: "1", 2: "2", 25: "2.5"}[version],
		layer:   layer,
		mono:    mono,
	}
	switch {
	case layer == 1:
		f.samples = 384
		f.length = (12*f.bitrate/f.rate + padding) * 4
	case layer == 2 || family == 1:
		f.samples = 1152
		f.length = 144*f.bitrate/f.rate + padding
	default:
		f.samples = 576
		f.length = 72*f.bitrate/f.rate + padding
	}
	switch {
	case family == 1 && mono:
		f.xingAt = 4 + 17
	case family == 1:
		f.xingAt = 4 + 32
	case mono:
		f.xingAt = 4 + 9
	default:
		f.xingAt = 4 + 17
	}
	f.vbriAt = 4 + 32
	return f
}

func (f *frame) duration() time.Duration {
	return time.Duration(float64(f.samples) / float64(f.rate) * float64(time.Second))
}

// mpegDuration measures an MPEG audio file as analyzeMPEG describes,
// walking its frames only when nothing else answers.
func mpegDuration(r io.ReadSeeker) (time.Duration, error) {
	a, err := analyzeMPEG(r, false)
	if err != nil {
		return 0, err
	}
	return a.Chosen, nil
}

// headerFrameCount returns the kind and frame count of a Xing, Info or
// VBRI header in the first frame, or "" and 0 for none.
func headerFrameCount(f *frame, head []byte) (string, int) {
	if n := f.xingAt; n+12 <= len(head) {
		tag := string(head[n : n+4])
		if tag == "Xing" || tag == "Info" {
			flags := binary.BigEndian.Uint32(head[n+4:])
			if flags&1 != 0 {
				return tag, int(binary.BigEndian.Uint32(head[n+8:]))
			}
		}
	}
	if n := f.vbriAt; n+18 <= len(head) && string(head[n:n+4]) == "VBRI" {
		return "VBRI", int(binary.BigEndian.Uint32(head[n+14:]))
	}
	return "", 0
}

// skipID3v2 returns the offset of the first byte after any ID3v2 tags at
// the start of the file.
func skipID3v2(r io.ReadSeeker) (int64, error) {
	var pos int64
	var h [10]byte
	for {
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return 0, err
		}
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return pos, nil
		}
		if string(h[:3]) != "ID3" {
			return pos, nil
		}
		size := int64(h[6]&0x7F)<<21 | int64(h[7]&0x7F)<<14 | int64(h[8]&0x7F)<<7 | int64(h[9]&0x7F)
		pos += 10 + size
		if h[5]&0x10 != 0 {
			pos += 10 // a footer
		}
	}
}

// trimID3v1 returns where the audio ends: before a trailing ID3v1 tag,
// if there is one.
func trimID3v1(r io.ReadSeeker, size int64) (int64, error) {
	if size < 128 {
		return size, nil
	}
	if _, err := r.Seek(size-128, io.SeekStart); err != nil {
		return 0, err
	}
	var tag [3]byte
	if _, err := io.ReadFull(r, tag[:]); err != nil {
		return 0, err
	}
	if string(tag[:]) == "TAG" {
		return size - 128, nil
	}
	return size, nil
}

// firstFrame scans forward from start for a frame header that another
// valid header follows, so a stray sync byte in junk isn't taken for one.
func firstFrame(r io.ReadSeeker, start, end int64) (*frame, int64, error) {
	const window = 64 * 1024
	if _, err := r.Seek(start, io.SeekStart); err != nil {
		return nil, 0, err
	}
	buf := make([]byte, min(window, int(end-start)))
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, 0, err
	}
	buf = buf[:n]
	for i := 0; i+4 <= len(buf); i++ {
		f := parseFrame(buf[i:])
		if f == nil {
			continue
		}
		at := start + int64(i)
		next, err := frameAt(r, at+int64(f.length), end)
		if err != nil {
			return nil, 0, err
		}
		if next != nil && next.rate == f.rate || at+int64(f.length) >= end {
			return f, at, nil
		}
	}
	return nil, 0, ErrUnknown
}

// frameAt parses the frame header at pos, or returns nil if there isn't
// one there or pos is past the end.
func frameAt(r io.ReadSeeker, pos, end int64) (*frame, error) {
	if pos+4 > end {
		return nil, nil
	}
	if _, err := r.Seek(pos, io.SeekStart); err != nil {
		return nil, err
	}
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, nil
	}
	return parseFrame(h[:]), nil
}

package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"
)

// mp3Frame makes one MPEG-1 Layer III frame at 44.1 kHz, stereo, at the
// bitrate index given: 5 is 64 kbps, 9 is 128, 14 is 320.
func mp3Frame(bitrateIndex int) []byte {
	f := parseFrame([]byte{0xFF, 0xFB, byte(bitrateIndex << 4), 0})
	b := make([]byte, f.length)
	copy(b, []byte{0xFF, 0xFB, byte(bitrateIndex << 4), 0})
	return b
}

// mp3Frames makes n frames at the given bitrate indexes, cycling.
func mp3Frames(n int, bitrateIndexes ...int) []byte {
	var b bytes.Buffer
	for i := 0; i < n; i++ {
		b.Write(mp3Frame(bitrateIndexes[i%len(bitrateIndexes)]))
	}
	return b.Bytes()
}

// mp3Header makes a first frame carrying a Xing-style header that
// declares the frame count and the byte count.
func mp3Header(tag string, frames, bytes int) []byte {
	b := mp3Frame(9)
	at := 4 + 32
	copy(b[at:], tag)
	binary.BigEndian.PutUint32(b[at+4:], 3)
	binary.BigEndian.PutUint32(b[at+8:], uint32(frames))
	binary.BigEndian.PutUint32(b[at+12:], uint32(bytes))
	return b
}

func id3v2(size int) []byte {
	b := make([]byte, 10+size)
	copy(b, "ID3\x03\x00\x00")
	b[6], b[7], b[8], b[9] = byte(size>>21&0x7F), byte(size>>14&0x7F), byte(size>>7&0x7F), byte(size&0x7F)
	return b
}

func id3v1() []byte {
	b := make([]byte, 128)
	copy(b, "TAG")
	return b
}

func box(typ string, payload ...[]byte) []byte {
	var b bytes.Buffer
	size := 8
	for _, p := range payload {
		size += len(p)
	}
	binary.Write(&b, binary.BigEndian, uint32(size))
	b.WriteString(typ)
	for _, p := range payload {
		b.Write(p)
	}
	return b.Bytes()
}

func mvhd(version byte, scale uint32, length uint64) []byte {
	var b bytes.Buffer
	b.Write([]byte{version, 0, 0, 0})
	if version == 0 {
		binary.Write(&b, binary.BigEndian, [2]uint32{0, 0})
		binary.Write(&b, binary.BigEndian, scale)
		binary.Write(&b, binary.BigEndian, uint32(length))
	} else {
		binary.Write(&b, binary.BigEndian, [2]uint64{0, 0})
		binary.Write(&b, binary.BigEndian, scale)
		binary.Write(&b, binary.BigEndian, length)
	}
	b.Write(make([]byte, 80))
	return b.Bytes()
}

func concat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func checkDuration(t *testing.T, name string, data []byte, contentType string, want time.Duration) {
	t.Helper()
	got, err := Duration(bytes.NewReader(data), contentType)
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return
	}
	if diff := got - want; diff < -time.Millisecond || diff > time.Millisecond {
		t.Errorf("%s: %v, want %v", name, got, want)
	}
}

func checkUnknown(t *testing.T, name string, data []byte, contentType string) {
	t.Helper()
	if _, err := Duration(bytes.NewReader(data), contentType); !errors.Is(err, ErrUnknown) {
		t.Errorf("%s: err = %v, want ErrUnknown", name, err)
	}
}

func TestMPEG(t *testing.T) {
	frame := 1152 * time.Second / 44100
	ftyp := box("ftyp", []byte("M4A \x00\x00\x00\x00"))

	cbr := func(frames int) time.Duration { return time.Duration(frames*417*8) * time.Second / 128000 }
	checkDuration(t, "Xing", concat(mp3Header("Xing", 1000, 21*417), mp3Frames(20, 9)), "audio/mpeg", 1000*frame)
	checkDuration(t, "Info", concat(mp3Header("Info", 1000, 21*417), mp3Frames(20, 9)), "audio/mpeg", 1000*frame)
	checkDuration(t, "header whose byte count is wrong", concat(mp3Header("Xing", 1000, 1000*417), mp3Frames(20, 9)), "audio/mpeg", cbr(21))
	checkDuration(t, "header whose byte count is wrong, variable rate", concat(mp3Header("Xing", 1000, 1000*417), mp3Frames(400, 5, 14)), "audio/mpeg", 401*frame)
	checkDuration(t, "constant at the start, variable later", concat(mp3Frames(100, 9), mp3Frames(300, 5, 14)), "audio/mpeg", 400*frame)
	checkDuration(t, "audio then junk", concat(mp3Frames(100, 9), []byte{0, 0}, bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x00, 0x00}, 10000)), "audio/mpeg", 100*frame)
	checkDuration(t, "constant rate, no header", mp3Frames(500, 9), "audio/mpeg", cbr(500))
	checkDuration(t, "variable rate, no header", mp3Frames(400, 5, 14), "audio/mpeg", 400*frame)
	checkDuration(t, "tags either side", concat(id3v2(2000), mp3Frames(500, 9), id3v1()), "audio/mpeg", cbr(500))
	checkDuration(t, "junk before the frames", concat([]byte("junk\xff\xfbjunk"), mp3Header("Xing", 300, 6*417), mp3Frames(5, 9)), "audio/mpeg", 300*frame)
	checkDuration(t, "junk between frames", concat(mp3Frames(100, 5, 14), []byte("\x00\x00junk"), mp3Frames(100, 5, 14)), "audio/mpeg", 200*frame)
	checkUnknown(t, "no frames", []byte("this is not audio at all, not even a little"), "audio/mpeg")

	mdat := box("mdat", make([]byte, 1000))
	checkDuration(t, "moov last, version 0", concat(ftyp, mdat, box("moov", box("mvhd", mvhd(0, 44100, 44100*30)))), "audio/mp4", 30*time.Second)
	checkDuration(t, "moov first, version 1", concat(ftyp, box("moov", box("udta"), box("mvhd", mvhd(1, 1000, 123456))), mdat), "audio/mp4", 123456*time.Millisecond)
	checkUnknown(t, "no moov", concat(ftyp, mdat), "audio/mp4")
	checkUnknown(t, "no mvhd", concat(ftyp, box("moov", box("udta")), mdat), "audio/mp4")

	if _, err := Duration(bytes.NewReader(nil), "audio/ogg"); err == nil {
		t.Error("unknown content type: no error")
	}
}

func TestAnalyze(t *testing.T) {
	// A header whose frame count lies but whose byte count is right is
	// still believed: only a walk could tell, and the walk is what the
	// analysis adds.
	body := mp3Frames(400, 5, 14)
	a, err := Analyze(bytes.NewReader(concat(mp3Header("Xing", 300, 417+len(body)), body)))
	if err != nil {
		t.Fatal(err)
	}
	if a.Header != "Xing" || a.HeaderFrames != 300 || !a.HeaderTrusted || a.ConstantRate || !a.Walked || a.WalkFrames != 401 || a.MinBitrate != 64000 || a.MaxBitrate != 320000 || a.Chosen != a.HeaderDuration {
		t.Errorf("analysis = %+v", a)
	}
	if a.Version != "1" || a.Layer != 3 || a.SampleRate != 44100 || a.Mono {
		t.Errorf("first frame = MPEG-%s layer %d %d Hz mono %v", a.Version, a.Layer, a.SampleRate, a.Mono)
	}
	if s := a.String(); !strings.Contains(s, "Xing header: 300 frames") || !strings.Contains(s, "walk: 401 frames") {
		t.Errorf("String() = %q", s)
	}
}

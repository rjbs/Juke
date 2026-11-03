package artwork

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"io/fs"
	"math/bits"
	"os"
	"sort"
)

// Pictures embedded one file at a time are re-encoded one file at a
// time, so an album often carries several blobs of one picture.  Each
// stored image gets a difference hash: shrunk to nine by eight grey
// cells, each bit says whether a cell is brighter than the one to its
// right.  Two images whose hashes differ in NearBits bits or fewer are
// taken for the same picture when they are on the same album, and the
// rule votes by picture rather than by blob; see doc/artwork.md.
// -- claude, 2026-11-03

// NearBits is how many of the 64 bits two hashes may differ in and
// still be one picture.  Strict on purpose: the images compared are
// always from one album, so a miss costs a review, and a false match
// would hide a real difference.
const NearBits = 4

// dhash is the difference hash of an image.
func dhash(img image.Image) uint64 {
	const cols, rows = 9, 8
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return 0
	}
	// Each cell is the mean grey of the pixels under it, so a resize that
	// doesn't divide evenly still weighs every pixel once.
	var cells [rows][cols]float64
	var counts [rows][cols]int
	for y := 0; y < h; y++ {
		cy := y * rows / h
		for x := 0; x < w; x++ {
			cx := x * cols / w
			g := color.GrayModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.Gray)
			cells[cy][cx] += float64(g.Y)
			counts[cy][cx]++
		}
	}
	var hash uint64
	for y := 0; y < rows; y++ {
		for x := 0; x < cols-1; x++ {
			left := cells[y][x] / float64(max(counts[y][x], 1))
			right := cells[y][x+1] / float64(max(counts[y][x+1], 1))
			hash <<= 1
			if left > right {
				hash |= 1
			}
		}
	}
	return hash
}

// hashOf decodes image data and hashes it.
func hashOf(data []byte) (uint64, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	return dhash(img), nil
}

// Near says whether two hashes are of one picture.
func Near(a, b uint64) bool { return bits.OnesCount64(a^b) <= NearBits }

func near(a, b uint64) bool { return Near(a, b) }

// An imageInfo is what the run knows of a stored image for grouping.
type imageInfo struct {
	hash   uint64
	hashed bool
	pixels int
}

// group sorts image ids into pictures: ids whose hashes are near are one
// picture, transitively.  It returns each id's picture, named by the
// picture's exemplar: the member with the most pixels, and among equals
// the first given, so the caller's order, play order, decides.  An image
// with no hash yet is a picture of its own.
func (r *run) group(ids []string) map[string]string {
	parent := map[string]string{}
	var find func(string) string
	find = func(id string) string {
		for parent[id] != id {
			parent[id] = parent[parent[id]]
			id = parent[id]
		}
		return id
	}
	for _, id := range ids {
		if _, seen := parent[id]; !seen {
			parent[id] = id
		}
	}
	for i, a := range ids {
		for _, b := range ids[:i] {
			ia, ib := r.images[a], r.images[b]
			if ia.hashed && ib.hashed && near(ia.hash, ib.hash) {
				parent[find(a)] = find(b)
			}
		}
	}
	members := map[string][]string{}
	for _, id := range ids {
		root := find(id)
		if len(members[root]) == 0 || members[root][len(members[root])-1] != id {
			members[root] = append(members[root], id)
		}
	}
	out := map[string]string{}
	for _, m := range members {
		exemplar := m[0]
		for _, id := range m[1:] {
			if r.images[id].pixels > r.images[exemplar].pixels {
				exemplar = id
			}
		}
		for _, id := range m {
			out[id] = exemplar
		}
	}
	return out
}

// distinct is ids in first-seen order, each once.
func distinct(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// hashImages hashes every stored image that has no hash yet, reading
// each from its file, and returns how many it hashed.  An image whose
// file is gone or unreadable is left unhashed, a picture of its own.
func (r *run) hashImages() (int, error) {
	var todo []string
	for id, info := range r.images {
		if !info.hashed {
			todo = append(todo, id)
		}
	}
	sort.Strings(todo)
	n := 0
	for _, id := range todo {
		data, err := readFile(Path(r.library, id))
		if err != nil {
			return n, err
		}
		if data == nil {
			continue
		}
		hash, err := hashOf(data)
		if err != nil {
			continue
		}
		if _, err := r.db.ExecContext(r.ctx, `UPDATE artwork SET dhash = ? WHERE id = ?`, int64(hash), id); err != nil {
			return n, err
		}
		info := r.images[id]
		info.hash, info.hashed = hash, true
		r.images[id] = info
		n++
	}
	return n, nil
}

// readFile reads a file, or returns nil if it isn't there.
func readFile(name string) ([]byte, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

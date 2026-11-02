// Package artwork finds album art and decides whose it is.  The pictures
// embedded in the library's files are known from the stubs the import
// stored, so only images not yet seen are read from files; image files
// beside an album's tracks count too.  Each distinct image is stored once
// under the library, named by its hash, and served as a blob.  Then each
// album is given its image, and each track whose own picture differs is
// given that.  The rule is in doc/artwork.md.
package artwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/dhowden/tag"

	"semiotic.systems/juke/internal/store"
)

// Dir is where images live: a directory under the library, each image
// in a file named by its id.
func Dir(library string) string { return filepath.Join(library, "artwork") }

// Path is the file holding the image with this id.
func Path(library, id string) string { return filepath.Join(Dir(library), id) }

// Report counts what a run did.
type Report struct {
	Images     int // distinct images known after the run
	Extracted  int // images read out of audio files this run
	Folder     int // albums with an image file beside their tracks
	Unreadable int // pictures the stubs promised that no file gave
	Albums     int // albums with art
	Ties       int // albums whose tracks disagree with no majority
	Overrides  int // tracks with a picture of their own
	Changed    int // albums and tracks whose art changed this run
}

// A picture is one front cover a track's file carries, as its stub
// records it.
type picture struct {
	sha string
	key string // the tag frame, so a track's pictures keep their order
}

// A track, with the front covers its file carries, in frame order.
type track struct {
	id        string
	albumID   string // "" for a track on no album
	path      string // "" for a track with no audio
	discIndex int
	position  int
	artworkID string // as stored before the run
	pictures  []picture
}

// A run's working state.
type run struct {
	ctx     context.Context
	db      *sql.DB
	library string
	report  Report
	ids     map[string]string // image sha -> artwork id, for every stored image
}

// Run finds every image, stores the ones not stored yet, and sets each
// album's and track's art.  Albums and tracks whose art changes get a
// new modseq, one per type, so clients refetch them once.
func Run(ctx context.Context, db *sql.DB, library string) (*Report, error) {
	r := &run{ctx: ctx, db: db, library: library, ids: map[string]string{}}
	if err := os.MkdirAll(Dir(library), 0o755); err != nil {
		return nil, err
	}
	if err := r.loadImages(); err != nil {
		return nil, err
	}
	tracks, err := r.loadTracks()
	if err != nil {
		return nil, err
	}
	if err := r.extract(tracks); err != nil {
		return nil, err
	}
	folder, err := r.folderImages(tracks)
	if err != nil {
		return nil, err
	}
	if err := r.assign(tracks, folder); err != nil {
		return nil, err
	}
	r.report.Images = len(r.ids)
	return &r.report, nil
}

func (r *run) loadImages() error {
	rows, err := r.db.QueryContext(r.ctx, `SELECT id, sha256 FROM artwork`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, sha string
		if err := rows.Scan(&id, &sha); err != nil {
			return err
		}
		r.ids[sha] = id
	}
	return rows.Err()
}

// isPictureFrame says whether a tag frame carries a picture: ID3v2.3's
// and v2.4's APIC, v2.2's PIC, and MP4's covr, each perhaps with the
// "_n" suffix the tag reader gives a repeated frame.
func isPictureFrame(key string) bool {
	base, _, _ := strings.Cut(key, "_")
	return base == "APIC" || base == "PIC" || base == "covr"
}

// isFrontCover says whether a stub's picture type makes it a candidate
// for album art: a front cover, or a picture with no type, which is what
// iTunes writes and what MP4 files have.  Back covers, booklets and the
// like are not.
func isFrontCover(pictureType string) bool {
	return pictureType == "" || pictureType == "Other" || pictureType == "Cover (front)"
}

// loadTracks reads every track, with the front covers its stored stubs
// record, in album order.
func (r *run) loadTracks() ([]*track, error) {
	rows, err := r.db.QueryContext(r.ctx, `
		SELECT id, album_id, path, disc_index, position, artwork_id, original_tags
		FROM track ORDER BY album_id, disc_index, position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*track
	for rows.Next() {
		var t track
		var albumID, p, artworkID, raw sql.NullString
		var disc, pos sql.NullInt64
		if err := rows.Scan(&t.id, &albumID, &p, &disc, &pos, &artworkID, &raw); err != nil {
			return nil, err
		}
		t.albumID, t.path, t.artworkID = albumID.String, p.String, artworkID.String
		t.discIndex, t.position = int(disc.Int64), int(pos.Int64)
		if raw.Valid {
			t.pictures, err = picturesIn(raw.String)
			if err != nil {
				return nil, fmt.Errorf("track %s: %w", t.id, err)
			}
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// picturesIn reads the front covers out of a track's stored tags.
func picturesIn(raw string) ([]picture, error) {
	var tags map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, err
	}
	var out []picture
	for key, v := range tags {
		if !isPictureFrame(key) {
			continue
		}
		var stub struct {
			Binary      bool   `json:"binary"`
			PictureType string `json:"pictureType"`
			SHA256      string `json:"sha256"`
		}
		if json.Unmarshal(v, &stub) != nil || !stub.Binary || stub.SHA256 == "" {
			continue
		}
		if isFrontCover(stub.PictureType) {
			out = append(out, picture{sha: stub.SHA256, key: key})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out, nil
}

// extract reads out of the files every picture the stubs name that isn't
// stored yet.  A picture may be in many files; the first file that still
// has it serves, and a picture no file has any more is counted and left.
func (r *run) extract(tracks []*track) error {
	carriers := map[string][]*track{}
	var order []string
	for _, t := range tracks {
		if t.path == "" {
			continue
		}
		for _, p := range t.pictures {
			if _, stored := r.ids[p.sha]; stored {
				continue
			}
			if carriers[p.sha] == nil {
				order = append(order, p.sha)
			}
			carriers[p.sha] = append(carriers[p.sha], t)
		}
	}
	for _, sha := range order {
		found := false
		for _, t := range carriers[sha] {
			data, err := pictureFromFile(filepath.Join(r.library, filepath.FromSlash(t.path)), sha)
			if err != nil {
				return fmt.Errorf("reading %s: %w", t.path, err)
			}
			if data == nil {
				continue
			}
			if _, err := r.store(data, sha); err != nil {
				return err
			}
			r.report.Extracted++
			found = true
			break
		}
		if !found {
			r.report.Unreadable++
		}
	}
	return nil
}

// pictureFromFile returns the picture with this hash from the file's
// tags, or nil if it isn't there.  A file whose tags can't be read is
// treated as having none; the stub came from an earlier read of the same
// file, so the file has changed, which is not this run's problem.
func pictureFromFile(name, sha string) ([]byte, error) {
	fh, err := os.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	m, err := tag.ReadFrom(fh)
	if err != nil {
		return nil, nil
	}
	for _, v := range m.Raw() {
		if p, ok := v.(*tag.Picture); ok && p != nil {
			if sum := sha256.Sum256(p.Data); hex.EncodeToString(sum[:]) == sha {
				return p.Data, nil
			}
		}
	}
	return nil, nil
}

// store keeps an image, writing its file and recording it, and returns
// its id.  An image already stored is not written again.
func (r *run) store(data []byte, sha string) (string, error) {
	if id, ok := r.ids[sha]; ok {
		return id, nil
	}
	id := store.ArtworkID(sha)
	name := Path(r.library, id)
	if info, err := os.Stat(name); err != nil || info.Size() != int64(len(data)) {
		tmp := name + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, name); err != nil {
			return "", err
		}
	}
	mime := http.DetectContentType(data)
	var width, height *int
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		width, height = &cfg.Width, &cfg.Height
	}
	_, err := r.db.ExecContext(r.ctx, `
		INSERT INTO artwork (id, sha256, mime, size, width, height) VALUES (?, ?, ?, ?, ?, ?)`,
		id, sha, mime, len(data), width, height)
	if err != nil {
		return "", err
	}
	r.ids[sha] = id
	return id, nil
}

// folderStems are the names, less extension, that an image file beside
// an album's tracks may have to count as the album's cover, in order of
// preference.  Anything else in the directory is not assumed to be art.
var folderStems = []string{"cover", "folder", "front", "artwork", "album"}

var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true}

// folderImages finds, for each album, an image file in the directory its
// tracks are in, and returns the image ids by album id.
func (r *run) folderImages(tracks []*track) (map[string]string, error) {
	dirs := map[string]string{}
	for _, t := range tracks {
		if t.albumID != "" && t.path != "" && dirs[t.albumID] == "" {
			dirs[t.albumID] = path.Dir(t.path)
		}
	}
	out := map[string]string{}
	for _, albumID := range sortedKeys(dirs) {
		name, err := folderImage(filepath.Join(r.library, filepath.FromSlash(dirs[albumID])))
		if err != nil {
			return nil, err
		}
		if name == "" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		id, err := r.store(data, hex.EncodeToString(sum[:]))
		if err != nil {
			return nil, err
		}
		out[albumID] = id
		r.report.Folder++
	}
	return out, nil
}

// folderImage returns the path of the image file in a directory that
// counts as its cover, or "" if there is none.  A directory that can't
// be read has none.
func folderImage(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	best, bestRank := "", len(folderStems)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !imageExts[ext] {
			continue
		}
		stem := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
		for rank, s := range folderStems {
			if stem == s && (rank < bestRank || (rank == bestRank && name < best)) {
				best, bestRank = name, rank
			}
		}
	}
	if best == "" {
		return "", nil
	}
	return filepath.Join(dir, best), nil
}

// own is the image a track's own file gives it: its first front cover
// that is stored, or "".
func (r *run) own(t *track) string {
	for _, p := range t.pictures {
		if id, ok := r.ids[p.sha]; ok {
			return id
		}
	}
	return ""
}

// choose picks an album's image from its tracks, which are in play
// order: the image on the most tracks, or on a tie the first track's,
// so a re-run lands on the same answer.  It reports whether there was
// a tie.
func (r *run) choose(tracks []*track) (string, bool) {
	counts := map[string]int{}
	most := 0
	for _, t := range tracks {
		if id := r.own(t); id != "" {
			counts[id]++
			most = max(most, counts[id])
		}
	}
	if most == 0 {
		return "", false
	}
	for _, t := range tracks {
		if id := r.own(t); counts[id] == most {
			return id, countOf(counts, most) > 1
		}
	}
	return "", false
}

func countOf(counts map[string]int, n int) int {
	k := 0
	for _, c := range counts {
		if c == n {
			k++
		}
	}
	return k
}

// assign sets every album's image and every track's own, where that
// differs from its album's, writing only what changed.
func (r *run) assign(tracks []*track, folder map[string]string) error {
	byAlbum := map[string][]*track{}
	for _, t := range tracks {
		byAlbum[t.albumID] = append(byAlbum[t.albumID], t)
	}
	albums := map[string]string{}
	rows, err := r.db.QueryContext(r.ctx, `SELECT id, artwork_id FROM album`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var art sql.NullString
		if err := rows.Scan(&id, &art); err != nil {
			rows.Close()
			return err
		}
		albums[id] = art.String
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := r.db.BeginTx(r.ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var albumModseq, trackModseq int64
	setAlbum := func(id, art string) error {
		if albumModseq == 0 {
			if albumModseq, err = store.NextModseq(r.ctx, tx); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(r.ctx, `UPDATE album SET artwork_id = ?, modseq = ? WHERE id = ?`, nullable(art), albumModseq, id)
		r.report.Changed++
		return err
	}
	setTrack := func(id, art string) error {
		if trackModseq == 0 {
			if trackModseq, err = store.NextModseq(r.ctx, tx); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(r.ctx, `UPDATE track SET artwork_id = ?, modseq = ? WHERE id = ?`, nullable(art), trackModseq, id)
		r.report.Changed++
		return err
	}

	for _, albumID := range sortedKeys(albums) {
		art, tie := folder[albumID], false
		if art == "" {
			art, tie = r.choose(byAlbum[albumID])
		}
		if art != "" {
			r.report.Albums++
		}
		if tie {
			r.report.Ties++
		}
		if art != albums[albumID] {
			if err := setAlbum(albumID, art); err != nil {
				return err
			}
		}
		for _, t := range byAlbum[albumID] {
			if err := r.override(t, art, setTrack); err != nil {
				return err
			}
		}
	}
	for _, t := range byAlbum[""] {
		if err := r.override(t, "", setTrack); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// override sets a track's own image where it differs from its album's.
func (r *run) override(t *track, albumArt string, set func(id, art string) error) error {
	art := r.own(t)
	if art == albumArt {
		art = ""
	}
	if art != "" {
		r.report.Overrides++
	}
	if art != t.artworkID {
		return set(t.id, art)
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

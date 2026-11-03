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
	"time"

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
	Kept       int // albums and tracks whose art was chosen by hand and left alone
	Hashed     int // images hashed this run, for telling near-identical ones
	Collapsed  int // albums whose winning picture is carried as more than one image
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
	source    string // whose choice artworkID is: "rule", "user", or ""
	pictureID string // the stored picture its file carries, as recorded before the run
	pictures  []picture
}

// A run's working state.
type run struct {
	ctx     context.Context
	db      *sql.DB
	library string
	report  Report
	ids     map[string]string    // image sha -> artwork id, for every stored image
	images  map[string]imageInfo // artwork id -> what grouping needs
}

// Run finds every image, stores the ones not stored yet, and sets each
// album's and track's art.  Albums and tracks whose art changes get a
// new modseq, one per type, so clients refetch them once.
func Run(ctx context.Context, db *sql.DB, library string) (*Report, error) {
	r := &run{ctx: ctx, db: db, library: library, ids: map[string]string{}, images: map[string]imageInfo{}}
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
	if r.report.Hashed, err = r.hashImages(); err != nil {
		return nil, err
	}
	if err := r.assign(tracks, folder); err != nil {
		return nil, err
	}
	r.report.Images = len(r.ids)
	return &r.report, nil
}

func (r *run) loadImages() error {
	rows, err := r.db.QueryContext(r.ctx, `SELECT id, sha256, dhash, coalesce(width * height, 0) FROM artwork`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, sha string
		var hash sql.NullInt64
		var pixels int
		if err := rows.Scan(&id, &sha, &hash, &pixels); err != nil {
			return err
		}
		r.ids[sha] = id
		r.images[id] = imageInfo{hash: uint64(hash.Int64), hashed: hash.Valid, pixels: pixels}
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
		SELECT id, album_id, path, disc_index, position, artwork_id, artwork_source, picture_id, original_tags
		FROM track ORDER BY album_id, disc_index, position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*track
	for rows.Next() {
		var t track
		var albumID, p, artworkID, source, pictureID, raw sql.NullString
		var disc, pos sql.NullInt64
		if err := rows.Scan(&t.id, &albumID, &p, &disc, &pos, &artworkID, &source, &pictureID, &raw); err != nil {
			return nil, err
		}
		t.albumID, t.path, t.artworkID = albumID.String, p.String, artworkID.String
		t.source, t.pictureID = source.String, pictureID.String
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

// store keeps an image the run found, and returns its id.
func (r *run) store(data []byte, sha string) (string, error) {
	if id, ok := r.ids[sha]; ok {
		return id, nil
	}
	img, err := Store(r.ctx, r.db, r.library, data, nil)
	if err != nil {
		return "", err
	}
	r.ids[sha] = img.ID
	r.images[img.ID] = imageInfo{hash: img.hash, hashed: true, pixels: img.pixels}
	return img.ID, nil
}

// An Image is a stored image as the artwork table records it.
type Image struct {
	ID   string
	Mime string
	Size int

	hash   uint64
	pixels int
}

// Store keeps an image, writing its file under the library and
// recording it, and returns it.  The id is the content's, so an image
// already stored is returned as it is, not written again; uploadedAt,
// for an image a client sent rather than one found in the library, is
// kept for the collector's grace period and is not changed for an image
// already stored.  The data must be an image the server can decode.
func Store(ctx context.Context, db store.Queryer, library string, data []byte, uploadedAt *time.Time) (Image, error) {
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	id := store.ArtworkID(sha)
	var mime string
	var size, pixels int
	var hash sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT mime, size, dhash, coalesce(width * height, 0) FROM artwork WHERE id = ?`, id).Scan(&mime, &size, &hash, &pixels)
	if err == nil {
		return Image{ID: id, Mime: mime, Size: size, hash: uint64(hash.Int64), pixels: pixels}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Image{}, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("not an image the server can read: %w", err)
	}
	cfg := img.Bounds().Size()
	computed := dhash(img)
	if err := os.MkdirAll(Dir(library), 0o755); err != nil {
		return Image{}, err
	}
	name := Path(library, id)
	if info, err := os.Stat(name); err != nil || info.Size() != int64(len(data)) {
		tmp := name + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return Image{}, err
		}
		if err := os.Rename(tmp, name); err != nil {
			return Image{}, err
		}
	}
	mime = http.DetectContentType(data)
	var at *string
	if uploadedAt != nil {
		s := uploadedAt.UTC().Format(time.RFC3339)
		at = &s
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO artwork (id, sha256, mime, size, width, height, uploaded_at, dhash) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, sha, mime, len(data), cfg.X, cfg.Y, at, int64(computed)); err != nil {
		return Image{}, err
	}
	return Image{ID: id, Mime: mime, Size: len(data), hash: computed, pixels: cfg.X * cfg.Y}, nil
}

// Collect removes the images nothing names: no album's art or folder
// image, no track's art or own picture.  An upload is kept until
// uploadedBefore, since a client that uploaded may be about to set it,
// as RFC 8620 allows a server to do; an image the library gave goes as
// soon as nothing names it.  The file goes with the row.  It returns
// how many went.
func Collect(ctx context.Context, db *sql.DB, library string, uploadedBefore time.Time) (int, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id FROM artwork
		WHERE (uploaded_at IS NULL OR uploaded_at < ?)
		  AND NOT EXISTS (SELECT 1 FROM album WHERE artwork_id = artwork.id OR folder_artwork_id = artwork.id)
		  AND NOT EXISTS (SELECT 1 FROM track WHERE artwork_id = artwork.id OR picture_id = artwork.id)
		ORDER BY id`, uploadedBefore.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	var dead []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		dead = append(dead, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range dead {
		if _, err := db.ExecContext(ctx, `DELETE FROM artwork WHERE id = ?`, id); err != nil {
			return 0, err
		}
		if err := os.Remove(Path(library, id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, err
		}
	}
	return len(dead), nil
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
// order: the picture on the most tracks, counting its encodings
// together, or on a tie the first track's, so a re-run lands on the
// same answer; the picture's exemplar is the image.  pictures is the
// album's grouping, from group.  It reports whether there was a tie,
// and whether the winner was carried as more than one image.
func (r *run) choose(tracks []*track, pictures map[string]string) (art string, tie, collapsed bool) {
	counts := map[string]int{}
	members := map[string]map[string]bool{}
	most := 0
	for _, t := range tracks {
		if id := r.own(t); id != "" {
			p := pictures[id]
			counts[p]++
			if members[p] == nil {
				members[p] = map[string]bool{}
			}
			members[p][id] = true
			most = max(most, counts[p])
		}
	}
	if most == 0 {
		return "", false, false
	}
	for _, t := range tracks {
		if id := r.own(t); id != "" && counts[pictures[id]] == most {
			p := pictures[id]
			return p, countOf(counts, most) > 1, len(members[p]) > 1
		}
	}
	return "", false, false
}

// pictures groups the images an album's tracks carry, and its folder
// image, into pictures.
func (r *run) pictures(tracks []*track, folder string) map[string]string {
	ids := []string{folder}
	for _, t := range tracks {
		ids = append(ids, r.own(t))
	}
	return r.group(distinct(ids))
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

// An album's art as stored before the run.
type albumArt struct {
	art    string
	source string // whose choice art is: "rule", "user", or ""
	folder string // the image file beside its tracks
	tie    bool
}

// assign sets every album's image and every track's own, where that
// differs from its album's, writing only what changed.  Art chosen by
// hand, on the review page, is left alone: the rule applies only where
// its own earlier choice stands, or none.  Along the way it records
// what the review page shows: the picture each track's file carries, the
// image file beside each album, and whether the choice was a tie.
// Those don't change what clients show, so they take no new modseq.
// -- claude, 2026-11-02
func (r *run) assign(tracks []*track, folder map[string]string) error {
	byAlbum := map[string][]*track{}
	for _, t := range tracks {
		byAlbum[t.albumID] = append(byAlbum[t.albumID], t)
	}
	albums := map[string]albumArt{}
	rows, err := r.db.QueryContext(r.ctx, `SELECT id, artwork_id, artwork_source, folder_artwork_id, art_tie FROM album`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var art, source, folderArt sql.NullString
		var tie bool
		if err := rows.Scan(&id, &art, &source, &folderArt, &tie); err != nil {
			rows.Close()
			return err
		}
		albums[id] = albumArt{art: art.String, source: source.String, folder: folderArt.String, tie: tie}
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
		_, err := tx.ExecContext(r.ctx, `UPDATE album SET artwork_id = ?, artwork_source = ?, modseq = ? WHERE id = ?`,
			nullable(art), nullable(ruleIf(art)), albumModseq, id)
		r.report.Changed++
		return err
	}
	setTrack := func(id, art string) error {
		if trackModseq == 0 {
			if trackModseq, err = store.NextModseq(r.ctx, tx); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(r.ctx, `UPDATE track SET artwork_id = ?, artwork_source = ?, modseq = ? WHERE id = ?`,
			nullable(art), nullable(ruleIf(art)), trackModseq, id)
		r.report.Changed++
		return err
	}

	albumIDs := make([]string, 0, len(albums))
	for id := range albums {
		albumIDs = append(albumIDs, id)
	}
	sort.Strings(albumIDs)
	for _, albumID := range albumIDs {
		was := albums[albumID]
		pictures := r.pictures(byAlbum[albumID], folder[albumID])
		art, tie, collapsed := folder[albumID], false, false
		if art == "" {
			art, tie, collapsed = r.choose(byAlbum[albumID], pictures)
		}
		if collapsed {
			r.report.Collapsed++
		}
		if was.folder != folder[albumID] || was.tie != tie {
			if _, err := tx.ExecContext(r.ctx, `UPDATE album SET folder_artwork_id = ?, art_tie = ? WHERE id = ?`,
				nullable(folder[albumID]), tie, albumID); err != nil {
				return err
			}
		}
		if tie {
			r.report.Ties++
		}
		switch {
		case was.source == "user":
			art = was.art
			r.report.Kept++
		case art != was.art:
			if err := setAlbum(albumID, art); err != nil {
				return err
			}
		}
		if art != "" {
			r.report.Albums++
		}
		for _, t := range byAlbum[albumID] {
			if err := r.override(tx, t, art, pictures, setTrack); err != nil {
				return err
			}
		}
	}
	for _, t := range byAlbum[""] {
		if err := r.override(tx, t, "", nil, setTrack); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// override records the picture a track's file carries, and sets it as
// the track's own image where it is another picture than its album's,
// by the album's grouping, unless the track's image was chosen by hand.
func (r *run) override(tx *sql.Tx, t *track, albumArt string, pictures map[string]string, set func(id, art string) error) error {
	own := r.own(t)
	if own != t.pictureID {
		if _, err := tx.ExecContext(r.ctx, `UPDATE track SET picture_id = ? WHERE id = ?`, nullable(own), t.id); err != nil {
			return err
		}
	}
	if t.source == "user" {
		if t.artworkID != "" {
			r.report.Overrides++
		}
		r.report.Kept++
		return nil
	}
	art := own
	if art == albumArt || (pictures != nil && art != "" && pictures[art] == pictures[albumArt]) {
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

// ruleIf is the source to record with art the rule chose: "rule" for an
// image, "" for none.
func ruleIf(art string) string {
	if art == "" {
		return ""
	}
	return "rule"
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

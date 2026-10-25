// Package importer builds a fresh Juke database from a library tree and
// the recovery's catalog of the 2014 iTunes library.  See doc/import.md.
//
// The files' tags win for everything about the music.  The catalog wins
// for listening data: rating, play count, last played and date added.
// Each directory is an album, except an "Unknown Album" directory, whose
// tracks have no album.  Where the catalog knows an album had tracks
// that no longer exist, each one becomes a gap: a track with no audio,
// holding its place on the album.
package importer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dhowden/tag"

	"semiotic.systems/juke/internal/store"
)

// Options configures an import.
type Options struct {
	// Library is the root of the library tree, holding Undamaged and so on.
	Library string

	// Include names the top-level directories of Library to import.
	Include []string

	// Catalog is the recovery's catalog database, opened read-only.
	Catalog string

	// CatalogRoot is the library root as the recovery's move_log recorded
	// it.  A file at Library/P is looked up as CatalogRoot/P.
	CatalogRoot string

	// Playlists, if set, is a JSON file of extra playlists; see
	// doc/sample-playlists.json.
	Playlists string

	// Out is the path of the new database.  It must not exist.
	Out string

	// Log receives a line for each file refused.
	Log io.Writer
}

// Report counts what an import did.
type Report struct {
	Files          int
	Tracks         int
	GapTracks      int
	Albums         int
	AlbumlessTrack int
	Playlists      int
	SkippedFiles   int
	Refused        []string
}

var audioTypes = map[string]string{
	".mp3": "audio/mpeg",
	".mp2": "audio/mpeg",
	".m4a": "audio/mp4",
}

// catalogTrack is the part of a 2014 catalog track the import uses.
type catalogTrack struct {
	ID          int64
	Name        sql.NullString
	Artist      sql.NullString
	AlbumArtist sql.NullString
	Album       sql.NullString
	TrackNumber sql.NullInt64
	DiscNumber  sql.NullInt64
	Rating      sql.NullInt64
	PlayCount   sql.NullInt64
	PlayDate    sql.NullString
	DateAdded   sql.NullString
	AlbumID     sql.NullInt64
}

const catalogTrackColumns = `
	t.track_id, t.name, t.artist, t.album_artist, t.album,
	t.track_number, t.disc_number, t.rating, t.play_count,
	t.play_date, t.date_added, ta.album_id`

func scanCatalogTrack(rows interface{ Scan(...any) error }) (catalogTrack, error) {
	var c catalogTrack
	err := rows.Scan(
		&c.ID, &c.Name, &c.Artist, &c.AlbumArtist, &c.Album,
		&c.TrackNumber, &c.DiscNumber, &c.Rating, &c.PlayCount,
		&c.PlayDate, &c.DateAdded, &c.AlbumID,
	)
	return c, err
}

// audioFile is one file found in the tree, with what its tags and the
// catalog say about it.
type audioFile struct {
	rel         string
	name        string
	size        int64
	mtime       time.Time
	contentType string

	tagFormat    *string
	originalTags *string
	title        string
	artist       string
	albumArtist  string
	album        string
	trackNumber  int
	discNumber   int

	// The catalog track whose listening data this file gets, if any, and
	// every catalog track id that refers to this file, directly or as a
	// copy it displaced, for mapping 2014 playlists.
	catalog   *catalogTrack
	catalogID []int64
}

// Run performs an import.
func Run(ctx context.Context, opts Options) (*Report, error) {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	catalog, err := sql.Open("sqlite", "file:"+opts.Catalog+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer catalog.Close()
	if err := catalog.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("opening catalog: %w", err)
	}

	if _, err := os.Stat(opts.Out); err == nil {
		return nil, fmt.Errorf("%s exists; imports always build a new database", opts.Out)
	}
	s, err := store.Create(opts.Out)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	im := &run{opts: opts, catalog: catalog, report: &Report{}, trackByCatalogID: map[int64]string{}}
	if err := im.prepare(ctx); err != nil {
		return nil, err
	}
	defer im.close()

	dirs, err := im.walk(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	im.tx = tx
	im.trackByPath = map[string]string{}
	im.tracksByDir = map[string][]namedTrack{}

	dirNames := make([]string, 0, len(dirs))
	for dir := range dirs {
		dirNames = append(dirNames, dir)
	}
	sort.Strings(dirNames)
	for _, dir := range dirNames {
		if err := im.importDir(ctx, dir, dirs[dir]); err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
	}

	if err := im.importCatalogPlaylists(ctx); err != nil {
		return nil, err
	}
	if opts.Playlists != "" {
		if err := im.importPlaylistFile(ctx, opts.Playlists); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return im.report, nil
}

type namedTrack struct {
	title string
	id    string
}

type run struct {
	opts    Options
	catalog *sql.DB
	tx      *sql.Tx
	report  *Report

	byDest    *sql.Stmt
	displaced *sql.Stmt
	gaps      *sql.Stmt

	trackByCatalogID map[int64]string
	trackByPath      map[string]string
	tracksByDir      map[string][]namedTrack
}

func (im *run) prepare(ctx context.Context) error {
	var err error
	im.byDest, err = im.catalog.PrepareContext(ctx, `
		SELECT `+catalogTrackColumns+`
		FROM move_log l
		JOIN itunes_track_file tf ON tf.file_id = l.file_id
		JOIN itunes_track t ON t.track_id = tf.track_id
		LEFT JOIN itunes_track_album ta ON ta.track_id = t.track_id
		WHERE l.dest = ?
		ORDER BY coalesce(t.play_count, 0) DESC, t.track_id`)
	if err != nil {
		return err
	}
	im.displaced, err = im.catalog.PrepareContext(ctx, `
		SELECT `+catalogTrackColumns+`
		FROM move_log l
		JOIN move_plan p ON p.displaced_by = l.file_id
		JOIN itunes_track_file tf ON tf.file_id = p.file_id
		JOIN itunes_track t ON t.track_id = tf.track_id
		LEFT JOIN itunes_track_album ta ON ta.track_id = t.track_id
		WHERE l.dest = ?
		ORDER BY coalesce(t.play_count, 0) DESC, t.track_id`)
	if err != nil {
		return err
	}
	im.gaps, err = im.catalog.PrepareContext(ctx, `
		SELECT `+catalogTrackColumns+`
		FROM itunes_track_album ta
		JOIN itunes_audio_track t ON t.track_id = ta.track_id
		WHERE ta.album_id = ?
		  AND NOT EXISTS (SELECT 1 FROM itunes_track_file x WHERE x.track_id = t.track_id)
		ORDER BY t.disc_number, t.track_number, t.track_id`)
	return err
}

func (im *run) close() {
	for _, st := range []*sql.Stmt{im.byDest, im.displaced, im.gaps} {
		if st != nil {
			st.Close()
		}
	}
}

// walk finds every file under the included directories and groups the
// audio files by directory.
func (im *run) walk(ctx context.Context) (map[string][]*audioFile, error) {
	dirs := map[string][]*audioFile{}
	for _, top := range im.opts.Include {
		root := filepath.Join(im.opts.Library, top)
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			im.report.Files++
			rel, err := filepath.Rel(im.opts.Library, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			contentType, ok := audioTypes[strings.ToLower(path.Ext(rel))]
			if !ok {
				im.report.SkippedFiles++
				return nil
			}
			f, err := im.readFile(ctx, p, rel, contentType)
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			if f.title == "" && f.catalog == nil {
				im.report.Refused = append(im.report.Refused, rel)
				fmt.Fprintf(im.opts.Log, "refused, no title in tags or catalog: %s\n", rel)
				return nil
			}
			dir := path.Dir(rel)
			dirs[dir] = append(dirs[dir], f)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return dirs, nil
}

func (im *run) readFile(ctx context.Context, p, rel, contentType string) (*audioFile, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	f := &audioFile{
		rel:         rel,
		name:        path.Base(rel),
		size:        info.Size(),
		mtime:       info.ModTime().UTC(),
		contentType: contentType,
	}

	fh, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	m, err := tag.ReadFrom(fh)
	switch {
	case errors.Is(err, tag.ErrNoTagsFound):
	case err != nil:
		// A tag the library can't parse is treated as no tags; the file is
		// still audio, and the catalog may know it. -- claude, 2026-10-24
	default:
		format := string(m.Format())
		f.tagFormat = &format
		raw, err := originalTags(m.Raw())
		if err != nil {
			return nil, err
		}
		f.originalTags = &raw
		f.title = strings.TrimSpace(m.Title())
		f.artist = strings.TrimSpace(m.Artist())
		f.albumArtist = strings.TrimSpace(m.AlbumArtist())
		f.album = strings.TrimSpace(m.Album())
		f.trackNumber, _ = m.Track()
		f.discNumber, _ = m.Disc()
	}

	dest := strings.TrimSuffix(im.opts.CatalogRoot, "/") + "/" + rel
	direct, err := queryCatalogTracks(ctx, im.byDest, dest)
	if err != nil {
		return nil, err
	}
	displaced, err := queryCatalogTracks(ctx, im.displaced, dest)
	if err != nil {
		return nil, err
	}

	// The file's own catalog entry wins; failing that, the busiest entry
	// of a copy it displaced, whose listening data it inherits.
	switch {
	case len(direct) > 0:
		f.catalog = &direct[0]
	case len(displaced) > 0:
		f.catalog = &displaced[0]
	}
	for _, c := range append(direct, displaced...) {
		f.catalogID = append(f.catalogID, c.ID)
	}
	return f, nil
}

func queryCatalogTracks(ctx context.Context, st *sql.Stmt, args ...any) ([]catalogTrack, error) {
	rows, err := st.QueryContext(ctx, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []catalogTrack
	for rows.Next() {
		c, err := scanCatalogTrack(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// originalTags renders a file's tags as JSON, replacing each binary value
// with a stub that says what it was without carrying its bytes.
func originalTags(raw map[string]any) (string, error) {
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		switch v := v.(type) {
		case *tag.Picture:
			if v == nil {
				continue
			}
			sum := sha256.Sum256(v.Data)
			out[k] = map[string]any{
				"binary":      true,
				"mime":        v.MIMEType,
				"pictureType": v.Type,
				"description": v.Description,
				"size":        len(v.Data),
				"sha256":      hex.EncodeToString(sum[:]),
			}
		case []byte:
			sum := sha256.Sum256(v)
			out[k] = map[string]any{
				"binary": true,
				"size":   len(v),
				"sha256": hex.EncodeToString(sum[:]),
			}
		default:
			out[k] = v
		}
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// importDir imports one directory: an album, or a set of tracks with no
// album.
func (im *run) importDir(ctx context.Context, dir string, files []*audioFile) error {
	if path.Base(dir) == "Unknown Album" {
		sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
		for _, f := range files {
			if _, err := im.insertFile(ctx, dir, f, nil, nil, nil); err != nil {
				return err
			}
			im.report.AlbumlessTrack++
		}
		return nil
	}

	gaps, err := im.findGaps(ctx, files)
	if err != nil {
		return err
	}

	type entry struct {
		file  *audioFile
		gap   *catalogTrack
		disc  int
		track int
		name  string
	}
	var entries []entry
	for _, f := range files {
		disc := f.discNumber
		if disc <= 0 && f.catalog != nil && f.catalog.DiscNumber.Valid {
			disc = int(f.catalog.DiscNumber.Int64)
		}
		track := f.trackNumber
		if track <= 0 && f.catalog != nil && f.catalog.TrackNumber.Valid {
			track = int(f.catalog.TrackNumber.Int64)
		}
		entries = append(entries, entry{file: f, disc: max(disc, 1), track: max(track, 0), name: f.name})
	}
	for i := range gaps {
		g := &gaps[i]
		entries = append(entries, entry{
			gap:   g,
			disc:  max(int(g.DiscNumber.Int64), 1),
			track: max(int(g.TrackNumber.Int64), 0),
			name:  g.Name.String,
		})
	}

	// Numbered tracks first, in number order; then unnumbered ones by
	// name, files before gaps, since a gap with no number has no better
	// claim to any spot. -- claude, 2026-10-24
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.disc != b.disc {
			return a.disc < b.disc
		}
		if (a.track == 0) != (b.track == 0) {
			return a.track != 0
		}
		if a.track != b.track {
			return a.track < b.track
		}
		if (a.gap == nil) != (b.gap == nil) {
			return a.gap == nil
		}
		return a.name < b.name
	})

	discIndex := map[int]int{}
	var discs []int
	for _, e := range entries {
		if _, ok := discIndex[e.disc]; !ok {
			discIndex[e.disc] = len(discs)
			discs = append(discs, e.disc)
		}
	}
	// discs were met in sorted order, so their indexes follow disc order.

	title, artist := im.albumNaming(dir, files)
	albumID, err := store.InsertAlbum(ctx, im.tx, store.NewAlbum{
		Title:      title,
		Artist:     artist,
		DiscTitles: make([]*string, len(discs)),
	})
	if err != nil {
		return err
	}
	im.report.Albums++

	positions := map[int]int{}
	for _, e := range entries {
		di := discIndex[e.disc]
		pos := positions[di]
		positions[di]++
		if e.file != nil {
			if _, err := im.insertFile(ctx, dir, e.file, &albumID, &di, &pos); err != nil {
				return err
			}
			continue
		}
		if err := im.insertGap(ctx, dir, e.gap, albumID, di, pos); err != nil {
			return err
		}
	}
	return nil
}

// findGaps returns the catalog tracks of the files' album that no file
// survives for.  The album is the one most of the files' catalog entries
// belong to.
func (im *run) findGaps(ctx context.Context, files []*audioFile) ([]catalogTrack, error) {
	votes := map[int64]int{}
	var best int64
	for _, f := range files {
		if f.catalog == nil || !f.catalog.AlbumID.Valid {
			continue
		}
		id := f.catalog.AlbumID.Int64
		votes[id]++
		if votes[id] > votes[best] || (votes[id] == votes[best] && id < best) {
			best = id
		}
	}
	if votes[best] == 0 {
		return nil, nil
	}
	return queryCatalogTracks(ctx, im.gaps, best)
}

// albumNaming picks an album's title and artist.  The tags win; the
// catalog fills in when they are silent; the directory names are the
// last resort.  An album under Compilations has no artist.
func (im *run) albumNaming(dir string, files []*audioFile) (string, *string) {
	var tagAlbums, catAlbums, tagAlbumArtists, tagArtists, catAlbumArtists, catArtists []string
	for _, f := range files {
		tagAlbums = append(tagAlbums, f.album)
		tagAlbumArtists = append(tagAlbumArtists, f.albumArtist)
		tagArtists = append(tagArtists, f.artist)
		if f.catalog != nil {
			catAlbums = append(catAlbums, f.catalog.Album.String)
			catAlbumArtists = append(catAlbumArtists, f.catalog.AlbumArtist.String)
			catArtists = append(catArtists, f.catalog.Artist.String)
		}
	}

	title := firstNonEmpty(majority(tagAlbums), majority(catAlbums), path.Base(dir))

	parent := path.Base(path.Dir(dir))
	if parent == "Compilations" {
		return title, nil
	}
	artist := firstNonEmpty(
		majority(tagAlbumArtists),
		unanimous(tagArtists),
		majority(catAlbumArtists),
		unanimous(catArtists),
	)
	if artist == "" && parent != "_" && parent != "." {
		artist = parent
	}
	if artist == "" {
		return title, nil
	}
	return title, &artist
}

// majority returns the most common non-empty string, preferring the
// earliest on a tie, or "" if there are none.
func majority(values []string) string {
	count := map[string]int{}
	best := ""
	for _, v := range values {
		if v == "" {
			continue
		}
		count[v]++
		if count[v] > count[best] {
			best = v
		}
	}
	return best
}

// unanimous returns the value if every entry is that same non-empty
// value, or "".
func unanimous(values []string) string {
	if len(values) == 0 || values[0] == "" {
		return ""
	}
	for _, v := range values[1:] {
		if v != values[0] {
			return ""
		}
	}
	return values[0]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// listening converts a catalog track's listening data.  Ratings are
// stored 0 to 100 in steps of 10; Juke's are 1 to 5 in steps of 0.5.
func listening(c *catalogTrack) (rating *float64, playCount int64, lastPlayed *string, dateAdded string) {
	if c == nil {
		return nil, 0, nil, ""
	}
	if c.Rating.Valid && c.Rating.Int64 > 0 {
		r := float64(c.Rating.Int64) / 20
		rating = &r
	}
	if c.PlayCount.Valid {
		playCount = c.PlayCount.Int64
	}
	if c.PlayDate.Valid && c.PlayDate.String != "" {
		lastPlayed = &c.PlayDate.String
	}
	return rating, playCount, lastPlayed, c.DateAdded.String
}

var leadingNumber = regexp.MustCompile(`^\d+[-. ]+`)

func (im *run) insertFile(ctx context.Context, dir string, f *audioFile, albumID *string, disc, pos *int) (string, error) {
	rating, playCount, lastPlayed, dateAdded := listening(f.catalog)
	if dateAdded == "" {
		dateAdded = f.mtime.Format("2006-01-02T15:04:05Z")
	}
	var catName, catArtist string
	if f.catalog != nil {
		catName, catArtist = f.catalog.Name.String, f.catalog.Artist.String
	}
	stem := leadingNumber.ReplaceAllString(strings.TrimSuffix(f.name, path.Ext(f.name)), "")

	rel := f.rel
	id, err := store.InsertTrack(ctx, im.tx, store.NewTrack{
		Title:        firstNonEmpty(f.title, catName, stem),
		Artist:       nonEmpty(firstNonEmpty(f.artist, catArtist)),
		AlbumID:      albumID,
		DiscIndex:    disc,
		Position:     pos,
		Rating:       rating,
		PlayCount:    playCount,
		LastPlayed:   lastPlayed,
		DateAdded:    dateAdded,
		Path:         &rel,
		Size:         &f.size,
		ContentType:  &f.contentType,
		TagFormat:    f.tagFormat,
		OriginalTags: f.originalTags,
	})
	if err != nil {
		return "", err
	}
	im.report.Tracks++
	im.trackByPath[f.rel] = id
	im.tracksByDir[dir] = append(im.tracksByDir[dir], namedTrack{title: firstNonEmpty(f.title, catName, stem), id: id})
	for _, cid := range f.catalogID {
		if _, ok := im.trackByCatalogID[cid]; !ok {
			im.trackByCatalogID[cid] = id
		}
	}
	return id, nil
}

func (im *run) insertGap(ctx context.Context, dir string, g *catalogTrack, albumID string, disc, pos int) error {
	rating, playCount, lastPlayed, dateAdded := listening(g)
	title := firstNonEmpty(g.Name.String, "Untitled")
	id, err := store.InsertTrack(ctx, im.tx, store.NewTrack{
		Title:      title,
		Artist:     nonEmpty(g.Artist.String),
		AlbumID:    &albumID,
		DiscIndex:  &disc,
		Position:   &pos,
		Rating:     rating,
		PlayCount:  playCount,
		LastPlayed: lastPlayed,
		DateAdded:  dateAdded,
	})
	if err != nil {
		return err
	}
	im.report.GapTracks++
	im.trackByCatalogID[g.ID] = id
	im.tracksByDir[dir] = append(im.tracksByDir[dir], namedTrack{title: title, id: id})
	return nil
}

// importCatalogPlaylists imports each plain 2014 playlist whose tracks
// were all imported.  Smart playlists, folders, and the library's own
// built-in lists are skipped.
func (im *run) importCatalogPlaylists(ctx context.Context) error {
	rows, err := im.catalog.QueryContext(ctx, `
		SELECT p.playlist_id, p.name, i.track_id
		FROM itunes_playlist p
		JOIN itunes_playlist_item i ON i.playlist_id = p.playlist_id
		WHERE p.master = 0 AND p.folder = 0 AND p.all_items = 0
		  AND p.music = 0 AND p.movies = 0 AND p.tv_shows = 0
		  AND p.podcasts = 0 AND p.audiobooks = 0
		  AND p.distinguished_kind IS NULL
		  AND p.smart_criteria IS NULL
		ORDER BY p.playlist_id, i.position`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type playlist struct {
		name     string
		trackIDs []string
		complete bool
	}
	var order []int64
	lists := map[int64]*playlist{}
	for rows.Next() {
		var pid, tid int64
		var name sql.NullString
		if err := rows.Scan(&pid, &name, &tid); err != nil {
			return err
		}
		p, ok := lists[pid]
		if !ok {
			p = &playlist{name: name.String, complete: true}
			lists[pid] = p
			order = append(order, pid)
		}
		id, ok := im.trackByCatalogID[tid]
		if !ok {
			p.complete = false
			continue
		}
		p.trackIDs = append(p.trackIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, pid := range order {
		p := lists[pid]
		if !p.complete || len(p.trackIDs) == 0 {
			continue
		}
		if _, err := store.InsertPlaylist(ctx, im.tx, p.name, p.trackIDs); err != nil {
			return err
		}
		im.report.Playlists++
	}
	return nil
}

// playlistEntry is one entry in a playlist file: either a path relative
// to the library, or an album directory and a track title, which can
// name a gap.
type playlistEntry struct {
	Path  string
	Album string
	Title string
}

func (e *playlistEntry) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &e.Path); err == nil {
		return nil
	}
	var obj struct {
		Album string `json:"album"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	e.Album, e.Title = obj.Album, obj.Title
	return nil
}

func (im *run) importPlaylistFile(ctx context.Context, file string) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var lists []struct {
		Title  string          `json:"title"`
		Tracks []playlistEntry `json:"tracks"`
	}
	if err := json.Unmarshal(b, &lists); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	for _, l := range lists {
		var ids []string
		for _, e := range l.Tracks {
			id, err := im.resolveEntry(e)
			if err != nil {
				return fmt.Errorf("%s: playlist %q: %w", file, l.Title, err)
			}
			ids = append(ids, id)
		}
		if _, err := store.InsertPlaylist(ctx, im.tx, l.Title, ids); err != nil {
			return err
		}
		im.report.Playlists++
	}
	return nil
}

func (im *run) resolveEntry(e playlistEntry) (string, error) {
	if e.Path != "" {
		id, ok := im.trackByPath[e.Path]
		if !ok {
			return "", fmt.Errorf("no imported track at %s", e.Path)
		}
		return id, nil
	}
	for _, t := range im.tracksByDir[e.Album] {
		if t.title == e.Title {
			return t.id, nil
		}
	}
	return "", fmt.Errorf("no track %q in %s", e.Title, e.Album)
}

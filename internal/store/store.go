// Package store holds Juke's metadata database: the SQLite schema, and the
// writes the importer and the JMAP methods share.  JMAP state, which is to
// say each type's modseq counter and the tombstones of what has been
// destroyed, is kept by Gizmo's sqlitekit, in tables of its own.
//
// Album membership lives on the track rows, as an album id, a disc index
// and a position.  That makes it impossible for Track.albumId and
// Album.discs to disagree; both are read from the same columns.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/rjbs/gizmo/sqlitekit"
)

// Store is an open Juke database.
type Store struct {
	DB *sql.DB
}

// The object types that have JMAP state.
const (
	TypeTrack         = "Track"
	TypeAlbum         = "Album"
	TypePlaylist      = "Playlist"
	TypeSmartPlaylist = "SmartPlaylist"
)

// migrations is applied in order, and PRAGMA user_version records how many
// have run.  Never edit an entry once it has been applied to a database worth
// keeping; append a new one.  The first is the schema as it stood before
// versioning began, so a database made then counts as version 1.
// -- claude, 2026-11-01
var migrations = []string{
	`
CREATE TABLE meta (
  name  TEXT NOT NULL PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE album (
  id             TEXT    NOT NULL PRIMARY KEY,
  title          TEXT    NOT NULL,
  artist         TEXT,
  created_modseq INTEGER NOT NULL,
  modseq         INTEGER NOT NULL
);

CREATE TABLE disc (
  album_id   TEXT    NOT NULL REFERENCES album (id),
  disc_index INTEGER NOT NULL,
  title      TEXT,
  PRIMARY KEY (album_id, disc_index)
);

CREATE TABLE track (
  id             TEXT    NOT NULL PRIMARY KEY,
  title          TEXT    NOT NULL,
  artist         TEXT,
  album_id       TEXT    REFERENCES album (id),
  disc_index     INTEGER,
  position       INTEGER,
  rating         REAL,
  play_count     INTEGER NOT NULL DEFAULT 0,
  last_played    TEXT,
  date_added     TEXT    NOT NULL,
  blob_id        TEXT    UNIQUE,
  path           TEXT,
  size           INTEGER,
  content_type   TEXT,
  tag_format     TEXT,
  original_tags  TEXT,
  created_modseq INTEGER NOT NULL,
  modseq         INTEGER NOT NULL,
  UNIQUE (album_id, disc_index, position)
);

CREATE TABLE playlist (
  id             TEXT    NOT NULL PRIMARY KEY,
  title          TEXT    NOT NULL,
  created_modseq INTEGER NOT NULL,
  modseq         INTEGER NOT NULL
);

CREATE TABLE playlist_track (
  playlist_id TEXT    NOT NULL REFERENCES playlist (id),
  position    INTEGER NOT NULL,
  track_id    TEXT    NOT NULL REFERENCES track (id),
  PRIMARY KEY (playlist_id, position)
);

CREATE TABLE tombstone (
  type           TEXT    NOT NULL,
  id             TEXT    NOT NULL,
  created_modseq INTEGER NOT NULL,
  modseq         INTEGER NOT NULL,
  PRIMARY KEY (type, id)
);

CREATE INDEX album_modseq    ON album (modseq);
CREATE INDEX track_modseq    ON track (modseq);
CREATE INDEX track_album     ON track (album_id, disc_index, position);
CREATE INDEX playlist_modseq ON playlist (modseq);
CREATE INDEX playlist_track_track ON playlist_track (track_id);
CREATE INDEX tombstone_modseq ON tombstone (type, modseq);
`,

	// A track's position is its track number on its disc, so a number
	// nothing has is a missing track, and a disc records how many tracks
	// its tags or the catalog say it has, so missing tracks at its end can
	// be seen too.  NULL is a disc with no declared total.  (#40)
	`
ALTER TABLE disc ADD COLUMN track_count INTEGER;
`,

	// A track's genre, as its tags give it, or the catalog's for a gap
	// track.  A later backfill fills it from original_tags.  (#43)
	`
ALTER TABLE track ADD COLUMN genre TEXT;
`,

	// How long a track plays, in whole seconds, read from its audio at
	// import or by backfill, or the catalog's figure for a gap track.  (#44)
	`
ALTER TABLE track ADD COLUMN duration INTEGER;
`,

	// A smart playlist is a saved search: a title and the arguments of a
	// Track/query, stored as the JSON the client sent.  The refs table
	// records which playlists of either kind a rule names, so a reference
	// can be refused before it dangles and a cycle before it closes.  (#11)
	`
CREATE TABLE smart_playlist (
  id             TEXT    NOT NULL PRIMARY KEY,
  title          TEXT    NOT NULL,
  filter         TEXT,
  sort           TEXT    NOT NULL,
  "limit"        INTEGER,
  limit_bytes    INTEGER,
  limit_seconds  INTEGER,
  created_modseq INTEGER NOT NULL,
  modseq         INTEGER NOT NULL
);
CREATE INDEX smart_playlist_modseq ON smart_playlist (modseq);

CREATE TABLE smart_playlist_ref (
  smart_playlist_id TEXT NOT NULL REFERENCES smart_playlist (id),
  ref_type          TEXT NOT NULL,
  ref_id            TEXT NOT NULL,
  PRIMARY KEY (smart_playlist_id, ref_type, ref_id)
);
CREATE INDEX smart_playlist_ref_target ON smart_playlist_ref (ref_type, ref_id);
`,

	// An image of album art, stored once under the library by content
	// hash and served as a blob.  An album names its image, and a track
	// names one only when its own picture differs from its album's;
	// see doc/artwork.md.  (#3)
	`
CREATE TABLE artwork (
  id      TEXT    NOT NULL PRIMARY KEY,
  sha256  TEXT    NOT NULL UNIQUE,
  mime    TEXT    NOT NULL,
  size    INTEGER NOT NULL,
  width   INTEGER,
  height  INTEGER
);
ALTER TABLE album ADD COLUMN artwork_id TEXT REFERENCES artwork (id);
ALTER TABLE track ADD COLUMN artwork_id TEXT REFERENCES artwork (id);
`,

	// What the review page needs to show an album's troubles and to
	// keep a fix made there from being undone: whose choice an album's
	// or track's art is ('rule' or 'user'), the picture each track's own
	// file carries, the image file beside an album's tracks, whether the
	// rule's choice was a tie, and where a track's number came from
	// ('tags', 'catalog', 'file', 'user', or 'none'; NULL where an older
	// import didn't say).  Art set before this was the rule's.
	`
ALTER TABLE album ADD COLUMN artwork_source TEXT;
ALTER TABLE album ADD COLUMN folder_artwork_id TEXT REFERENCES artwork (id);
ALTER TABLE album ADD COLUMN art_tie INTEGER NOT NULL DEFAULT 0;
ALTER TABLE track ADD COLUMN artwork_source TEXT;
ALTER TABLE track ADD COLUMN picture_id TEXT REFERENCES artwork (id);
ALTER TABLE track ADD COLUMN number_source TEXT;
UPDATE album SET artwork_source = 'rule' WHERE artwork_id IS NOT NULL;
UPDATE track SET artwork_source = 'rule' WHERE artwork_id IS NOT NULL;
`,

	// When an image was uploaded, for the collector's grace period; NULL
	// for one read out of a file or found beside one.
	`
ALTER TABLE artwork ADD COLUMN uploaded_at TEXT;
`,

	// A difference hash of each image, for telling one picture encoded
	// several ways from different pictures; see internal/artwork/near.go.
	// NULL until the artwork run has hashed it.
	`
ALTER TABLE artwork ADD COLUMN dhash INTEGER;
`,

	// When an album came out, as "YYYY-MM-DD" with "00" for a month or
	// day not known, so it sorts as text: from the tracks' tags at
	// import or by backfill, or set by hand.  NULL when nothing says.
	`
ALTER TABLE album ADD COLUMN released TEXT;
`,

	// A helper playlist is one kept for other smart playlists' rules to
	// name, not for listening; clients show helpers apart from the rest.
	`
ALTER TABLE playlist ADD COLUMN is_helper INTEGER NOT NULL DEFAULT 0;
ALTER TABLE smart_playlist ADD COLUMN is_helper INTEGER NOT NULL DEFAULT 0;
`,
}

// Kit is Gizmo's state kit as Juke uses it: each type's table, and the
// letter its ids begin with.
var Kit = sqlitekit.New(
	sqlitekit.Type{Name: TypeTrack, Table: "track", NewID: prefixedID("T")},
	sqlitekit.Type{Name: TypeAlbum, Table: "album", NewID: prefixedID("A")},
	sqlitekit.Type{Name: TypePlaylist, Table: "playlist", NewID: prefixedID("P")},
	sqlitekit.Type{Name: TypeSmartPlaylist, Table: "smart_playlist", NewID: prefixedID("S")},
)

// Create makes a new, empty database at path, with a fresh epoch.  The
// file must not exist yet: imports always build a new database.
func Create(path string) (*Store, error) {
	db, err := sqlitekit.Open(path)
	if err != nil {
		return nil, err
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err != nil {
		db.Close()
		return nil, err
	}
	if n > 0 {
		db.Close()
		return nil, fmt.Errorf("%s already holds a database", path)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

// Open opens an existing database made by Create.
func Open(path string) (*Store, error) {
	db, err := sqlitekit.Open(path)
	if err != nil {
		return nil, err
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'track'`).Scan(&n); err != nil || n == 0 {
		db.Close()
		if err == nil {
			err = errors.New("it has no tracks table")
		}
		return nil, fmt.Errorf("%s is not a Juke database: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &Store{DB: db}, nil
}

// SchemaVersion is how many of the migrations have been applied.
func SchemaVersion(ctx context.Context, q Queryer) (int, error) {
	var v int
	err := q.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v)
	return v, err
}

// migrate applies the migrations the database hasn't had yet, and then
// sees that it has Gizmo's state tables.  A database with a meta table
// and no version predates versioning, and has the first migration's schema
// already.
func migrate(db *sql.DB) error {
	ctx := context.Background()
	version, err := SchemaVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	if version == 0 {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'meta'`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
				return err
			}
		}
	}
	if err := sqlitekit.Migrate(ctx, db, migrations); err != nil {
		return err
	}
	if err := Kit.Init(ctx, db); err != nil {
		return err
	}
	return adoptKit(ctx, db)
}

// adoptKit moves a database that kept its own state onto Gizmo's state
// tables, which Kit.Init has just made.  Juke used to keep an epoch and one
// modseq counter for every type in the meta table, and tombstones in a
// table of its own.  The epoch and the tombstones are carried over, and
// each type's state is what it was, the highest modseq among its objects
// and tombstones, so that the state strings clients hold stay good.  The
// one counter becomes a counter for each type, which goes on from there.
//
// This runs once: it ends by dropping the old tables, and does nothing
// when they aren't there.  A database just created has them too, empty,
// from the first migration.  -- claude, 2026-11-22
func adoptKit(ctx context.Context, db *sql.DB) error {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'tombstone'`).Scan(&n); err != nil || n == 0 {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var epoch string
	err = tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE name = 'epoch'`).Scan(&epoch)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// A new database: the epoch Kit.Init gave it stands.
	case err != nil:
		return err
	default:
		if err := sqlitekit.SetEpoch(ctx, tx, epoch); err != nil {
			return err
		}
	}
	for _, step := range []string{
		`INSERT INTO gizmo_tombstone (type, id, created_modseq, modseq, destroyed_at)
		 SELECT type, id, created_modseq, modseq, unixepoch() FROM tombstone`,
		`UPDATE gizmo_state SET highest_modseq = max(highest_modseq,
		   coalesce((SELECT max(modseq) FROM gizmo_tombstone WHERE gizmo_tombstone.type = gizmo_state.type), 0))`,
		`DROP TABLE tombstone`,
		`DROP TABLE meta`,
	} {
		if _, err := tx.ExecContext(ctx, step); err != nil {
			return fmt.Errorf("moving state to Gizmo's tables: %w", err)
		}
	}
	return tx.Commit()
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// Epoch is a random token minted when the database was created.  It is
// part of every state string, so that a client holding state from an
// earlier import, whose modseqs may coincide with this one's, is told it
// cannot calculate changes instead of getting wrong ones.
// -- claude, 2026-10-24
func (s *Store) Epoch(ctx context.Context) (string, error) {
	var epoch string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM gizmo_meta WHERE name = 'epoch'`).Scan(&epoch)
	return epoch, err
}

// Queryer is satisfied by both *sql.DB and *sql.Tx.
type Queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// NextModseq advances a type's modseq counter and returns the new value,
// which is the type's new state.  Every object written gets its own
// modseq, so that /changes can stop after any object without splitting a
// group that shares one.
func NextModseq(ctx context.Context, q Queryer, typ string) (int64, error) {
	return Kit.NextModseq(ctx, q, typ)
}

var tableFor = map[string]string{
	TypeTrack:         "track",
	TypeAlbum:         "album",
	TypePlaylist:      "playlist",
	TypeSmartPlaylist: "smart_playlist",
}

// TypeModseq returns the type's modseq counter: the modseq part of its
// state.
func TypeModseq(ctx context.Context, q Queryer, typ string) (int64, error) {
	if _, ok := tableFor[typ]; !ok {
		return 0, fmt.Errorf("no such type %q", typ)
	}
	var n int64
	err := q.QueryRowContext(ctx, `SELECT highest_modseq FROM gizmo_state WHERE type = ?`, typ).Scan(&n)
	return n, err
}

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return idEncoding.EncodeToString(b), nil
}

// NewSeed mints a seed for a random sort: enough randomness that two
// reshuffles never repeat, in the id alphabet so it reads as opaque.
func NewSeed() (string, error) { return randomToken(8) }

// prefixedID makes ids that are a one-letter type prefix and random
// characters from the JMAP id alphabet.
func prefixedID(prefix string) func() (string, error) {
	return func() (string, error) {
		t, err := randomToken(10)
		if err != nil {
			return "", err
		}
		return prefix + t, nil
	}
}

// NewID mints an opaque id for a new object of the type, one that isn't
// already in use.
func NewID(ctx context.Context, q Queryer, typ string) (string, error) {
	return Kit.NewID(ctx, q, typ)
}

// BlobID derives a track's blobId from its id, the version of its
// metadata, and the recipe that builds the served file; see
// doc/storage.md.  The first pass serves originals untouched, so every
// track is at metadata version 1 with the "original" recipe.
func BlobID(trackID string, metadataVersion int, recipe string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", trackID, metadataVersion, recipe)))
	return "G" + idEncoding.EncodeToString(sum[:15])
}

// ArtworkID is the blob id of an image with this SHA-256, so the same
// picture stored from any file lands on the same id.
func ArtworkID(sha256Hex string) string {
	sum, err := hex.DecodeString(sha256Hex)
	if err != nil || len(sum) != 32 {
		panic("ArtworkID: not a SHA-256 hex digest: " + sha256Hex)
	}
	return "A" + idEncoding.EncodeToString(sum[:15])
}

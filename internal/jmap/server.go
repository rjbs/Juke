// Package jmap serves a Juke database over JMAP (RFC 8620): the session
// resource, the API endpoint, and blob download.  The object types are
// Track, Album and Playlist; see doc/types.md.
package jmap

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/rjbs/gizmo/auth"
	"github.com/rjbs/gizmo/blob"
	"github.com/rjbs/gizmo/push"

	"semiotic.systems/juke/internal/artwork"
	"semiotic.systems/juke/internal/store"
)

// Config configures a Server.  Zero limits take the defaults.
type Config struct {
	Store   *store.Store
	Library string // where track paths are relative to

	AccountID string
	Username  string

	// BaseURL, if set, is used for the URLs in the session object instead
	// of one inferred from each request.
	BaseURL string

	MaxSizeRequest    int
	MaxSizeUpload     int
	MaxCallsInRequest int
	MaxObjectsInGet   int
	MaxObjectsInSet   int
	MaxQueryLimit     int

	// PushPollInterval is how often an event-source connection checks for
	// changes made outside this server, such as by an import.
	PushPollInterval time.Duration

	Now func() time.Time
	Log *log.Logger
}

// Server is a JMAP server over one Juke database, with one account.
type Server struct {
	cfg    Config
	store  *store.Store
	dir    *auth.Directory
	broker *push.Broker
	log    *slog.Logger
}

// New returns a Server.
func New(cfg Config) *Server {
	if cfg.AccountID == "" {
		cfg.AccountID = "music"
	}
	if cfg.Username == "" {
		cfg.Username = "juke"
	}
	if cfg.MaxSizeRequest == 0 {
		cfg.MaxSizeRequest = 10 << 20
	}
	if cfg.MaxSizeUpload == 0 {
		cfg.MaxSizeUpload = 20 << 20
	}
	if cfg.MaxCallsInRequest == 0 {
		cfg.MaxCallsInRequest = 64
	}
	if cfg.MaxObjectsInGet == 0 {
		cfg.MaxObjectsInGet = 5000
	}
	if cfg.MaxObjectsInSet == 0 {
		cfg.MaxObjectsInSet = 1000
	}
	if cfg.MaxQueryLimit == 0 {
		cfg.MaxQueryLimit = 5000
	}
	if cfg.PushPollInterval == 0 {
		cfg.PushPollInterval = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = log.New(os.Stderr, "juke: ", log.LstdFlags)
	}
	s := &Server{cfg: cfg, store: cfg.Store, broker: push.NewBroker(), log: slogTo(cfg.Log)}
	s.dir = s.directory()

	// The broker is told the states as they stand, so that the first
	// change published is taken for the one change it is, and not for a
	// change to every type.
	if states, err := s.States(context.Background()); err == nil {
		s.broker.Prime(cfg.AccountID, states)
	} else {
		cfg.Log.Printf("push: %v", err)
	}
	return s
}

// Handler returns the server's HTTP handler.  Every request it is given
// acts as the server's one user; whoever lets requests in decides who may
// be that user.
func (s *Server) Handler() http.Handler {
	return auth.Loopback(s.dir, s.handler())
}

// Get finds a blob for the download endpoint: a track's audio, or an image
// of album art.  It is half of what makes the server a blob.Store.
func (s *Server) Get(ctx context.Context, _, blobID string) (*blob.Blob, error) {
	var path, contentType string
	err := s.store.DB.QueryRowContext(ctx,
		`SELECT path, content_type FROM track WHERE blob_id = ?`, blobID,
	).Scan(&path, &contentType)
	name := filepath.Join(s.cfg.Library, filepath.FromSlash(path))
	if errors.Is(err, sql.ErrNoRows) {
		err = s.store.DB.QueryRowContext(ctx, `SELECT mime FROM artwork WHERE id = ?`, blobID).Scan(&contentType)
		name = artwork.Path(s.cfg.Library, blobID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, blob.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f, err := os.Open(name)
	if err != nil {
		// The database knows the blob and the library hasn't got it.
		s.cfg.Log.Printf("blob %s: %v", blobID, err)
		return nil, blob.ErrNotFound
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &blob.Blob{Content: f, Type: contentType, ModTime: info.ModTime()}, nil
}

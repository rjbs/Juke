// Package jmap serves a Juke database over JMAP (RFC 8620): the session
// resource, the API endpoint, and blob download.  The object types are
// Track, Album and Playlist; see doc/types.md.
package jmap

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

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
	cfg     Config
	store   *store.Store
	methods map[string]method
	changed wakeup // woken when a /set commits
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
	s := &Server{cfg: cfg, store: cfg.Store}
	s.methods = s.methodTable()
	return s
}

// Handler returns the server's HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/jmap", s.handleSession)
	mux.HandleFunc("GET /jmap/session", s.handleSession)
	mux.HandleFunc("POST /jmap/api", s.handleAPI)
	mux.HandleFunc("GET /jmap/download/{accountId}/{blobId}/{name}", s.handleDownload)
	mux.HandleFunc("POST /jmap/upload/{accountId}/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "uploads are not supported", http.StatusForbidden)
	})
	mux.HandleFunc("GET /jmap/eventsource", s.handleEventSource)
	return mux
}

func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	epoch, err := s.store.Epoch(r.Context())
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	base := s.baseURL(r)
	session := map[string]any{
		"capabilities": map[string]any{
			CapCore: map[string]any{
				"maxSizeUpload":         0,
				"maxConcurrentUpload":   1,
				"maxSizeRequest":        s.cfg.MaxSizeRequest,
				"maxConcurrentRequests": 4,
				"maxCallsInRequest":     s.cfg.MaxCallsInRequest,
				"maxObjectsInGet":       s.cfg.MaxObjectsInGet,
				"maxObjectsInSet":       s.cfg.MaxObjectsInSet,
				"collationAlgorithms":   []string{"i;unicode-casemap"},
			},
			CapJuke:   map[string]any{},
			CapReview: map[string]any{},
		},
		"accounts": map[string]any{
			s.cfg.AccountID: map[string]any{
				"name":                s.cfg.Username,
				"isPersonal":          true,
				"isReadOnly":          false,
				"accountCapabilities": map[string]any{CapJuke: map[string]any{}, CapReview: map[string]any{}},
			},
		},
		"primaryAccounts": map[string]string{CapJuke: s.cfg.AccountID},
		"username":        s.cfg.Username,
		"apiUrl":          base + "/jmap/api",
		"downloadUrl":     base + "/jmap/download/{accountId}/{blobId}/{name}?type={type}",
		"uploadUrl":       base + "/jmap/upload/{accountId}/",
		"eventSourceUrl":  base + "/jmap/eventsource?types={types}&closeafter={closeafter}&ping={ping}",
		"state":           epoch,
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(session)
}

// handleDownload serves a blob: a track's audio, or an image of album
// art.  A blobId always names the same bytes, so the response may be
// cached forever; range requests come from http.ServeContent.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("accountId") != s.cfg.AccountID {
		http.NotFound(w, r)
		return
	}
	blobID := r.PathValue("blobId")
	var path, contentType string
	err := s.store.DB.QueryRowContext(r.Context(),
		`SELECT path, content_type FROM track WHERE blob_id = ?`, blobID,
	).Scan(&path, &contentType)
	name := filepath.Join(s.cfg.Library, filepath.FromSlash(path))
	if errors.Is(err, sql.ErrNoRows) {
		err = s.store.DB.QueryRowContext(r.Context(), `SELECT mime FROM artwork WHERE id = ?`, blobID).Scan(&contentType)
		name = artwork.Path(s.cfg.Library, blobID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	f, err := os.Open(name)
	if err != nil {
		s.cfg.Log.Printf("blob %s: %v", r.PathValue("blobId"), err)
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "cannot read blob", http.StatusInternalServerError)
		return
	}

	if t := r.URL.Query().Get("type"); t != "" {
		contentType = t
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, immutable, max-age=31536000")
	http.ServeContent(w, r, r.PathValue("name"), info.ModTime(), f)
}

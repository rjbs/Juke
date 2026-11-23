package jmap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"

	"github.com/rjbs/gizmo/auth"
	"github.com/rjbs/gizmo/httpd"
	gizmo "github.com/rjbs/gizmo/jmap"
	"github.com/rjbs/gizmo/push"
	"github.com/rjbs/gizmo/std"

	"semiotic.systems/juke/internal/store"
)

// The capabilities this server speaks.
const (
	CapCore = gizmo.CoreCapability
	CapJuke = "https://semiotic.systems/jmap/juke"

	// CapReview is for the review page: what might be wrong with an
	// album, the pictures it could have, and choosing its art.  A client
	// not using it sees Album as it is without; see doc/types.md.
	CapReview = "https://semiotic.systems/jmap/juke/review"
)

// reviewing says whether the request is using the review capability.
func reviewing(ctx context.Context) bool {
	return gizmo.Using(ctx, CapReview)
}

// methodError is a JMAP method-level error, returned as an "error"
// response in place of the method's own.
type methodError = gizmo.MethodError

func errInvalidArguments(format string, args ...any) *methodError {
	return gizmo.ArgError(format, args...)
}

func errorOf(typ, format string, args ...any) *methodError {
	return gizmo.Errorf(typ, format, args...)
}

// decodeArgs decodes a method's arguments, rejecting unknown ones.
func decodeArgs(raw json.RawMessage, into any) error {
	return gizmo.DecodeArgs(raw, into)
}

// A method takes its arguments, already resolved of result references,
// and returns the arguments of its response.
type method struct {
	capability string
	fn         func(c *gizmo.Call, s *Server, args json.RawMessage) (any, error)
}

// processor makes a Gizmo processor of the server's methods.  Every one
// of them is for the server's one account.
func (s *Server) processor() *gizmo.Processor {
	p := gizmo.NewProcessor()
	p.Logger = s.log
	p.AddCapability(CapReview)
	find := func(id string) (*Server, bool) { return s, id == s.cfg.AccountID }
	for name, m := range s.methodTable() {
		p.Register(name, m.capability, auth.ForAccount(find, func(s *Server, c *gizmo.Call, args json.RawMessage) (any, error) {
			return m.fn(c, s, args)
		}))
	}
	return p
}

// directory is the server's whole population: one user, who owns the one
// account, and is who every request acts as.  Who may make requests at
// all is decided before they get here, by the listener.
func (s *Server) directory() *auth.Directory {
	dir, err := auth.NewDirectory(
		map[string]*auth.User{s.cfg.Username: {Credentials: []auth.Credential{{Type: auth.CredLoopback}}}},
		map[string]*auth.Account{s.cfg.AccountID: {Owner: s.cfg.Username, Name: s.cfg.Username}},
	)
	if err != nil {
		// One user owning one account can't be a bad directory.
		panic(err)
	}
	return dir
}

// Capabilities is the account's accountCapabilities object.
func (s *Server) Capabilities() map[string]any {
	return map[string]any{CapJuke: map[string]any{}, CapReview: map[string]any{}}
}

var pushTypes = []string{store.TypeTrack, store.TypeAlbum, store.TypePlaylist, store.TypeSmartPlaylist}

// States gives the state of each type, for the event source.
func (s *Server) States(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	for _, t := range pushTypes {
		state, _, err := s.state(ctx, s.store.DB, t)
		if err != nil {
			return nil, err
		}
		out[t] = state
	}
	return out, nil
}

// publish tells the event source of whatever has changed.  It offers every
// type's state, and the broker passes on the ones that are news.
func (s *Server) publish(ctx context.Context) {
	states, err := s.States(ctx)
	if err != nil {
		s.cfg.Log.Printf("push: %v", err)
		return
	}
	s.broker.Publish(s.cfg.AccountID, states)
}

// Watch publishes the changes made by writers outside this server, such
// as an import, by looking for them every PushPollInterval.  It returns
// when ctx is done.
func (s *Server) Watch(ctx context.Context) {
	push.Watch(ctx, s.broker, s.cfg.AccountID, s.cfg.PushPollInterval, s.States)
}

// handler builds the JMAP handler: Gizmo's, over this server's methods and
// blobs.
func (s *Server) handler() *httpd.Server {
	return httpd.New(httpd.Config{
		BaseURL:   s.cfg.BaseURL,
		Processor: s.processor(),
		Directory: s.dir,
		Account: func(id string) httpd.Account {
			if id == s.cfg.AccountID {
				return s
			}
			return nil
		},
		Broker: s.broker,
		Limits: gizmo.Limits{
			MaxSizeUpload:       int64(s.cfg.MaxSizeUpload),
			MaxConcurrentUpload: 1,
			MaxSizeRequest:      int64(s.cfg.MaxSizeRequest),
			MaxCallsInRequest:   s.cfg.MaxCallsInRequest,
			MaxObjectsInGet:     s.cfg.MaxObjectsInGet,
			MaxObjectsInSet:     s.cfg.MaxObjectsInSet,
		},
		Capabilities: map[string]any{CapJuke: map[string]any{}, CapReview: map[string]any{}},
		Collations:   []string{"i;unicode-casemap"},
		Download:     http.HandlerFunc(s.handleDownload),
		Upload:       http.HandlerFunc(s.handleUpload),
		Logger:       s.log,
	})
}

// slogTo makes a structured logger that writes where a plain one does.
func slogTo(l *log.Logger) *slog.Logger {
	return slog.New(slog.NewTextHandler(l.Writer(), nil))
}

// txn is a transaction as Gizmo's standard methods know one.  A method
// that writes gets a real one.  A method that only reads gets the database
// itself, as reads here have always had: its statements each see what is
// committed when they run.
type txn struct {
	ctx context.Context
	s   *Server
	q   store.Queryer
	tx  *sql.Tx // nil when only reading
}

func (s *Server) begin(ctx context.Context, write bool) (*txn, error) {
	if !write {
		return &txn{ctx: ctx, s: s, q: s.store.DB}, nil
	}
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &txn{ctx: ctx, s: s, q: tx, tx: tx}, nil
}

func (t *txn) State(ctx context.Context, typ string) (string, error) {
	state, _, err := t.s.state(ctx, t.q, typ)
	return state, err
}

func (t *txn) Savepoint(ctx context.Context, fn func() error) error {
	if t.tx == nil {
		return fn()
	}
	if _, err := t.tx.ExecContext(ctx, "SAVEPOINT object"); err != nil {
		return err
	}
	err := fn()
	if err != nil {
		if _, undo := t.tx.ExecContext(ctx, "ROLLBACK TO object"); undo != nil {
			return undo
		}
	}
	if _, release := t.tx.ExecContext(ctx, "RELEASE object"); release != nil && err == nil {
		return release
	}
	return err
}

func (t *txn) Commit() error {
	if t.tx == nil {
		return errors.New("a transaction that only reads can't be committed")
	}
	if err := t.tx.Commit(); err != nil {
		return err
	}
	t.s.publish(t.ctx)
	return nil
}

func (t *txn) Rollback() error {
	if t.tx == nil {
		return nil
	}
	if err := t.tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return err
	}
	return nil
}

// limits are the limits the standard methods keep to.
func (s *Server) limits() std.Limits {
	return std.Limits{
		MaxObjectsInGet: s.cfg.MaxObjectsInGet,
		MaxObjectsInSet: s.cfg.MaxObjectsInSet,
		MaxQueryLimit:   s.cfg.MaxQueryLimit,
	}
}

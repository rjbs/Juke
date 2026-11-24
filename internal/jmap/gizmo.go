package jmap

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"

	"github.com/rjbs/gizmo/auth"
	"github.com/rjbs/gizmo/httpd"
	gizmo "github.com/rjbs/gizmo/jmap"
	"github.com/rjbs/gizmo/push"
	"github.com/rjbs/gizmo/sqlitekit"
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
	return store.Kit.States(ctx, s.store.DB)
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
		Guard:  s.guard,
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
		Blobs:        s,
		Logger:       s.log,
	})
}

// slogTo makes a structured logger that writes where a plain one does.
func slogTo(l *log.Logger) *slog.Logger {
	return slog.New(slog.NewTextHandler(l.Writer(), nil))
}

// txn is a transaction on the database, of the kind Gizmo's state kit
// makes: a *sql.Tx that also knows each type's state, and so is one the
// standard methods can run in.
type txn = sqlitekit.Tx

// begin begins a transaction.  One that writes tells the event source,
// when it commits, of the states it changed.
func (s *Server) begin(ctx context.Context, write bool) (*txn, error) {
	if !write {
		return store.Kit.Read(ctx, s.store.DB)
	}
	return store.Kit.Begin(ctx, s.store.DB, s.broker.Publisher(s.cfg.AccountID))
}

// limits are the limits the standard methods keep to.
func (s *Server) limits() std.Limits {
	return std.Limits{
		MaxObjectsInGet: s.cfg.MaxObjectsInGet,
		MaxObjectsInSet: s.cfg.MaxObjectsInSet,
		MaxQueryLimit:   s.cfg.MaxQueryLimit,
	}
}

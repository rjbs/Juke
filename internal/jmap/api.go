package jmap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// The capabilities this server speaks.
const (
	CapCore = "urn:ietf:params:jmap:core"
	CapJuke = "https://semiotic.systems/jmap/juke"

	// CapReview is for the review page: what might be wrong with an
	// album, the pictures it could have, and choosing its art.  A client
	// not using it sees Album as it is without; see doc/types.md.
	CapReview = "https://semiotic.systems/jmap/juke/review"
)

// usingKey carries the request's capabilities in a context, for the
// methods whose behaviour depends on them.
type usingKey struct{}

// reviewing says whether the request is using the review capability.
func reviewing(ctx context.Context) bool {
	using, _ := ctx.Value(usingKey{}).(map[string]bool)
	return using[CapReview]
}

// methodError is a JMAP method-level error, returned as an "error"
// response in place of the method's own.
type methodError struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Properties  []string `json:"properties,omitempty"`
}

func (e *methodError) Error() string { return e.Type + ": " + e.Description }

func errInvalidArguments(format string, args ...any) *methodError {
	return &methodError{Type: "invalidArguments", Description: fmt.Sprintf(format, args...)}
}

func errorOf(typ, format string, args ...any) *methodError {
	return &methodError{Type: typ, Description: fmt.Sprintf(format, args...)}
}

// A method takes its arguments, already resolved of result references,
// and returns the arguments of its response.
type method struct {
	capability string
	fn         func(ctx context.Context, s *Server, args json.RawMessage) (any, error)
}

// invocation is one method call or response: [name, arguments, callId].
type invocation struct {
	Name   string
	Args   json.RawMessage
	CallID string
}

func (i invocation) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{i.Name, i.Args, i.CallID})
}

func (i *invocation) UnmarshalJSON(b []byte) error {
	var parts []json.RawMessage
	if err := json.Unmarshal(b, &parts); err != nil || len(parts) != 3 {
		return errors.New("an invocation is a three-element array")
	}
	if err := json.Unmarshal(parts[0], &i.Name); err != nil {
		return errors.New("an invocation's name must be a string")
	}
	if err := json.Unmarshal(parts[2], &i.CallID); err != nil {
		return errors.New("an invocation's call id must be a string")
	}
	if len(parts[1]) == 0 || parts[1][0] != '{' {
		return errors.New("an invocation's arguments must be an object")
	}
	i.Args = parts[1]
	return nil
}

type apiRequest struct {
	Using       []string          `json:"using"`
	MethodCalls []invocation      `json:"methodCalls"`
	CreatedIDs  map[string]string `json:"createdIds,omitempty"`
}

type apiResponse struct {
	MethodResponses []invocation      `json:"methodResponses"`
	CreatedIDs      map[string]string `json:"createdIds,omitempty"`
	SessionState    string            `json:"sessionState"`
}

// requestError writes a request-level error, an RFC 7807 problem
// document, as RFC 8620 section 3.6.1 describes.
func requestError(w http.ResponseWriter, typ, detail string, extra map[string]any) {
	body := map[string]any{
		"type":   "urn:ietf:params:jmap:error:" + typ,
		"status": http.StatusBadRequest,
		"detail": detail,
	}
	for k, v := range extra {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(body)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(s.cfg.MaxSizeRequest)+1))
	if err != nil {
		requestError(w, "notRequest", "could not read the request body", nil)
		return
	}
	if len(body) > s.cfg.MaxSizeRequest {
		requestError(w, "limit", "the request is too large", map[string]any{"limit": "maxSizeRequest"})
		return
	}
	if !json.Valid(body) {
		requestError(w, "notJSON", "the request body is not valid JSON", nil)
		return
	}
	var req apiRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.Using == nil || req.MethodCalls == nil {
		detail := "the request is not a JMAP Request object"
		if err != nil {
			detail += ": " + err.Error()
		}
		requestError(w, "notRequest", detail, nil)
		return
	}
	using := map[string]bool{}
	for _, c := range req.Using {
		if c != CapCore && c != CapJuke && c != CapReview {
			requestError(w, "unknownCapability", "unknown capability "+c, nil)
			return
		}
		using[c] = true
	}
	if len(req.MethodCalls) > s.cfg.MaxCallsInRequest {
		requestError(w, "limit", "too many method calls", map[string]any{"limit": "maxCallsInRequest"})
		return
	}

	epoch, err := s.store.Epoch(r.Context())
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	resp := apiResponse{MethodResponses: []invocation{}, CreatedIDs: req.CreatedIDs, SessionState: epoch}
	ctx := context.WithValue(r.Context(), usingKey{}, using)
	for _, call := range req.MethodCalls {
		resp.MethodResponses = append(resp.MethodResponses, s.invoke(ctx, call, using, resp.MethodResponses))
	}

	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(resp)
}

func (s *Server) invoke(ctx context.Context, call invocation, using map[string]bool, prior []invocation) invocation {
	fail := func(err error) invocation {
		var me *methodError
		if !errors.As(err, &me) {
			s.cfg.Log.Printf("%s: %v", call.Name, err)
			me = errorOf("serverFail", "internal error")
		}
		b, _ := json.Marshal(me)
		return invocation{Name: "error", Args: b, CallID: call.CallID}
	}

	m, ok := s.methods[call.Name]
	if !ok || !using[m.capability] {
		return fail(errorOf("unknownMethod", "no method %s with the capabilities in use", call.Name))
	}
	args, err := resolveReferences(call.Args, prior)
	if err != nil {
		return fail(err)
	}
	result, err := m.fn(ctx, s, args)
	if err != nil {
		return fail(err)
	}
	b, err := json.Marshal(result)
	if err != nil {
		return fail(err)
	}
	return invocation{Name: call.Name, Args: b, CallID: call.CallID}
}

// decodeArgs decodes a method's arguments, rejecting unknown ones.
func decodeArgs(raw json.RawMessage, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return errInvalidArguments("%v", err)
	}
	return nil
}

// resolveReferences replaces each "#name" argument, a ResultReference,
// with the value it points at in an earlier response; see RFC 8620
// section 3.7.
func resolveReferences(raw json.RawMessage, prior []invocation) (json.RawMessage, error) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errInvalidArguments("arguments must be an object")
	}
	changed := false
	for key, val := range args {
		if !strings.HasPrefix(key, "#") {
			continue
		}
		name := key[1:]
		if _, ok := args[name]; ok {
			return nil, errInvalidArguments("both %s and #%s given", name, name)
		}
		var ref struct {
			ResultOf string `json:"resultOf"`
			Name     string `json:"name"`
			Path     string `json:"path"`
		}
		if err := json.Unmarshal(val, &ref); err != nil {
			return nil, errorOf("invalidResultReference", "malformed reference in #%s", name)
		}
		var target *invocation
		for i := range prior {
			if prior[i].CallID == ref.ResultOf {
				target = &prior[i]
			}
		}
		if target == nil || target.Name != ref.Name {
			return nil, errorOf("invalidResultReference", "no %s response with call id %q", ref.Name, ref.ResultOf)
		}
		var doc any
		if err := json.Unmarshal(target.Args, &doc); err != nil {
			return nil, err
		}
		value, err := evaluatePointer(doc, ref.Path)
		if err != nil {
			return nil, errorOf("invalidResultReference", "%s: %v", ref.Path, err)
		}
		b, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		delete(args, key)
		args[name] = b
		changed = true
	}
	if !changed {
		return raw, nil
	}
	return json.Marshal(args)
}

// evaluatePointer evaluates a JSON Pointer with JMAP's extension: a "*"
// token applies the rest of the pointer to every element of an array,
// flattening the results when they are arrays themselves.
func evaluatePointer(doc any, pointer string) (any, error) {
	if pointer == "" {
		return doc, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, errors.New("a pointer must start with /")
	}
	return walkPointer(doc, strings.Split(pointer[1:], "/"))
}

func walkPointer(doc any, tokens []string) (any, error) {
	if len(tokens) == 0 {
		return doc, nil
	}
	tok := strings.NewReplacer("~1", "/", "~0", "~").Replace(tokens[0])
	switch v := doc.(type) {
	case map[string]any:
		next, ok := v[tok]
		if !ok {
			return nil, fmt.Errorf("no member %q", tok)
		}
		return walkPointer(next, tokens[1:])
	case []any:
		if tok == "*" {
			out := []any{}
			for _, item := range v {
				got, err := walkPointer(item, tokens[1:])
				if err != nil {
					return nil, err
				}
				if arr, ok := got.([]any); ok {
					out = append(out, arr...)
				} else {
					out = append(out, got)
				}
			}
			return out, nil
		}
		i, err := strconv.Atoi(tok)
		if err != nil || i < 0 || i >= len(v) {
			return nil, fmt.Errorf("no element %q", tok)
		}
		return walkPointer(v[i], tokens[1:])
	default:
		return nil, fmt.Errorf("cannot descend into a scalar at %q", tok)
	}
}

package jmap

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"semiotic.systems/juke/internal/artwork"
)

// handleUpload is the JMAP upload endpoint (RFC 8620 section 6.1).  For
// now an upload must be an image, and goes straight into the artwork
// store: its blob id is the image's content id, so the same picture
// uploaded twice is one image, and Album/set and Track/set can take the
// id as artworkBlobId at once.  Anything but an image is refused with
// 415.  An upload nobody sets is removed by `juke gc` after a grace
// period; see doc/server.md.  Other kinds of upload, such as audio to
// import, will need a staging area of their own.  -- claude, 2026-11-02
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("accountId") != s.cfg.AccountID {
		http.Error(w, "no such account", http.StatusNotFound)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, int64(s.cfg.MaxSizeUpload)+1))
	if err != nil {
		http.Error(w, "could not read the upload", http.StatusBadRequest)
		return
	}
	if len(data) > s.cfg.MaxSizeUpload {
		http.Error(w, "the upload is larger than maxSizeUpload", http.StatusRequestEntityTooLarge)
		return
	}
	if !strings.HasPrefix(http.DetectContentType(data), "image/") {
		http.Error(w, "only images can be uploaded", http.StatusUnsupportedMediaType)
		return
	}
	now := s.cfg.Now()
	img, err := artwork.Store(r.Context(), s.store.DB, s.cfg.Library, data, &now)
	if err != nil {
		if strings.HasPrefix(err.Error(), "not an image") {
			http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
			return
		}
		s.cfg.Log.Printf("upload: %v", err)
		http.Error(w, "could not store the upload", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"accountId": s.cfg.AccountID,
		"blobId":    img.ID,
		"type":      img.Mime,
		"size":      img.Size,
	})
}

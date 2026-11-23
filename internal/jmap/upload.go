package jmap

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/rjbs/gizmo/blob"

	"semiotic.systems/juke/internal/artwork"
)

// Put stores an upload, for the JMAP upload endpoint (RFC 8620 section
// 6.1).  It is half of what makes the server a blob.Store.
//
// For now an upload must be an image, and goes straight into the artwork
// store: its blob id is the image's content id, so the same picture
// uploaded twice is one image, and Album/set and Track/set can take the
// id as artworkBlobId at once.  Anything but an image is refused with
// 415.  An upload nobody sets is removed by `juke gc` after a grace
// period; see doc/server.md.  Other kinds of upload, such as audio to
// import, will need a staging area of their own.  -- claude, 2026-11-02
//
// An image is read whole, since it has to be decoded to be stored, and
// the server's limit on uploads keeps it small.
func (s *Server) Put(ctx context.Context, up *blob.Upload) (*blob.Uploaded, error) {
	data, err := io.ReadAll(up.Body)
	if err != nil {
		return nil, err
	}
	notImage := &blob.Refusal{Status: http.StatusUnsupportedMediaType, Detail: "only images can be uploaded"}
	if !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return nil, notImage
	}
	now := s.cfg.Now()
	img, err := artwork.Store(ctx, s.store.DB, s.cfg.Library, data, &now)
	if err != nil {
		if strings.HasPrefix(err.Error(), "not an image") {
			notImage.Detail = err.Error()
			return nil, notImage
		}
		return nil, err
	}
	return &blob.Uploaded{BlobID: img.ID, Type: img.Mime, Size: int64(img.Size)}, nil
}

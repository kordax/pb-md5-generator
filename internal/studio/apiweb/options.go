// Package apiweb adapts the managed content and authentication services to HTTP.
package apiweb

import (
	"context"
	"io/fs"
	"net/http"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

type Options struct {
	BasePath   string
	PublicURL  string
	Auth       *auth.Service
	Content    *content.Service
	Assets     fs.FS
	Contract   []byte
	Preview    func(context.Context, Document, []byte, *auth.User) (Preview, error)
	Publishing func(context.Context) (PublishingPolicy, error)
	Appearance http.Handler
	Reader     http.Handler
}

type Document struct {
	ID                string `json:"id"`
	Path              string `json:"path"`
	Title             string `json:"title"`
	Kind              string `json:"kind"`
	OutputPath        string `json:"outputPath,omitempty"`
	Revision          string `json:"revision"`
	PublishedRevision string `json:"publishedRevision,omitempty"`
	Content           string `json:"content,omitempty"`
	Draft             bool   `json:"draft,omitempty"`
	DraftID           string `json:"draftId,omitempty"`
	DraftRevision     string `json:"draftRevision,omitempty"`
	BaseRevision      string `json:"baseRevision,omitempty"`
	CanWrite          bool   `json:"canWrite"`
}

type Diagnostic struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Severity string `json:"severity,omitempty"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
}

type Preview struct {
	HTML        string       `json:"html"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type principalKey struct{}
type principalResolver func(context.Context) (*auth.User, error)

// Principal rechecks a request's session against current authentication state.
// A nil user and nil error identify a trusted context outside the HTTP adapter.
func Principal(ctx context.Context) (*auth.User, error) {
	resolve, _ := ctx.Value(principalKey{}).(principalResolver)
	if resolve == nil {
		return nil, nil
	}
	return resolve(ctx)
}

// New validates dependencies and constructs the CMS HTTP handler.
func New(options Options) (http.Handler, error) { return newHTTPHandler(options) }

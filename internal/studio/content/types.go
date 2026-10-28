// Package content manages isolated documentation sources, persistent drafts and publication.
package content

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid content operation")
	ErrConflict    = errors.New("content revision conflict")
	ErrNotFound    = errors.New("content not found")
	ErrLimit       = errors.New("content resource limit exceeded")
	ErrOwnership   = errors.New("content ownership mismatch")
	ErrUnsupported = errors.New("storage operation is unavailable")
)

const (
	MaxTextBytes    = 4 << 20
	MaxBinaryBytes  = 16 << 20
	MaxProjectBytes = 256 << 20
	MaxStateBytes   = 256 << 20
	MaxFiles        = 4096
	MaxDrafts       = 128
	MaxHistory      = 256
	MaxChanges      = 32
)

type Actor struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type Config struct {
	Root             string
	StateDir         string
	OutputDir        string
	Initialize       bool
	PushOnPublish    bool
	OperationTimeout time.Duration
	Compile          func(context.Context, string, string) error
	Validate         func(context.Context, string, []Change, Actor) error
	AuthorizeStorage func(context.Context, Actor) error
	Storage          Backend
}

type Entry struct {
	ID         string `json:"id,omitempty"`
	Path       string `json:"path"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`
	OutputPath string `json:"outputPath,omitempty"`
	Revision   string `json:"revision"`
	Directory  bool   `json:"directory"`
	Size       int64  `json:"size"`
}

type Project struct {
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	Revision    string  `json:"revision"`
	Documents   []Entry `json:"documents"`
	Files       []Entry `json:"files"`
}

type File struct {
	Entry
	Content string `json:"content,omitempty"`
	Data    []byte `json:"data,omitempty"`
	Binary  bool   `json:"binary"`
}

type Change struct {
	Operation  string  `json:"operation"`
	Path       string  `json:"path"`
	To         string  `json:"to,omitempty"`
	Title      string  `json:"title,omitempty"`
	Kind       string  `json:"kind,omitempty"`
	OutputPath string  `json:"outputPath,omitempty"`
	DocumentID string  `json:"documentId,omitempty"`
	Content    *string `json:"content,omitempty"`
	Data       []byte  `json:"data,omitempty"`
}

type DraftInput struct {
	ID           string   `json:"id,omitempty"`
	Revision     string   `json:"revision,omitempty"`
	BaseRevision string   `json:"baseRevision"`
	Change       Change   `json:"change"`
	Changes      []Change `json:"changes,omitempty"`
}

type Draft struct {
	ID           string    `json:"id"`
	Revision     string    `json:"revision"`
	BaseRevision string    `json:"baseRevision"`
	Branch       string    `json:"branch,omitempty"`
	Change       Change    `json:"change"`
	Changes      []Change  `json:"changes,omitempty"`
	Actor        Actor     `json:"actor"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type PublishInput struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	Message  string `json:"message,omitempty"`
	Push     bool   `json:"push"`
}

type PublishResult struct {
	Revision  string     `json:"revision"`
	Commit    string     `json:"commit,omitempty"`
	Published bool       `json:"published"`
	Sync      SyncStatus `json:"sync"`
}

type Revision struct {
	ID         string    `json:"id"`
	Path       string    `json:"path"`
	Branch     string    `json:"branch,omitempty"`
	Revision   string    `json:"revision"`
	Actor      Actor     `json:"actor"`
	CreatedAt  time.Time `json:"createdAt"`
	Deleted    bool      `json:"deleted"`
	Size       int64     `json:"size"`
	Title      string    `json:"title,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Output     string    `json:"outputPath,omitempty"`
	DocumentID string    `json:"documentId,omitempty"`
}

type Branch struct {
	Name    string `json:"name"`
	Head    string `json:"head"`
	Current bool   `json:"current"`
}

type SyncStatus struct {
	Backend   string `json:"backend"`
	Branch    string `json:"branch,omitempty"`
	Head      string `json:"head,omitempty"`
	State     string `json:"state"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	Dirty     bool   `json:"dirty"`
	Retryable bool   `json:"retryable"`
	Error     string `json:"error,omitempty"`
}

// Backend synchronization never receives arbitrary shell commands or remote URLs.
// Paths passed to Commit are the explicit files in a successful local publication.
type Backend interface {
	Status(context.Context) (SyncStatus, error)
	Branches(context.Context) ([]Branch, error)
	CreateBranch(context.Context, string) error
	Checkout(context.Context, string) error
	Fetch(context.Context) error
	Pull(context.Context) error
	RestoreHead(context.Context, string) error
	Commit(context.Context, []string, Actor, string) (string, error)
	Push(context.Context) error
}

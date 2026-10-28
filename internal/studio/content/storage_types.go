package content

import "time"

const MaxStorageRemotes = 8

// StorageRemote names a connection. Credentials remain in the private SSH
// configuration, outside documentation and public API responses.
type StorageRemote struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Push bool   `json:"push"`
}

type StorageSettings struct {
	Revision   string          `json:"revision"`
	Backend    string          `json:"backend"`
	PullRemote string          `json:"pullRemote"`
	Remotes    []StorageRemote `json:"remotes"`
}

type StorageUpdate struct {
	Revision      string          `json:"revision"`
	BaseRevision  string          `json:"baseRevision"`
	Backend       string          `json:"backend"`
	InitialBranch string          `json:"initialBranch,omitempty"`
	PullRemote    string          `json:"pullRemote"`
	Remotes       []StorageRemote `json:"remotes"`
}

type RemoteSyncStatus struct {
	Name           string `json:"name"`
	Branch         string `json:"branch,omitempty"`
	Head           string `json:"head,omitempty"`
	LastPushedHead string `json:"lastPushedHead,omitempty"`
	State          string `json:"state"`
	Retryable      bool   `json:"retryable"`
	Error          string `json:"error,omitempty"`
}

type StorageResult struct {
	Settings        StorageSettings    `json:"settings"`
	ProjectRevision string             `json:"projectRevision"`
	Sync            SyncStatus         `json:"sync"`
	Remotes         []RemoteSyncStatus `json:"remotes"`
}

// ManagedStorageConfig supplies first-start defaults. Persisted settings win on
// subsequent starts. Construction performs no Git or filesystem mutation;
// Service.New recovers the backend only after acquiring all lifetime locks.
type ManagedStorageConfig struct {
	Root           string
	StateDir       string
	InitialBackend string
	InitialRemote  string
	Timeout        time.Duration
}

type storedStorage struct {
	Format string `json:"format"`
	StorageSettings
	LocalHistoryBranch string `json:"localHistoryBranch,omitempty"`
}

type storedRemoteStatus struct {
	URL string `json:"url"`
	RemoteSyncStatus
}

type remoteJournal struct {
	Format string               `json:"format"`
	States []storedRemoteStatus `json:"states"`
}

type storageTransaction struct {
	Format       string        `json:"format"`
	ID           string        `json:"id"`
	Old          storedStorage `json:"old"`
	New          storedStorage `json:"new"`
	OldGitConfig []byte        `json:"oldGitConfig,omitempty"`
	NewGitConfig []byte        `json:"newGitConfig"`
	Enrolling    bool          `json:"enrolling"`
}

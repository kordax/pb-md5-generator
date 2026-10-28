package content

const (
	MaxDeleteTreeEntries        = 1024
	MaxDeleteTreeFiles          = 127
	MaxDeleteTreeBytes          = 64 << 20
	MaxDeleteTreePreviewEntries = 100
)

type DeleteTreeInput struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
}

type DeleteTreeEntry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
}

// Counts describe the entire authorized subtree, including its root directory.
// Entries is a deterministic preview only; PlanRevision binds the full scope.
type DeleteTreePlan struct {
	Path           string            `json:"path"`
	Revision       string            `json:"revision"`
	PlanRevision   string            `json:"planRevision"`
	FileCount      int               `json:"fileCount"`
	DirectoryCount int               `json:"directoryCount"`
	DocumentCount  int               `json:"documentCount"`
	TotalBytes     int64             `json:"totalBytes"`
	Entries        []DeleteTreeEntry `json:"entries"`
	Truncated      bool              `json:"truncated"`
}

package apiweb

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path"
	"strings"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
)

type fileDTO struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
	Revision  string `json:"revision"`
	CanWrite  bool   `json:"canWrite"`
}

func queryPath(r *http.Request) (string, bool) {
	values := r.URL.Query()
	items := values["path"]
	if len(values) != 1 || len(items) != 1 || !validSourcePath(items[0]) {
		return "", false
	}
	return items[0], true
}

func (h *handler) files(w http.ResponseWriter, r *http.Request, user *auth.User) {
	switch r.URL.Path {
	case "/files":
		if !allowMethod(w, r, "GET, HEAD, POST") {
			return
		}
		if r.Method == http.MethodPost {
			var input struct {
				Path     string `json:"path"`
				Revision string `json:"revision"`
			}
			if decode(w, r, &input) {
				h.stage(w, r, user, content.Change{Operation: "mkdir", Path: input.Path}, input.Revision, nil)
			}
			return
		}
		h.listFiles(w, r, user)
	case "/files/move":
		if !allowMethod(w, r, "POST") {
			return
		}
		var input struct {
			Path     string `json:"path"`
			To       string `json:"to"`
			Revision string `json:"revision"`
		}
		if decode(w, r, &input) {
			h.stage(w, r, user, content.Change{Operation: "move", Path: input.Path, To: input.To}, input.Revision, nil)
		}
	case "/files/delete-preview", "/files/delete-tree":
		h.deleteTree(w, r, user)
	case "/files/content":
		h.fileContent(w, r)
	}
}

func (h *handler) listFiles(w http.ResponseWriter, r *http.Request, user *auth.User) {
	values := r.URL.Query()
	items := values["path"]
	if len(values) > 1 || len(items) > 1 {
		serviceError(w, content.ErrInvalid)
		return
	}
	folder := "."
	if len(items) == 1 {
		folder = items[0]
	}
	if folder != "." && !validSourcePath(folder) {
		serviceError(w, content.ErrInvalid)
		return
	}
	project, err := h.opts.Content.Project(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	result := []fileDTO{}
	for _, entry := range project.Files {
		if path.Dir(entry.Path) != folder {
			continue
		}
		visible := permitted(user, auth.ActionRead, entry.Path)
		if entry.Directory && !visible {
			for _, child := range project.Files {
				if !child.Directory && strings.HasPrefix(child.Path, entry.Path+"/") && permitted(user, auth.ActionRead, child.Path) {
					visible = true
					break
				}
			}
		}
		if visible {
			result = append(result, fileDTO{Path: entry.Path, Name: path.Base(entry.Path), Directory: entry.Directory, Size: entry.Size, Revision: project.Revision, CanWrite: permitted(user, auth.ActionWrite, entry.Path)})
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Files    []fileDTO `json:"files"`
		Revision string    `json:"revision"`
	}{result, project.Revision})
}

func (h *handler) deleteFile(w http.ResponseWriter, r *http.Request, user *auth.User, name string) {
	project, err := h.opts.Content.Project(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	operation, found := "delete", false
	for _, entry := range project.Files {
		if entry.Path == name {
			found = true
			if entry.Directory {
				operation = "rmdir"
			}
		}
	}
	if !found {
		serviceError(w, content.ErrNotFound)
		return
	}
	if operation == "rmdir" {
		for _, entry := range project.Files {
			if strings.HasPrefix(entry.Path, name+"/") {
				writeError(w, http.StatusConflict, "folder_not_empty", "Remove the folder's files and subfolders before removing the folder.", project.Revision, nil)
				return
			}
		}
	}
	h.stage(w, r, user, content.Change{Operation: operation, Path: name}, ifMatch(r), nil)
}

func (h *handler) download(w http.ResponseWriter, r *http.Request, name string) {
	file, err := h.opts.Content.Read(r.Context(), name)
	if err != nil {
		serviceError(w, err)
		return
	}
	data := file.Data
	if !file.Binary {
		data = []byte(file.Content)
	}
	if _, ok := h.filePrincipal(w, r, name, false); !ok {
		return
	}
	digest := sha256.Sum256(data)
	w.Header().Set("X-Source-Revision", hex.EncodeToString(digest[:]))
	writeFileDownload(w, r, name, data, file.Revision)
}

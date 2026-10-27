package apiweb

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
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
	case "/files/content":
		if !allowMethod(w, r, "GET, HEAD, PUT, DELETE") {
			return
		}
		name, ok := queryPath(r)
		if !ok {
			serviceError(w, content.ErrInvalid)
			return
		}
		if !permitted(user, auth.ActionRead, name) {
			serviceError(w, content.ErrNotFound)
			return
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			h.download(w, r, name)
			return
		}
		if !permitted(user, auth.ActionWrite, name) {
			serviceError(w, auth.ErrForbidden)
			return
		}
		if r.Method == http.MethodDelete {
			h.deleteFile(w, r, user, name)
			return
		}
		if !revision(ifMatch(r)) {
			serviceError(w, content.ErrInvalid)
			return
		}
		media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/octet-stream" || len(params) != 0 || r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity" {
			writeError(w, http.StatusUnsupportedMediaType, "invalid_request", "Upload original bytes with Content-Type application/octet-stream.", "", nil)
			return
		}
		if r.ContentLength > content.MaxBinaryBytes {
			serviceError(w, content.ErrLimit)
			return
		}
		defer r.Body.Close()
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, content.MaxBinaryBytes))
		if err != nil {
			var maximum *http.MaxBytesError
			if errors.As(err, &maximum) {
				serviceError(w, content.ErrLimit)
			} else {
				serviceError(w, content.ErrInvalid)
			}
			return
		}
		if err := r.Context().Err(); err != nil {
			serviceError(w, err)
			return
		}
		h.stage(w, r, user, content.Change{Operation: "write", Path: name, Data: data}, ifMatch(r), nil)
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
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(name)}))
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("ETag", "\""+file.Revision+"\"")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

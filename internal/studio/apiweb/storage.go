package apiweb

import (
	"net/http"
	"strings"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/kordax/pb-md5-generator/internal/studio/httpjson"
)

const maxStorageJSONBody = 128 << 10

type storageRemoteRequest struct {
	Name *string `json:"name"`
	URL  *string `json:"url"`
	Push *bool   `json:"push"`
}

type storageUpdateRequest struct {
	Revision      *string                 `json:"revision"`
	BaseRevision  *string                 `json:"baseRevision"`
	Backend       *string                 `json:"backend"`
	InitialBranch string                  `json:"initialBranch,omitempty"`
	PullRemote    *string                 `json:"pullRemote"`
	Remotes       *[]storageRemoteRequest `json:"remotes"`
}

func storageAccess(user *auth.User) bool {
	return auth.Can(user, auth.ActionAdmin, "") && auth.Can(user, auth.ActionRead, ".") && auth.Can(user, auth.ActionWrite, ".")
}

func decodeStorage(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := httpjson.Decode(w, r, target, maxStorageJSONBody); err != nil {
		writeError(w, httpjson.Status(err), "invalid_request", "Send one valid JSON object using the documented fields and resource limits.", "", nil)
		return false
	}
	return true
}

func (input storageUpdateRequest) update() (content.StorageUpdate, bool) {
	if input.Revision == nil || input.BaseRevision == nil || input.Backend == nil || input.PullRemote == nil || input.Remotes == nil || !revision(*input.Revision) || !revision(*input.BaseRevision) || len(*input.Remotes) > content.MaxStorageRemotes {
		return content.StorageUpdate{}, false
	}
	update := content.StorageUpdate{Revision: *input.Revision, BaseRevision: *input.BaseRevision, Backend: *input.Backend, InitialBranch: input.InitialBranch, PullRemote: *input.PullRemote, Remotes: []content.StorageRemote{}}
	for _, remote := range *input.Remotes {
		if remote.Name == nil || remote.URL == nil || remote.Push == nil {
			return content.StorageUpdate{}, false
		}
		update.Remotes = append(update.Remotes, content.StorageRemote{Name: *remote.Name, URL: *remote.URL, Push: *remote.Push})
	}
	return update, true
}

func (h *handler) managedStorage(w http.ResponseWriter, r *http.Request, user *auth.User) {
	if !storageAccess(user) {
		serviceError(w, auth.ErrForbidden)
		return
	}
	if r.URL.RawQuery != "" {
		serviceError(w, content.ErrInvalid)
		return
	}
	if r.URL.Path == "/storage/configuration" {
		h.storageConfiguration(w, r, user)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[0] == "storage" && parts[1] == "remotes" && parts[2] != "" && (parts[3] == "check" || parts[3] == "push") {
		h.storageRemote(w, r, user, parts[2], parts[3] == "push")
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "This resource was not found.", "", nil)
}

func (h *handler) storageConfiguration(w http.ResponseWriter, r *http.Request, user *auth.User) {
	if !allowMethod(w, r, "GET, HEAD, PUT") {
		return
	}
	var result content.StorageResult
	var err error
	if r.Method == http.MethodPut {
		var input storageUpdateRequest
		if !decodeStorage(w, r, &input) {
			return
		}
		update, valid := input.update()
		if !valid {
			serviceError(w, content.ErrInvalid)
			return
		}
		result, err = h.opts.Content.ConfigureStorage(r.Context(), update, actor(user))
	} else {
		result, err = h.opts.Content.Storage(r.Context())
	}
	if err != nil {
		// Storage settings and source content have independent revisions. A
		// project revision must not be reported as the current settings revision.
		serviceError(w, err)
		return
	}
	result = publicStorageResult(result)
	w.Header().Set("ETag", "\""+result.Settings.Revision+"\"")
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) storageRemote(w http.ResponseWriter, r *http.Request, user *auth.User, name string, push bool) {
	if !allowMethod(w, r, "POST") {
		return
	}
	var settingsRevision, projectRevision string
	if push {
		var input struct {
			Revision     *string `json:"revision"`
			BaseRevision *string `json:"baseRevision"`
		}
		if !decodeStorage(w, r, &input) {
			return
		}
		if input.Revision == nil || input.BaseRevision == nil || !revision(*input.Revision) || !revision(*input.BaseRevision) {
			serviceError(w, content.ErrInvalid)
			return
		}
		settingsRevision, projectRevision = *input.Revision, *input.BaseRevision
	} else {
		var input struct {
			Revision *string `json:"revision"`
		}
		if !decodeStorage(w, r, &input) {
			return
		}
		if input.Revision == nil || !revision(*input.Revision) {
			serviceError(w, content.ErrInvalid)
			return
		}
		settingsRevision = *input.Revision
	}
	var result content.RemoteSyncStatus
	var err error
	if push {
		result, err = h.opts.Content.PushRemote(r.Context(), name, projectRevision, settingsRevision, actor(user))
	} else {
		result, err = h.opts.Content.CheckRemote(r.Context(), name, settingsRevision, actor(user))
	}
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, publicRemoteStatus(result))
}

func publicRemoteStatus(status content.RemoteSyncStatus) content.RemoteSyncStatus {
	if status.Error != "" {
		status.Error = "The remote operation failed. Check its configuration and access."
	}
	return status
}

func publicStorageResult(result content.StorageResult) content.StorageResult {
	result.Settings.Remotes = append([]content.StorageRemote{}, result.Settings.Remotes...)
	result.Remotes = append([]content.RemoteSyncStatus{}, result.Remotes...)
	for index, status := range result.Remotes {
		result.Remotes[index] = publicRemoteStatus(status)
	}
	if result.Sync.Error != "" {
		result.Sync.Error = "The storage operation failed. Check its configuration and access."
	}
	return result
}

package appearance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kordax/pb-md5-generator/document"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/kordax/pb-md5-generator/internal/studio/httpjson"
)

type themeInput struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Mode        string                 `json:"mode"`
	Accent      string                 `json:"accent"`
	Font        string                 `json:"font"`
	Density     string                 `json:"density"`
	Width       string                 `json:"width"`
	Radius      int                    `json:"radius"`
	Light       *document.ThemePalette `json:"light,omitempty"`
	Dark        *document.ThemePalette `json:"dark,omitempty"`
	Revision    string                 `json:"revision,omitempty"`
}

func (value themeInput) theme() document.Theme {
	return document.Theme{ID: value.ID, Name: value.Name, Description: value.Description, Mode: value.Mode, Accent: value.Accent, Font: value.Font, Density: value.Density, Width: value.Width, Radius: value.Radius, Light: value.Light, Dark: value.Dark}
}

// Handler expects the stripped /settings and /themes paths. Authentication and
// unsafe-request origin/CSRF checks also apply when it is mounted independently.
func (s *Service) Handler(authentication *auth.Service) http.Handler {
	if authentication == nil {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			failure(writer, http.StatusServiceUnavailable, "unavailable", "settings authentication is unavailable")
		})
	}
	return authentication.Require(auth.RoleViewer, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		user := auth.RequestUser(request)
		if request.URL.RawQuery != "" {
			failure(writer, http.StatusBadRequest, "invalid_request", "settings endpoints do not accept query parameters")
			return
		}
		switch {
		case request.URL.Path == "/settings":
			s.settingsHTTP(writer, request, user)
		case request.URL.Path == "/themes":
			s.themesHTTP(writer, request, user)
		case strings.HasPrefix(request.URL.Path, "/themes/") && strings.Count(request.URL.Path, "/") == 2:
			s.deleteHTTP(writer, request, user)
		default:
			failure(writer, http.StatusNotFound, "not_found", "settings endpoint not found")
		}
	}))
}

func permitted(user *auth.User, name string) bool {
	return auth.Can(user, auth.ActionAdmin, "") && auth.Can(user, auth.ActionWrite, "docs.json") && auth.Can(user, auth.ActionWrite, name)
}

func actor(user *auth.User) content.Actor {
	return content.Actor{ID: user.ID, Name: user.DisplayName}
}

func (s *Service) settingsHTTP(writer http.ResponseWriter, request *http.Request, user *auth.User) {
	switch request.Method {
	case http.MethodGet:
		settings, err := s.Read(request.Context())
		if err != nil {
			serviceFailure(writer, err)
			return
		}
		writer.Header().Set("ETag", `"`+settings.Revision+`"`)
		writeJSON(writer, http.StatusOK, settings)
	case http.MethodPut:
		if !permitted(user, s.manifest) {
			failure(writer, http.StatusForbidden, "forbidden", "administrator access and writable settings are required")
			return
		}
		var input Settings
		if err := httpjson.Decode(writer, request, &input, 32<<10); err != nil {
			decodeFailure(writer, err)
			return
		}
		settings, err := s.Update(request.Context(), input, actor(user))
		if err != nil {
			serviceFailure(writer, err)
			return
		}
		writer.Header().Set("ETag", `"`+settings.Revision+`"`)
		writeJSON(writer, http.StatusOK, settings)
	default:
		writer.Header().Set("Allow", "GET, PUT")
		failure(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Service) themesHTTP(writer http.ResponseWriter, request *http.Request, user *auth.User) {
	switch request.Method {
	case http.MethodGet:
		themes, err := s.Themes(request.Context())
		if err != nil {
			serviceFailure(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, struct {
			Themes []Theme `json:"themes"`
		}{Themes: themes})
	case http.MethodPost:
		if !permitted(user, s.manifest) {
			failure(writer, http.StatusForbidden, "forbidden", "administrator access and writable themes are required")
			return
		}
		var input themeInput
		if err := httpjson.Decode(writer, request, &input, maxThemeBytes); err != nil {
			decodeFailure(writer, err)
			return
		}
		if !permitted(user, themePath(input.ID)) {
			failure(writer, http.StatusForbidden, "forbidden", "theme path is not writable")
			return
		}
		theme, err := s.SaveTheme(request.Context(), input.theme(), input.Revision, actor(user))
		if err != nil {
			serviceFailure(writer, err)
			return
		}
		writer.Header().Set("ETag", `"`+theme.Revision+`"`)
		writeJSON(writer, http.StatusOK, theme)
	default:
		writer.Header().Set("Allow", "GET, POST")
		failure(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (s *Service) deleteHTTP(writer http.ResponseWriter, request *http.Request, user *auth.User) {
	if request.Method != http.MethodDelete {
		writer.Header().Set("Allow", "DELETE")
		failure(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	id := strings.TrimPrefix(request.URL.Path, "/themes/")
	if !permitted(user, themePath(id)) {
		failure(writer, http.StatusForbidden, "forbidden", "administrator access and writable themes are required")
		return
	}
	revisions := request.Header.Values("If-Match")
	if len(revisions) != 1 || len(revisions[0]) != 66 || revisions[0][0] != '"' || revisions[0][65] != '"' {
		failure(writer, http.StatusBadRequest, "invalid_revision", "a single quoted revision in If-Match is required")
		return
	}
	sync, err := s.deleteTheme(request.Context(), id, revisions[0][1:65], actor(user))
	if err != nil {
		serviceFailure(writer, err)
		return
	}
	if sync != nil {
		writeJSON(writer, http.StatusOK, struct {
			Sync *content.SyncStatus `json:"sync"`
		}{Sync: sync})
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func decodeFailure(writer http.ResponseWriter, err error) {
	failure(writer, httpjson.Status(err), "invalid_request", "request must contain a bounded valid JSON object with known fields")
}

func serviceFailure(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, content.ErrConflict):
		failure(writer, http.StatusConflict, "conflict", "settings or themes changed; reload before saving")
	case errors.Is(err, content.ErrInvalid), errors.Is(err, content.ErrUnsupported):
		failure(writer, http.StatusBadRequest, "invalid_settings", err.Error())
	case errors.Is(err, content.ErrLimit):
		failure(writer, http.StatusRequestEntityTooLarge, "limit_exceeded", "settings or theme limit exceeded")
	case errors.Is(err, content.ErrNotFound):
		failure(writer, http.StatusNotFound, "not_found", "theme not found")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		failure(writer, http.StatusRequestTimeout, "request_canceled", "request canceled")
	default:
		failure(writer, http.StatusInternalServerError, "settings_failed", "settings could not be saved; existing content and drafts remain available")
	}
}

func failure(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message}})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = writer.Write(data)
}

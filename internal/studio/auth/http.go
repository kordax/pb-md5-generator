package auth

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kordax/pb-md5-generator/internal/studio/httpjson"
)

type userContextKey struct{}

// UserFromContext returns the request-bound principal installed by Require.
// Permission changes and revocation are resolved again on the next request.
func UserFromContext(ctx context.Context) *User {
	user, _ := ctx.Value(userContextKey{}).(*User)
	return user
}

func RequestUser(request *http.Request) *User {
	return UserFromContext(request.Context())
}

func (s *Service) Resolve(request *http.Request) (*User, error) {
	token := s.requestToken(request)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	if token == "" {
		return nil, nil
	}
	session, exists := s.state.Sessions[sessionHash(token)]
	if !exists || !s.runtime.now().Before(session.Expires) {
		return nil, nil
	}
	record, exists := s.state.Users[session.UserID]
	if !exists || record.Disabled || record.Version != session.Version {
		return nil, nil
	}
	user := cloneUser(record.User)
	user.version = record.Version
	return &user, nil
}

func (s *Service) requestToken(request *http.Request) string {
	value, count := "", 0
	for _, cookie := range request.Cookies() {
		if cookie.Name == s.CookieName() {
			value, count = cookie.Value, count+1
		}
	}
	if count != 1 || len(value) != 64 {
		return ""
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return ""
	}
	return value
}

func (s *Service) Require(role Role, next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		user, err := s.Resolve(request)
		if err != nil {
			writeServiceError(writer, err)
			return
		}
		if user == nil {
			writeServiceError(writer, ErrUnauthorized)
			return
		}
		if roleRank(role) == 0 || roleRank(user.Role) < roleRank(role) {
			writeServiceError(writer, ErrForbidden)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			if !s.originAllowed(request) {
				writeAPIError(writer, http.StatusForbidden, "origin_denied", "a same-origin request is required")
				return
			}
			expected := s.csrfToken(s.requestToken(request))
			provided := request.Header.Get("X-CSRF-Token")
			if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
				writeAPIError(writer, http.StatusForbidden, "csrf_failed", "a valid CSRF token is required")
				return
			}
		}
		ctx := context.WithValue(request.Context(), userContextKey{}, user)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (s *Service) originAllowed(request *http.Request) bool {
	origins := request.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}
	origin, err := url.Parse(origins[0])
	if err != nil || origin.User != nil || origin.Hostname() == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.Opaque != "" || origin.Scheme != "http" && origin.Scheme != "https" {
		return false
	}
	if canonicalOrigin(origin) != s.origin {
		return false
	}
	public, _ := url.Parse(s.origin)
	requestOrigin := &url.URL{Scheme: public.Scheme, Host: request.Host}
	return canonicalOrigin(requestOrigin) == s.origin
}

func canonicalOrigin(parsed *url.URL) string {
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if parsed.Scheme == "https" && port == "443" || parsed.Scheme == "http" && port == "80" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return parsed.Scheme + "://" + host
}

// Handler accepts paths relative to /_docs/api/v1. The parent mounts it and owns
// authorization of document, reader, publication and machine-readable assets.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		switch request.URL.Path {
		case "/session":
			switch request.Method {
			case http.MethodGet, http.MethodHead:
				s.getSession(writer, request)
			case http.MethodPost:
				s.postSession(writer, request)
			case http.MethodDelete:
				s.Require(RoleViewer, http.HandlerFunc(s.deleteSession)).ServeHTTP(writer, request)
			default:
				methodNotAllowed(writer, "GET, HEAD, POST, DELETE")
			}
		case "/users":
			s.Require(RoleAdmin, http.HandlerFunc(s.users)).ServeHTTP(writer, request)
		default:
			if strings.HasPrefix(request.URL.Path, "/users/") && validID(strings.TrimPrefix(request.URL.Path, "/users/")) {
				s.Require(RoleAdmin, http.HandlerFunc(s.user)).ServeHTTP(writer, request)
			} else {
				writeAPIError(writer, http.StatusNotFound, "not_found", "resource not found")
			}
		}
	})
}

func (s *Service) getSession(writer http.ResponseWriter, request *http.Request) {
	user, err := s.Resolve(request)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	response := SessionResponse{User: user, SetupRequired: s.NeedsSetup()}
	if user != nil {
		response.CSRFToken = s.csrfToken(s.requestToken(request))
	}
	writeJSON(writer, http.StatusOK, response)
}

func (s *Service) postSession(writer http.ResponseWriter, request *http.Request) {
	if !s.originAllowed(request) {
		writeAPIError(writer, http.StatusForbidden, "origin_denied", "a same-origin request is required")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(writer, request, &input, 8<<10) {
		return
	}
	if len(input.Username) > 64 || !utf8.ValidString(input.Username) || len(input.Password) > maxPasswordBytes || !utf8.ValidString(input.Password) {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request", "login input exceeds its limits")
		return
	}
	client := request.RemoteAddr
	if host, _, err := net.SplitHostPort(client); err == nil {
		client = host
	}
	user, token, err := s.login(request.Context(), input.Username, input.Password, client)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	http.SetCookie(writer, s.cookie(token, false))
	writeJSON(writer, http.StatusOK, SessionResponse{User: &user, CSRFToken: s.csrfToken(token)})
}

func (s *Service) deleteSession(writer http.ResponseWriter, request *http.Request) {
	if err := s.logout(s.requestToken(request)); err != nil {
		writeServiceError(writer, err)
		return
	}
	http.SetCookie(writer, s.cookie("", true))
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Service) cookie(token string, remove bool) *http.Cookie {
	cookie := &http.Cookie{Name: s.CookieName(), Value: token, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int(s.config.SessionTTL / time.Second), Expires: s.runtime.now().UTC().Add(s.config.SessionTTL)}
	if remove {
		cookie.MaxAge = -1
		cookie.Expires = time.Unix(1, 0).UTC()
	}
	return cookie
}

func (s *Service) users(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		users, err := s.listUsers(RequestUser(request))
		if err != nil {
			writeServiceError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, struct {
			Users []User `json:"users"`
		}{Users: users})
	case http.MethodPost:
		var input CreateUserRequest
		if !readJSON(writer, request, &input, 32<<10) {
			return
		}
		user, err := s.createUser(request.Context(), RequestUser(request), input)
		if err != nil {
			writeServiceError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, user)
	default:
		methodNotAllowed(writer, "GET, POST")
	}
}

func (s *Service) user(writer http.ResponseWriter, request *http.Request) {
	id := strings.TrimPrefix(request.URL.Path, "/users/")
	switch request.Method {
	case http.MethodPatch:
		var input UpdateUserRequest
		if !readJSON(writer, request, &input, 32<<10) {
			return
		}
		user, err := s.updateUser(request.Context(), RequestUser(request), id, input)
		if err != nil {
			writeServiceError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, user)
	case http.MethodDelete:
		if err := s.deleteUser(RequestUser(request), id); err != nil {
			writeServiceError(writer, err)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(writer, "PATCH, DELETE")
	}
}

func readJSON(writer http.ResponseWriter, request *http.Request, target any, limit int64) bool {
	media, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") || request.Header.Get("Content-Encoding") != "" && request.Header.Get("Content-Encoding") != "identity" {
		writeAPIError(writer, http.StatusUnsupportedMediaType, "json_required", "an uncompressed UTF-8 application/json request is required")
		return false
	}
	data, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, limit))
	if err != nil || !utf8.Valid(data) || !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request", "request body must be one bounded UTF-8 JSON object")
		return false
	}
	if err := httpjson.Unmarshal(data, target); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid or exceeds its limits")
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func methodNotAllowed(writer http.ResponseWriter, allow string) {
	writer.Header().Set("Allow", allow)
	writeAPIError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func writeAPIError(writer http.ResponseWriter, status int, code, message string) {
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

func writeServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnauthorized):
		writeAPIError(writer, http.StatusUnauthorized, "unauthorized", ErrUnauthorized.Error())
	case errors.Is(err, ErrForbidden):
		writeAPIError(writer, http.StatusForbidden, "forbidden", ErrForbidden.Error())
	case errors.Is(err, ErrInvalid):
		writeAPIError(writer, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, ErrNotFound):
		writeAPIError(writer, http.StatusNotFound, "not_found", ErrNotFound.Error())
	case errors.Is(err, ErrLastAdmin):
		writeAPIError(writer, http.StatusConflict, "last_admin", ErrLastAdmin.Error())
	case errors.Is(err, ErrConflict):
		writeAPIError(writer, http.StatusConflict, "conflict", ErrConflict.Error())
	case errors.Is(err, ErrRateLimited):
		writer.Header().Set("Retry-After", "60")
		writeAPIError(writer, http.StatusTooManyRequests, "rate_limited", ErrRateLimited.Error())
	case errors.Is(err, ErrBusy):
		writer.Header().Set("Retry-After", "1")
		writeAPIError(writer, http.StatusServiceUnavailable, "busy", ErrBusy.Error())
	default:
		writeAPIError(writer, http.StatusServiceUnavailable, "storage_unavailable", "authentication is temporarily unavailable")
	}
}

package auth

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This exercises libcurl's documented localhost Secure-cookie exception without
// opening a browser. Production HTTP clients are covered separately over TLS.
func TestCurlLocalhostSecureCookieSession(t *testing.T) {
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl is unavailable; localhost cookie compatibility is not tested")
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	origin := "http://localhost:" + port
	config := testConfig(t)
	config.PublicURL = origin
	clock := newTestClock()
	service, err := openService(config, runtimeOptions{iterations: 2, now: clock.now, derive: derivePassword})
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Close() })
	server.Config.Handler = service.Handler()
	server.Start()
	t.Cleanup(server.Close)
	testAdmin(t, service)
	private := filepath.Join(t.TempDir(), "curl-private")
	require.NoError(t, os.Mkdir(private, 0700))
	jar := filepath.Join(private, "cookies")
	require.NoError(t, os.WriteFile(jar, nil, 0600))
	call := func(method string, body []byte, csrf string) []byte {
		t.Helper()
		args := []string{"--silent", "--show-error", "--fail", "--noproxy", "*", "--max-time", "5", "--resolve", "localhost:" + port + ":127.0.0.1", "--request", method, "--cookie", jar, "--cookie-jar", jar}
		var input []byte
		switch method {
		case http.MethodPost:
			args = append(args, "--header", "Origin: "+origin, "--header", "Content-Type: application/json", "--data-binary", "@-")
			input = body
		case http.MethodDelete:
			// Session and CSRF secrets remain in a private file or stdin, never
			// process arguments, subprocess diagnostics or test output.
			args = append(args, "--header", "@-")
			input = []byte("Origin: " + origin + "\nX-CSRF-Token: " + csrf + "\n")
		}
		args = append(args, origin+"/session")
		command := exec.CommandContext(t.Context(), curl, args...)
		command.Stdin = bytes.NewReader(input)
		command.Stderr = io.Discard
		data, err := command.Output()
		if err != nil {
			t.Fatal("curl localhost session request failed")
		}
		return data
	}
	body, err := json.Marshal(map[string]string{"username": "admin", "password": testPassword})
	require.NoError(t, err)
	login := call(http.MethodPost, body, "")
	var session SessionResponse
	if err := json.Unmarshal(login, &session); err != nil {
		t.Fatal("curl login returned invalid session JSON")
	}
	require.NotNil(t, session.User)
	assert.Equal(t, "admin", session.User.Username)
	require.NotEmpty(t, session.CSRFToken)
	jarData, err := os.ReadFile(jar)
	require.NoError(t, err)
	secure := false
	for _, line := range strings.Split(string(jarData), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 7 && fields[5] == "docs_session" {
			secure = fields[1] == "FALSE" && fields[2] == "/" && fields[3] == "TRUE"
		}
	}
	require.True(t, secure, "localhost cookie must remain Secure and host-only")
	get := call(http.MethodGet, nil, "")
	var resolved SessionResponse
	if err := json.Unmarshal(get, &resolved); err != nil {
		t.Fatal("curl session lookup returned invalid JSON")
	}
	require.NotNil(t, resolved.User)
	assert.Equal(t, "admin", resolved.User.Username)
	assert.True(t, subtle.ConstantTimeCompare([]byte(session.CSRFToken), []byte(resolved.CSRFToken)) == 1)
	assert.Empty(t, call(http.MethodDelete, nil, session.CSRFToken))
	get = call(http.MethodGet, nil, "")
	if err := json.Unmarshal(get, &resolved); err != nil {
		t.Fatal("curl logged-out session lookup returned invalid JSON")
	}
	assert.Nil(t, resolved.User)
}

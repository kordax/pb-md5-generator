package studio

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/kordax/pb-md5-generator/internal/studio/auth"
)

var ErrInvalidConfig = errors.New("invalid CMS configuration")

// Config describes an isolated CMS instance. DataDir contains separate managed
// content, private state and published output directories. It is not a source checkout.
type Config struct {
	DataDir       string
	Address       string
	PublicURL     string
	Backend       string
	GitRemote     string
	Initialize    bool
	PushOnPublish bool
	Listener      net.Listener
}

type runtimeConfig struct {
	Config
	contentDir string
	stateDir   string
	outputDir  string
	basePath   string
}

// DefaultConfig creates local-development defaults without creating files.
func DefaultConfig(dataDir string) Config {
	return Config{DataDir: dataDir, Address: "127.0.0.1:8090", Backend: "local", GitRemote: "origin", Initialize: true}
}

func prepareConfig(config Config) (runtimeConfig, error) {
	if config.DataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return runtimeConfig{}, fmt.Errorf("%w: select a data directory", ErrInvalidConfig)
		}
		config.DataDir = filepath.Join(base, "pb-docs")
	}
	data, err := filepath.Abs(config.DataDir)
	if err != nil || filepath.Dir(data) == data || strings.ContainsRune(data, 0) {
		return runtimeConfig{}, fmt.Errorf("%w: data directory must be a dedicated directory", ErrInvalidConfig)
	}
	config.DataDir = filepath.Clean(data)
	if config.Address == "" {
		config.Address = "127.0.0.1:8090"
	}
	if config.Listener != nil {
		config.Address = config.Listener.Addr().String()
		if config.PublicURL == "" {
			return runtimeConfig{}, fmt.Errorf("%w: a custom listener needs an explicit public URL", ErrInvalidConfig)
		}
	} else {
		_, port, splitErr := net.SplitHostPort(config.Address)
		number, numberErr := strconv.Atoi(port)
		if splitErr != nil || numberErr != nil || number < 1 || number > 65535 {
			return runtimeConfig{}, fmt.Errorf("%w: listening address needs a valid host and nonzero port", ErrInvalidConfig)
		}
		if config.PublicURL == "" {
			config.PublicURL = "http://localhost:" + port
		}
	}
	public, err := url.Parse(config.PublicURL)
	if err != nil || public.Hostname() == "" || public.User != nil || public.RawQuery != "" || public.ForceQuery || public.Fragment != "" || public.Opaque != "" {
		return runtimeConfig{}, fmt.Errorf("%w: public URL must contain only an origin and optional hosting prefix", ErrInvalidConfig)
	}
	public.Scheme, public.Host = strings.ToLower(public.Scheme), strings.ToLower(public.Host)
	if port := public.Port(); port != "" {
		number, portErr := strconv.Atoi(port)
		if portErr != nil || number < 1 || number > 65535 {
			return runtimeConfig{}, fmt.Errorf("%w: public URL has an invalid port", ErrInvalidConfig)
		}
	}
	if public.Scheme != "https" && (public.Scheme != "http" || public.Hostname() != "localhost") {
		return runtimeConfig{}, fmt.Errorf("%w: use HTTPS, or HTTP on localhost for local development", ErrInvalidConfig)
	}
	prefix := strings.TrimSuffix(public.Path, "/")
	if public.RawPath != "" || (prefix != "" && (path.Clean(prefix) != prefix || !strings.HasPrefix(prefix, "/"))) {
		return runtimeConfig{}, fmt.Errorf("%w: hosting prefix must be a clean URL path", ErrInvalidConfig)
	}
	for _, value := range prefix {
		if value > unicode.MaxASCII || !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._~-", value) {
			return runtimeConfig{}, fmt.Errorf("%w: hosting prefix contains unsupported characters", ErrInvalidConfig)
		}
	}
	public.Path, public.RawPath = prefix, ""
	config.PublicURL = public.String()
	if config.Backend == "" {
		config.Backend = "local"
	}
	if config.Backend != "local" && config.Backend != "git" {
		return runtimeConfig{}, fmt.Errorf("%w: storage backend must be local or git", ErrInvalidConfig)
	}
	if config.GitRemote == "" {
		config.GitRemote = "origin"
	}
	return runtimeConfig{Config: config, contentDir: filepath.Join(config.DataDir, "content"), stateDir: filepath.Join(config.DataDir, "state"), outputDir: filepath.Join(config.DataDir, "site"), basePath: prefix}, nil
}

func ensurePrivateDirectory(name string) error {
	if err := os.MkdirAll(name, 0o700); err != nil {
		return fmt.Errorf("create private CMS directory: %w", err)
	}
	if err := auth.CheckPrivateDirectory(name); err != nil {
		return fmt.Errorf("%w: private CMS directory needs secure ownership and permissions: %w", ErrInvalidConfig, err)
	}
	return nil
}

package studio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/kordax/pb-md5-generator/internal/studio/apiweb"
	"github.com/kordax/pb-md5-generator/internal/studio/appearance"
	"github.com/kordax/pb-md5-generator/internal/studio/auth"
	"github.com/kordax/pb-md5-generator/internal/studio/content"
	"github.com/kordax/pb-md5-generator/internal/studio/web"
	"go.uber.org/fx"
)

// Server owns the HTTP listener and the private auth/content services. Closing
// it releases all directory locks, including when construction or startup fails.
type Server struct {
	app       *fx.App
	resources *runtimeResources
	http      *runtimeHTTP
	once      sync.Once
	closeErr  error
}

type runtimeResources struct {
	closers []func() error
}

func (resources *runtimeResources) add(closeResource func() error) {
	resources.closers = append(resources.closers, closeResource)
}

func (resources *runtimeResources) close() error {
	var failures []error
	for i := len(resources.closers) - 1; i >= 0; i-- {
		failures = append(failures, resources.closers[i]())
	}
	resources.closers = nil
	return errors.Join(failures...)
}

type runtimeHTTP struct {
	server   *http.Server
	listener net.Listener
	done     chan error
	stopped  chan struct{}
}

// Start composes the CMS in named Fx modules. It returns after the listener is
// ready; the caller must Close the server or use Run for a blocking lifecycle.
// An explicitly supplied Listener is transferred to Start, including failures.
func Start(ctx context.Context, config Config) (*Server, error) {
	server, err := startRuntime(ctx, config)
	if err != nil && config.Listener != nil {
		return nil, errors.Join(err, closeRuntimeListener(config.Listener))
	}
	return server, err
}

func startRuntime(ctx context.Context, config Config) (*Server, error) {
	if ctx == nil {
		return nil, fmt.Errorf("CMS context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prepared, err := prepareConfig(config)
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(prepared.DataDir); err != nil {
		return nil, err
	}
	if err := ensurePrivateDirectory(prepared.stateDir); err != nil {
		return nil, err
	}
	resources := &runtimeResources{}
	var transport *runtimeHTTP
	app := fx.New(
		fx.NopLogger,
		fx.Supply(prepared, resources),
		fx.Module("authentication", fx.Provide(newRuntimeAuth)),
		fx.Module("content", fx.Provide(newRuntimeContent)),
		fx.Module("appearance", fx.Provide(newRuntimeAppearance)),
		fx.Module("http", fx.Provide(newRuntimeHandler, newRuntimeHTTP)),
		fx.Invoke(func(server *runtimeHTTP) { transport = server }),
	)
	if err := app.Err(); err != nil {
		return nil, errors.Join(err, resources.close())
	}
	if err := app.Start(ctx); err != nil {
		return nil, errors.Join(err, resources.close())
	}
	return &Server{app: app, resources: resources, http: transport}, nil
}

// Run serves the CMS until cancellation or an unexpected HTTP server failure.
func Run(ctx context.Context, config Config) error {
	server, err := Start(ctx, config)
	if err != nil {
		return err
	}
	select {
	case err = <-server.http.done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(err, server.Close(stop))
}

// Close waits for active HTTP requests and then releases persistent state.
func (server *Server) Close(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("shutdown context is required")
	}
	server.once.Do(func() {
		stopErr := server.app.Stop(ctx)
		if stopErr != nil {
			stopErr = errors.Join(stopErr, server.http.server.Close(), closeRuntimeListener(server.http.listener))
		}
		server.closeErr = errors.Join(stopErr, server.resources.close())
	})
	return server.closeErr
}

// Address returns the bound address, including an ephemeral port in tests.
func (server *Server) Address() net.Addr { return server.http.listener.Addr() }

// Bootstrap creates the first owner before the server holds the auth state
// lock. Password bytes are used exactly as supplied and never written to logs.
func Bootstrap(ctx context.Context, config Config, username, displayName string, password []byte) error {
	if ctx == nil {
		return fmt.Errorf("bootstrap context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared, err := prepareConfig(config)
	if err != nil {
		return err
	}
	if err := ensurePrivateDirectory(prepared.DataDir); err != nil {
		return err
	}
	if err := ensurePrivateDirectory(prepared.stateDir); err != nil {
		return err
	}
	service, err := auth.Open(auth.Config{StateDir: filepath.Join(prepared.stateDir, "auth"), PublicURL: prepared.PublicURL})
	if err != nil {
		return err
	}
	_, bootstrapErr := service.Bootstrap(ctx, username, displayName, password)
	return errors.Join(bootstrapErr, service.Close())
}

func newRuntimeAuth(config runtimeConfig, resources *runtimeResources) (*auth.Service, error) {
	service, err := auth.Open(auth.Config{StateDir: filepath.Join(config.stateDir, "auth"), PublicURL: config.PublicURL})
	if err == nil {
		resources.add(service.Close)
	}
	return service, err
}

func newRuntimeContent(config runtimeConfig, authentication *auth.Service, resources *runtimeResources) (*content.Service, error) {
	storage := content.NewLocalBackend()
	if config.Backend == "git" {
		git, err := content.NewGitBackend(content.GitConfig{Root: config.contentDir, Remote: config.GitRemote, Timeout: 30 * time.Second})
		if err != nil {
			return nil, err
		}
		storage = git
	}
	service, err := content.New(content.Config{
		Root: config.contentDir, StateDir: filepath.Join(config.stateDir, "content"), OutputDir: config.outputDir,
		Initialize: config.Initialize, PushOnPublish: config.PushOnPublish, Storage: storage,
		OperationTimeout: 45 * time.Second, Compile: compilePublication,
		Validate: func(ctx context.Context, root string, changes []content.Change, actor content.Actor) error {
			return validateMutation(ctx, root, changes, actor, authentication, config.contentDir)
		},
	})
	if err == nil {
		resources.add(service.Close)
	}
	return service, err
}

func newRuntimeAppearance(config runtimeConfig, contents *content.Service) (*appearance.Service, error) {
	return appearance.New(appearance.Config{Content: contents, Root: config.contentDir, Manifest: "docs.json"})
}

func newRuntimeHandler(config runtimeConfig, authentication *auth.Service, contents *content.Service, settings *appearance.Service) (http.Handler, error) {
	reader := newReader(config, authentication, contents)
	return apiweb.New(apiweb.Options{
		BasePath: config.basePath, PublicURL: config.PublicURL, Auth: authentication, Content: contents,
		Assets: web.Assets(), Contract: APIContract(), Appearance: settings.Handler(authentication), Reader: reader,
		Preview: func(ctx context.Context, page apiweb.Document, source []byte, _ *auth.User) (apiweb.Preview, error) {
			result, err := previewDocument(ctx, contents, page, source)
			if err == nil && result.HTML != "" {
				result.HTML, err = resolvePreviewURLs([]byte(result.HTML), config.basePath, page.OutputPath)
			}
			return result, err
		},
		Publishing: func(ctx context.Context) (apiweb.PublishingPolicy, error) {
			current, err := settings.Read(ctx)
			return apiweb.PublishingPolicy{AutoPublish: current.Publishing.AutoPublish, GitPushOnPublish: current.Publishing.GitPushOnPublish}, err
		},
	})
}

func newRuntimeHTTP(lifecycle fx.Lifecycle, handler http.Handler, config runtimeConfig, contents *content.Service) *runtimeHTTP {
	transport := &runtimeHTTP{
		server:  &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: log.New(io.Discard, "", 0)},
		done:    make(chan error, 1),
		stopped: make(chan struct{}),
	}
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			// An invalid source must remain repairable in the CMS. Configuration,
			// ownership and storage failures still prevent startup.
			_, err := contents.BuildCurrent(ctx, content.Actor{ID: "system", Name: "Documentation service"})
			if err != nil && !errors.Is(err, content.ErrInvalid) {
				return err
			}
			listener := config.Listener
			if listener == nil {
				var listenConfig net.ListenConfig
				listener, err = listenConfig.Listen(ctx, "tcp", config.Address)
				if err != nil {
					return fmt.Errorf("listen for CMS: %w", err)
				}
			}
			transport.listener = listener
			go func() {
				defer close(transport.stopped)
				serveErr := transport.server.Serve(listener)
				if errors.Is(serveErr, http.ErrServerClosed) {
					serveErr = nil
				}
				transport.done <- serveErr
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			shutdownErr := transport.server.Shutdown(ctx)
			if shutdownErr != nil {
				shutdownErr = errors.Join(shutdownErr, transport.server.Close())
			}
			listenerErr := closeRuntimeListener(transport.listener)
			select {
			case <-transport.stopped:
				return errors.Join(shutdownErr, listenerErr)
			case <-ctx.Done():
				return errors.Join(shutdownErr, listenerErr, ctx.Err())
			}
		},
	})
	return transport
}

func closeRuntimeListener(listener net.Listener) error {
	err := listener.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

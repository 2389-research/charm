// ABOUTME: PocketBase app lifecycle management for embedded deployment.
// ABOUTME: Handles initialization, collection setup, and server startup.

package pocketbase

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// App wraps the PocketBase application instance.
type App struct {
	pb      *pocketbase.PocketBase
	dataDir string
	port    int
}

// Config holds PocketBase configuration.
type Config struct {
	DataDir    string
	Port       int
	AdminEmail string
	AdminPass  string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		DataDir: "data",
		Port:    35357,
	}
}

// New creates a new PocketBase app instance.
func New(cfg *Config) (*App, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	pbDataDir := filepath.Join(cfg.DataDir, "pb_data")
	if err := os.MkdirAll(pbDataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create pb_data dir: %w", err)
	}

	pb := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir: pbDataDir,
	})

	app := &App{
		pb:      pb,
		dataDir: cfg.DataDir,
		port:    cfg.Port,
	}

	return app, nil
}

// PB returns the underlying PocketBase instance for direct access.
func (a *App) PB() *pocketbase.PocketBase {
	return a.pb
}

// Bootstrap initializes PocketBase without starting the server.
func (a *App) Bootstrap() error {
	return a.pb.Bootstrap()
}

// Start begins serving the PocketBase admin UI and API.
func (a *App) Start() error {
	log.Info("Starting PocketBase", "port", a.port)

	// Configure and start server
	return apis.Serve(a.pb, apis.ServeConfig{
		HttpAddr:        fmt.Sprintf(":%d", a.port),
		ShowStartBanner: false,
	})
}

// StartAsync starts PocketBase in a goroutine.
func (a *App) StartAsync() {
	go func() {
		if err := a.Start(); err != nil {
			log.Error("PocketBase server error", "err", err)
		}
	}()
}

// OnBeforeServe registers a callback for before serve.
func (a *App) OnBeforeServe(fn func(e *core.ServeEvent) error) {
	a.pb.OnServe().BindFunc(func(e *core.ServeEvent) error {
		if err := fn(e); err != nil {
			return err
		}
		return e.Next()
	})
}

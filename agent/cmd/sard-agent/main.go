// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Command sard-agent runs on a backed-up host. It dials sard-server,
// receives commands and streams backup data straight to storage.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/Artur-Abalov/sard/agent/internal/app"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/transport"
	"github.com/Artur-Abalov/sard/agent/plugins"
)

// version is set at build time: -ldflags "-X main.version=...".
var version = "dev"

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Hostname)
	stop() // equivalent mutant: os.Exit follows immediately
	os.Exit(code)
}

// hostnameFunc is os.Hostname, injectable for tests.
type hostnameFunc func() (string, error)

func run(ctx context.Context, args []string, stdout, stderr io.Writer, hostname hostnameFunc) int {
	fs := flag.NewFlagSet("sard-agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print the version and exit")
	configPath := fs.String("config", "", "path to the agent YAML config")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *showVersion {
		_, _ = fmt.Fprintf(stdout, "sard-agent %s\n", version)
		return exitOK
	}
	if *configPath == "" {
		_, _ = fmt.Fprintln(stderr, "sard-agent: --config <path> is required")
		fs.Usage()
		return exitUsage
	}
	if err := start(ctx, *configPath, stdout, hostname, os.Executable); err != nil {
		_, _ = fmt.Fprintln(stderr, "sard-agent:", err)
		return exitError
	}
	return exitOK
}

// start is the composition root: the only place that knows concrete types.
func start(ctx context.Context, configPath string, stdout io.Writer, hostnameOf hostnameFunc, executable func() (string, error)) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	hostname, err := hostnameOf()
	if err != nil {
		return fmt.Errorf("hostname: %w", err)
	}
	_, _ = fmt.Fprintf(stdout, "sard-agent %s: connecting to %s\n", version, cfg.Server.Address)
	resticBinary, err := resticPath(cfg.Restic.Path, executable)
	if err != nil {
		return err
	}
	// Repository keys stay on this host (ADR 0008).
	keys := crypto.NewResticAES(cfg.PasswordFiles())
	agent := &app.Agent{
		Plugins:  plugins.Registry(),
		Hostname: hostname,
		Version:  version,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Local:    cfg,
		RepositoryID: func(ctx context.Context, r config.Repository) (string, error) {
			return restic.New(restic.Options{
				Binary:   resticBinary,
				CacheDir: cfg.Restic.CacheDir,
				Path:     os.Getenv("PATH"),
				Exec:     restic.ProcessExecutor{},
				Keys:     keys,
				ReadFile: os.ReadFile,
			}, r).ID(ctx)
		},
	}
	// The executor (A4) is wired in after A3 and A4 are merged.
	link, err := transport.New(transport.Options{
		Address:  cfg.Server.Address,
		TLS:      cfg.TLS,
		Register: agent.RegisterRequest,
		Commands: app.NoExecutor{},
		State:    app.NoExecutor{},
	})
	if err != nil {
		return err
	}
	agent.Link = link
	return agent.Run(ctx)
}

// resticPath is restic.path, or the restic shipped next to sard-agent
// (docs/adr/0017-restic-shipped-with-agent.md).
func resticPath(configured string, executable func() (string, error)) (string, error) {
	if configured != "" {
		return configured, nil
	}
	self, err := executable()
	if err != nil {
		return "", fmt.Errorf("restic.path: %w", err)
	}
	return filepath.Join(filepath.Dir(self), "restic"), nil
}

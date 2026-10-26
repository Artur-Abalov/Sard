// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// The sandbox of a copy (V5): the official image of PostgreSQL of the major
// version of the source, its server reached through the socket of the
// container, its tools run in the container.
const (
	sandboxImage    = "postgres"
	sandboxDataDir  = "/var/lib/postgresql/data"
	sandboxSocket   = "/var/run/postgresql"
	sandboxUser     = "postgres"
	sandboxDatabase = "sard_verify"
	// sandboxPath is where the plugin "finds" the tools of a sandbox: the
	// sandbox runs them by name, by the PATH of its image.
	sandboxPath = "/sandbox"
)

// readyPoll is how often the server of a sandbox is asked whether it serves.
const readyPoll = 500 * time.Millisecond

var _ sdk.Sandboxed = Plugin{}

// Sandbox implements sdk.Sandboxed: the image of the major version of the
// source; any for a snapshot without a fingerprint (the newest restores the
// dumps of older ones).
func (Plugin) Sandbox(f *sdk.Fingerprint) (sdk.SandboxSpec, error) {
	spec := sdk.SandboxSpec{
		Image: sandboxImage, DataDir: sandboxDataDir,
		Env: []string{"PGDATA=" + sandboxDataDir + "/pgdata"}, PasswordEnv: "POSTGRES_PASSWORD",
	}
	if f == nil {
		return spec, nil
	}
	major, ok := f.Lookup(factMajor)
	if !ok {
		return sdk.SandboxSpec{}, fmt.Errorf("the fingerprint has no %s", factMajor)
	}
	spec.Major = major.Value
	return spec, nil
}

// SandboxReady implements sdk.Sandboxed: the image starts a temporary server
// without TCP to initialise the cluster, then the real one; the real one
// listens. Then every extension of the source must be available.
func (p Plugin) SandboxReady(ctx context.Context, h sdk.SandboxHost, f *sdk.Fingerprint) error {
	if err := p.awaitServer(ctx, h); err != nil {
		return err
	}
	if f == nil {
		return nil
	}
	return p.extensionsAvailable(ctx, h, f)
}

func (p Plugin) awaitServer(ctx context.Context, h sdk.SandboxHost) error {
	for {
		out, err := sandboxQuery(ctx, h, "postgres", "SHOW listen_addresses")
		if err == nil && out != "" {
			return nil
		}
		if waited := p.sleep(ctx, readyPoll); waited != nil {
			return fmt.Errorf("the server of the sandbox did not come up: %w (last answer: %v)", waited, err)
		}
	}
}

// extensionsAvailable: the image has every extension the source had.
func (p Plugin) extensionsAvailable(ctx context.Context, h sdk.SandboxHost, f *sdk.Fingerprint) error {
	out, err := sandboxQuery(ctx, h, "postgres", "SELECT name FROM pg_catalog.pg_available_extensions")
	if err != nil {
		return err
	}
	available := strings.Fields(out)
	for _, fact := range f.Facts {
		name, ok := strings.CutPrefix(fact.Name, extensionFact)
		if ok && !slices.Contains(available, name) {
			return &sdk.SandboxRefusal{Reason: fmt.Sprintf("the image has no extension %s, which the source has", name)}
		}
	}
	return nil
}

// OpenCopy implements sdk.Sandboxed: the restored database takes a session
// and answers from its catalog.
func (p Plugin) OpenCopy(ctx context.Context, h sdk.SandboxHost, c sdk.RestoredCopy) error {
	var cfg copyConfig
	if err := json.Unmarshal(c.Config, &cfg); err != nil {
		return err
	}
	_, err := sandboxQuery(ctx, h, cfg.Database, "SELECT count(*) FROM pg_catalog.pg_class")
	return err
}

// sandboxQuery runs one query with psql in the sandbox, as its superuser over
// the socket; the output without its line end.
func sandboxQuery(ctx context.Context, h sdk.SandboxHost, database, query string) (string, error) {
	var out bytes.Buffer
	var stderr []string
	code, err := h.Run(ctx, sdk.Process{
		Path: "psql", Args: []string{"-X", "-A", "-t", "-q", "-h", sandboxSocket, "-U", sandboxUser, "-d", database, "-c", query},
		Env: []string{"LC_MESSAGES=C"}, Stdout: &out, Stderr: func(l string) { stderr = append(stderr, l) },
	})
	switch {
	case err != nil:
		return "", err
	case code != 0:
		return "", fmt.Errorf("psql exited %d: %s", code, strings.Join(stderr, "; "))
	}
	return strings.TrimSpace(out.String()), nil
}

func (p Plugin) sleep(ctx context.Context, d time.Duration) error {
	if p.Sleep != nil {
		return p.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-t.C:
		return nil
	}
}

// inSandbox is the plugin that works in the sandbox of h: its tools run there
// by name, with nothing of the agent's environment.
func (p Plugin) inSandbox(h sdk.SandboxHost) Plugin {
	p.Runner = sandboxRunner{h}
	p.FS = sandboxTools{}
	p.Environ = func() []string { return []string{"PATH=" + sandboxPath} }
	return p
}

// sandboxTarget is the restore config of a copy in the sandbox.
func sandboxTarget() sdk.Config {
	cfg, _ := json.Marshal(map[string]any{
		"host": sandboxSocket, "port": defaultPort, "user": sandboxUser, "password_ref": sdk.SandboxPassword,
		"database": sandboxDatabase,
	})
	return cfg
}

// sandboxRunner runs the tools in the sandbox: by their name, with the
// variables of PostgreSQL and of the locale only.
type sandboxRunner struct{ h sdk.SandboxHost }

func (r sandboxRunner) Run(ctx context.Context, c Cmd) (int, error) {
	var env []string
	for _, v := range c.Env {
		if strings.HasPrefix(v, "PG") || strings.HasPrefix(v, "LC_") {
			env = append(env, v)
		}
	}
	return r.h.Run(ctx, sdk.Process{Path: filepath.Base(c.Path), Args: c.Args, Env: env, Stdin: c.Stdin, Stdout: c.Stdout, Stderr: c.Stderr})
}

// sandboxTools are the tools of the image: always there.
type sandboxTools struct{}

func (sandboxTools) Stat(name string) (fs.FileInfo, error) { return tool(filepath.Base(name)), nil }

type tool string

func (t tool) Name() string     { return string(t) }
func (tool) Size() int64        { return 0 }
func (tool) Mode() fs.FileMode  { return 0o755 }
func (tool) ModTime() time.Time { return time.Time{} }
func (tool) IsDir() bool        { return false }
func (tool) Sys() any           { return nil }

// CopySize implements sdk.CopySizer: the size of the database of the source.
func (Plugin) CopySize(f sdk.Fingerprint) (uint64, bool) {
	fact, ok := f.Lookup(factSize)
	if !ok {
		return 0, false
	}
	size, err := strconv.ParseUint(fact.Value, 10, 64)
	return size, err == nil
}

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package restic wraps the restic binary, which stores, deduplicates and
// encrypts backup data. restic gets an environment built from scratch:
// nothing of the agent's own environment reaches it except PATH.
package restic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
)

// Repository is a restic repository on S3, SFTP or local disk.
type Repository interface {
	// ID returns restic's repository id (from the repository config). It is
	// not a secret: the server uses it to see how many hosts hold the key.
	ID(ctx context.Context) (string, error)
	// Init creates the repository and returns its id.
	Init(ctx context.Context) (string, error)
	// Backup stores req.Paths as a new snapshot. progress, if not nil, is
	// called as restic reports it. When some files could not be read the
	// snapshot exists anyway: the summary is returned with a *PartialError.
	Backup(ctx context.Context, req BackupRequest, progress func(Progress)) (BackupSummary, error)
	// Restore writes snapshot snapshotID into the target directory.
	Restore(ctx context.Context, snapshotID, target string) error
}

// Defaults for Options.
const (
	DefaultBinary   = "restic"
	DefaultCacheDir = "/var/cache/sard/restic"
	DefaultPath     = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
)

// Options configure the wrapper; zero values take the defaults above.
type Options struct {
	// Binary is the restic executable.
	Binary string
	// CacheDir is restic's cache and HOME.
	CacheDir string
	// Path is the PATH restic gets, for helpers such as ssh or rclone.
	Path string
	Exec Executor
	Keys crypto.Provider
	// ReadFile reads the repository's env_file.
	ReadFile func(name string) ([]byte, error)
	// OnStderr receives every stderr line of restic, for the agent's logs.
	OnStderr func(line string)
}

// Errors for restic's documented exit codes and for output the wrapper
// cannot trust.
var (
	ErrNoRepository   = errors.New("repository does not exist")      // exit 10
	ErrLocked         = errors.New("repository is locked")           // exit 11
	ErrWrongPassword  = errors.New("wrong password or no key found") // exit 12
	ErrBadOutput      = errors.New("unexpected restic output")
	ErrInvalidRequest = errors.New("invalid request")
)

// ExitError is a restic failure without a more specific error.
type ExitError struct {
	Code int
	// Message is restic's fatal message, if it printed one.
	Message string
}

func (e *ExitError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("exit code %d", e.Code)
	}
	return fmt.Sprintf("exit code %d: %s", e.Code, e.Message)
}

// CLI runs the restic binary for one repository configured on this host.
type CLI struct {
	opts Options
	repo config.Repository
}

// New returns a Repository backed by the restic binary.
func New(opts Options, repo config.Repository) *CLI {
	if opts.Binary == "" {
		opts.Binary = DefaultBinary
	}
	if opts.CacheDir == "" {
		opts.CacheDir = DefaultCacheDir
	}
	if opts.Path == "" {
		opts.Path = DefaultPath
	}
	if opts.OnStderr == nil {
		opts.OnStderr = func(string) {}
	}
	return &CLI{opts: opts, repo: repo}
}

// ID runs `restic cat config` and returns the repository id.
func (c *CLI) ID(ctx context.Context) (string, error) {
	var out bytes.Buffer
	if _, err := c.runRepo(ctx, []string{"cat", "config"}, collect(&out)); err != nil {
		return "", err
	}
	var cfg struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(out.Bytes(), &cfg) // invalid JSON leaves the id empty
	return requireID("restic cat", cfg.ID)
}

// Init runs `restic init` and returns the new repository's id.
func (c *CLI) Init(ctx context.Context) (string, error) {
	var out bytes.Buffer
	if _, err := c.runRepo(ctx, []string{"init", "--json"}, collect(&out)); err != nil {
		return "", err
	}
	var msg struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(out.Bytes(), &msg) // invalid JSON leaves the id empty
	return requireID("restic init", msg.ID)
}

func requireID(cmd, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("%s: %w: no repository id", cmd, ErrBadOutput)
	}
	return id, nil
}

// Restore runs `restic restore`. The snapshot goes after "--", so an id
// starting with "-" is not a flag.
func (c *CLI) Restore(ctx context.Context, snapshotID, target string) error {
	if snapshotID == "" || target == "" {
		return fmt.Errorf("%w: snapshot and target are required", ErrInvalidRequest)
	}
	_, err := c.runRepo(ctx, []string{"restore", "--json", "--target", target, "--", snapshotID}, nil)
	return err
}

// runRepo runs a restic command against the repository.
func (c *CLI) runRepo(ctx context.Context, args []string, stdout func([]byte)) (*result, error) {
	env, err := c.repoEnv(ctx)
	if err != nil {
		return &result{}, err
	}
	return c.run(ctx, env, args, stdout)
}

// run runs a restic command and turns a non-zero exit code into an error.
func (c *CLI) run(ctx context.Context, env, args []string, stdout func([]byte)) (*result, error) {
	res, err := c.start(ctx, env, args, stdout)
	if err != nil {
		return res, err
	}
	return res, res.err()
}

// start runs restic; the error covers only a failed start or cancellation.
func (c *CLI) start(ctx context.Context, env, args []string, stdout func([]byte)) (*result, error) {
	res := &result{cmd: "restic " + args[0]}
	code, err := c.opts.Exec.Run(ctx, Command{
		Path:   c.opts.Binary,
		Args:   args,
		Env:    env,
		Stdout: stdout,
		Stderr: res.stderr(c.opts.OnStderr),
	})
	res.code = code
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("%s: %w", res.cmd, ctxErr)
	}
	if err != nil {
		return res, fmt.Errorf("%s: %w", res.cmd, err)
	}
	return res, nil
}

// collect appends stdout lines to buf.
func collect(buf *bytes.Buffer) func([]byte) {
	return func(line []byte) {
		buf.Write(line)
		buf.WriteByte('\n')
	}
}

// result is what restic said on stderr and how it exited.
type result struct {
	cmd   string
	code  int
	fatal string      // restic's last fatal message
	items []ItemError // per-file errors of backup --json
}

func (r *result) stderr(forward func(string)) func([]byte) {
	return func(line []byte) {
		forward(string(line))
		var msg message
		if json.Unmarshal(line, &msg) != nil {
			if bytes.HasPrefix(line, []byte("Fatal: ")) {
				r.fatal = string(line)
			}
			return
		}
		switch msg.Type {
		case "error":
			r.items = append(r.items, ItemError{Item: msg.Item, During: msg.During, Message: msg.Error.Message})
		case "exit_error":
			r.fatal = msg.Message
		}
	}
}

// err maps restic's exit code to an error; nil for 0.
func (r *result) err() error {
	var err error
	switch r.code {
	case 0:
		return nil
	case 10:
		err = ErrNoRepository
	case 11:
		err = ErrLocked
	case 12:
		err = ErrWrongPassword
	default:
		err = &ExitError{Code: r.code, Message: r.fatal}
	}
	return fmt.Errorf("%s: %w", r.cmd, err)
}

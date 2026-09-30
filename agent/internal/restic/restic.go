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
	"io"
	"slices"
	"strings"

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
	// Backup stores req.Paths, or the stream req.Stdin, as a new snapshot.
	// progress, if not nil, is called as restic reports it. When some files
	// could not be read the snapshot exists anyway: the summary is returned
	// with a *PartialError.
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
	ErrNoRepository  = errors.New("repository does not exist")      // exit 10
	ErrLocked        = errors.New("repository is locked")           // exit 11
	ErrWrongPassword = errors.New("wrong password or no key found") // exit 12
	ErrBadOutput     = errors.New("unexpected restic output")
	// Exit code 1 with a recognised message (restic 0.19.1, A5b): the
	// errors also carry the *ExitError.
	ErrRepositoryExists = errors.New("repository already exists")
	ErrEmptyPassword    = errors.New("empty password")
	ErrNetwork          = errors.New("network failure")
	ErrInvalidRequest   = errors.New("invalid request")
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
	if _, err := c.runRepo(ctx, call{args: []string{"cat", "config"}, stdout: collect(&out)}); err != nil {
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
	if _, err := c.runRepo(ctx, call{args: []string{"init", "--json"}, stdout: collect(&out)}); err != nil {
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
	_, err := c.runRepo(ctx, call{args: []string{"restore", "--json", "--target", target, "--", snapshotID}})
	return err
}

// call is one restic invocation; zero fields discard or leave empty.
type call struct {
	args   []string
	stdout func(line []byte)
	stdin  io.Reader
	// stderr sees each stderr line after the wrapper has.
	stderr func(line []byte)
}

// runRepo runs a restic command against the repository.
func (c *CLI) runRepo(ctx context.Context, cl call) (*result, error) {
	env, err := c.repoEnv(ctx)
	if err != nil {
		return &result{}, err
	}
	return c.run(ctx, env, cl)
}

// run runs a restic command and turns a non-zero exit code into an error.
func (c *CLI) run(ctx context.Context, env []string, cl call) (*result, error) {
	res, err := c.start(ctx, env, cl)
	if err != nil {
		return res, err
	}
	return res, res.err()
}

// start runs restic; the error covers only a failed start or cancellation.
func (c *CLI) start(ctx context.Context, env []string, cl call) (*result, error) {
	res := &result{cmd: "restic " + cl.args[0]}
	code, err := c.opts.Exec.Run(ctx, Command{
		Path:   c.opts.Binary,
		Args:   cl.args,
		Env:    env,
		Stdin:  cl.stdin,
		Stdout: cl.stdout,
		Stderr: res.stderr(c.opts.OnStderr, cl.stderr),
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

func (r *result) stderr(forward func(string), observe func([]byte)) func([]byte) {
	return func(line []byte) {
		forward(string(line))
		if observe != nil {
			observe(line)
		}
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
		err = r.exitError()
	}
	return fmt.Errorf("%s: %w", r.cmd, err)
}

// exitError is the error for any other exit code; a fatal message the
// agent knows how to read adds a sentinel next to the *ExitError.
func (r *result) exitError() error {
	exit := &ExitError{Code: r.code, Message: r.fatal}
	if kind := fatalKind(r.fatal); kind != nil {
		return fmt.Errorf("%w: %w", kind, exit)
	}
	return exit
}

// networkCauses are the messages of a backend that cannot be reached.
var networkCauses = []string{
	"connection refused", "no such host", "i/o timeout", "network is unreachable",
	"no route to host", "connection reset by peer", "connection timed out", "TLS handshake timeout",
}

// fatalKind recognises restic's fatal message; nil for the unknown ones.
func fatalKind(msg string) error {
	switch {
	case strings.Contains(msg, "config file already exists"):
		return ErrRepositoryExists
	case strings.Contains(msg, "an empty password is not allowed"):
		return ErrEmptyPassword
	case slices.ContainsFunc(networkCauses, func(c string) bool { return strings.Contains(msg, c) }):
		return ErrNetwork
	}
	return nil
}

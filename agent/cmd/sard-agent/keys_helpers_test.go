// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/role"
)

// keysHost is the world of docs/specs/agent/restic-keys.feature: the host of
// setupHost (named src1, role source) whose repository base is initialized and
// has the keys H (opens it from this host) and R (recovery); stdout and stdin
// are terminals unless a scenario says otherwise.
type keysHost struct {
	*setupHost
}

func newKeysHost(t *testing.T) *keysHost {
	t.Helper()
	h := &keysHost{setupHost: newSetupHost(t)}
	h.deps.hostname = func() (string, error) { return "src1", nil }
	h.deps.stdoutTerminal = true
	h.deps.random = &sequence{}
	h.deps.hostRole = func(string) role.Role { return role.Source }
	ok(t, os.MkdirAll(h.agentD(), 0o750))
	base := h.base()
	base.initialized, base.password = true, passMarker
	base.keyring.keys = []fakeKey{keyH(), keyR()}
	return h
}

// newIssueHost is newKeysHost whose repository has the key H only: a scenario
// that issues a recovery key without a label needs a repository without one
// (K10), while the list scenarios start from H and R.
func newIssueHost(t *testing.T) *keysHost {
	t.Helper()
	h := newKeysHost(t)
	h.keys(keyH())
	return h
}

// sequence is a random generator whose bytes are all different from one
// read to the next: 0x01 0x02 ... for the first one.
type sequence struct{ n uint64 }

func (s *sequence) Read(p []byte) (int, error) {
	for i := range p {
		s.n++
		p[i] = byte(s.n)
	}
	return len(p), nil
}

// keys sets the keys of base.
func (h *keysHost) keys(keys ...fakeKey) { h.base().keyring.keys = keys }

// keyCmd runs "sard-agent repo key <args>" as root.
func (h *keysHost) keyCmd(args ...string) (int, string, string) {
	return h.sudo(append([]string{"repo", "key"}, args...)...)
}

// added is the key restic key add made last, with the password it got.
func (h *keysHost) added() keyAdd { return h.base().lastAdded() }

// assertNoResticCalls is "restic не вызывался".
func (h *keysHost) assertNoResticCalls() {
	h.t.Helper()
	if n := h.restic.backendCalls(); n != 0 {
		h.t.Fatalf("restic was called: %v", h.restic.subs())
	}
}

// assertNoKeyAdd is "restic key add не вызывался".
func (h *keysHost) assertNoKeyAdd() {
	h.t.Helper()
	if n := len(h.base().keyring.adds); n != 0 {
		h.t.Fatalf("restic key add was called %d times", n)
	}
}

// assertNoKeyRemove is "restic key remove не вызывался".
func (h *keysHost) assertNoKeyRemove() {
	h.t.Helper()
	if n := len(h.base().keyring.removes); n != 0 {
		h.t.Fatalf("restic key remove was called: %v", h.base().keyring.removes)
	}
}

// assertConfigUntouched is "C, D и agent.d не изменились": the host is the
// same but for the lock file, which lives while a command runs.
func (h *keysHost) assertConfigUntouched(before map[string]string) {
	h.t.Helper()
	after := h.hostTree()
	for p := range after {
		if strings.HasSuffix(p, ".sard-config.lock") {
			delete(after, p)
		}
	}
	if d := diff(before, after); len(d) > 0 {
		h.t.Fatalf("the host changed: %v", d)
	}
}

// assertPasswordHidden is "пароль ключа не раскрыт": the password and
// PASS-MARKER are in no stderr, audit line, fragment of agent.d, config or
// file of the secrets directory, and in stdout at most once.
func (h *keysHost) assertPasswordHidden(password, stdout, stderr string) {
	h.t.Helper()
	if n := strings.Count(stdout, password); n > 1 {
		h.t.Fatalf("the password is in stdout %d times:\n%s", n, stdout)
	}
	outputs := []string{stderr, strings.ReplaceAll(stdout, password, "")}
	outputs = append(outputs, h.log.lines...)
	outputs = append(outputs, h.fileContent(h.cfgPath))
	for _, dir := range []string{h.agentD(), h.stateDir()} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			outputs = append(outputs, string(data))
		}
	}
	for _, out := range outputs {
		for _, secret := range []string{password, passMarker} {
			if secret != "" && strings.Contains(out, secret) {
				h.t.Fatalf("%q is exposed:\n%s", secret, out)
			}
		}
	}
}

// filesHolding lists the files under the host directory (D, with the secrets
// K and the journal S) that contain the text.
func (h *keysHost) filesHolding(text string) []string {
	var found []string
	for _, root := range []string{h.dir} {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			if data, err := os.ReadFile(p); err == nil && bytes.Contains(data, []byte(text)) {
				found = append(found, p)
			}
			return nil
		})
	}
	return found
}

// known32 are 32 bytes and the base64url they make without padding.
func known32() ([]byte, string) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(0xa0 + i)
	}
	return raw, "oKGio6SlpqeoqaqrrK2ur7CxsrO0tba3uLm6u7y9vr8"
}

// splitClock gives every duration its own timer channel, so that a test fires
// the --timeout of a command without firing its --connect-timeout.
type splitClock struct {
	*fakeEnrollClock
	mu    sync.Mutex
	timer map[time.Duration]chan time.Time
}

func newSplitClock() *splitClock {
	return &splitClock{fakeEnrollClock: newFakeEnrollClock(), timer: map[time.Duration]chan time.Time{}}
}

func (c *splitClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	ch, found := c.timer[d]
	if !found {
		ch = make(chan time.Time, 1)
		c.timer[d] = ch
	}
	c.mu.Unlock()
	c.added <- d
	return ch
}

// fireTimer fires the timer of d.
func (c *splitClock) fireTimer(d time.Duration) {
	c.mu.Lock()
	ch := c.timer[d]
	c.mu.Unlock()
	ch <- c.Now()
}

// startKeyCmd runs "sard-agent repo key <args>" as root in the background.
func (h *keysHost) startKeyCmd(ctx context.Context, args ...string) <-chan cmdResult {
	h.deps.euid = 0
	done := make(chan cmdResult, 1)
	go func() {
		var out, errOut bytes.Buffer
		code := runRepoWithDeps(ctx, append([]string{"key"}, h.subst(args)...), &out, &errOut, h.deps)
		done <- cmdResult{code, out.String(), errOut.String()}
	}()
	return done
}

// verifierRole is a host whose certificate says it is a verifier.
func verifierRole(string) role.Role { return role.Verifier }

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// Rule "restic под root получает из env_file только разрешённые переменные
// своего вида хранилища" (Р72).

// extraRepoHost has the repository extra at the address, with an env_file
// that holds the content (none if it is empty), and the snapshots of base.
func extraRepoHost(t *testing.T, url, envContent string) (*setupHost, string) {
	t.Helper()
	h := restoreHost(t)
	env := ""
	if envContent != "" {
		env = h.path("secrets/restic-extra.env")
		h.write(env, envContent, 0o600)
	}
	h.write(h.path("agent.d/repo-extra.yaml"), string(hostsetup.RepositoryYAMLWithEnv("extra", url, h.path("secrets/base.pass"), env)), 0o640)
	repo := h.restic.repo(url)
	repo.initialized = true
	repo.snapshots = snapshotSet()
	repo.restoreState.files = h.base().restoreState.files
	return h, env
}

func (h *setupHost) restoreExtra() (int, string, string) {
	return h.sudo("repo", "restore", "extra", "aaaa1111", "--target", h.target(), "--config", "C")
}

// assertNoRootRestic: no restic ran as root, and so none restored.
func (h *setupHost) assertNoRootRestic() {
	h.t.Helper()
	for _, c := range h.restic.calls {
		if c.runAs == nil {
			h.t.Errorf("restic %v ran as root", c.args)
		}
	}
}

const (
	s3URL   = "s3:https://s3.example.com/bucket-b/extra"
	sftpURL = "sftp://backup@sftp.example.com:2222/srv/restic"
)

func TestAForeignVariableInTheEnvFileIsRefusedBeforeResticRunsAsRoot(t *testing.T) {
	for _, c := range []struct{ url, line, name string }{
		{s3URL, "RCLONE_PASSWORD_COMMAND=touch /pwned", "RCLONE_PASSWORD_COMMAND"},
		{s3URL, "AWS_SHARED_CREDENTIALS_FILE=/etc/shadow", "AWS_SHARED_CREDENTIALS_FILE"},
		{s3URL, "HTTPS_PROXY=http://evil.example.com:3128", "HTTPS_PROXY"},
		{s3URL, "SSL_CERT_FILE=/etc/shadow", "SSL_CERT_FILE"},
		{sftpURL, "AWS_ACCESS_KEY_ID=VALUE-MARKER", "AWS_ACCESS_KEY_ID"},
		{"rest:https://rest.example.com/x", "HTTP_PROXY=http://evil.example.com", "HTTP_PROXY"},
	} {
		h, env := extraRepoHost(t, c.url, "# the keys\n\n"+c.line+"\n")
		before := h.hostTree()
		code, stdout, stderr := h.restoreExtra()
		assertRefusal(t, code, stderr, exitUsage, "ENV_FILE_INVALID")
		for _, want := range []string{env, "line 3", c.name} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%s: stderr lacks %q: %s", c.name, want, stderr)
			}
		}
		if value := c.line[strings.Index(c.line, "=")+1:]; strings.Contains(stderr+stdout, value) {
			t.Errorf("%s: the value is in the output: %s", c.name, stderr)
		}
		h.assertNoRootRestic()
		h.assertHostUnchanged(before)
	}
}

func TestALocalRepositoryTakesNoVariableFromTheEnvFileUnderRoot(t *testing.T) {
	h := restoreHost(t)
	env := h.path("secrets/restic-base.env")
	h.write(env, "HTTPS_PROXY=http://evil.example.com:3128\n", 0o600)
	h.cfg.Repositories[0].EnvFile = env
	h.saveConfig()
	code, _, stderr := h.restore(idF)
	assertRefusal(t, code, stderr, exitUsage, "ENV_FILE_INVALID")
	h.assertNoRootRestic()
}

func TestARepositoryWithoutAnAllowlistIsNotRestoredAsRoot(t *testing.T) {
	for url, kind := range map[string]string{
		"rclone:remote:x": "rclone", "azure:container:/x": "azure", "b2:bucket:x": "b2", "gs:bucket:/x": "gs", "swift:container:/x": "swift",
	} {
		h, _ := extraRepoHost(t, url, "")
		before := h.hostTree()
		code, _, stderr := h.restoreExtra()
		assertRefusal(t, code, stderr, exitUsage, "BACKEND_NOT_SUPPORTED")
		if !strings.Contains(stderr, kind) {
			t.Errorf("%s: stderr %q", url, stderr)
		}
		h.assertNoBackendCalls()
		h.assertHostUnchanged(before)
	}
}

func TestTheAllowedVariablesOfS3ReachResticAsRoot(t *testing.T) {
	h, _ := extraRepoHost(t, s3URL, "# keys\nAWS_ACCESS_KEY_ID=K\n\nAWS_SECRET_ACCESS_KEY=S\nAWS_SESSION_TOKEN=T\nAWS_DEFAULT_REGION=eu-west-1\n")
	code, _, stderr := h.restoreExtra()
	if code != exitOK {
		t.Fatalf("code %d: %s", code, stderr)
	}
	env := h.restic.callsTo(s3URL, "restore")[0].env
	for _, want := range []string{"AWS_ACCESS_KEY_ID=K", "AWS_SECRET_ACCESS_KEY=S", "AWS_SESSION_TOKEN=T", "AWS_DEFAULT_REGION=eu-west-1"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s: %q", want, env)
		}
	}
}

func TestTheListOfTheServiceUserStillTakesOtherVariables(t *testing.T) {
	h := snapshotsHost(t)
	url := s3URL
	env := h.path("secrets/restic-extra.env")
	h.write(env, "AWS_ACCESS_KEY_ID=K\nAWS_SECRET_ACCESS_KEY=S\nHTTPS_PROXY=http://proxy.example.com:3128\n", 0o600)
	h.write(h.path("agent.d/repo-extra.yaml"), string(hostsetup.RepositoryYAMLWithEnv("extra", url, h.path("secrets/base.pass"), env)), 0o640)
	h.restic.repo(url).snapshots = snapshotSet()
	code, _, stderr := h.sudo("repo", "snapshots", "extra", "--config", "C")
	if code != exitOK {
		t.Fatalf("code %d: %s", code, stderr)
	}
	c := h.restic.callsTo(url, "snapshots")[0]
	if c.runAs == nil || c.runAs.UID != serviceUID || !slices.Contains(c.env, "HTTPS_PROXY=http://proxy.example.com:3128") {
		t.Errorf("ran as %+v with env %q", c.runAs, c.env)
	}
}

// Rule "restic под root получает пароль от агента каналом, файл пароля читает
// агент" (Р78).

func TestAnUnusableRepositoryPasswordFileIsRefusedBeforeResticRunsAsRootAndHangsNothing(t *testing.T) {
	for name, c := range map[string]struct {
		arrange func(h *setupHost, pass string)
		reason  string
	}{
		"fifo": {func(h *setupHost, pass string) {
			ok(t, os.Remove(pass))
			ok(t, syscall.Mkfifo(pass, 0o600))
		}, "SECRET_FILE_REJECTED"},
		"link": {func(h *setupHost, pass string) {
			h.write(h.path("secrets/other.pass"), "other\n", 0o600)
			ok(t, os.Remove(pass))
			ok(t, os.Symlink(h.path("secrets/other.pass"), pass))
		}, "SECRET_FILE_REJECTED"},
		"another owner": {func(h *setupHost, pass string) { h.fsys.setOwner(pass, aliceUID, aliceUID) }, "SECRET_FILE_REJECTED"},
		"mode 0644":     {func(h *setupHost, pass string) { ok(t, os.Chmod(pass, 0o644)) }, "SECRET_FILE_REJECTED"},
		"two names": {func(h *setupHost, pass string) {
			ok(t, os.Link(pass, h.path("secrets/second-name")))
		}, "SECRET_FILE_REJECTED"},
		"missing": {func(h *setupHost, pass string) { ok(t, os.Remove(pass)) }, "PASSWORD_FILE_MISSING"},
	} {
		h := restoreHost(t)
		// the real read of a secret file, as the owner the test process is
		h.deps.readOwned = func(path string, _ uint32) ([]byte, error) { return secrets.ReadOwned(path, uint32(os.Getuid())) }
		c.arrange(h, h.path("secrets/base.pass"))
		before := h.hostTree()
		done := make(chan struct{})
		var code int
		var stderr string
		go func() {
			defer close(done)
			code, _, stderr = h.restore(idF)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the command hangs", name)
		}
		if code != exitUsage || !strings.Contains(stderr, c.reason) {
			t.Errorf("%s: exit %d, stderr %q", name, code, stderr)
		}
		h.assertNoRootRestic()
		h.assertHostUnchanged(before)
	}
}

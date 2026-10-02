// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is the agent's log: the agent writes to it from several goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startAgentWith runs the agent against a closed port until a short timeout
// with extra config lines, and returns the exit code and the agent's log.
func startAgentWith(t *testing.T, extra func(dir string) string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	ca := writeIdentity(t, dir)
	cfg := "server:\n  address: 127.0.0.1:1\n" +
		"tls: {ca_file: " + ca + ", cert_file: " + dir + "/agent.pem, key_file: " + dir + "/agent.key}\n" +
		"executor: {state_dir: " + dir + "/state}\n" + withRestic(t) + extra(dir)
	var log lockedBuffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var out, errOut bytes.Buffer
	code := run(ctx, []string{"--config", writeConfig(t, cfg)}, &out, &errOut, fixedHostname)
	return code, log.String()
}

func secretsConfig(t *testing.T, files map[string]string) func(dir string) string {
	return func(dir string) string {
		cfg := "secrets:\n"
		for name, value := range files {
			path := filepath.Join(dir, name)
			if value != "<missing>" {
				writeFile(t, path, []byte(value+"\n"), 0o600)
			}
			cfg += "  " + name + ": " + path + "\n"
		}
		return cfg
	}
}

func TestAShortSecretIsWarnedAboutAtStartAndTheAgentStillStarts(t *testing.T) {
	code, log := startAgentWith(t, secretsConfig(t, map[string]string{"short": "ab7"}))
	if code != 0 {
		t.Fatalf("code = %d, log:\n%s", code, log)
	}
	warnings := 0
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "level=WARN") && strings.Contains(line, "short") && strings.Contains(line, "less readable") {
			warnings++
		}
	}
	if warnings != 1 || strings.Contains(log, "ab7") {
		t.Fatalf("log:\n%s", log)
	}
}

func TestAnUnreadableSecretFileAtStartIsNamedWithoutItsPath(t *testing.T) {
	var path string
	code, log := startAgentWith(t, func(dir string) string {
		path = filepath.Join(dir, "tok")
		return secretsConfig(t, map[string]string{"tok": "<missing>"})(dir)
	})
	if code != 0 || !strings.Contains(log, "level=WARN") || !strings.Contains(log, "tok") || strings.Contains(log, path) {
		t.Fatalf("code = %d, log:\n%s", code, log)
	}
}

func TestAShortEnvFileValueIsWarnedAboutAtStartByRepositoryAndVariable(t *testing.T) {
	_, log := startAgentWith(t, func(dir string) string {
		env := filepath.Join(dir, "main.env")
		writeFile(t, env, []byte("AWS_DEFAULT_REGION=eu\n"), 0o600)
		return "repositories:\n  - {name: main, url: " + dir + "/repo, password_file: " + dir + "/pass, env_file: " + env + "}\n"
	})
	if !strings.Contains(log, "main") || !strings.Contains(log, "AWS_DEFAULT_REGION") || strings.Contains(log, "=eu") {
		t.Fatalf("log:\n%s", log)
	}
}

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedHostname() (string, error) { return "db1", nil }

func runAgent(args ...string) (int, string, string) {
	return runAgentOn(fixedHostname, args...)
}

func runAgentOn(hostname hostnameFunc, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, &out, &errOut, hostname)
	return code, out.String(), errOut.String()
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVersionFlag(t *testing.T) {
	code, out, _ := runAgent("--version")
	if code != 0 || out != "sard-agent dev\n" {
		t.Fatalf("code = %d, out = %q", code, out)
	}
}

func TestMissingConfigIsAUsageError(t *testing.T) {
	code, _, errOut := runAgent()
	if code != 2 || !strings.Contains(errOut, "--config <path> is required") || !strings.Contains(errOut, "-config string") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

// "enroll" alone, with no other arguments, must still dispatch to the
// enroll subcommand (len(args) > 0, not > 1 — a boundary a "just enroll,
// no flags" invocation is the only way to exercise).
func TestEnrollWithNoOtherArgumentsDispatchesToEnroll(t *testing.T) {
	code, _, errOut := runAgent("enroll")
	if code != exitUsage {
		t.Fatalf("code = %d, want usage", code)
	}
	if strings.Contains(errOut, "--config <path> is required") {
		t.Fatalf("stderr = %q: dispatched to the agent command, not enroll", errOut)
	}
	if !strings.Contains(errOut, "enrollment token") {
		t.Fatalf("stderr = %q, want the enroll subcommand's own missing-token message", errOut)
	}
}

func TestUnknownFlagIsAUsageError(t *testing.T) {
	code, _, errOut := runAgent("--listen", ":9090")
	if code != 2 || !strings.Contains(errOut, "flag provided but not defined: -listen") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestUnknownFlagAfterConfigStopsBeforeStarting(t *testing.T) {
	cfg := writeConfig(t, "server:\n  address: sard.example.com:9090\n")
	code, out, _ := runAgent("--config", cfg, "--listen", ":9090")
	if code != 2 || out != "" {
		t.Fatalf("code = %d, stdout = %q", code, out)
	}
}

// withRestic is the YAML that points restic.path at a restic of the pinned
// version: since A5b the agent does not start without a usable one.
func withRestic(t *testing.T) string {
	t.Helper()
	return "restic: {path: " + resticScript(t, filepath.Join(t.TempDir(), "restic"), restic.Pinned.String()) + "}\n"
}

func TestHostnameFailure(t *testing.T) {
	cfg := writeConfig(t, "server:\n  address: sard.example.com:9090\n"+withRestic(t))
	broken := func() (string, error) { return "", errors.New("uname failed") }
	code, _, errOut := runAgentOn(broken, "--config", cfg)
	if code != 1 || errOut != "sard-agent: hostname: uname failed\n" {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestConfigErrorsExitWithOne(t *testing.T) {
	cases := map[string]string{
		filepath.Join(t.TempDir(), "missing.yaml"): "no such file",
		writeConfig(t, "server: ["):                "parse config",
		writeConfig(t, "tls:\n  ca_file: /x\n"):    "server.address is required",
	}
	for path, want := range cases {
		code, _, errOut := runAgent("--config", path)
		if code != 1 || !strings.HasPrefix(errOut, "sard-agent: ") || !strings.Contains(errOut, want) {
			t.Errorf("config %s: code = %d, stderr = %q, want %q", path, code, errOut, want)
		}
	}
}

// Without tls.* the agent cannot dial and says which setting is missing.
func TestConfigWithoutTLSStopsBeforeDialing(t *testing.T) {
	cfg := "server:\n  address: sard.example.com:9090\n" + withRestic(t)
	code, _, errOut := runAgent("--config", writeConfig(t, cfg))
	if code != 1 || errOut != "sard-agent: invalid transport options: tls.ca_file is required\n" {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

// With a valid config the agent announces the server and keeps dialing it
// over TLS; nothing listens on the port here, so it retries until stopped,
// and a stop is a clean exit.
func TestValidConfigDialsTheServerUntilStopped(t *testing.T) {
	dir := t.TempDir()
	ca := writeIdentity(t, dir)
	cfg := "server:\n  address: 127.0.0.1:1\n" +
		"tls: {ca_file: " + ca + ", cert_file: " + dir + "/agent.pem, key_file: " + dir + "/agent.key}\n" +
		"executor: {state_dir: " + dir + "/state}\n" + withRestic(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var out, errOut bytes.Buffer
	code := run(ctx, []string{"--config", writeConfig(t, cfg)}, &out, &errOut, fixedHostname)
	if code != 0 || out.String() != "sard-agent dev: connecting to 127.0.0.1:1\n" || errOut.String() != "" {
		t.Fatalf("code = %d, out = %q, stderr = %q", code, out.String(), errOut.String())
	}
}

// The executor's state dir is checked before the agent connects.
func TestAnUnusableStateDirStopsBeforeDialing(t *testing.T) {
	dir := t.TempDir()
	ca := writeIdentity(t, dir)
	cfg := "server:\n  address: 127.0.0.1:1\n" +
		"tls: {ca_file: " + ca + ", cert_file: " + dir + "/agent.pem, key_file: " + dir + "/agent.key}\n" +
		"executor: {state_dir: " + ca + "/state}\n" + withRestic(t) // under a file
	code, _, errOut := runAgent("--config", writeConfig(t, cfg))
	if code != 1 || !strings.Contains(errOut, "invalid executor options") {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

func TestTheExecutorKnowsTheConfiguredRepositories(t *testing.T) {
	got := repositoryNames([]config.Repository{{Name: "main"}, {Name: "offsite"}})
	if strings.Join(got, ",") != "main,offsite" {
		t.Fatalf("names = %v", got)
	}
}

func TestEveryConfiguredRepositoryIsOpenedByName(t *testing.T) {
	repos := openRepositories(config.Config{Repositories: []config.Repository{{Name: "main"}, {Name: "offsite"}}}, restic.Options{})
	if _, ok := repos.get("offsite"); !ok || len(repos) != 2 {
		t.Errorf("repositories = %v", repos)
	}
	if _, ok := repos.get("nas"); ok {
		t.Error("an unconfigured repository was found")
	}
}

func TestRestoredCopiesLiveInTheExecutorStateDir(t *testing.T) {
	if got := restoreDir(""); got != "/var/lib/sard-agent/executor/restore" {
		t.Errorf("default = %q", got)
	}
	if got := restoreDir("/srv/sard"); got != "/srv/sard/restore" {
		t.Errorf("configured = %q", got)
	}
}

func TestExecutorStateDirDefaultsUnderTheSystemdStateDirectory(t *testing.T) {
	if got := executorStateDir(""); got != "/var/lib/sard-agent/executor" {
		t.Errorf("default = %q", got)
	}
	if got := executorStateDir("/srv/sard"); got != "/srv/sard" {
		t.Errorf("configured = %q", got)
	}
}

func selfSignedPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// TestMainProcess runs the real binary entry point in a child process.
func TestMainProcess(t *testing.T) {
	if os.Getenv("SARD_AGENT_TEST_MAIN") == "1" {
		os.Args = []string{"sard-agent", "--version"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainProcess$")
	cmd.Env = append(os.Environ(), "SARD_AGENT_TEST_MAIN=1")
	out, err := cmd.Output()
	if err != nil || string(out) != "sard-agent dev\n" {
		t.Fatalf("err = %v, out = %q", err, out)
	}
}

// A1: a secret file open to the group or others stops the agent before it
// tries to connect to anything.
func TestASecretFileOpenBeyondItsOwnerRefusesStart(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "agent.key")
	if err := os.WriteFile(keyFile, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	cfg := "server:\n  address: sard.example.com:9090\n" +
		"tls: {ca_file: " + dir + "/ca.pem, cert_file: " + dir + "/agent.pem, key_file: " + keyFile + "}\n"
	code, _, errOut := runAgent("--config", writeConfig(t, cfg))
	if code != 1 || !strings.Contains(errOut, "tls.key_file") || !strings.Contains(errOut, keyFile) {
		t.Fatalf("code = %d, stderr = %q", code, errOut)
	}
}

// Without restic.path the agent runs the restic shipped next to it.
func TestResticPath(t *testing.T) {
	exe := func() (string, error) { return "/opt/sard/bin/sard-agent", nil }
	if got, err := resticPath("", exe); got != "/opt/sard/bin/restic" || err != nil {
		t.Errorf("default = %q, %v", got, err)
	}
	if got, err := resticPath("/usr/bin/restic", exe); got != "/usr/bin/restic" || err != nil {
		t.Errorf("configured = %q, %v", got, err)
	}
	broken := func() (string, error) { return "", errors.ErrUnsupported }
	if _, err := resticPath("", broken); !errors.Is(err, errors.ErrUnsupported) || !strings.HasPrefix(err.Error(), "restic.path: ") {
		t.Errorf("no executable: %v", err)
	}
}

func TestStartStopsWhenResticCannotBeLocated(t *testing.T) {
	cfg := writeConfig(t, "server:\n  address: sard.example.com:9090\n")
	broken := func() (string, error) { return "", errors.ErrUnsupported }
	err := start(context.Background(), cfg, io.Discard, fixedHostname, broken)
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

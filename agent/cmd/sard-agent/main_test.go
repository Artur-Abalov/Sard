// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

func TestHostnameFailure(t *testing.T) {
	cfg := writeConfig(t, "server:\n  address: sard.example.com:9090\n")
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

// With a valid config the agent announces the server and stops at the
// transport stub, which is not implemented yet.
func TestValidConfigReachesTheTransportStub(t *testing.T) {
	code, out, errOut := runAgent("--config", writeConfig(t, "server:\n  address: sard.example.com:9090\n"))
	if code != 1 || out != "sard-agent dev: connecting to sard.example.com:9090\n" || errOut != "sard-agent: register: not implemented\n" {
		t.Fatalf("code = %d, out = %q, stderr = %q", code, out, errOut)
	}
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

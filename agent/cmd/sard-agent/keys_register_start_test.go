// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// docs/specs/agent/restic-keys.feature, rule "Служба сообщает серверу признак
// ключа восстановления при каждом Register" (@start): the agent starts against
// a server of a test CA, with a shell script for restic that answers key list.

const (
	registerRepoID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	keyIDR         = "11111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	keyIDV         = "22222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	keyIDH         = "33333333cccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func jsonKey(id, user, host string) string {
	return `{"current":false,"id":"` + id + `","userName":"` + user + `","hostName":"` + host + `","created":"2026-10-20T10:00:00Z"}`
}

// keyedHost is an agent host with the repository base, which the script of
// restic opens (cat answers its id) and whose key list is the file keys.
type keyedHost struct {
	*agentHost
	keys  string // the file the script prints for key list
	calls string // every invocation of restic: its arguments
}

func newKeyedHost(t *testing.T, keysJSON string, keyBranch string) *keyedHost {
	t.Helper()
	h := &keyedHost{agentHost: newAgentHost(t)}
	h.keys = filepath.Join(h.dir, "keys.json")
	h.calls = filepath.Join(h.dir, "restic-args")
	h.setKeys(keysJSON)
	if keyBranch == "" {
		keyBranch = `cat ` + h.keys
	}
	script := "#!/bin/sh\necho \"$*\" >> " + h.calls + "\ncase \"$1\" in\n" +
		"  version) echo 'restic 0.19.1 compiled with go1.26.4 on linux/amd64';;\n" +
		"  cat) echo '{\"version\":2,\"id\":\"" + registerRepoID + "\"}';;\n" +
		"  key) " + keyBranch + ";;\nesac\n"
	h.restic = "restic: {path: " + writeScript(t, filepath.Join(h.dir, "restic"), script) + ", cache_dir: " + t.TempDir() + "}\n"
	pass := filepath.Join(h.dir, "base.pass")
	writeFile(t, pass, []byte(passMarker+"\n"), 0o600)
	h.extra = "repositories:\n  - {name: base, url: rest:http://qa:" + urlMarker + "@127.0.0.1:9/base, password_file: " + pass + "}\n"
	return h
}

func (h *keyedHost) setKeys(keysJSON string) {
	h.t.Helper()
	writeFile(h.t, h.keys, []byte(keysJSON), 0o600)
}

func list(keys ...string) string { return "[" + strings.Join(keys, ",") + "]" }

func (h *keyedHost) registeredRepository(n int) *agentv1.RepositoryInfo {
	h.t.Helper()
	code, _, stderr := h.registered(n)
	if code != 0 {
		h.t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	repos := h.server.registerRequests()[n-1].GetRepositories()
	if len(repos) != 1 || repos[0].GetName() != "base" {
		h.t.Fatalf("repositories %v", repos)
	}
	return repos[0]
}

func TestRegisterCarriesTheRecoveryKeyStateOfEveryRepository(t *testing.T) {
	for _, c := range []struct {
		name string
		keys string
		want agentv1.RecoveryKeyState
	}{
		{"H and R", list(jsonKey(keyIDH, "sard-agent", "src1"), jsonKey(keyIDR, "sard:recovery", "src1")), agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_PRESENT},
		{"H and a labelled recovery key", list(jsonKey(keyIDH, "sard-agent", "src1"), jsonKey(keyIDR, "sard:recovery:box-2", "src1")), agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_PRESENT},
		{"H and V", list(jsonKey(keyIDH, "sard-agent", "src1"), jsonKey(keyIDV, "sard:verifier:vrf-1", "src1")), agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_ABSENT},
		{"H only", list(jsonKey(keyIDH, "sard-agent", "src1")), agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_ABSENT},
	} {
		h := newKeyedHost(t, c.keys, "")
		if got := h.registeredRepository(1).GetRecoveryKey(); got != c.want {
			t.Errorf("%s: recovery_key = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAnUninitializedRepositoryIsRegisteredAsUnknownWithoutReadingKeys(t *testing.T) {
	h := newKeyedHost(t, "[]", "")
	script := "#!/bin/sh\necho \"$*\" >> " + h.calls + "\ncase \"$1\" in\n" +
		"  version) echo 'restic 0.19.1 compiled with go1.26.4 on linux/amd64';;\n" +
		"  cat) echo 'Fatal: unable to open config file: does not exist' >&2; exit 10;;\n" +
		"  key) cat " + h.keys + ";;\nesac\n"
	writeScript(t, filepath.Join(h.dir, "restic"), script)
	if got := h.registeredRepository(1).GetRecoveryKey(); got != agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_UNSPECIFIED {
		t.Errorf("recovery_key = %v", got)
	}
	if calls := fileContent(t, h.calls); strings.Contains(calls, "key list") {
		t.Errorf("restic key list was called:\n%s", calls)
	}
}

func TestAFailureToReadTheKeysDoesNotStopTheRegistration(t *testing.T) {
	for name, branch := range map[string]string{
		"exit code 1":  `echo "Fatal: ` + urlMarker + ` ` + passMarker + ` unavailable" >&2; exit 1`,
		"not JSON":     `echo "this is not json"`,
		"empty answer": `true`,
	} {
		h := newKeyedHost(t, "[]", branch)
		if got := h.registeredRepository(1).GetRecoveryKey(); got != agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_UNSPECIFIED {
			t.Errorf("%s: recovery_key = %v", name, got)
		}
		logged := h.log.String()
		if !strings.Contains(logged, "level=WARN") || !strings.Contains(logged, "base") {
			t.Errorf("%s: no WARN line that names the repository:\n%s", name, logged)
		}
		for _, secret := range []string{passMarker, urlMarker} {
			if strings.Contains(logged, secret) {
				t.Errorf("%s: the log holds %s:\n%s", name, secret, logged)
			}
		}
	}
}

func TestTheRecoveryKeyStateIsReadAgainAtEveryReconnect(t *testing.T) {
	h := newKeyedHost(t, list(jsonKey(keyIDH, "sard-agent", "src1")), "")
	h.server.dropStream = true
	h.server.onRegister = func() {
		if len(h.server.registerRequests()) == 1 {
			h.setKeys(list(jsonKey(keyIDH, "sard-agent", "src1"), jsonKey(keyIDR, "sard:recovery", "src1")))
		}
	}
	code, _, stderr := h.registered(2)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	regs := h.server.registerRequests()
	first, second := regs[0].GetRepositories()[0].GetRecoveryKey(), regs[1].GetRepositories()[0].GetRecoveryKey()
	if first != agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_ABSENT || second != agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_PRESENT {
		t.Errorf("first %v, second %v", first, second)
	}
}

func TestRegisterHoldsNothingAboutTheKeysButTheState(t *testing.T) {
	h := newKeyedHost(t, list(jsonKey(keyIDH, "sard-agent", "src1"), jsonKey(keyIDR, "sard:recovery", "src1"), jsonKey(keyIDV, "sard:verifier:vrf-1", "src1")), "")
	h.registeredRepository(1)
	wire := h.server.registerRequests()[0].String()
	for _, leak := range []string{keyIDH, keyIDR, keyIDV, keyIDH[:8], "vrf-1", "sard:verifier", "sard:recovery", "src1", passMarker} {
		if strings.Contains(wire, leak) {
			t.Errorf("the Register request holds %q: %s", leak, wire)
		}
	}
}

func TestTheKeysAreReadWithoutTheLock(t *testing.T) {
	h := newKeyedHost(t, list(jsonKey(keyIDH, "sard-agent", "src1")), "")
	h.registeredRepository(1)
	calls, err := os.ReadFile(h.calls)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, line := range strings.Split(string(calls), "\n") {
		if strings.HasPrefix(line, "key list ") {
			found = true
			if !strings.Contains(line, "--json") || !strings.Contains(line, "--no-lock") {
				t.Errorf("restic %s", line)
			}
		}
	}
	if !found {
		t.Errorf("restic key list was not called:\n%s", calls)
	}
}

func TestAVerifierReportsTheRecoveryKeyStateToo(t *testing.T) {
	h := newKeyedHost(t, list(jsonKey(keyIDH, "sard-agent", "src1"), jsonKey(keyIDV, "sard:verifier:vrf-1", "src1")), "")
	installPair(t, h.dir, withRole("verifier")...)
	if got := h.registeredRepository(1).GetRecoveryKey(); got != agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_ABSENT {
		t.Errorf("recovery_key = %v", got)
	}
}

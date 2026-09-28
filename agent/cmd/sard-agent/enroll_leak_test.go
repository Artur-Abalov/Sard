// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
)

// leakCase is one class' outcome: what exit code it produced, what it
// should have produced, the secret that must never appear, and the
// command's full output.
type leakCase struct {
	code, wantCode int
	secret         string
	out, errOut    string
}

// Ни токен, ни ключ не попадают в вывод команды
func TestNeitherTheTokenNorTheKeyAppearInCommandOutput(t *testing.T) {
	cases := map[string]func(t *testing.T) leakCase{
		"успех":             leakCaseSuccess,
		"использование":     leakCaseUsage,
		"отказ по токену":   leakCaseTokenRefused,
		"идентичность есть": leakCaseIdentityExists,
		"доверие":           leakCaseTrust,
		"временная":         leakCaseTemporary,
		"запись":            leakCaseWrite,
		"ошибка агента":     leakCaseAgentError,
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			requireLeakCase(t, run(t))
		})
	}
}

func requireLeakCase(t *testing.T, c leakCase) {
	t.Helper()
	if c.code != c.wantCode {
		t.Fatalf("code = %d, want %d", c.code, c.wantCode)
	}
	combined := c.out + c.errOut
	if c.secret != "" && strings.Contains(combined, c.secret) {
		t.Errorf("output leaked the token: %q", combined)
	}
	if strings.Contains(combined, "PRIVATE KEY") {
		t.Errorf("output leaked private key material: %q", combined)
	}
}

func leakCaseSuccess(t *testing.T) leakCase {
	f := newSucceedingFakeFixture(t, "a1")
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	return leakCase{code, exitOK, f.token, out, errOut}
}

func leakCaseUsage(t *testing.T) leakCase {
	f := newLocalFixture(t)
	bad := "not-a-token"
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", bad)
	return leakCase{code, exitUsage, bad, out, errOut}
}

func leakCaseTokenRefused(t *testing.T) leakCase {
	f := newFakeFixture(t, []string{"127.0.0.1"}, failingAnswer(codes.Unauthenticated, "TOKEN_USED"))
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	return leakCase{code, exitTokenRefused, f.token, out, errOut}
}

func leakCaseIdentityExists(t *testing.T) leakCase {
	f := newLocalFixture(t)
	writeAgentCertForTest(t, f.h.certFile, "existing")
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	return leakCase{code, exitIdentityExists, f.token, out, errOut}
}

func leakCaseTrust(t *testing.T) leakCase {
	f := newSucceedingFakeFixture(t, "a1")
	wrong := newToken(t, strings.Repeat("0", 64))
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", wrong)
	return leakCase{code, exitTrust, wrong, out, errOut}
}

func leakCaseTemporary(t *testing.T) leakCase {
	h := newHost(t, "127.0.0.1:1")
	token := newToken(t, strings.Repeat("a", 64))
	code, out, errOut := runEnrollCmdTest("--config", h.configPath, "--token", token)
	return leakCase{code, exitTemporary, token, out, errOut}
}

func leakCaseWrite(t *testing.T) leakCase {
	f := newLocalFixture(t)
	f.h.configPath = writeConfigWithMovedFile(t, f.h, "cert", filepath.Join(f.h.dir, "no-such-cert-dir"))
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	return leakCase{code, exitWrite, f.token, out, errOut}
}

// F3: a token pasted without --token (a positional argument), or pasted
// as the --token-file path by mistake, must never be echoed back.
func TestPositionalArgumentsAndTokenFilePathsAreNeverEchoedBack(t *testing.T) {
	f := newLocalFixture(t)
	t.Run("token pasted without --token", func(t *testing.T) {
		code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token, f.token)
		if code != exitUsage {
			t.Fatalf("code = %d, want usage; stderr = %q", code, errOut)
		}
		if strings.Contains(errOut, f.token) || strings.Contains(errOut, "sard_") {
			t.Errorf("stderr echoed the positional token: %q", errOut)
		}
	})
	t.Run("token pasted as --token-file path", func(t *testing.T) {
		code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token-file", f.token)
		if code != exitUsage {
			t.Fatalf("code = %d, want usage; stderr = %q", code, errOut)
		}
		if strings.Contains(errOut, f.token) || strings.Contains(errOut, "sard_") {
			t.Errorf("stderr echoed the token passed as --token-file: %q", errOut)
		}
	})
}

func leakCaseAgentError(t *testing.T) leakCase {
	f := newFakeFixture(t, []string{"127.0.0.1"}, failingAnswer(codes.InvalidArgument, "CSR_INVALID"))
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	return leakCase{code, exitAgentError, f.token, out, errOut}
}

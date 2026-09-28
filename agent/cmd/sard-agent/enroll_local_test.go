// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// localFixture is a host whose server.address points at a running stub
// server, plus a token that would succeed if the command ever reached it.
// @local scenarios refuse before contacting the server; every test here
// finishes by checking the stub's call count is exactly 0.
type localFixture struct {
	h     *host
	ca    *testCA
	srv   *enrollServer
	token string
}

func newLocalFixture(t *testing.T) *localFixture {
	t.Helper()
	ca := newTestCA(t)
	leaf := chainOf(ca.leaf(t, []string{"127.0.0.1"}, 0), ca)
	srv := &enrollServer{answer: succeedingAnswer(ca, "a1")}
	addr := startFakeServer(t, leaf, srv)
	h := newHost(t, addr)
	token := newToken(t, ca.fingerprint())
	return &localFixture{h: h, ca: ca, srv: srv, token: token}
}

func (f *localFixture) requireServerNotContacted(t *testing.T) {
	t.Helper()
	if f.srv.callCount() != 0 {
		t.Fatalf("server was contacted %d times, want 0", f.srv.callCount())
	}
}

func (f *localFixture) requireHostUnchanged(t *testing.T) {
	t.Helper()
	for _, p := range []string{f.h.keyFile, f.h.certFile, f.h.caFile} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s exists after a refused command, want the host untouched", p)
		}
	}
}

// Несколько источников токена — ошибка использования
func TestSeveralTokenSourcesAreAUsageError(t *testing.T) {
	cases := []struct {
		name     string
		useToken bool
		useFile  bool
		useEnv   bool
	}{
		{"--token and --token-file", true, true, false},
		{"--token and SARD_ENROLL_TOKEN", true, false, true},
		{"--token-file and SARD_ENROLL_TOKEN", false, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newLocalFixture(t)
			args := tokenSourceArgs(t, f.token, c.useToken, c.useFile, c.useEnv)
			args = append([]string{"--config", f.h.configPath}, args...)
			code, _, errOut := runEnrollCmdTest(args...)
			if code != exitUsage {
				t.Fatalf("code = %d, want %d (usage); stderr = %q", code, exitUsage, errOut)
			}
			if !strings.Contains(errOut, "--token") {
				t.Errorf("stderr does not name a conflicting source: %q", errOut)
			}
			if strings.Contains(errOut, f.token) {
				t.Fatal("stderr leaked the token")
			}
			f.requireServerNotContacted(t)
			f.requireHostUnchanged(t)
		})
	}
}

// tokenSourceArgs builds --token/--token-file/env flags for token per the
// requested combination of sources.
func tokenSourceArgs(t *testing.T, token string, useToken, useFile, useEnv bool) []string {
	t.Helper()
	var args []string
	if useToken {
		args = append(args, "--token", token)
	}
	if useFile {
		tokFile := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(tokFile, []byte(token), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--token-file", tokFile)
	}
	if useEnv {
		t.Setenv("SARD_ENROLL_TOKEN", token)
	}
	return args
}

// --token, --token-file и SARD_ENROLL_TOKEN все три сразу
func TestAllThreeTokenSourcesAreAUsageError(t *testing.T) {
	f := newLocalFixture(t)
	tokFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokFile, []byte(f.token), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SARD_ENROLL_TOKEN", f.token)
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token, "--token-file", tokFile)
	if code != exitUsage {
		t.Fatalf("code = %d, want usage; stderr = %q", code, errOut)
	}
	f.requireServerNotContacted(t)
}

// Без токена команда отказывает с ошибкой использования
func TestNoTokenIsAUsageError(t *testing.T) {
	f := newLocalFixture(t)
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath)
	if code != exitUsage {
		t.Fatalf("code = %d, want %d; stderr = %q", code, exitUsage, errOut)
	}
	for _, want := range []string{"--token", "--token-file", "SARD_ENROLL_TOKEN"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr does not mention %q: %q", want, errOut)
		}
	}
	f.requireServerNotContacted(t)
}

// Файл токена не найден
func TestTokenFileNotFound(t *testing.T) {
	f := newLocalFixture(t)
	missing := filepath.Join(t.TempDir(), "nope")
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token-file", missing)
	if code != exitUsage || !strings.Contains(errOut, missing) {
		t.Fatalf("code = %d, stderr = %q, want usage naming %q", code, errOut, missing)
	}
	f.requireServerNotContacted(t)
}

// Пустой файл токена отклоняется
func TestEmptyTokenFileIsRejected(t *testing.T) {
	f := newLocalFixture(t)
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token-file", empty)
	if code != exitUsage || !strings.Contains(errOut, "empty") {
		t.Fatalf("code = %d, stderr = %q, want usage saying the file is empty", code, errOut)
	}
	f.requireServerNotContacted(t)
}

// Прочие лишние символы в файле токена делают его повреждённым
func TestOtherStrayCharactersInTheTokenFileAreMalformed(t *testing.T) {
	cases := map[string]func(token string) string{
		"с пробелом в начале":    func(tok string) string { return " " + tok },
		"с пробелом в конце":     func(tok string) string { return tok + " " },
		"и два перевода строки":  func(tok string) string { return tok + "\n\n" },
		"и вторую строку токена": func(tok string) string { return tok + "\n" + tok },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLocalFixture(t)
			path := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(path, []byte(mutate(f.token)), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token-file", path)
			if code != exitUsage || !strings.Contains(errOut, "TOKEN_MALFORMED") {
				t.Fatalf("code = %d, stderr = %q, want usage naming TOKEN_MALFORMED", code, errOut)
			}
			f.requireServerNotContacted(t)
		})
	}
}

// Пустое значение флага --token считается заданным и повреждённым
func TestEmptyTokenFlagValueIsSetAndMalformed(t *testing.T) {
	f := newLocalFixture(t)
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", "")
	if code != exitUsage || !strings.Contains(errOut, "TOKEN_MALFORMED") {
		t.Fatalf("code = %d, stderr = %q, want usage naming TOKEN_MALFORMED", code, errOut)
	}
	f.requireServerNotContacted(t)
}

// Повреждённая строка токена отклоняется локально
func TestMalformedTokenStringsAreRejectedLocally(t *testing.T) {
	f := newLocalFixture(t)
	valid := f.token
	secretAndFP := strings.TrimPrefix(valid, "sard_")
	secret, fp, _ := strings.Cut(secretAndFP, ".")

	shortSecret := base64.RawURLEncoding.EncodeToString(make([]byte, 31)) // != 43 chars

	cases := map[string]string{
		"тестовый вектор без префикса sard_":              secretAndFP,
		"тестовый вектор с двумя точками":                 valid + ".",
		"тестовый вектор с секретом из 42 символов":       "sard_" + shortSecret + "." + fp,
		"тестовый вектор с отпечатком в верхнем регистре": "sard_" + secret + "." + strings.ToUpper(fp),
		"тестовый вектор с отпечатком из 63 символов":     "sard_" + secret + "." + fp[:63],
		"тестовый вектор длиннее 113 символов":            valid + "x",
	}
	for name, tokenStr := range cases {
		t.Run(name, func(t *testing.T) {
			code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", tokenStr)
			if code != exitUsage {
				t.Fatalf("code = %d, want usage; stderr = %q", code, errOut)
			}
			if !strings.Contains(errOut, "TOKEN_MALFORMED") {
				t.Errorf("stderr does not name TOKEN_MALFORMED: %q", errOut)
			}
			if !strings.Contains(errOut, "corrupted") || !strings.Contains(errOut, "not help") {
				t.Errorf("stderr does not say the string looks corrupted and retrying will not help: %q", errOut)
			}
			if strings.Contains(errOut, tokenStr) {
				t.Errorf("stderr leaked the token string: %q", errOut)
			}
			f.requireServerNotContacted(t)
			f.requireHostUnchanged(t)
		})
	}
}

// Неразбираемый адрес сервера — ошибка использования
func TestUnparsableServerAddressIsAUsageError(t *testing.T) {
	for _, addr := range []string{"sard.example.com", "https://sard.example.com:9090", "sard.example.com:0", "sard.example.com:65536", "::1:9090"} {
		t.Run(addr, func(t *testing.T) {
			f := newLocalFixture(t)
			code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--server", addr, "--token", f.token)
			if code != exitUsage {
				t.Fatalf("addr %q: code = %d, want usage; stderr = %q", addr, code, errOut)
			}
			if !strings.Contains(errOut, addr) {
				t.Errorf("stderr does not name %q: %q", addr, errOut)
			}
			f.requireServerNotContacted(t)
		})
	}
}

// Неизвестный флаг или лишний аргумент — ошибка использования
func TestUnknownFlagOrExtraArgumentIsAUsageError(t *testing.T) {
	f := newLocalFixture(t)
	t.Run("с флагом --insecure", func(t *testing.T) {
		code, _, _ := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token, "--insecure")
		if code != exitUsage {
			t.Fatalf("code = %d, want usage", code)
		}
	})
	t.Run(`с лишним аргументом "extra"`, func(t *testing.T) {
		code, _, _ := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token, "extra")
		if code != exitUsage {
			t.Fatalf("code = %d, want usage", code)
		}
	})
	f.requireServerNotContacted(t)
}

// Без --config используется конфиг службы по умолчанию
func TestWithoutConfigTheDefaultServiceConfigIsUsed(t *testing.T) {
	// The literal path, not defaultEnrollConfigPath: comparing against the
	// same constant the code returns would pass even if that constant were
	// itself wrong (F9). Going through parseEnrollFlags, not
	// resolveEnrollConfigPath directly, proves the whole flag path reaches
	// the default — not just the helper in isolation.
	opts, code := parseEnrollFlags([]string{"--token", "x"}, io.Discard)
	if code != exitOK {
		t.Fatalf("code = %d, want 0", code)
	}
	if opts.configPath != "/etc/sard/agent.yaml" {
		t.Fatalf("config path = %q, want /etc/sard/agent.yaml", opts.configPath)
	}

	opts, code = parseEnrollFlags([]string{"--token", "x", "--config", "/custom/agent.yaml"}, io.Discard)
	if code != exitOK {
		t.Fatalf("code = %d, want 0", code)
	}
	if opts.configPath != "/custom/agent.yaml" {
		t.Fatalf("config path = %q, want it unchanged", opts.configPath)
	}
}

// Отсутствующий конфиг — ошибка использования
func TestMissingConfigIsAUsageErrorForEnroll(t *testing.T) {
	f := newLocalFixture(t)
	missing := filepath.Join(t.TempDir(), "nope.yaml")
	code, _, errOut := runEnrollCmdTest("--config", missing, "--server", f.h.address, "--token", f.token)
	if code != exitUsage || !strings.Contains(errOut, missing) {
		t.Fatalf("code = %d, stderr = %q, want usage naming %q", code, errOut, missing)
	}
	f.requireServerNotContacted(t)
}

// Конфиг без пути tls — ошибка использования
func TestConfigWithoutATLSPathIsAUsageError(t *testing.T) {
	for _, key := range []string{"tls.ca_file", "tls.cert_file", "tls.key_file"} {
		t.Run(key, func(t *testing.T) {
			f := newLocalFixture(t)
			path := writeConfigMissingKey(t, f.h, key)
			code, _, errOut := runEnrollCmdTest("--config", path, "--server", f.h.address, "--token", f.token)
			if code != exitUsage || !strings.Contains(errOut, key) {
				t.Fatalf("code = %d, stderr = %q, want usage naming %q", code, errOut, key)
			}
			f.requireServerNotContacted(t)
		})
	}
}

func writeConfigMissingKey(t *testing.T, h *host, missing string) string {
	t.Helper()
	lines := map[string]string{
		"tls.ca_file":   "ca_file: " + h.caFile,
		"tls.cert_file": "cert_file: " + h.certFile,
		"tls.key_file":  "key_file: " + h.keyFile,
	}
	var b strings.Builder
	b.WriteString("server:\n  address: " + h.address + "\ntls:\n")
	for k, line := range lines {
		if k == missing {
			continue
		}
		b.WriteString("  " + line + "\n")
	}
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Недопустимый таймаут — ошибка использования
func TestInvalidTimeoutIsAUsageError(t *testing.T) {
	for _, v := range []string{"0s", "-1s", "abc"} {
		t.Run(v, func(t *testing.T) {
			f := newLocalFixture(t)
			code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token, "--timeout", v)
			if code != exitUsage {
				t.Fatalf("timeout %q: code = %d, want usage; stderr = %q", v, code, errOut)
			}
			f.requireServerNotContacted(t)
		})
	}
}

// Конфиг без адреса сервера — ошибка использования
func TestConfigWithoutServerAddressIsAUsageError(t *testing.T) {
	f := newLocalFixture(t)
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	content := "tls:\n  ca_file: " + f.h.caFile + "\n  cert_file: " + f.h.certFile + "\n  key_file: " + f.h.keyFile + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runEnrollCmdTest("--config", path, "--server", f.h.address, "--token", f.token)
	if code != exitUsage {
		t.Fatalf("code = %d, want usage; stderr = %q", code, errOut)
	}
	if !strings.Contains(errOut, "server.address") {
		t.Errorf("stderr does not name server.address: %q", errOut)
	}
	f.requireServerNotContacted(t)
}

// Адрес в конфиге отличается от адреса команды
func TestAddressInTheConfigDiffersFromTheCommand(t *testing.T) {
	f := newLocalFixture(t)
	other := "old.example.com:9090"
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--server", other, "--token", f.token)
	if code != exitUsage {
		t.Fatalf("code = %d, want usage; stderr = %q", code, errOut)
	}
	if !strings.Contains(errOut, other) || !strings.Contains(errOut, f.h.address) {
		t.Errorf("stderr does not name both addresses: %q", errOut)
	}
	if strings.Contains(errOut, f.token) {
		t.Fatal("stderr leaked the token")
	}
	f.requireServerNotContacted(t)
	f.requireHostUnchanged(t)
}

// Конфликт адреса не обходится флагом --force
func TestAddressConflictIsNotBypassedByForce(t *testing.T) {
	f := newLocalFixture(t)
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--server", "old.example.com:9090", "--token", f.token, "--force")
	if code != exitUsage {
		t.Fatalf("code = %d, want usage; stderr = %q", code, errOut)
	}
	f.requireServerNotContacted(t)
}

// Адреса с разными портами не совпадают
func TestAddressesWithDifferentPortsDoNotMatch(t *testing.T) {
	f := newLocalFixture(t)
	host, port := splitHostPortForTest(t, f.h.address)
	otherPort := "1"
	if port == "1" {
		otherPort = "2"
	}
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--server", host+":"+otherPort, "--token", f.token)
	if code != exitUsage {
		t.Fatalf("code = %d, want usage; stderr = %q", code, errOut)
	}
	f.requireServerNotContacted(t)
}

func splitHostPortForTest(t *testing.T, addr string) (string, string) {
	t.Helper()
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		t.Fatalf("address %q has no port", addr)
	}
	return addr[:idx], addr[idx+1:]
}

// Есть сертификат — команда отказывает и называет текущую личность
func TestAnExistingCertificateRefusesAndNamesTheCurrentIdentity(t *testing.T) {
	f := newLocalFixture(t)
	certPEM := writeAgentCertForTest(t, f.h.certFile, "existing-agent")
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitIdentityExists {
		t.Fatalf("code = %d, want %d; stderr = %q", code, exitIdentityExists, errOut)
	}
	for _, want := range []string{"existing-agent", f.h.address, "--force"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr does not mention %q: %q", want, errOut)
		}
	}
	f.requireServerNotContacted(t)
	requireFileContentUnchanged(t, f.h.certFile, certPEM)
	requireFileAbsent(t, f.h.keyFile)
	requireFileAbsent(t, f.h.caFile)
}

// Существующая идентичность проверяется раньше доступности сервера
func TestExistingIdentityIsCheckedBeforeServerReachability(t *testing.T) {
	f := newLocalFixture(t)
	writeAgentCertForTest(t, f.h.certFile, "existing-agent")
	unreachable := "127.0.0.1:1"
	cfg := f.h.writeConfig(t, unreachable)
	code, _, errOut := runEnrollCmdTest("--config", cfg, "--token", f.token)
	if code != exitIdentityExists {
		t.Fatalf("code = %d, want %d (identity checked before reachability); stderr = %q", code, exitIdentityExists, errOut)
	}
}

// Есть только ключ — команда считает идентичность существующей
func TestOnlyAKeyExistingCountsAsAnIdentity(t *testing.T) {
	f := newLocalFixture(t)
	if err := os.WriteFile(f.h.keyFile, []byte("key material"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitIdentityExists {
		t.Fatalf("code = %d, want %d; stderr = %q", code, exitIdentityExists, errOut)
	}
	requireFileContentUnchanged(t, f.h.keyFile, "key material")
	requireFileAbsent(t, f.h.certFile)
	requireFileAbsent(t, f.h.caFile)
}

// Нечитаемый сертификат — команда отказывает и говорит, что agent_id неизвестен
func TestAnUnreadableCertificateSaysAgentIDIsUnknown(t *testing.T) {
	f := newLocalFixture(t)
	if err := os.WriteFile(f.h.certFile, []byte("not a certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitIdentityExists {
		t.Fatalf("code = %d, want %d; stderr = %q", code, exitIdentityExists, errOut)
	}
	if !strings.Contains(errOut, f.h.certFile) {
		t.Errorf("stderr does not name the certificate path: %q", errOut)
	}
	requireFileContentUnchanged(t, f.h.certFile, "not a certificate")
	requireFileAbsent(t, f.h.keyFile)
	requireFileAbsent(t, f.h.caFile)
}

// Непригодный для записи каталог обнаруживается до обращения к серверу
func TestAnUnusableDirectoryIsFoundBeforeContactingTheServer(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f *localFixture)
	}{
		{"tls.key_file directory does not exist", func(t *testing.T, f *localFixture) {
			f.h.configPath = writeConfigWithMovedFile(t, f.h, "key", filepath.Join(f.h.dir, "no-such-key-dir"))
		}},
		{"tls.cert_file directory is not writable", func(t *testing.T, f *localFixture) {
			f.h.configPath = writeConfigWithMovedFile(t, f.h, "cert", filepath.Join(f.h.dir, "no-such-cert-dir"))
		}},
		{"tls.ca_file directory is not writable", func(t *testing.T, f *localFixture) {
			f.h.configPath = writeConfigWithMovedFile(t, f.h, "ca", filepath.Join(f.h.dir, "no-such-ca-dir"))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newLocalFixture(t)
			c.setup(t, f)
			code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
			if code != exitWrite {
				t.Fatalf("code = %d, want %d (write); stderr = %q", code, exitWrite, errOut)
			}
			f.requireServerNotContacted(t)
		})
	}
}

// writeConfigWithMovedFile points which's path at a directory that does not
// exist: CheckWritable's probe fails there without CreateWritable ever
// creating it (В12), and — unlike chmod, which root bypasses — this is
// root-proof and does not disturb the other two tls.* files, so
// InspectIdentity still finds them (or their absence) normally.
func writeConfigWithMovedFile(t *testing.T, h *host, which, missingDir string) string {
	t.Helper()
	keyFile, certFile, caFile := h.keyFile, h.certFile, h.caFile
	switch which {
	case "key":
		keyFile = filepath.Join(missingDir, "agent.key")
	case "cert":
		certFile = filepath.Join(missingDir, "agent.pem")
	case "ca":
		caFile = filepath.Join(missingDir, "ca.pem")
	}
	content := fmt.Sprintf("server:\n  address: %s\ntls:\n  ca_file: %s\n  cert_file: %s\n  key_file: %s\n",
		h.address, caFile, certFile, keyFile)
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Недопустимое имя хоста отклоняется до обращения к серверу
func TestInvalidHostnameIsRejectedBeforeContactingTheServer(t *testing.T) {
	cases := map[string]string{
		"пустое":          "",
		"из 254 символов": strings.Repeat("h", 254),
	}
	for name, hn := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLocalFixture(t)
			bad := func() (string, error) { return hn, nil }
			code, _, errOut := runEnrollCmdOn(bad, "--config", f.h.configPath, "--token", f.token)
			if code != exitAgentError {
				t.Fatalf("code = %d, want %d (agent error); stderr = %q", code, exitAgentError, errOut)
			}
			if !strings.Contains(errOut, "HOSTNAME_INVALID") {
				t.Errorf("stderr does not name HOSTNAME_INVALID: %q", errOut)
			}
			f.requireServerNotContacted(t)
		})
	}
}

// Токен из файла не раскрывается при ошибке его разбора
func TestATokenFromAFileIsNotRevealedWhenItsParsingFails(t *testing.T) {
	f := newLocalFixture(t)
	bad := f.token + " "
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token-file", path)
	if code != exitUsage {
		t.Fatalf("code = %d, want usage; stderr = %q", code, errOut)
	}
	if strings.Contains(errOut, f.token) {
		t.Fatalf("stderr leaked the token: %q", errOut)
	}
}

func writeAgentCertForTest(t *testing.T, path, agentID string) string {
	t.Helper()
	ca := newTestCA(t)
	pemStr := ca.agentLeafPEM(t, agentID)
	if err := os.WriteFile(path, []byte(pemStr), 0o644); err != nil {
		t.Fatal(err)
	}
	return pemStr
}

func requireFileContentUnchanged(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s changed: got %q, want %q", path, got, want)
	}
}

func requireFileAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists, want it absent", path)
	}
}

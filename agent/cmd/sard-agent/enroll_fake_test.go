// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/enroll"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// fakeFixture is a host wired to a real TLS+gRPC EnrollmentService.
type fakeFixture struct {
	h     *host
	ca    *testCA
	srv   *enrollServer
	token string
}

func newFakeFixture(t *testing.T, names []string, answer func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error)) *fakeFixture {
	t.Helper()
	ca := newTestCA(t)
	leaf := chainOf(ca.leaf(t, names, 0), ca)
	srv := &enrollServer{answer: answer}
	addr := startFakeServer(t, leaf, srv)
	h := newHost(t, addr)
	token := newToken(t, ca.fingerprint())
	return &fakeFixture{h: h, ca: ca, srv: srv, token: token}
}

func newSucceedingFakeFixture(t *testing.T, agentID string) *fakeFixture {
	t.Helper()
	ca := newTestCA(t)
	leaf := chainOf(ca.leaf(t, []string{"127.0.0.1"}, 0), ca)
	srv := &enrollServer{}
	srv.answer = succeedingAnswer(ca, agentID)
	addr := startFakeServer(t, leaf, srv)
	h := newHost(t, addr)
	token := newToken(t, ca.fingerprint())
	return &fakeFixture{h: h, ca: ca, srv: srv, token: token}
}

// Регистрация по активному токену записывает ключ, сертификат и бандл
func TestEnrollmentByAnActiveTokenWritesKeyCertificateAndBundle(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stdout=%q stderr=%q", code, out, errOut)
	}
	for _, p := range []string{f.h.keyFile, f.h.certFile, f.h.caFile} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("Stat(%s): %v", p, err)
		}
	}
	requireCertKeyMatch(t, f.h.certFile, f.h.keyFile)
	requireCAFingerprint(t, f.h.caFile, f.ca.fingerprint())
}

// Ключ, записанный командой enroll, проходит проверку при старте (@a1 @fake)
func TestTheKeyWrittenByEnrollPassesTheStartupCheck(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
	cfg, err := config.Load(f.h.configPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := secrets.CheckAll(cfg, uint32(os.Getuid()), secrets.RealStat); err != nil {
		t.Fatalf("secrets.CheckAll: %v", err)
	}
}

// Итог успеха называет agent_id, адрес, пути и следующий шаг
func TestSuccessNamesAgentIDAddressPathsAndNextStep(t *testing.T) {
	f := newSucceedingFakeFixture(t, "agent-x")
	code, out, _ := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0", code)
	}
	for _, want := range []string{"agent-x", f.h.address, f.h.keyFile, f.h.certFile, f.h.caFile} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout does not mention %q: %q", want, out)
		}
	}
	if !strings.Contains(strings.ToLower(out), "restart") && !strings.Contains(strings.ToLower(out), "start") {
		t.Errorf("stdout does not say to start or restart the service: %q", out)
	}
}

// Адрес сервера в виде IPv6 в скобках принимается
//
// This sandbox has no IPv6 stack at all (no /proc/net/if_inet6): a real
// TCP connection to [::1] cannot succeed here. F6's dial seam stands in
// for just that one step — the injected DialFunc insists on being asked
// for the IPv6 address, then hands back a real connection to the fake
// server's actual (IPv4) listener; everything else, including hostname
// verification, runs unmodified against a leaf certificate whose SAN is
// the IPv6 address "::1".
func TestIPv6ServerAddressInBracketsIsAccepted(t *testing.T) {
	ca := newTestCA(t)
	leaf := chainOf(ca.leaf(t, []string{"::1"}, 0), ca)
	srv := &enrollServer{}
	addr := startFakeServer(t, leaf, srv) // 127.0.0.1:<port>
	srv.answer = succeedingAnswer(ca, "a1")
	_, port := splitHostPortForTest(t, addr)
	ipv6Addr := "[::1]:" + port
	h := newHost(t, ipv6Addr)
	token := newToken(t, ca.fingerprint())

	code, _, errOut := runEnrollCmdWithDial(dialToInstead(t, ipv6Addr, addr), "--config", h.configPath, "--server", ipv6Addr, "--token", token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
}

// Токен принимается из любого одного источника
func TestTokenAcceptedFromAnySingleSource(t *testing.T) {
	t.Run("флаг --token", func(t *testing.T) {
		f := newSucceedingFakeFixture(t, "a1")
		code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
		if code != exitOK {
			t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
		}
	})
	t.Run("файл по флагу --token-file", func(t *testing.T) {
		f := newSucceedingFakeFixture(t, "a1")
		path := writeTempFile(t, f.token)
		code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token-file", path)
		if code != exitOK {
			t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
		}
	})
	t.Run("переменную SARD_ENROLL_TOKEN", func(t *testing.T) {
		f := newSucceedingFakeFixture(t, "a1")
		t.Setenv("SARD_ENROLL_TOKEN", f.token)
		code, _, errOut := runEnrollCmdTest("--config", f.h.configPath)
		if code != exitOK {
			t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
		}
	})
}

// Один завершающий перевод строки в файле токена отбрасывается
func TestOneTrailingNewlineInTheTokenFileIsDropped(t *testing.T) {
	for _, ending := range []struct {
		name string
		nl   string
	}{{"LF", "\n"}, {"CR LF", "\r\n"}} {
		t.Run(ending.name, func(t *testing.T) {
			f := newSucceedingFakeFixture(t, "a1")
			path := writeTempFile(t, f.token+ending.nl)
			code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token-file", path)
			if code != exitOK {
				t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
			}
		})
	}
}

// Пустая переменная SARD_ENROLL_TOKEN считается незаданной
func TestEmptySARDEnrollTokenEnvIsUnset(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	t.Setenv("SARD_ENROLL_TOKEN", "")
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
}

// Без --server команда регистрируется по адресу из конфига
func TestWithoutServerFlagTheConfigAddressIsUsed(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
	if !strings.Contains(out, f.h.address) {
		t.Errorf("stdout does not mention %q: %q", f.h.address, out)
	}
}

// Команда не меняет файл конфига
func TestTheCommandDoesNotChangeTheConfigFile(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	before, err := os.ReadFile(f.h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
	after, err := os.ReadFile(f.h.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the config file changed")
	}
	if strings.Contains(out, "server.address") {
		t.Errorf("stdout must not suggest writing the address to the config: %q", out)
	}
}

// Имя хоста в адресах сравнивается без учёта регистра
func TestHostnameInAddressesIsComparedCaseInsensitively(t *testing.T) {
	ca := newTestCA(t)
	leaf := chainOf(ca.leaf(t, []string{"127.0.0.1"}, 0), ca)
	srv := &enrollServer{answer: succeedingAnswer(ca, "a1")}
	addr := startFakeServer(t, leaf, srv)
	host, port := splitHostPortForTest(t, addr)
	_ = host
	h := newHost(t, "127.0.0.1:"+port)
	token := newToken(t, ca.fingerprint())
	code, _, errOut := runEnrollCmdTest("--config", h.configPath, "--server", "127.0.0.1:"+port, "--token", token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
}

// IPv6-адреса сравниваются по значению
//
// Same sandbox limitation as above (no IPv6 stack), same seam (F6). This
// also exercises real value-equality of the two forms twice over: the
// address conflict check (config.AddressEqual, --server "[::1]" against
// server.address "[0:0:0:0:0:0:0:1]") and the TLS handshake's own
// hostname check (crypto/x509.VerifyHostname("0:0:0:0:0:0:0:1") against a
// certificate whose SAN is the literal IP "::1") — both must agree these
// are the same address for the command to reach exitOK.
func TestIPv6AddressesAreComparedByValue(t *testing.T) {
	ca := newTestCA(t)
	leaf := chainOf(ca.leaf(t, []string{"::1"}, 0), ca)
	srv := &enrollServer{}
	addr := startFakeServer(t, leaf, srv)
	srv.answer = succeedingAnswer(ca, "a1")
	_, port := splitHostPortForTest(t, addr)
	configAddr := "[0:0:0:0:0:0:0:1]:" + port
	h := newHost(t, configAddr)
	token := newToken(t, ca.fingerprint())

	code, _, errOut := runEnrollCmdWithDial(dialToInstead(t, configAddr, addr), "--config", h.configPath, "--server", "[::1]:"+port, "--token", token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
}

// Один бандл CA без ключа и сертификата не мешает регистрации
func TestALoneCABundleDoesNotBlockEnrollment(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	if err := os.WriteFile(f.h.caFile, []byte("old bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
	requireCAFingerprint(t, f.h.caFile, f.ca.fingerprint())
}

// --force заменяет ключ, сертификат и бандл новой личностью
func TestForceReplacesKeyCertificateAndBundleWithANewIdentity(t *testing.T) {
	f := newSucceedingFakeFixture(t, "old-agent")
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitOK {
		t.Fatalf("first enroll: code = %d, stderr = %q", code, errOut)
	}
	oldKey, err := os.ReadFile(f.h.keyFile)
	if err != nil {
		t.Fatal(err)
	}

	f.srv.answer = succeedingAnswer(f.ca, "new-agent")
	token2 := newToken(t, f.ca.fingerprint())
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--force", "--token", token2)
	if code != exitOK {
		t.Fatalf("forced enroll: code = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "new-agent") {
		t.Errorf("stdout does not mention the new agent id: %q", out)
	}
	newKey, err := os.ReadFile(f.h.keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(oldKey) == string(newKey) {
		t.Fatal("the key did not change")
	}
	requireCertKeyMatch(t, f.h.certFile, f.h.keyFile)
}

// --force не оставляет резервных копий прежней личности
func TestForceLeavesNoBackupsOfThePreviousIdentity(t *testing.T) {
	f := newSucceedingFakeFixture(t, "old-agent")
	if code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token); code != exitOK {
		t.Fatalf("first enroll: code = %d, stderr = %q", code, errOut)
	}
	f.srv.answer = succeedingAnswer(f.ca, "new-agent")
	token2 := newToken(t, f.ca.fingerprint())
	if code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--force", "--token", token2); code != exitOK {
		t.Fatalf("forced enroll: code = %d, stderr = %q", code, errOut)
	}
	for _, dir := range []string{f.h.keyDir, f.h.certDir, f.h.caDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("%s has %d entries after --force, want exactly 1: %v", dir, len(entries), entries)
		}
	}
}

// Итог --force называет брошенную личность
func TestForceResultNamesTheAbandonedIdentity(t *testing.T) {
	f := newSucceedingFakeFixture(t, "old-agent")
	if code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token); code != exitOK {
		t.Fatalf("first enroll: code = %d, stderr = %q", code, errOut)
	}
	f.srv.answer = succeedingAnswer(f.ca, "new-agent")
	token2 := newToken(t, f.ca.fingerprint())
	code, out, errOut := runEnrollCmdTest("--config", f.h.configPath, "--force", "--token", token2)
	if code != exitOK {
		t.Fatalf("forced enroll: code = %d, stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "old-agent") {
		t.Errorf("stdout does not name the abandoned identity: %q", out)
	}
}

// Отказ при --force оставляет прежние файлы
func TestARefusalWithForceLeavesThePreviousFiles(t *testing.T) {
	cases := map[string]func(t *testing.T, f *fakeFixture) string{
		"отказом TOKEN_USED": func(t *testing.T, f *fakeFixture) string {
			f.srv.answer = failingAnswer(codes.Unauthenticated, "TOKEN_USED")
			return newToken(t, f.ca.fingerprint())
		},
		"отказом INTERNAL_RETRYABLE": func(t *testing.T, f *fakeFixture) string {
			f.srv.answer = failingAnswer(codes.Unavailable, "INTERNAL_RETRYABLE")
			return newToken(t, f.ca.fingerprint())
		},
		"несовпадением отпечатка CA": func(t *testing.T, f *fakeFixture) string {
			return newToken(t, strings.Repeat("0", 64))
		},
		"недоступностью сервера": func(t *testing.T, f *fakeFixture) string {
			f.h.address = "127.0.0.1:1"
			f.h.configPath = f.h.writeConfig(t, f.h.address)
			return newToken(t, f.ca.fingerprint())
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSucceedingFakeFixture(t, "old-agent")
			if code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token); code != exitOK {
				t.Fatalf("first enroll: code = %d, stderr = %q", code, errOut)
			}
			oldKey, _ := os.ReadFile(f.h.keyFile)
			oldCert, _ := os.ReadFile(f.h.certFile)
			oldCA, _ := os.ReadFile(f.h.caFile)

			token2 := mutate(t, f)
			code, _, _ := runEnrollCmdTest("--config", f.h.configPath, "--force", "--token", token2)
			if code == exitOK {
				t.Fatalf("code = %d, want a refusal", code)
			}
			requireFileContentUnchanged(t, f.h.keyFile, string(oldKey))
			requireFileContentUnchanged(t, f.h.certFile, string(oldCert))
			requireFileContentUnchanged(t, f.h.caFile, string(oldCA))
		})
	}
}

// --force без существующей идентичности регистрирует как обычно
func TestForceWithoutAnExistingIdentityEnrollsNormally(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--force", "--token", f.token)
	if code != exitOK {
		t.Fatalf("code = %d, want 0; stderr = %q", code, errOut)
	}
}

// Отпечаток CA сервера не совпал с токеном
func TestServerCAFingerprintDoesNotMatchTheToken(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	wrongToken := newToken(t, strings.Repeat("0", 64))
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", wrongToken)
	if code != exitTrust {
		t.Fatalf("code = %d, want %d (trust); stderr = %q", code, exitTrust, errOut)
	}
	if !strings.Contains(errOut, "another server") || !strings.Contains(errOut, "intercepted") {
		t.Errorf("stderr does not name both possible reasons: %q", errOut)
	}
	if strings.Contains(strings.ToLower(errOut), "bypass") || strings.Contains(strings.ToLower(errOut), "skip") {
		t.Errorf("stderr must not offer a bypass: %q", errOut)
	}
	if strings.Contains(errOut, wrongToken) {
		t.Fatal("stderr leaked the token")
	}
	if f.srv.callCount() != 0 {
		t.Fatalf("server was contacted %d times, want 0", f.srv.callCount())
	}
}

// Сервер без корня CA в цепочке не проходит проверку
func TestAServerWithoutTheRootInTheChainFailsVerification(t *testing.T) {
	ca := newTestCA(t)
	leaf := ca.leaf(t, []string{"127.0.0.1"}, 0) // no root appended
	srv := &enrollServer{}
	addr := startFakeServer(t, leaf, srv)
	h := newHost(t, addr)
	token := newToken(t, ca.fingerprint())
	code, _, errOut := runEnrollCmdTest("--config", h.configPath, "--token", token)
	if code != exitTrust {
		t.Fatalf("code = %d, want %d (trust); stderr = %q", code, exitTrust, errOut)
	}
	if srv.callCount() != 0 {
		t.Fatalf("server was contacted %d times, want 0", srv.callCount())
	}
}

// Сертификат сервера не подписан корнем из цепочки
func TestTheServerCertificateIsNotSignedByTheRootInTheChain(t *testing.T) {
	ca := newTestCA(t)
	signer := newTestCA(t)
	leaf := signer.leaf(t, []string{"127.0.0.1"}, 0)
	leaf.Certificate = append(leaf.Certificate, ca.cert.Raw) // fingerprint matches ca, signature doesn't
	srv := &enrollServer{}
	addr := startFakeServer(t, leaf, srv)
	h := newHost(t, addr)
	token := newToken(t, ca.fingerprint())
	code, _, errOut := runEnrollCmdTest("--config", h.configPath, "--token", token)
	if code != exitTrust {
		t.Fatalf("code = %d, want %d (trust); stderr = %q", code, exitTrust, errOut)
	}
}

// Просроченный сертификат сервера не проходит проверку
func TestAnExpiredServerCertificateFailsVerification(t *testing.T) {
	ca := newTestCA(t)
	leaf := chainOf(ca.leaf(t, []string{"127.0.0.1"}, -time.Hour), ca)
	srv := &enrollServer{}
	addr := startFakeServer(t, leaf, srv)
	h := newHost(t, addr)
	token := newToken(t, ca.fingerprint())
	code, _, errOut := runEnrollCmdTest("--config", h.configPath, "--token", token)
	if code != exitTrust {
		t.Fatalf("code = %d, want %d (trust); stderr = %q", code, exitTrust, errOut)
	}
}

// Сервер без TLS не проходит проверку
func TestAServerWithoutTLSFailsVerification(t *testing.T) {
	lis := listenNoTLS(t)
	h := newHost(t, lis)
	token := newToken(t, strings.Repeat("a", 64))
	code, _, errOut := runEnrollCmdTest("--config", h.configPath, "--token", token)
	if code != exitTrust {
		t.Fatalf("code = %d, want %d (trust); stderr = %q", code, exitTrust, errOut)
	}
}

// Отказ сервера TOKEN_FOREIGN_CA — ошибка доверия
func TestServerRefusalTokenForeignCAIsATrustError(t *testing.T) {
	f := newFakeFixture(t, []string{"127.0.0.1"}, failingAnswer(codes.Unauthenticated, "TOKEN_FOREIGN_CA"))
	code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if code != exitTrust {
		t.Fatalf("code = %d, want %d (trust); stderr = %q", code, exitTrust, errOut)
	}
	if !strings.Contains(errOut, "TOKEN_FOREIGN_CA") {
		t.Errorf("stderr does not name TOKEN_FOREIGN_CA: %q", errOut)
	}
}

// Имя хоста не совпало с сертификатом сервера
func TestTheHostnameDoesNotMatchTheServerCertificate(t *testing.T) {
	ca := newTestCA(t)
	leaf := chainOf(ca.leaf(t, []string{"sard.example.com", "localhost", "127.0.0.1", "::1"}, 0), ca)
	srv := &enrollServer{}
	lis := startFakeServer(t, leaf, srv)
	_, port := splitHostPortForTest(t, lis)
	addr := "127.0.0.2:" + port // loopback, but not a name in the certificate
	h := newHost(t, addr)
	token := newToken(t, ca.fingerprint())
	code, _, errOut := runEnrollCmdTest("--config", h.configPath, "--server", addr, "--token", token)
	if code != exitTrust {
		t.Fatalf("code = %d, want %d (trust); stderr = %q", code, exitTrust, errOut)
	}
	if !strings.Contains(errOut, "127.0.0.2") {
		t.Errorf("stderr does not name 127.0.0.2: %q", errOut)
	}
	if !strings.Contains(errOut, "SARD_AGENT_ENDPOINT") {
		t.Errorf("stderr does not say the address must match SARD_AGENT_ENDPOINT or a certificate name: %q", errOut)
	}
	if srv.callCount() != 0 {
		t.Fatalf("server was contacted %d times, want 0", srv.callCount())
	}
}

// Сообщение о несовпадении имени перечисляет имена сертификата
func TestTheHostnameMismatchMessageListsTheCertificateNames(t *testing.T) {
	ca := newTestCA(t)
	leaf := chainOf(ca.leaf(t, []string{"sard.example.com", "localhost"}, 0), ca)
	srv := &enrollServer{}
	lis := startFakeServer(t, leaf, srv)
	_, port := splitHostPortForTest(t, lis)
	addr := "127.0.0.1:" + port
	h := newHost(t, addr)
	token := newToken(t, ca.fingerprint())
	code, _, errOut := runEnrollCmdTest("--config", h.configPath, "--token", token)
	if code != exitTrust {
		t.Fatalf("code = %d, want %d (trust); stderr = %q", code, exitTrust, errOut)
	}
	for _, want := range []string{"sard.example.com", "localhost"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr does not list certificate name %q: %q", want, errOut)
		}
	}
}

// Несовпадение отпечатка важнее несовпадения имени
func TestFingerprintMismatchOutranksNameMismatch(t *testing.T) {
	ca := newTestCA(t)
	other := newTestCA(t)
	leaf := chainOf(ca.leaf(t, []string{"127.0.0.1"}, 0), ca) // no "backup.example.org"
	srv := &enrollServer{}
	addr := startFakeServer(t, leaf, srv)
	h := newHost(t, addr)
	token := newToken(t, other.fingerprint()) // wrong CA entirely
	code, _, errOut := runEnrollCmdTest("--config", h.configPath, "--token", token)
	if code != exitTrust {
		t.Fatalf("code = %d, want %d (trust); stderr = %q", code, exitTrust, errOut)
	}
	if !strings.Contains(errOut, "fingerprint") {
		t.Errorf("stderr does not talk about a fingerprint mismatch: %q", errOut)
	}
}

// Отказ сервера по причине даёт свой класс и совет о повторе
func TestServerRefusalByReasonGivesItsClassAndRetryAdvice(t *testing.T) {
	cases := []struct {
		code   codes.Code
		reason string
		class  int
	}{
		{codes.Unauthenticated, "TOKEN_UNKNOWN", exitTokenRefused},
		{codes.Unauthenticated, "TOKEN_USED", exitTokenRefused},
		{codes.Unauthenticated, "TOKEN_EXPIRED", exitTokenRefused},
		{codes.Unauthenticated, "TOKEN_REVOKED", exitTokenRefused},
		{codes.InvalidArgument, "TOKEN_MALFORMED", exitUsage},
		{codes.Unavailable, "INTERNAL_RETRYABLE", exitTemporary},
	}
	for _, c := range cases {
		t.Run(c.reason, func(t *testing.T) {
			f := newFakeFixture(t, []string{"127.0.0.1"}, failingAnswer(c.code, c.reason))
			cmdCode, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
			if cmdCode != c.class {
				t.Fatalf("code = %d, want %d; stderr = %q", cmdCode, c.class, errOut)
			}
			if !strings.Contains(errOut, c.reason) {
				t.Errorf("stderr does not name %q: %q", c.reason, errOut)
			}
			if strings.Contains(errOut, f.token) {
				t.Fatal("stderr leaked the token")
			}
		})
	}
}

// Отказ сервера по вине агента — класс «ошибка агента»
func TestServerRefusalByTheAgentsFaultIsAgentError(t *testing.T) {
	for _, reason := range []string{"CSR_INVALID", "HOSTNAME_INVALID"} {
		t.Run(reason, func(t *testing.T) {
			f := newFakeFixture(t, []string{"127.0.0.1"}, failingAnswer(codes.InvalidArgument, reason))
			code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
			if code != exitAgentError {
				t.Fatalf("code = %d, want %d; stderr = %q", code, exitAgentError, errOut)
			}
			if !strings.Contains(errOut, reason) {
				t.Errorf("stderr does not name %q: %q", reason, errOut)
			}
			if !strings.Contains(errOut, "intact") || !strings.Contains(errOut, "updat") {
				t.Errorf("stderr does not say the token is intact and to retry after updating the agent: %q", errOut)
			}
		})
	}
}

// Непредусмотренный ответ сервера — класс «ошибка агента»
func TestAnUnforeseenServerResponseIsAgentError(t *testing.T) {
	cases := map[string]func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error){
		"статусом UNIMPLEMENTED без ErrorInfo": func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
			return nil, status.Error(codes.Unimplemented, "not implemented")
		},
		"статусом INTERNAL без ErrorInfo": func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
		"UNAUTHENTICATED с неизвестной причиной": failingAnswer(codes.Unauthenticated, "SOMETHING_NEW"),
		"успехом без сертификата": func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
			return &agentv1.EnrollResponse{AgentId: "a1"}, nil
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeFixture(t, []string{"127.0.0.1"}, answer)
			code, _, errOut := runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
			if code != exitAgentError {
				t.Fatalf("code = %d, want %d; stderr = %q", code, exitAgentError, errOut)
			}
		})
	}
}

// Команда не повторяет регистрацию после временной ошибки
func TestTheCommandDoesNotRetryAfterATemporaryFailure(t *testing.T) {
	f := newFakeFixture(t, []string{"127.0.0.1"}, failingAnswer(codes.Unavailable, "INTERNAL_RETRYABLE"))
	runEnrollCmdTest("--config", f.h.configPath, "--token", f.token)
	if f.srv.callCount() != 1 {
		t.Fatalf("calls = %d, want exactly 1", f.srv.callCount())
	}
}

// Сервер не принимает соединение
func TestTheServerRefusesTheConnection(t *testing.T) {
	h := newHost(t, "127.0.0.1:1")
	token := newToken(t, strings.Repeat("a", 64))
	code, _, errOut := runEnrollCmdTest("--config", h.configPath, "--token", token)
	if code != exitTemporary {
		t.Fatalf("code = %d, want %d (temporary); stderr = %q", code, exitTemporary, errOut)
	}
	if !strings.Contains(errOut, "127.0.0.1:1") {
		t.Errorf("stderr does not name the address: %q", errOut)
	}
	if !strings.Contains(errOut, "intact") {
		t.Errorf("stderr does not say the token is intact: %q", errOut)
	}
}

// Имя сервера не разрешается
//
// F6: CLAUDE.md forbids tests that touch the real network — a name that
// happens not to resolve today is still a real DNS lookup. The injected
// DialFunc returns the exact failure a real resolver gives for an unknown
// name (*net.DNSError, IsNotFound) without performing one.
func TestTheServerNameDoesNotResolve(t *testing.T) {
	h := newHost(t, "sard.example.com:9090")
	token := newToken(t, strings.Repeat("a", 64))
	dial := func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "sard.example.com", IsNotFound: true}
	}
	code, _, errOut := runEnrollCmdWithDial(dial, "--config", h.configPath, "--token", token)
	if code != exitTemporary {
		t.Fatalf("code = %d, want %d (temporary); stderr = %q", code, exitTemporary, errOut)
	}
	if !strings.Contains(errOut, "sard.example.com:9090") {
		t.Errorf("stderr does not name the address: %q", errOut)
	}
}

// runEnrollCmdWithDial runs the command with a fake DialFunc instead of a
// real one (F6): needed wherever a scenario cannot be dialed for real in
// this environment (no IPv6, no DNS lookups in tests) but the rest of the
// pipeline — including TLS verification — must still run unmodified.
func runEnrollCmdWithDial(dial enroll.DialFunc, args ...string) (int, string, string) {
	var out, errOut strings.Builder
	deps := testEnrollDeps(fixedHostname)
	deps.dial = dial
	code := runEnrollWithDeps(context.Background(), args, &out, &errOut, deps)
	return code, out.String(), errOut.String()
}

// dialToInstead returns a DialFunc that insists on being asked to dial
// want, then connects to actual instead — the seam behind the two IPv6
// scenarios above: address handling and TLS hostname verification run for
// real against want, only the raw TCP connection is redirected to a
// reachable stand-in.
func dialToInstead(t *testing.T, want, actual string) enroll.DialFunc {
	t.Helper()
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != want {
			t.Fatalf("dialed %q, want %q", addr, want)
		}
		return enroll.RealDial(ctx, network, actual)
	}
}

// helpers

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/token"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func failingAnswer(code codes.Code, reason string) func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	return func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
		st, err := status.New(code, "enroll refused").WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "sard.dev"})
		if err != nil {
			panic(err)
		}
		return nil, st.Err()
	}
}

// requireCertKeyMatch checks the certificate's public key matches the
// private key WriteIdentity put on disk.
func requireCertKeyMatch(t *testing.T, certFile, keyFile string) {
	t.Helper()
	cert := parsePEMCertificateForTest(t, certFile)
	key := parsePEMPrivateKeyForTest(t, keyFile)
	certPub, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPub, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	if string(certPub) != string(keyPub) {
		t.Fatal("certificate public key does not match the written private key")
	}
}

func requireCAFingerprint(t *testing.T, caFile, want string) {
	t.Helper()
	cert := parsePEMCertificateForTest(t, caFile)
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("ca fingerprint = %s, want %s", got, want)
	}
}

func parsePEMCertificateForTest(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("%s has no PEM block", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func parsePEMPrivateKeyForTest(t *testing.T, path string) *ecdsa.PrivateKey {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("%s has no PEM block", path)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("%s: unexpected key type %T", path, key)
	}
	return ecKey
}

// listenNoTLS accepts plain TCP and answers in plaintext, so a TLS
// handshake against it fails immediately instead of hanging.
func listenNoTLS(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
				_ = conn.Close()
			}()
		}
	}()
	t.Cleanup(func() { _ = lis.Close() })
	return lis.Addr().String()
}

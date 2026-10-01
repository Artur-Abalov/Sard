// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// Rule "Поддерживается только встроенное шифрование restic".

// Неподдерживаемый провайдер шифрования — отказ до обращения к бэкенду
func TestAnUnsupportedCryptoProviderIsRefusedBeforeTheBackend(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Repositories[0].CryptoProvider = "gost"
	h.saveConfig()
	before := h.snapshot()
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "CRYPTO_PROVIDER_UNSUPPORTED")
	if !strings.Contains(stderr, "gost") || !strings.Contains(stderr, "restic-aes") {
		t.Errorf("stderr = %q", stderr)
	}
	h.assertNoBackendCalls()
	h.assertUnchanged(before)
}

// Провайдер шифрования проверяется раньше файла пароля
func TestTheCryptoProviderIsCheckedBeforeThePasswordFile(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Repositories[0].CryptoProvider = "gost"
	h.saveConfig()
	if err := os.Remove(h.pass()); err != nil {
		t.Fatal(err)
	}
	_, _, stderr := h.initCmd()
	assertReason(t, stderr, "CRYPTO_PROVIDER_UNSUPPORTED")
}

// Rule "Файл пароля и файл окружения проверяются до обращения к бэкенду".

// Нет файла пароля и нет флага генерации — отказ
func TestWithoutAPasswordFileAndWithoutTheGenerateFlagTheCommandRefuses(t *testing.T) {
	h := newRepoHost(t)
	if err := os.Remove(h.pass()); err != nil {
		t.Fatal(err)
	}
	before := h.snapshot()
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "PASSWORD_FILE_MISSING")
	for _, want := range []string{"password_file", h.pass(), "--generate-password"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q: %s", want, stderr)
		}
	}
	h.assertNoBackendCalls()
	h.assertUnchanged(before)
}

// Файл пароля нулевого размера — отказ
func TestAPasswordFileOfZeroSizeIsRefused(t *testing.T) {
	h := newRepoHost(t)
	h.write(h.pass(), "", 0o600)
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "PASSWORD_FILE_EMPTY")
	if !strings.Contains(stderr, h.pass()) {
		t.Errorf("stderr = %q", stderr)
	}
	h.assertNoBackendCalls()
}

// a1Message is what the agent prints at start for the same files (A1).
func a1Message(t *testing.T, h *repoHost, stat secrets.StatFunc) string {
	t.Helper()
	err := secrets.CheckAll(h.cfg, uint32(os.Getuid()), stat)
	if err == nil {
		t.Fatal("A1 accepts the files")
	}
	return err.Error()
}

// Секретный файл репозитория, доступный не только владельцу, — отказ с сообщением A1
func TestASecretFileOpenBeyondItsOwnerIsRefusedWithTheA1Message(t *testing.T) {
	for _, c := range []struct {
		file func(*repoHost) string
		mode os.FileMode
	}{
		{(*repoHost).pass, 0o640}, {(*repoHost).pass, 0o604},
		{(*repoHost).envFile, 0o640}, {(*repoHost).envFile, 0o644},
	} {
		h := newRepoHost(t)
		if err := os.Chmod(c.file(h), c.mode); err != nil {
			t.Fatal(err)
		}
		before := h.snapshot()
		code, _, stderr := h.initCmd()
		assertCode(t, code, exitUsage)
		if want := a1Message(t, h, secrets.RealStat); !strings.Contains(stderr, want) {
			t.Errorf("%v: stderr = %q, want A1's %q", c.mode, stderr, want)
		}
		h.assertNoBackendCalls()
		h.assertUnchanged(before)
	}
}

// Секретный файл репозитория другого владельца — отказ с сообщением A1
func TestASecretFileOfAnotherOwnerIsRefusedWithTheA1Message(t *testing.T) {
	for _, file := range []func(*repoHost) string{(*repoHost).pass, (*repoHost).envFile} {
		h := newRepoHost(t)
		alien := file(h)
		h.deps.stat = func(path string) (secrets.Info, error) {
			info, err := secrets.RealStat(path)
			if path == alien {
				info.UID++
			}
			return info, err
		}
		code, _, stderr := h.initCmd()
		assertCode(t, code, exitUsage)
		if want := a1Message(t, h, h.deps.stat); !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want A1's %q", stderr, want)
		}
		h.assertNoBackendCalls()
	}
}

// Файл пароля с правами только на чтение владельцу принимается
func TestAReadOnlyPasswordFileOfTheOwnerIsAccepted(t *testing.T) {
	h := newRepoHost(t)
	if err := os.Chmod(h.pass(), 0o400); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
}

// Секретные файлы других репозиториев и секретов не проверяются
func TestSecretFilesOfOtherRepositoriesAndSecretsAreNotChecked(t *testing.T) {
	h := newRepoHost(t)
	h.write(h.path("secret"), "s", 0o644)
	h.write(h.path("tls/agent.key"), "k", 0o644)
	h.cfg.Secrets = map[string]string{"pg": h.path("secret")}
	h.saveConfig()
	if err := os.Chmod(h.pass2(), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitOK)
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
}

// Заданный, но отсутствующий файл окружения — отказ
func TestAnEnvFileThatIsSetButMissingIsRefused(t *testing.T) {
	h := newRepoHost(t)
	if err := os.Remove(h.envFile()); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "ENV_FILE_MISSING")
	for _, want := range []string{"env_file", h.envFile()} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q: %s", want, stderr)
		}
	}
	h.assertNoBackendCalls()
}

// Неверный файл окружения — отказ без значений
func TestAnInvalidEnvFileIsRefusedWithoutItsValues(t *testing.T) {
	for _, line := range []string{
		envMarker + " без знака равенства",
		"RESTIC_PASSWORD=" + envMarker,
		"RESTIC_REPOSITORY=" + envMarker,
		"LD_PRELOAD=" + envMarker,
	} {
		h := newRepoHost(t)
		h.write(h.envFile(), "AWS_ACCESS_KEY_ID=key\n"+line+"\n", 0o600)
		code, stdout, stderr := h.initCmd()
		assertCode(t, code, exitUsage)
		assertReason(t, stderr, "ENV_FILE_INVALID")
		if !strings.Contains(stderr, h.envFile()) || !strings.Contains(stderr, "line 2") {
			t.Errorf("%q: stderr = %q", line, stderr)
		}
		h.assertNoBackendCalls()
		assertNoSecrets(t, stdout, stderr)
	}
}

// Репозиторий без файла окружения инициализируется
func TestARepositoryWithoutAnEnvFileIsInitialised(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Repositories[0] = config.Repository{Name: "main", URL: h.repoURL(), PasswordFile: h.pass()}
	h.saveConfig()
	code, _, _ := h.initCmd()
	assertCode(t, code, exitOK)
}

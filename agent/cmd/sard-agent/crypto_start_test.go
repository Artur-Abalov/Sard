// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// docs/specs/agent/agent-start-refusals.feature (OQ-050), tag @start.

// providerYAML is the repository's crypto_provider entry; unset leaves it out.
func providerYAML(value *string) string {
	if value == nil {
		return ""
	}
	return ", crypto_provider: " + strconv.Quote(*value)
}

func ptr(s string) *string { return &s }

// cryptoConfig is config C with the given crypto_provider of main and offsite.
func (h *startHost) cryptoConfig(passMode uint32, restic string, main, offsite *string) string {
	pass := filepath.Join(h.dir, "pass")
	writeFile(h.t, pass, []byte("x\n"), 0o600)
	if passMode != 0o600 {
		writeFile(h.t, pass, []byte("x\n"), 0o644)
	}
	return h.config("restic: {path: " + restic + "}\nrepositories:\n" +
		"  - {name: main, url: " + filepath.Join(h.dir, "repo") + ", password_file: " + pass + providerYAML(main) + "}\n" +
		"  - {name: offsite, url: \"rest:http://qa:URL-MARKER@127.0.0.1:9/offsite\", password_file: " + pass + providerYAML(offsite) + "}\n")
}

func (h *startHost) goodRestic() string {
	return resticScript(h.t, filepath.Join(h.dir, "restic"), "0.19.1")
}

// Scenario: Неподдерживаемый crypto_provider одного репозитория останавливает агента
func TestAnUnsupportedCryptoProviderOfOneRepositoryStopsTheAgent(t *testing.T) {
	for _, value := range []string{"gost", "RESTIC-AES", "restic-aes ", "restic"} {
		h := newStartHost(t)
		_, stderr := h.refuses(h.cryptoConfig(0o600, h.goodRestic(), nil, ptr(value)))
		if !strings.HasPrefix(stderr, "sard-agent: ") || strings.Count(stderr, "\n") != 1 {
			t.Errorf("%q: stderr = %q", value, stderr)
		}
		for _, want := range []string{"CRYPTO_PROVIDER_UNSUPPORTED", "crypto_provider", "restic-aes", `"offsite"`, strconv.Quote(value)} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%q: stderr does not contain %q: %s", value, want, stderr)
			}
		}
		if strings.Contains(stderr, "URL-MARKER") {
			t.Errorf("%q: stderr shows the url: %s", value, stderr)
		}
	}
}

// Scenario: Встроенный или не заданный crypto_provider не мешает старту
func TestTheBuiltInOrAnUnsetCryptoProviderDoesNotPreventTheStart(t *testing.T) {
	rows := [][2]*string{{nil, nil}, {ptr(""), nil}, {ptr("restic-aes"), nil}, {ptr("restic-aes"), ptr("")}}
	for _, row := range rows {
		h := newStartHost(t)
		if stderr := h.connects(h.cryptoConfig(0o600, h.goodRestic(), row[0], row[1])); strings.Contains(stderr, "CRYPTO_PROVIDER_UNSUPPORTED") {
			t.Errorf("stderr = %q", stderr)
		}
	}
}

// Scenario: Из нескольких неподдерживаемых crypto_provider называется первый по порядку конфига
func TestTheFirstUnsupportedCryptoProviderInConfigOrderIsNamed(t *testing.T) {
	h := newStartHost(t)
	_, stderr := h.refuses(h.cryptoConfig(0o600, h.goodRestic(), ptr("gost"), ptr("aes-gcm")))
	if !strings.Contains(stderr, `"main"`) || !strings.Contains(stderr, `"gost"`) || strings.Contains(stderr, "offsite") || strings.Contains(stderr, "aes-gcm") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// Scenario: Перевод строки в crypto_provider не разрывает сообщение
func TestANewlineInCryptoProviderDoesNotBreakTheMessage(t *testing.T) {
	h := newStartHost(t)
	_, stderr := h.refuses(h.cryptoConfig(0o600, h.goodRestic(), nil, ptr("gost\nx")))
	if strings.Count(stderr, "\n") != 1 || !strings.Contains(stderr, `"gost\nx"`) {
		t.Fatalf("stderr = %q", stderr)
	}
}

// Scenario: Неподдерживаемый crypto_provider сообщается раньше нарушения прав секретного файла
func TestAnUnsupportedCryptoProviderIsReportedBeforeASecretFilePermissionProblem(t *testing.T) {
	h := newStartHost(t)
	_, stderr := h.refuses(h.cryptoConfig(0o644, h.goodRestic(), nil, ptr("gost")))
	if !strings.Contains(stderr, "CRYPTO_PROVIDER_UNSUPPORTED") || strings.Contains(stderr, "-rw-r--r--") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// Scenario: Неподдерживаемый crypto_provider сообщается раньше проблемы с restic
func TestAnUnsupportedCryptoProviderIsReportedBeforeAResticProblem(t *testing.T) {
	h := newStartHost(t)
	_, stderr := h.refuses(h.cryptoConfig(0o600, filepath.Join(h.dir, "nowhere"), nil, ptr("gost")))
	if !strings.Contains(stderr, "CRYPTO_PROVIDER_UNSUPPORTED") || strings.Contains(stderr, "RESTIC_NOT_FOUND") {
		t.Fatalf("stderr = %q", stderr)
	}
}

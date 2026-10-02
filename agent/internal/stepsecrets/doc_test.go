// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package stepsecrets_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func operatorDoc(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "operations", "step-log-masking.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(doc)
}

func wantAll(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("docs/operations/step-log-masking.md does not contain %q", want)
		}
	}
}

// Scenario: Документация перечисляет, что маскируется.
func TestTheDocumentationListsWhatIsMasked(t *testing.T) {
	wantAll(t, operatorDoc(t),
		"secrets",           // the agent's secrets
		"env_file",          // the values of env files
		"всех репозиториев", // of every repository
		"все секреты агента, а не только", // not only the ones the step uses
		"tls.key_file",   // the agent's private key
		"base64",         // the encodings
		"base64url",      //
		"%XX",            // URL escapes in both cases
		"JSON",           //
		"одним маркером", // a multiline key is one marker
		"[REDACTED]",     // the marker
		"имя секрета в журнале не раскрывается", // and it does not name the secret
		"короче 4", // the short value limit
		"WARN",     // the warning at start
	)
}

// Scenario: Документация говорит, что маскируется не всё.
func TestTheDocumentationSaysWhatIsNotMasked(t *testing.T) {
	wantAll(t, operatorDoc(t),
		"зашифрованн",              // encrypted
		"хешированн",               // hashed
		"по частям",                // printed piecewise
		"с переносами строк",       // line-wrapped base64 (MIME, PEM)
		"целиком в hex",            // the whole value in hex
		"password_file",            // the repository password
		"не читает и не маскирует", //
		"сервер",                   // the server does not mask again
		"повторно не маскирует",    //
		"со следующего шага",       // a new value of a secret
		"пробел",                   // whitespace around the value is part of it
		"Host.Log",                 // multiline text in one call
		"порядок строк",            // the order across sources
		"нечитаемого `env_file` другого репозитория", // OQ-125: a foreign file
		"не маскируются, пока файл не прочитается",   // its values are not masked
		"предупреждение в журнал агента",             // and the agent warns
	)
}

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew

import (
	"errors"
	"fmt"
	"testing"
)

func TestDescribeWritesControlCharactersTheWayGoDoes(t *testing.T) {
	t.Parallel()
	rows := map[string]string{
		"plain text":           "plain text",
		"line\nbreak":          `line\nbreak`,
		"tab\there":            `tab\there`,
		"nul\x00byte":          `nul\x00byte`,
		"two\r\nin a row":      `two\r\nin a row`,
		"ends with newline\n":  `ends with newline\n`,
		"\x1b[31mred":          `\x1b[31mred`,
		"after\nand more text": `after\nand more text`,
	}
	for in, want := range rows {
		if got := describe(errors.New(in)); got != want {
			t.Errorf("describe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDescribeOfAnAnswerOfTheServerIsItsTextWhateverWrapsIt(t *testing.T) {
	t.Parallel()
	ce := &CallError{Text: "Unavailable INTERNAL_RETRYABLE"}
	for _, err := range []error{ce, fmt.Errorf("renew: %w", ce)} {
		if got := describe(err); got != "Unavailable INTERNAL_RETRYABLE" {
			t.Errorf("describe(%q) = %q", err, got)
		}
	}
}

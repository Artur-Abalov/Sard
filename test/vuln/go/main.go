// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Command fixture calls a function with a known vulnerability on purpose
// (golang.org/x/text before v0.3.8, GO-2022-1059): scripts/test-vuln-scan.sh
// checks that govulncheck finds it and vulngate blocks it. Never built or shipped.
package main

import (
	"fmt"
	"os"

	"golang.org/x/text/language"
)

func main() {
	tags, _, err := language.ParseAcceptLanguage(os.Getenv("ACCEPT_LANGUAGE"))
	fmt.Println(tags, err)
}

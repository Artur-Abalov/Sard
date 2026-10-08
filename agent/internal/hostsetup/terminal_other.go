// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build !linux

package hostsetup

import (
	"io"
	"os"
)

// StdinTerminal is nil: the agent reads a secret from a terminal on
// Linux only; elsewhere the value comes from a flag.
func StdinTerminal(*os.File, io.Writer) Terminal { return nil }

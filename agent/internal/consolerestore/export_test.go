// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import "github.com/Artur-Abalov/sard/agent/internal/hostsetup"

// WriteMarker is writeMarker for the tests of the package.
func WriteMarker(dir hostsetup.Dir, destination string, files, bytes uint64) error {
	return writeMarker(dir, marker{Destination: destination, Files: files, Bytes: bytes})
}

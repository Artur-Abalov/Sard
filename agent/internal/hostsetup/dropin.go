// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"path/filepath"
	"strings"
)

// DropIn is the systemd drop-in that lets the service write to a local
// repository: sard-agent.service runs with ProtectSystem=strict, and
// without ReadWritePaths restic hangs on its lock (Н6).
type DropIn struct {
	FS FS
	// Dir is /etc/systemd/system/sard-agent.service.d.
	Dir string
}

// Path is the drop-in of the repository name.
func (d DropIn) Path(name string) string {
	return filepath.Join(d.Dir, "sard-repo-"+name+".conf")
}

// Write creates the drop-in, and its directory if needed, owned by root.
func (d DropIn) Write(name, repositoryPath string) error {
	if _, err := EnsureDir(d.FS, d.Dir, Attrs{Mode: 0o755}); err != nil {
		return err
	}
	// systemd splits the value at white space: a space is \x20.
	content := "# Written by sard-agent repo add; removed by sard-agent repo remove.\n[Service]\nReadWritePaths=" +
		strings.ReplaceAll(repositoryPath, " ", `\x20`) + "\n"
	return WriteFile(d.FS, d.Path(name), []byte(content), Attrs{Mode: 0o644})
}

// Remove deletes the drop-in; it reports whether there was one.
func (d DropIn) Remove(name string) (bool, error) {
	return RemoveFile(d.FS, d.Path(name))
}

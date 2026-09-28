// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

const lockSuffix = ".sard-enroll.lock"

// LockPath is where Lock puts its lock file: next to certFile (В15).
func LockPath(certFile string) string {
	return filepath.Join(filepath.Dir(certFile), filepath.Base(certFile)+lockSuffix)
}

// Lock refuses a second concurrent "enroll" for the same config (В15): it
// creates a lock file next to certFile exclusively and returns a function
// that removes it. A second Lock call while the first is held fails with a
// ClassTemporary *Error, before either command talks to the server.
func Lock(certFile string) (unlock func(), err error) {
	path := LockPath(certFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, &Error{Class: ClassTemporary, msg: "an enrollment is already in progress on this host"}
		}
		return nil, &Error{Class: ClassWrite, msg: "creating the lock file " + path + " failed", err: err}
	}
	_, _ = fmt.Fprintln(f, strconv.Itoa(os.Getpid()))
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}

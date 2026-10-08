// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"errors"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// A root that cannot be opened (no descriptor left) is the open error, not
// a directory that fails later.
func TestOpenRootDirWithoutADescriptorLeftIsTheOpenError(t *testing.T) {
	var saved syscall.Rlimit
	ok(t, syscall.Getrlimit(syscall.RLIMIT_NOFILE, &saved))
	none := saved
	none.Cur = 0
	ok(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &none))
	d, err := hostsetup.OS{}.OpenRootDir()
	ok(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &saved))
	if d != nil || !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("dir = %v, err = %v", d, err)
	}
}

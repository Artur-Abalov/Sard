// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build !linux

package hostsetup

import "errors"

// OpenRootDir implements FS: the descriptor walk is Linux only.
func (OS) OpenRootDir() (Dir, error) { return nil, errors.New("not supported on this system") }

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"errors"
	"io/fs"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// checkHint ends the message of a failed step whose cause sard-agent check
// explains (A8d-4, Ч28). The server and the console show the text as it is.
const checkHint = "run `sudo sard-agent check` on the host"

// withCheckHint adds the hint to a failure that sard-agent check diagnoses: the
// storage cannot be reached, refuses the key or the access, the password is
// wrong or its file is missing, a program of the plugin is not found.
// Every other failure, and a rejected step, is returned as it is.
func withCheckHint(err error) error {
	if err == nil || errors.Is(err, executor.ErrRejected) || !checkExplains(err) {
		return err
	}
	return &hinted{err}
}

func checkExplains(err error) bool {
	switch {
	case errors.Is(err, restic.ErrNetwork), errors.Is(err, restic.ErrWrongPassword):
		return true
	case errors.Is(err, restic.ErrPasswordFile) && errors.Is(err, fs.ErrNotExist):
		return true
	case repoinit.RemoteReason(err) != refusal.Reason(""):
		return true
	}
	return errors.Is(err, sdk.ErrProgramMissing)
}

// hinted is an error whose message ends with the hint; the cause stays
// reachable with errors.Is and errors.As.
type hinted struct{ err error }

func (e *hinted) Error() string { return e.err.Error() + "\n" + checkHint }
func (e *hinted) Unwrap() error { return e.err }

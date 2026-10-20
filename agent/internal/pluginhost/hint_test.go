// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

const checkHint = "run `sudo sard-agent check` on the host"

func resticFatal(kind error, message string) error {
	exit := &restic.ExitError{Code: 1, Message: message}
	if kind == nil {
		return fmt.Errorf("restic backup: %w", exit)
	}
	return fmt.Errorf("restic backup: %w: %w", kind, exit)
}

// A8d-4, Ч28: the failures that sard-agent check explains end with its command.
func TestFailedBackupStepOfAKnownCauseEndsWithTheCheckHint(t *testing.T) {
	cases := map[string]error{
		"connection refused": resticFatal(restic.ErrNetwork, "Fatal: unable to open repository: dial tcp 10.0.0.1:9000: connection refused"),
		"wrong password":     fmt.Errorf("restic backup: %w", restic.ErrWrongPassword),
		"access denied":      resticFatal(nil, "Fatal: unable to open repository: Access Denied."),
		"s3 key rejected":    resticFatal(nil, "Fatal: unable to open repository: The Access Key Id you provided does not exist in our records. InvalidAccessKeyId"),
		"ssh key":            resticFatal(nil, "Fatal: unable to open repository: Permission denied (publickey)."),
		"password file":      fmt.Errorf("%w of repository %q: %w", restic.ErrPasswordFile, "main", &fs.PathError{Op: "open", Path: "/etc/sard/main.pass", Err: fs.ErrNotExist}),
	}
	for name, cause := range cases {
		t.Run(name, func(t *testing.T) {
			f := newHandlers(t, &plugin{})
			f.repo.err = cause
			_, err := f.run(t, step(backup, `{}`))
			if err == nil || !strings.HasSuffix(err.Error(), "\n"+checkHint) {
				t.Fatalf("err = %v, want a message ending with the hint", err)
			}
			if !errors.Is(err, cause) {
				t.Errorf("the hint hid the cause: %v", err)
			}
			if !strings.HasPrefix(err.Error(), "repository \"main\": ") {
				t.Errorf("the repository is not named: %v", err)
			}
		})
	}
}

func TestFailedBackupStepOfAMissingPluginProgramEndsWithTheCheckHint(t *testing.T) {
	msg := fmt.Sprintf("mydb: %v: %q", sdk.ErrProgramMissing, "mongodump")
	f := newHandlers(t, &plugin{prepare: func(context.Context, sdk.Host, sdk.Config) error {
		return fmt.Errorf("mydb: %w: %q", sdk.ErrProgramMissing, "mongodump")
	}})
	_, err := f.run(t, step(backup, `{}`))
	want := "prepare: " + msg + "\n" + checkHint
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestFailedRestoreStepOfAKnownCauseEndsWithTheCheckHint(t *testing.T) {
	h := newConsoleHost(t, &restorer{})
	h.repo.restoreTo = func(context.Context, restic.RestoreRequest) (restic.RestoreSummary, error) {
		return restic.RestoreSummary{}, fmt.Errorf("restic restore: %w", restic.ErrWrongPassword)
	}
	_, err := h.run(t, restoreStep(restoreConfigD))
	if err == nil || !strings.HasSuffix(err.Error(), "\n"+checkHint) {
		t.Fatalf("err = %v", err)
	}
}

// A8d-4: the other failures get no hint.
func TestOtherFailuresOfAStepGetNoHint(t *testing.T) {
	cases := map[string]error{
		"unreadable source": fmt.Errorf("restic backup: %w", restic.ErrUnreadableSource),
		"other exit":        resticFatal(nil, "Fatal: something else"),
		"locked":            fmt.Errorf("restic backup: %w", restic.ErrLocked),
		"other password":    fmt.Errorf("%w: other", restic.ErrPasswordFile),
	}
	for name, cause := range cases {
		t.Run(name, func(t *testing.T) {
			f := newHandlers(t, &plugin{})
			f.repo.err = cause
			_, err := f.run(t, step(backup, `{}`))
			if err == nil || strings.Contains(err.Error(), "sard-agent check") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestAMissingSourceAndARejectedStepGetNoHint(t *testing.T) {
	f := newHandlers(t, &plugin{prepare: func(context.Context, sdk.Host, sdk.Config) error {
		return errors.New(`path "/srv/data" does not exist`)
	}})
	_, err := f.run(t, step(backup, `{}`))
	if err == nil || strings.Contains(err.Error(), "sard-agent check") {
		t.Fatalf("missing source: %v", err)
	}
	_, err = f.run(t, step(backup, `{"nope": 1}`))
	if !errors.Is(err, executor.ErrRejected) || strings.Contains(err.Error(), "sard-agent check") {
		t.Fatalf("rejected: %v", err)
	}
}

// A8d-4 with R2: the restore failures of the other reasons get no hint.
func TestFailedRestoreStepOfAnotherCauseGetsNoHint(t *testing.T) {
	h := newConsoleHost(t, &restorer{})
	h.repo.restoreTo = func(context.Context, restic.RestoreRequest) (restic.RestoreSummary, error) {
		return restic.RestoreSummary{}, syscall.ENOSPC
	}
	_, err := h.run(t, restoreStep(restoreConfigD))
	if err == nil || strings.Contains(err.Error(), "sard-agent check") {
		t.Fatalf("err = %v", err)
	}
}

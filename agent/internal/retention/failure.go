// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package retention

import (
	"errors"
	"fmt"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// deleteDenied is the message of a storage account that may not delete.
const deleteDenied = "storage account has no delete permission: assign repository maintenance to a host whose account can delete"

// classify tells a failure of the step by its cause (КL11): the code for the
// server, the holder of a live lock and the error the step ends with. A step
// that did not fail (nil) or was stopped by its context has no failure code.
func classify(err error) (agentv1.MaintenanceFailure, string, error) {
	switch {
	case err == nil:
		return agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_UNSPECIFIED, "", nil
	case errors.Is(err, restic.ErrLocked):
		return agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_REPOSITORY_LOCKED, restic.LockHolder(err), lockedError(err)
	case errors.Is(err, restic.ErrNetwork):
		return agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_STORAGE_UNAVAILABLE, "",
			fmt.Errorf("repository storage is unavailable: %w", err)
	case restic.RemoteRefusalOf(err) == restic.AccessDenied:
		return agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_DELETE_DENIED, "", errors.New(deleteDenied)
	}
	return agentv1.MaintenanceFailure_MAINTENANCE_FAILURE_UNSPECIFIED, "", err
}

func lockedError(err error) error {
	if holder := restic.LockHolder(err); holder != "" {
		return fmt.Errorf("repository is locked by another process after waiting for it: %s", holder)
	}
	return fmt.Errorf("repository is locked by another process after waiting for it: %w", err)
}

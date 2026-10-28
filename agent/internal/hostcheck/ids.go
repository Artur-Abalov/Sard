// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

// The identifiers of the checks of sard-agent check that have no file of
// their own (Ч10). They are stable between versions: a new check gets a new
// identifier, an old one never changes its meaning and is never reused.
const (
	CheckServiceUser      = "service.user"
	CheckResticBinary     = "restic.binary"
	CheckTLSRole          = "tls.role"
	CheckServerReachable  = "server.reachable"
	CheckServerTLS        = "server.tls"
	CheckServerAccepted   = "server.accepted"
	CheckClockSkew        = "clock.skew"
	CheckSecretReadable   = "secret.readable"
	CheckSecretBound      = "secret.bound"
	CheckPluginDeclared   = "plugin.declared"
	CheckPluginPrograms   = "plugin.programs"
	CheckRestoreLocations = "restore.locations"
	CheckSELinuxLabels    = "selinux.labels"
	CheckSELinuxDenials   = "selinux.denials"
	CheckServiceRunning   = "service.running"
	CheckServiceSteps     = "service.steps"
)

// The identifiers of the checks of one repository, in their order (Ч3).
const (
	CheckRepositoryStorage     = "repository.storage"
	CheckRepositoryCredentials = "repository.credentials"
	CheckRepositoryInitialized = "repository.initialized"
	CheckRepositoryPassword    = "repository.password"
	CheckRepositoryID          = "repository.id"
	CheckRepositoryKeys        = "repository.keys"
	CheckRepositoryLocks       = "repository.locks"
	// CheckRepositoryIntegrity is asked for by name and is not in the default run.
	CheckRepositoryIntegrity = "repository.integrity"
	// CheckRepositoryReadOnly is a verifier's only (V5): its credentials only read.
	CheckRepositoryReadOnly = "repository.readonly"
)

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

// The reasons of a refusal of a restore (R2b ВК7, ВК22, ВК26, ADR 0066): the
// codes of this plugin, spelled as the operator and the server know them. The
// reasons every restore may have, such as SNAPSHOT_UNKNOWN or BACKEND_UNAVAILABLE,
// are those of the SDK.
const (
	ReasonClientMissing          = "PG_CLIENT_MISSING"
	ReasonClientTooOld           = "PG_CLIENT_TOO_OLD"
	ReasonServerTooOld           = "PG_SERVER_TOO_OLD"
	ReasonConnect                = "PG_CONNECT"
	ReasonCreatedbRequired       = "CREATEDB_REQUIRED"
	ReasonSuperuserRequired      = "SUPERUSER_REQUIRED"
	ReasonGlobalsSnapshotMissing = "GLOBALS_SNAPSHOT_MISSING"
	ReasonGlobalsTablespaces     = "GLOBALS_TABLESPACES"
	ReasonRolesExist             = "ROLES_EXIST"
	ReasonDatabaseExists         = "DATABASE_EXISTS"
	ReasonGlobalsFailed          = "PG_GLOBALS_FAILED"
	ReasonCreateDatabase         = "PG_CREATE_DATABASE"
	ReasonRestoreFailed          = "PG_RESTORE_FAILED"
	// The reasons the plugin shares in spelling with the other restores of the
	// agent: a plan or a step that breaks the rules, a secret that cannot be used,
	// a journal that cannot be written.
	ReasonTargetInvalid = "TARGET_INVALID"
	ReasonSecretInvalid = "SECRET_INVALID"
	// ReasonSecretNotAllowed: the binding of the secret on the agent host does
	// not allow this plugin, this target, or this TLS mode there (S11).
	ReasonSecretNotAllowed = "SECRET_NOT_ALLOWED"
	ReasonTargetWrite      = "TARGET_WRITE"
)

// The stages of a restore of a database, in the order they happen.
const (
	stageCheck    = "check"
	stageGlobals  = "globals"
	stageCreateDB = "create-database"
	stageRestore  = "restore"
)

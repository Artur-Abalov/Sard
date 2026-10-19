// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import (
	"fmt"
	"strconv"
	"strings"
)

// Restorer is implemented by plugins the console can restore. Only they
// announce a restore config schema to the server.
//
// A restore is all or nothing: the agent writes into a temporary directory
// of its own and moves the result into the destination with one rename, so
// a failed restore leaves no trace.
type Restorer interface {
	// RestoreConfigSchema returns the JSON Schema (draft 2020-12) of the
	// config of a restore. The UI renders the restore form from it and the
	// server validates the config against it. Secret fields hold the name of
	// a secret of the host, as in ConfigSchema.
	RestoreConfigSchema() []byte
	// PlanRestore reads a restore config that is valid against
	// RestoreConfigSchema and says what to restore and where. The agent
	// validates the plan (RestorePlan.Validate) before anything is written.
	PlanRestore(cfg Config) (RestorePlan, error)
}

// RestorePlan is a restore of a snapshot into a new directory of the host:
// the files of the snapshot appear under their original absolute paths
// inside it.
type RestorePlan struct {
	// Destination is a relative path inside the restore directory of the
	// host, which only the host's operator names. It is 1-4096 bytes of
	// components of 1-255 bytes; no component is empty, ".." or starts with
	// "." (those names are the agent's own), and there is no NUL byte.
	Destination string
	// Paths limits the restore to these absolute paths of the snapshot, each
	// taken literally (no * ? [ \ and no ".." component), 1-4096 bytes, at
	// most MaxRestorePaths, without repeats. Empty: the whole snapshot.
	Paths []string
	// Database, if not nil, makes this a restore into a new database of a
	// server instead of a directory: Destination and Paths stay empty.
	Database *DatabaseRestore
}

// DatabaseRestore is a restore into a new database of a server (R2b). The
// password is not here: PasswordRef names a secret of the host.
type DatabaseRestore struct {
	Host string
	Port int
	User string
	// PasswordRef is the name of a secret of the host that holds the password.
	PasswordRef string
	TLSMode     string
	TLSRootCert string
	// Name is the new database: 1-63 bytes, no NUL.
	Name string
	// Globals also restores the global objects of the cluster (roles).
	Globals bool
	// ClientPath is the absolute path of the restore tool of the host, if the
	// config names one; empty: the tools of the PATH of the agent.
	ClientPath string
}

// MaxDatabaseName is the longest name of a database, in bytes.
const MaxDatabaseName = 63

// Limits of a RestorePlan.
const (
	MaxRestorePaths       = 100
	MaxRestoreDestination = 4096
	maxComponent          = 255
	maxPath               = 4096
)

// Validate checks the plan against the rules of RestorePlan; a violation is
// reported as a *ConfigError whose paths are those of the restore config
// ("/destination", "/paths", "/paths/<index>").
func (p RestorePlan) Validate() error {
	if p.Database != nil {
		return p.validateDatabase()
	}
	var violations []Violation
	if msg := checkDestination(p.Destination); msg != "" {
		violations = append(violations, Violation{Path: "/destination", Message: msg})
	}
	violations = append(violations, checkPaths(p.Paths)...)
	if len(violations) == 0 {
		return nil
	}
	return &ConfigError{Violations: violations}
}

// validateDatabase: a database plan names a database and nothing else.
func (p RestorePlan) validateDatabase() error {
	var violations []Violation
	if msg := checkDatabaseName(p.Database.Name); msg != "" {
		violations = append(violations, Violation{Path: "/database", Message: msg})
	}
	if p.Destination != "" {
		violations = append(violations, Violation{Path: "/destination", Message: "a restore into a database has no destination directory"})
	}
	if len(p.Paths) > 0 {
		violations = append(violations, Violation{Path: "/paths", Message: "a restore into a database has no paths"})
	}
	if len(violations) == 0 {
		return nil
	}
	return &ConfigError{Violations: violations}
}

func checkDatabaseName(name string) string {
	switch {
	case len(name) < 1 || len(name) > MaxDatabaseName:
		return fmt.Sprintf("want 1-%d bytes, got %d", MaxDatabaseName, len(name))
	case strings.ContainsRune(name, 0):
		return "must not contain a NUL byte"
	}
	return ""
}

func checkDestination(d string) string {
	if msg := checkWhole(d); msg != "" {
		return msg
	}
	for _, c := range strings.Split(d, "/") {
		if msg := checkComponent(c); msg != "" {
			return msg
		}
	}
	return ""
}

// checkWhole judges the destination as a string, before it is cut into components.
func checkWhole(d string) string {
	switch {
	case d == "" || len(d) > MaxRestoreDestination:
		return fmt.Sprintf("want 1-%d bytes, got %d", MaxRestoreDestination, len(d))
	case strings.ContainsRune(d, 0):
		return "must not contain a NUL byte"
	case strings.HasPrefix(d, "/"):
		return "must be a relative path inside the restore directory of the host"
	}
	return ""
}

func checkComponent(c string) string {
	switch {
	case c == "" || len(c) > maxComponent:
		return fmt.Sprintf("each component must be 1-%d bytes, got one of %d", maxComponent, len(c))
	case strings.HasPrefix(c, "."):
		return fmt.Sprintf("component %q must not start with a dot: such names belong to the agent", c)
	}
	return ""
}

func checkPaths(paths []string) []Violation {
	if len(paths) > MaxRestorePaths {
		return []Violation{{Path: "/paths", Message: fmt.Sprintf("want at most %d paths, got %d", MaxRestorePaths, len(paths))}}
	}
	var violations []Violation
	seen := map[string]bool{}
	for i, p := range paths {
		msg := checkPath(p)
		if msg == "" && seen[p] {
			msg = "repeats an earlier path"
		}
		if msg != "" {
			violations = append(violations, Violation{Path: "/paths/" + strconv.Itoa(i), Message: msg})
		}
		seen[p] = true
	}
	return violations
}

func checkPath(p string) string {
	switch {
	case p == "" || len(p) > maxPath:
		return fmt.Sprintf("want 1-%d bytes, got %d", maxPath, len(p))
	case !strings.HasPrefix(p, "/"):
		return "must be an absolute path of the snapshot"
	case strings.ContainsAny(p, "\x00*?[\\"):
		return `is taken literally: NUL, *, ?, [ and \ are not allowed`
	case strings.Contains("/"+p+"/", "/../"):
		return `must not have a ".." component`
	}
	return ""
}

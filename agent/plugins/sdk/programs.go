// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

// ProgramChecker is implemented by a plugin that runs programs of the host
// (a database client, a dump tool) and wants "sard-agent check" to look for
// them. A plugin that needs none does not implement it: the agent says so.
type ProgramChecker interface {
	// CheckPrograms looks for the programs of the plugin on the host and says
	// what it found. It changes nothing and runs programs only to ask their
	// version.
	CheckPrograms(h ProgramHost) ProgramsCheck
}

// ProgramHost is what the agent offers a plugin that looks for its programs.
type ProgramHost interface {
	// Path are the directories of the PATH of the agent, in order.
	Path() []string
	// Run runs the program at path with args as the service user, the way the
	// plugin will run it, and returns what it printed on its standard output.
	// A program that cannot be started is an error: it is not there, or the
	// service user may not run it.
	Run(path string, args ...string) (string, error)
}

// ProgramsStatus is how well the host serves the programs of a plugin.
type ProgramsStatus int

// The statuses; the zero value is none.
const (
	_ ProgramsStatus = iota
	// ProgramsOK: everything the plugin needs is there.
	ProgramsOK
	// ProgramsSkip: the plugin cannot tell whether the host needs the
	// programs (e.g. it has no sources of the plugin), so nothing is wrong.
	ProgramsSkip
	// ProgramsWarn: something is missing that the plugin can do without.
	ProgramsWarn
	// ProgramsError: something the plugin needs is missing.
	ProgramsError
)

// ProgramsCheck is the outcome of CheckPrograms, shown as one line of the
// report of "sard-agent check".
type ProgramsCheck struct {
	Status  ProgramsStatus
	Message string
	// Hint says what to do about it; empty when nothing is wrong.
	Hint string
}

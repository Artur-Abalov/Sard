// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"fmt"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// ServiceUserLine is service.user (Ч10).
func ServiceUserLine(name string, uid uint32, found bool) Line {
	if !found {
		return Line{Check: CheckServiceUser, Status: Error,
			Message: fmt.Sprintf("the service user %q cannot be found on this host", name),
			Hint:    "create it, or set service.user in the agent config; see " + HelpURL(CheckServiceUser)}
	}
	return Line{Check: CheckServiceUser, Status: OK, Message: fmt.Sprintf("the service user %s exists (uid %d)", name, uid)}
}

// ResticVersionLine is restic.binary of a restic that can be used (Ч12): OK
// for the release shipped with the agent, WARN for another one.
func ResticVersionLine(path string, found, shipped restic.Version) Line {
	line := Line{Check: CheckResticBinary, Status: OK, Message: fmt.Sprintf("restic %s at %s", found, path)}
	if found != shipped {
		line.Status = Warn
		line.Message += fmt.Sprintf(" is not the release shipped with this agent (%s)", shipped)
		line.Hint = "reinstall the agent package, or remove restic.path from the config so the shipped restic is used"
	}
	return line
}

// ResticFailureLine is restic.binary of a restic that cannot be used.
func ResticFailureLine(f *refusal.Failure) Line {
	return Line{Check: CheckResticBinary, Status: Error, Message: f.Error(), Hint: "see " + HelpURL(CheckResticBinary)}
}

// PluginInfo is a plugin the agent declares to the server.
type PluginInfo struct{ Name, Version string }

// PluginDeclaredLine is plugin.declared.
func PluginDeclaredLine(plugins []PluginInfo) Line {
	var parts []string
	for _, p := range plugins {
		parts = append(parts, p.Name+" "+p.Version)
	}
	return Line{Check: CheckPluginDeclared, Status: OK, Message: "the agent declares to the server: " + strings.Join(parts, ", ")}
}

// NoProgramsLine is plugin.programs of a plugin that needs no program of the host.
func NoProgramsLine(plugin string) Line {
	return Line{Check: CheckPluginPrograms, Subject: plugin, Status: OK, Message: "the plugin needs no program of the host"}
}

// ProgramsLine is plugin.programs of a plugin that looks for its programs
// itself (sdk.ProgramChecker): the plugin's words, with the status mapped. An
// answer without a status the agent knows is an error of the check.
func ProgramsLine(plugin string, c sdk.ProgramsCheck) Line {
	line := Line{Check: CheckPluginPrograms, Subject: plugin, Message: c.Message, Hint: c.Hint}
	switch c.Status {
	case sdk.ProgramsOK:
		line.Status = OK
	case sdk.ProgramsSkip:
		line.Status = Skip
	case sdk.ProgramsWarn:
		line.Status = Warn
	case sdk.ProgramsError:
		line.Status = Error
	default:
		line.Status = Error
		line.Message = fmt.Sprintf("the plugin %s answered with no status: %s", plugin, c.Message)
	}
	return line
}

const checkAgain = "sudo sard-agent check"

// TLSRoleLine is tls.role: the role of the agent is checked once V1 is merged.
func TLSRoleLine() Line {
	return Line{Check: CheckTLSRole, Status: Skip, Message: "agent role is checked once V1 is available", Hint: "nothing to do yet; the check is added by a later version of the agent"}
}

// RestoreLocationsLine is restore.locations: checked once R2 is merged.
func RestoreLocationsLine() Line {
	return Line{Check: CheckRestoreLocations, Status: Skip, Message: "restore locations are checked once R2 is available", Hint: "nothing to do yet; the check is added by a later version of the agent"}
}

// NotCheckedLine is a check that was not made because a check it depends on failed.
func NotCheckedLine(check, subject, failed string) Line {
	return Line{Check: check, Subject: subject, Status: Skip, Message: fmt.Sprintf("not checked: %s failed", failed),
		Hint: fmt.Sprintf("fix %s first, then run `%s` again", failed, checkAgain)}
}

// OfflineLine is a check that --offline leaves out.
func OfflineLine(check, subject string) Line {
	return Line{Check: check, Subject: subject, Status: Skip, Message: "offline", Hint: fmt.Sprintf("run `%s` without --offline to make it", checkAgain)}
}

// NeedsRootLine is a check the service user cannot make: what stands in its way.
func NeedsRootLine(check, subject, what string) Line {
	return Line{Check: check, Subject: subject, Status: Skip, Message: fmt.Sprintf("needs root: %s", what), Hint: fmt.Sprintf("run `%s`", checkAgain)}
}

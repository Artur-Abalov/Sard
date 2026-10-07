// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"fmt"
	"io"
	"strconv"
)

// Auditor takes one audit line (the system log, tag sard-agent, Р9).
type Auditor interface {
	Write(line string) error
}

// OpenAuditor connects to the system log.
type OpenAuditor func() (Auditor, error)

// Actor is who made a change: the user who ran sudo, or the process's own.
type Actor struct {
	Name string
	UID  string
}

// ActorFrom is SUDO_USER and SUDO_UID when both are set, otherwise the
// user of the process.
func ActorFrom(getenv func(string) string, processName string, processUID uint32) Actor {
	name, uid := getenv("SUDO_USER"), getenv("SUDO_UID")
	if name != "" && uid != "" {
		return Actor{Name: name, UID: uid}
	}
	return Actor{Name: processName, UID: fmt.Sprint(processUID)}
}

// Audit writes the line for one change: kind ("secret", "repository"),
// the name and the action ("added", "password revealed"). It holds no
// values, no addresses and no file contents.
func Audit(a Auditor, who Actor, kind, name, action string) error {
	return a.Write(fmt.Sprintf("%s %s %s by %s (uid %s)", kind, auditName(name), action, who.Name, who.UID))
}

// auditName is the name as it is, unless it holds anything but plain
// characters: then it is quoted, so that a line break in a name of the
// config cannot make a second line of the log.
func auditName(name string) string {
	if quoted := strconv.Quote(name); quoted != `"`+name+`"` {
		return quoted
	}
	return name
}

// Record is Audit that cannot fail the command: a system log that is not
// there is a warning (Н9).
func Record(warn io.Writer, open OpenAuditor, who Actor, kind, name, action string) {
	a, err := open()
	if err == nil {
		err = Audit(a, who, kind, name, action)
	}
	if err != nil {
		_, _ = fmt.Fprintf(warn, "warning: the change was not recorded in the system log: %v\n", err)
	}
}

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import "log/syslog"

// auditTag is the syslog tag of audit lines: journalctl -t sard-agent.
const auditTag = "sard-agent"

type syslogAuditor struct{ w *syslog.Writer }

func (s syslogAuditor) Write(line string) error { return s.w.Notice(line) }

// OpenSyslog connects to the system log of this host.
func OpenSyslog() (Auditor, error) { return DialSyslog("", "") }

// DialSyslog connects to the system log at the address ("", "" is the
// local one). authpriv: who changed the secrets of a host is not for every
// reader of the log.
func DialSyslog(network, addr string) (Auditor, error) {
	w, err := syslog.Dial(network, addr, syslog.LOG_NOTICE|syslog.LOG_AUTHPRIV, auditTag)
	if err != nil {
		return nil, err
	}
	return syslogAuditor{w: w}, nil
}

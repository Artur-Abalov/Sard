// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

func TestSyslogAuditorSendsTheLineWithTheTag(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "log")
	conn, err := net.ListenPacket("unixgram", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	a, err := hostsetup.DialSyslog("unixgram", sock)
	if err != nil {
		t.Fatal(err)
	}
	if err := hostsetup.Audit(a, hostsetup.Actor{Name: "alice", UID: "1000"}, "secret", "db", "added"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	msg := string(buf[:n])
	// <85> is facility authpriv (10 * 8) and severity notice (5).
	if !strings.HasPrefix(msg, "<85>") {
		t.Errorf("message %q: want facility authpriv and severity notice", msg)
	}
	for _, want := range []string{"sard-agent", "secret db added by alice (uid 1000)"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
}

func TestSyslogThatCannotBeReachedIsAnError(t *testing.T) {
	if _, err := hostsetup.DialSyslog("unixgram", filepath.Join(t.TempDir(), "no-socket")); err == nil {
		t.Fatal("dialled a socket that is not there")
	}
}

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// The checks of the server (A8d-3, Ч14-Ч16): the facts come from the network,
// what they mean is decided here.

const (
	serverAddr   = "sard.example.org:9090"
	connectLimit = 2 * time.Second
)

var sent = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func serverFacts() hostcheck.ServerFacts {
	return hostcheck.ServerFacts{Address: serverAddr, Timeout: connectLimit, Answer: hostcheck.Answer{Kind: hostcheck.AnswerAccepted},
		Sent: sent, ServerTime: sent}
}

func lineIs(t *testing.T, l hostcheck.Line, status hostcheck.Status, message, hint string) {
	t.Helper()
	if l.Status != status || !strings.Contains(l.Message, message) || !strings.Contains(l.Hint, hint) {
		t.Errorf("want %v %q / %q, got %+v", status, message, hint, l)
	}
}

func TestReachableOKNamesTheAddress(t *testing.T) {
	l := hostcheck.ServerReachableLine(serverFacts())
	if l.Check != "server.reachable" || l.Status != hostcheck.OK || !strings.Contains(l.Message, serverAddr) {
		t.Errorf("%+v", l)
	}
}

func TestReachableFailures(t *testing.T) {
	cases := []struct {
		name string
		f    hostcheck.Failure
		text string
	}{
		{"no such host", hostcheck.Failure{Kind: hostcheck.FailNoSuchHost}, "no such host"},
		{"refused", hostcheck.Failure{Kind: hostcheck.FailRefused}, "connection refused"},
		{"silent", hostcheck.Failure{Kind: hostcheck.FailTimeout}, "did not answer within 2s"},
		{"other", hostcheck.Failure{Kind: hostcheck.FailOther, Detail: "network is unreachable"}, "network is unreachable"},
	}
	for _, c := range cases {
		f := serverFacts()
		f.Reach = c.f
		l := hostcheck.ServerReachableLine(f)
		lineIs(t, l, hostcheck.Error, c.text, "09-troubleshooting.md#serverreachable")
		if !strings.Contains(l.Message, serverAddr) {
			t.Errorf("%s: the message does not name the address: %q", c.name, l.Message)
		}
	}
}

func TestTLSOK(t *testing.T) {
	l := hostcheck.ServerTLSLine(serverFacts())
	if l.Check != "server.tls" || l.Status != hostcheck.OK {
		t.Errorf("%+v", l)
	}
}

func TestTLSFailures(t *testing.T) {
	cases := []struct {
		name string
		f    hostcheck.Failure
		text string
		hint string
	}{
		{"other CA", hostcheck.Failure{Kind: hostcheck.FailUnknownCA}, "not issued by the CA in tls.ca_file", "09-troubleshooting.md#servertls"},
		{"name", hostcheck.Failure{Kind: hostcheck.FailName}, "name in the certificate of the server does not match sard.example.org", "09-troubleshooting.md#servertls"},
		{"client certificate", hostcheck.Failure{Kind: hostcheck.FailClientCert}, "did not accept the client certificate", "sudo sard-agent enroll --force"},
		{"silent", hostcheck.Failure{Kind: hostcheck.FailTimeout}, "did not answer within 2s", "09-troubleshooting.md#servertls"},
		{"other", hostcheck.Failure{Kind: hostcheck.FailOther, Detail: "tls: protocol version not supported"}, "tls: protocol version not supported", "09-troubleshooting.md#servertls"},
	}
	for _, c := range cases {
		f := serverFacts()
		f.TLS = c.f
		lineIs(t, hostcheck.ServerTLSLine(f), hostcheck.Error, c.text, c.hint)
	}
}

func TestAcceptedOK(t *testing.T) {
	l := hostcheck.ServerAcceptedLine(serverFacts())
	if l.Check != "server.accepted" || l.Status != hostcheck.OK {
		t.Errorf("%+v", l)
	}
}

func TestTheServerRefusesTheAgentForEveryReasonItGives(t *testing.T) {
	for _, reason := range []string{"AGENT_REVOKED", "CERT_REVOKED", "CERT_UNKNOWN", "CERT_EXPIRED", "CERT_IDENTITY_MISMATCH", "CERT_MISSING"} {
		f := serverFacts()
		f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerRefused, Reason: reason}
		lineIs(t, hostcheck.ServerAcceptedLine(f), hostcheck.Error, reason, "sudo sard-agent enroll --force")
	}
}

func TestARefusalWithoutAReasonIsStillAnError(t *testing.T) {
	f := serverFacts()
	f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerRefused}
	lineIs(t, hostcheck.ServerAcceptedLine(f), hostcheck.Error, "refuses this agent", "sudo sard-agent enroll --force")
}

func TestAnOlderServerIsAWarningNotAnError(t *testing.T) {
	f := serverFacts()
	f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerOlder}
	lineIs(t, hostcheck.ServerAcceptedLine(f), hostcheck.Warn, "the server is older than this agent; acceptance and clock were not checked", "update")
}

func TestAServerThatFailsOrIsSilentIsAnError(t *testing.T) {
	f := serverFacts()
	f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerTimeout}
	lineIs(t, hostcheck.ServerAcceptedLine(f), hostcheck.Error, "did not answer the probe within 2s", "09-troubleshooting.md#serveraccepted")
	f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerFailed, Detail: "code = Unavailable (INTERNAL_RETRYABLE)"}
	lineIs(t, hostcheck.ServerAcceptedLine(f), hostcheck.Error, "code = Unavailable (INTERNAL_RETRYABLE)", "try again")
}

func TestTheClockSkewIsJudgedAgainstOneMinute(t *testing.T) {
	cases := []struct {
		name   string
		skew   time.Duration
		status hostcheck.Status
	}{
		{"59 seconds", 59 * time.Second, hostcheck.OK},
		{"60 seconds", 60 * time.Second, hostcheck.OK},
		{"61 seconds", 61 * time.Second, hostcheck.Warn},
		{"minus 5 minutes", -5 * time.Minute, hostcheck.Warn},
		{"none", 0, hostcheck.OK},
	}
	for _, c := range cases {
		f := serverFacts()
		f.ServerTime = sent.Add(c.skew)
		if l := hostcheck.ClockSkewLine(f); l.Check != "clock.skew" || l.Status != c.status {
			t.Errorf("%s: %+v", c.name, l)
		}
	}
}

func TestTheClockWarningNamesTheSkewAndTheFix(t *testing.T) {
	f := serverFacts()
	f.ServerTime = sent.Add(300 * time.Second)
	l := hostcheck.ClockSkewLine(f)
	lineIs(t, l, hostcheck.Warn, "300s behind", "timedatectl set-ntp true")
	f.ServerTime = sent.Add(-300 * time.Second)
	lineIs(t, hostcheck.ClockSkewLine(f), hostcheck.Warn, "300s ahead", "timedatectl set-ntp true")
}

func TestTheSkewIsCorrectedForHalfTheRoundTrip(t *testing.T) {
	f := serverFacts()
	f.RTT = 100 * time.Second
	f.ServerTime = sent.Add(50 * time.Second) // the server answered in the middle of the trip: no skew
	if l := hostcheck.ClockSkewLine(f); l.Status != hostcheck.OK || !strings.Contains(l.Message, "0s") {
		t.Errorf("%+v", l)
	}
	f.ServerTime = sent.Add(50*time.Second + 61*time.Second)
	if l := hostcheck.ClockSkewLine(f); l.Status != hostcheck.Warn {
		t.Errorf("%+v", l)
	}
}

func TestTheSkewIsNotJudgedWithoutAServerTime(t *testing.T) {
	f := serverFacts()
	f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerOlder}
	l := hostcheck.ClockSkewLine(f)
	lineIs(t, l, hostcheck.Skip, "no time of the server", "")
}

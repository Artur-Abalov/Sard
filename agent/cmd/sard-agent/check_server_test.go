// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"crypto/tls"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/serverprobe"
)

// A8d-3: the checks of the server, docs/specs/agent/host-check.feature,
// "check проверяет связь с сервером без побочных действий на нём". The
// server is a fake: what the network does is internal/serverprobe's test.

// fakeServerProbe is the server sard-agent check asks. By default it accepts
// the agent and its clock is the clock of the host; the test changes what it
// answers.
type fakeServerProbe struct {
	mu    sync.Mutex
	calls []serverprobe.Request
	// reach, handshake and answer are what goes wrong, nothing by default.
	reach, handshake hostcheck.Failure
	answer           hostcheck.Answer
	// skew is the time of the server less the clock of the host.
	skew time.Duration
}

func (f *fakeServerProbe) run(_ context.Context, r serverprobe.Request) hostcheck.ServerFacts {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r)
	facts := hostcheck.ServerFacts{Address: r.Address, Timeout: r.Timeout, Reach: f.reach, TLS: f.handshake, Answer: f.answer}
	if facts.Answer.Kind == hostcheck.AnswerNone && f.reach.Kind == 0 && f.handshake.Kind == 0 {
		facts.Answer = hostcheck.Answer{Kind: hostcheck.AnswerAccepted}
	}
	facts.Sent = r.Now()
	facts.ServerTime = facts.Sent.Add(f.skew)
	return facts
}

func (f *fakeServerProbe) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

var serverChecks = []string{"server.reachable", "server.tls", "server.accepted", "clock.skew"}

func TestTheServerAcceptsTheAgent(t *testing.T) {
	h := fullHost(t)
	lines, summary, code := h.checkLines()
	assertCode(t, code, exitOK)
	assertLines(t, lines, "OK", "-", serverChecks...)
	if summary != "summary: 27 ok, 0 warnings, 0 errors, 2 skipped" {
		t.Errorf("summary %q", summary)
	}
	if h.server.count() != 1 {
		t.Errorf("the server was asked %d times", h.server.count())
	}
}

func TestTheServerIsAskedAtTheAddressOfTheConfigWithTheLimitAndTheClockOfTheHost(t *testing.T) {
	h := fullHost(t)
	h.cfg.Server.Address = "sard.example.org:9090"
	h.saveHostConfig()
	h.checkLines("--connect-timeout", "2s")
	r := h.server.calls[0]
	if r.Address != "sard.example.org:9090" || r.Timeout != 2*time.Second || r.Now == nil || !r.Now().Equal(h.clock.now) {
		t.Errorf("%+v", r)
	}
}

func TestTheServerIsAskedWithTheCAAndTheCertificateOfTheAgent(t *testing.T) {
	h := fullHost(t)
	h.cfg.Server.Address = "sard.example.org:9090"
	h.saveHostConfig()
	h.checkLines()
	r := h.server.calls[0]
	if r.TLSErr != nil || r.TLS == nil {
		t.Fatalf("tls %v %v", r.TLS, r.TLSErr)
	}
	if r.TLS.ServerName != "sard.example.org" || r.TLS.RootCAs == nil || len(r.TLS.Certificates) != 1 || r.TLS.MinVersion < tls.VersionTLS12 {
		t.Errorf("tls %+v", r.TLS)
	}
}

func TestTheConnectTimeoutOfTheServerIsTenSecondsByDefault(t *testing.T) {
	h := fullHost(t)
	h.checkLines()
	if got := h.server.calls[0].Timeout; got != 10*time.Second {
		t.Errorf("timeout %v", got)
	}
}

func TestOfflineDoesNotAskTheServer(t *testing.T) {
	h := fullHost(t)
	lines, _, code := h.checkLines("--offline")
	assertCode(t, code, exitOK)
	assertLines(t, lines, "SKIP", "-", serverChecks...)
	for _, c := range serverChecks {
		if l := line(t, lines, c, "-"); l.message != "offline" {
			t.Errorf("%+v", l)
		}
	}
	if h.server.count() != 0 {
		t.Errorf("the server was asked")
	}
}

type serverFailure struct {
	name   string
	reach  hostcheck.Failure
	tls    hostcheck.Failure
	failed string
	text   string
	hint   string
}

var serverFailures = []serverFailure{
	{"no such host", hostcheck.Failure{Kind: hostcheck.FailNoSuchHost}, hostcheck.Failure{}, "server.reachable", "no such host", "09-troubleshooting.md#serverreachable"},
	{"closed port", hostcheck.Failure{Kind: hostcheck.FailRefused}, hostcheck.Failure{}, "server.reachable", "connection refused", "09-troubleshooting.md#serverreachable"},
	{"other CA", hostcheck.Failure{}, hostcheck.Failure{Kind: hostcheck.FailUnknownCA}, "server.tls", "not issued by", "09-troubleshooting.md#servertls"},
	{"other name", hostcheck.Failure{}, hostcheck.Failure{Kind: hostcheck.FailName}, "server.tls", "name", "09-troubleshooting.md#servertls"},
	{"client certificate", hostcheck.Failure{}, hostcheck.Failure{Kind: hostcheck.FailClientCert}, "server.tls", "client certificate", "sudo sard-agent enroll --force"},
	{"silent", hostcheck.Failure{}, hostcheck.Failure{Kind: hostcheck.FailTimeout}, "server.tls", "did not answer within 10s", "09-troubleshooting.md#servertls"},
}

func TestFailuresToReachTheServerAreErrorsWithAHint(t *testing.T) {
	for _, c := range serverFailures {
		h := fullHost(t)
		h.server.reach, h.server.handshake = c.reach, c.tls
		lines, _, code := h.checkLines()
		assertCode(t, code, exitError)
		l := line(t, lines, c.failed, "-")
		if l.status != "ERROR" || !strings.Contains(l.message, c.text) || !strings.Contains(l.hint, c.hint) {
			t.Errorf("%s: %+v", c.name, l)
		}
	}
}

func TestWhatFollowsAFailureToReachTheServerIsSkippedUpToTheClock(t *testing.T) {
	for _, c := range serverFailures {
		h := fullHost(t)
		h.server.reach, h.server.handshake = c.reach, c.tls
		lines, _, _ := h.checkLines()
		after := false
		for _, id := range serverChecks {
			if s := line(t, lines, id, "-"); after && (s.status != "SKIP" || s.message != "not checked: "+c.failed+" failed") {
				t.Errorf("%s: %+v", c.name, s)
			}
			after = after || id == c.failed
		}
	}
}

func TestTheServerRefusesTheAgent(t *testing.T) {
	for _, reason := range []string{"AGENT_REVOKED", "CERT_REVOKED", "CERT_UNKNOWN", "CERT_EXPIRED", "CERT_IDENTITY_MISMATCH", "CERT_MISSING"} {
		h := fullHost(t)
		h.server.answer = hostcheck.Answer{Kind: hostcheck.AnswerRefused, Reason: reason}
		lines, _, code := h.checkLines()
		assertCode(t, code, exitError)
		l := line(t, lines, "server.accepted", "-")
		if l.status != "ERROR" || !strings.Contains(l.message, reason) || !strings.Contains(l.hint, "sudo sard-agent enroll --force") {
			t.Errorf("%s: %+v", reason, l)
		}
		if s := line(t, lines, "clock.skew", "-"); s.status != "SKIP" {
			t.Errorf("%s: %+v", reason, s)
		}
	}
}

func TestAnOlderServerIsAWarningAndItsClockIsNotJudged(t *testing.T) {
	h := fullHost(t)
	h.server.answer = hostcheck.Answer{Kind: hostcheck.AnswerOlder}
	lines, _, code := h.checkLines()
	assertCode(t, code, exitWarnings)
	if l := line(t, lines, "server.accepted", "-"); l.status != "WARN" || !strings.Contains(l.message, "the server is older than this agent") {
		t.Errorf("%+v", l)
	}
	if l := line(t, lines, "clock.skew", "-"); l.status != "SKIP" {
		t.Errorf("%+v", l)
	}
}

func TestTheClockOfTheServerIsJudgedAgainstOneMinute(t *testing.T) {
	for _, c := range []struct {
		skew   time.Duration
		status string
		code   int
	}{
		{59 * time.Second, "OK", exitOK},
		{60 * time.Second, "OK", exitOK},
		{61 * time.Second, "WARN", exitWarnings},
		{-5 * time.Minute, "WARN", exitWarnings},
	} {
		h := fullHost(t)
		h.server.skew = c.skew
		lines, _, code := h.checkLines()
		assertCode(t, code, c.code)
		if l := line(t, lines, "clock.skew", "-"); l.status != c.status {
			t.Errorf("%v: %+v", c.skew, l)
		}
	}
}

func TestTheClockWarningNamesTheSkewAndTheFix(t *testing.T) {
	h := fullHost(t)
	h.server.skew = 300 * time.Second
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "clock.skew", "-"); !strings.Contains(l.message, "300s") || !strings.Contains(l.hint, "timedatectl set-ntp true") {
		t.Errorf("%+v", l)
	}
}

func TestTheServerIsNotAskedWithoutTheIdentityOfTheAgent(t *testing.T) {
	h := fullHost(t)
	h.write(h.cfg.TLS.KeyFile, string(h.certHolding(time.Hour).key), 0o600)
	h.checkLines()
	if h.server.count() != 0 {
		t.Errorf("the server was asked")
	}
}

func TestTheServerChecksInJSONHaveTheStatusesOfTheLines(t *testing.T) {
	h := fullHost(t)
	h.server.skew = 2 * time.Minute
	code, stdout, _ := h.check("--json")
	assertCode(t, code, exitWarnings)
	if !strings.Contains(stdout, `"check":"clock.skew","subject":null,"status":"warning"`) {
		t.Errorf("%s", stdout)
	}
}

func TestACAFileThatIsNotPEMIsReportedToTheProbeAsWhyTLSCannotBeBuilt(t *testing.T) {
	h := fullHost(t)
	h.write(h.cfg.TLS.CAFile, "not a certificate", 0o644)
	h.checkLines()
	if err := h.server.calls[0].TLSErr; err == nil || !strings.Contains(err.Error(), "tls.ca_file") {
		t.Errorf("%v", err)
	}
}

func TestACAFileThatIsMissingIsReportedToTheProbe(t *testing.T) {
	h := fullHost(t)
	ok(t, os.Remove(h.cfg.TLS.CAFile))
	h.checkLines()
	if err := h.server.calls[0].TLSErr; err == nil || !strings.Contains(err.Error(), "tls.ca_file") {
		t.Errorf("%v", err)
	}
}

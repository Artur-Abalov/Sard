// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"fmt"
	"math"
	"net"
	"time"
)

// The server checks (A8d-3, Ч14-Ч16). The facts come from the network
// (internal/serverprobe); what they mean is decided here.

// MaxClockSkew is how far the clock of the host may be from the server's
// before check warns (ЧВ1).
const MaxClockSkew = time.Minute

// FailKind is how a connection to the server failed.
type FailKind int

// The kinds of failure; the zero value is no failure.
const (
	FailNone FailKind = iota
	FailTimeout
	FailNoSuchHost
	FailRefused
	FailUnknownCA
	FailName
	FailClientCert
	FailOther
)

// Failure is a failed step of the connection; Detail is the error text of
// FailOther.
type Failure struct {
	Kind   FailKind
	Detail string
}

// AnswerKind is what the server said to the Probe call.
type AnswerKind int

// The answers; the zero value is no call made.
const (
	AnswerNone AnswerKind = iota
	AnswerAccepted
	AnswerRefused
	AnswerOlder
	AnswerTimeout
	AnswerFailed
)

// Answer is the outcome of Probe: for AnswerRefused the reason of the
// server (empty if it gave none), for AnswerFailed what went wrong.
type Answer struct {
	Kind   AnswerKind
	Reason string
	Detail string
}

// ServerFacts is everything the checks of the server learned in one run.
type ServerFacts struct {
	Address string
	// Timeout is the limit of each access (--connect-timeout).
	Timeout time.Duration
	Reach   Failure
	TLS     Failure
	Answer  Answer
	// Sent is the time of the host when Probe left, ServerTime what the
	// server said, RTT how long the call took.
	Sent       time.Time
	ServerTime time.Time
	RTT        time.Duration
}

func (f ServerFacts) host() string {
	host, _, err := net.SplitHostPort(f.Address)
	if err != nil {
		return f.Address
	}
	return host
}

func (f ServerFacts) silent() string {
	return fmt.Sprintf("%s did not answer within %s", f.Address, f.Timeout)
}

// ServerReachableLine is server.reachable: DNS and TCP to server.address.
func ServerReachableLine(f ServerFacts) Line {
	l := Line{Check: CheckServerReachable}
	switch f.Reach.Kind {
	case FailNone:
		l.Status, l.Message = OK, f.Address+" is reachable"
		return l
	case FailTimeout:
		l.Message = f.silent()
	case FailNoSuchHost:
		l.Message = fmt.Sprintf("cannot connect to %s: no such host", f.Address)
	case FailRefused:
		l.Message = fmt.Sprintf("cannot connect to %s: connection refused", f.Address)
	default:
		l.Message = fmt.Sprintf("cannot connect to %s: %s", f.Address, f.Reach.Detail)
	}
	l.Status, l.Hint = Error, "check server.address in the config and the network; see "+HelpURL(CheckServerReachable)
	return l
}

// ServerTLSLine is server.tls: the mTLS handshake.
func ServerTLSLine(f ServerFacts) Line {
	l := Line{Check: CheckServerTLS}
	l.Hint = "see " + HelpURL(CheckServerTLS)
	switch f.TLS.Kind {
	case FailNone:
		l.Status, l.Message, l.Hint = OK, "the server certificate is trusted and the server takes the certificate of this agent", ""
		return l
	case FailTimeout:
		l.Message = f.silent()
	case FailUnknownCA:
		l.Message = "the certificate of the server is not issued by the CA in tls.ca_file"
	case FailName:
		l.Message = fmt.Sprintf("the name in the certificate of the server does not match %s", f.host())
	case FailClientCert:
		l.Message, l.Hint = "the server did not accept the client certificate of this agent", enrollForce
	default:
		l.Message = fmt.Sprintf("TLS handshake with %s failed: %s", f.Address, f.TLS.Detail)
	}
	l.Status = Error
	return l
}

// ServerAcceptedLine is server.accepted (Ч15).
func ServerAcceptedLine(f ServerFacts) Line {
	l := Line{Check: CheckServerAccepted, Status: Error, Hint: "see " + HelpURL(CheckServerAccepted)}
	switch f.Answer.Kind {
	case AnswerAccepted:
		return Line{Check: CheckServerAccepted, Status: OK, Message: "the server accepts this agent"}
	case AnswerRefused:
		l.Message, l.Hint = "the server refuses this agent", enrollForce
		if f.Answer.Reason != "" {
			l.Message += ": " + f.Answer.Reason
		}
	case AnswerOlder:
		l.Status, l.Hint = Warn, "update the server to the version of this agent"
		l.Message = "the server is older than this agent; acceptance and clock were not checked"
	case AnswerTimeout:
		l.Message = fmt.Sprintf("the server did not answer the probe within %s", f.Timeout)
	default:
		l.Message = "the server could not tell whether it accepts this agent: " + f.Answer.Detail
		l.Hint = "try again in a moment; see " + HelpURL(CheckServerAccepted)
	}
	return l
}

// Skew is the server clock less the clock of the host at the moment the
// server answered, which is taken to be half a round trip after Probe left.
func (f ServerFacts) Skew() time.Duration {
	return f.ServerTime.Sub(f.Sent.Add(f.RTT / 2))
}

// ClockSkewLine is clock.skew (Ч16).
func ClockSkewLine(f ServerFacts) Line {
	if f.Answer.Kind != AnswerAccepted {
		return Line{Check: CheckClockSkew, Status: Skip, Message: "no time of the server: it did not answer the probe",
			Hint: fmt.Sprintf("fix %s first, then run `%s` again", CheckServerAccepted, checkAgain)}
	}
	skew := f.Skew()
	seconds := int(math.Round(math.Abs(skew.Seconds())))
	if skew.Abs() <= MaxClockSkew {
		return Line{Check: CheckClockSkew, Status: OK, Message: fmt.Sprintf("the clock of this host differs from the server's by %ds", seconds)}
	}
	side := "behind"
	if skew < 0 {
		side = "ahead of"
	}
	return Line{Check: CheckClockSkew, Status: Warn, Message: fmt.Sprintf("the clock of this host is %ds %s the server's", seconds, side),
		Hint: "turn on time synchronization: sudo timedatectl set-ntp true; see " + HelpURL(CheckClockSkew)}
}

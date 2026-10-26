// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build libpq

// The connection string of the plugin against the libpq of the host (S11): a
// server that asks for a weaker authentication than the binding allows gets
// no password. Needs psql of PostgreSQL 16 or newer on the PATH; run with
//
//	go test -tags libpq -run Libpq ./plugins/postgresql/
package postgresql

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/postgresql/pgclient"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// The authentication requests of the protocol of PostgreSQL.
const (
	authCleartext = 3
	authMD5       = 5
)

// fakeServer answers a start-up with one authentication request and records
// whether a password came back.
type fakeServer struct {
	ln   net.Listener
	auth uint32

	mu        sync.Mutex
	sessions  int
	passwords []string
}

func startFakeServer(t *testing.T, auth uint32) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{ln: ln, auth: auth}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.session(c)
	}
}

func (s *fakeServer) session(c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(c)
	s.mu.Lock()
	s.sessions++
	s.mu.Unlock()
	for {
		body, err := startup(r)
		if err != nil {
			return
		}
		code := binary.BigEndian.Uint32(body[:4])
		if code == 80877103 || code == 80877104 { // SSLRequest, GSSENCRequest: no
			_, _ = c.Write([]byte{'N'})
			continue
		}
		break
	}
	msg := []byte{'R', 0, 0, 0, 8, 0, 0, 0, byte(s.auth)}
	if s.auth == authMD5 {
		msg[4] = 12
		msg = append(msg, 1, 2, 3, 4)
	}
	_, _ = c.Write(msg)
	kind, err := r.ReadByte()
	if err != nil || kind != 'p' {
		return
	}
	var n uint32
	if binary.Read(r, binary.BigEndian, &n) != nil || n < 4 || n > 1<<16 {
		return
	}
	body := make([]byte, n-4)
	if _, err := io.ReadFull(r, body); err != nil {
		return
	}
	s.mu.Lock()
	s.passwords = append(s.passwords, strings.TrimRight(string(body), "\x00"))
	s.mu.Unlock()
}

// startup reads a message without a type byte.
func startup(r *bufio.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil || n < 8 || n > 1<<16 {
		return nil, io.ErrUnexpectedEOF
	}
	body := make([]byte, n-4)
	_, err := io.ReadFull(r, body)
	return body, err
}

func (s *fakeServer) seen() (int, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions, append([]string(nil), s.passwords...)
}

// psqlTo runs the psql of the host against the target as the plugin would.
func psqlTo(t *testing.T, to target) (string, error) {
	t.Helper()
	cmd := exec.Command("psql", "-X", "-w", "-A", "-t", "--dbname="+to.conninfo("app"), "-c", "select 1")
	cmd.Env = pgclient.Env(os.Environ(), []byte("hunter2-secret"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestLibpqSendsNoPasswordToAServerThatAsksForAWeakerMethod(t *testing.T) {
	for auth, words := range map[uint32]string{authMD5: "server requested a hashed password", authCleartext: "server requested a cleartext password"} {
		s := startFakeServer(t, auth)
		out, err := psqlTo(t, target{Host: "127.0.0.1", Port: s.port(), User: "backup", PasswordRef: "pg-app", TLSMode: tlsDisable})
		sessions, passwords := s.seen()
		if err == nil || !strings.Contains(out, words) || sessions == 0 || len(passwords) != 0 {
			t.Errorf("auth %d: err %v, %d sessions, passwords %q, output %s", auth, err, sessions, passwords, out)
		}
		if !strings.Contains(out+hintFor(out), "the password was not sent") {
			t.Errorf("auth %d: no hint for %q", auth, out)
		}
	}
}

func TestLibpqSendsThePasswordWhenTheBindingAllowsTheMethod(t *testing.T) {
	s := startFakeServer(t, authMD5)
	to := target{Host: "127.0.0.1", Port: s.port(), User: "backup", PasswordRef: "pg-app", TLSMode: tlsDisable,
		grant: sdk.Grant{Auth: []string{authSCRAM, "md5"}}}
	_, _ = psqlTo(t, to)
	if _, passwords := s.seen(); len(passwords) != 1 || !strings.HasPrefix(passwords[0], "md5") {
		t.Errorf("passwords %q, want the md5 answer", passwords)
	}
}

func TestLibpqOnASocketTakesTheServersMethodUnlessTheBindingSaysOtherwise(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("unix", dir+"/.s.PGSQL.5432")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{ln: ln, auth: authMD5}
	go s.serve()
	defer func() { _ = ln.Close() }()
	_, _ = psqlTo(t, target{Host: dir, Port: 5432, User: "backup", PasswordRef: "pg-app"})
	if _, passwords := s.seen(); len(passwords) != 1 {
		t.Errorf("socket: passwords %q, want one", passwords)
	}
	out, _ := psqlTo(t, target{Host: dir, Port: 5432, User: "backup", PasswordRef: "pg-app", grant: sdk.Grant{Auth: []string{authSCRAM}}})
	if _, passwords := s.seen(); len(passwords) != 1 || !strings.Contains(out, "hashed password") {
		t.Errorf("socket bound to SCRAM: passwords %q, output %s", passwords, out)
	}
}

func TestLibpqTakesTheConnectionStringOfAServerOnTheNetwork(t *testing.T) {
	s := startFakeServer(t, authMD5)
	to := target{Host: "127.0.0.1", Port: s.port(), User: "backup", PasswordRef: "pg-app", TLSMode: tlsVerifyFull}
	if c := to.conninfo("app"); !strings.Contains(c, "sslrootcert='system'") || !strings.Contains(c, "channel_binding='require'") {
		t.Fatalf("conninfo %s", c)
	}
	out, err := psqlTo(t, to)
	if _, passwords := s.seen(); err == nil || !strings.Contains(out, "server does not support SSL") || strings.Contains(out, "invalid") || len(passwords) != 0 {
		t.Errorf("err %v, passwords %q, output %s", err, passwords, out)
	}
}

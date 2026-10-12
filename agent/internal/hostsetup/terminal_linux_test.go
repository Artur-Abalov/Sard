// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build linux

package hostsetup_test

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// openPTY is a pseudo-terminal: master (what the "operator" types on) and
// slave (what the command reads).
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	var zero, n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&zero))); e != 0 {
		t.Fatal(e)
	}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		t.Fatal(e)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

func echoOn(t *testing.T, f *os.File) bool {
	t.Helper()
	var tio syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&tio))); e != 0 {
		t.Fatal(e)
	}
	return tio.Lflag&syscall.ECHO != 0
}

// promptSpy is the command's output: when the prompt arrives the terminal
// must already be quiet; the "operator" then types the line.
type promptSpy struct {
	t          *testing.T
	slave      *os.File
	master     *os.File
	typed      string
	echoAtAsk  []bool
	promptSeen bytes.Buffer
}

func (p *promptSpy) Write(b []byte) (int, error) {
	p.promptSeen.Write(b)
	p.echoAtAsk = append(p.echoAtAsk, echoOn(p.t, p.slave))
	_, err := p.master.WriteString(p.typed + "\n")
	return len(b), err
}

func TestTerminalIsReadWithoutEchoAndPutBack(t *testing.T) {
	master, slave := openPTY(t)
	spy := &promptSpy{t: t, slave: slave, master: master, typed: "SECRET-MARKER"}
	term := hostsetup.StdinTerminal(slave, spy)
	if term == nil {
		t.Fatal("a terminal was not recognised")
	}
	got, err := term.ReadSecret("Value: ")
	if err != nil || string(got) != "SECRET-MARKER" {
		t.Fatalf("got %q, err %v", got, err)
	}
	if len(spy.echoAtAsk) != 1 || spy.echoAtAsk[0] {
		t.Fatalf("echo while asking: %v", spy.echoAtAsk)
	}
	if !echoOn(t, slave) {
		t.Fatal("the echo was not put back")
	}
	if !strings.Contains(spy.promptSeen.String(), "Value: ") {
		t.Fatalf("prompt %q", spy.promptSeen.String())
	}
}

// Only the line break of what the operator types is echoed, so that the
// next line of output starts on a fresh line; the secret itself never is.
func TestOnlyTheLineBreakOfTheSecretIsEchoed(t *testing.T) {
	master, slave := openPTY(t)
	spy := &promptSpy{t: t, slave: slave, master: master, typed: "SECRET-MARKER"}
	got, err := hostsetup.StdinTerminal(slave, spy).ReadSecret("Value: ")
	if err != nil || string(got) != "SECRET-MARKER" {
		t.Fatalf("got %q, err %v", got, err)
	}
	ok(t, master.SetReadDeadline(time.Now().Add(5*time.Second)))
	buf := make([]byte, 64)
	n, err := master.Read(buf)
	if err != nil || string(buf[:n]) != "\r\n" {
		t.Fatalf("echoed %q, err %v", buf[:n], err)
	}
}

func TestAFreshTerminalEchoes(t *testing.T) {
	_, slave := openPTY(t)
	if !echoOn(t, slave) {
		t.Fatal("the fixture terminal does not echo: the test above proves nothing")
	}
}

func TestTerminalReadThatFailsIsReported(t *testing.T) {
	master, slave := openPTY(t)
	term := hostsetup.TTY{In: slave, Out: hangUp{master}}
	if _, err := term.ReadSecret("x"); err == nil {
		t.Fatal("a read from a hung up terminal succeeded")
	}
}

// hangUp closes the master side once the prompt is written.
type hangUp struct{ master *os.File }

func (h hangUp) Write(b []byte) (int, error) {
	_ = h.master.Close()
	return len(b), nil
}

func TestAFileThatIsNotATerminalIsNotOne(t *testing.T) {
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if hostsetup.StdinTerminal(f, nil) != nil {
		t.Fatal("a directory was taken for a terminal")
	}
}

func TestTerminalThatCannotBeConfiguredIsReported(t *testing.T) {
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := (hostsetup.TTY{In: f, Out: &bytes.Buffer{}}).ReadSecret("x"); err == nil {
		t.Fatal("no error for a file that is not a terminal")
	}
}

// Р40: the confirmation of a host key is read with the echo on, so that
// the operator sees what they answer; the line comes back without its break.
func TestALineAskedAtTheTerminalIsReadWithTheEchoOn(t *testing.T) {
	master, slave := openPTY(t)
	spy := &promptSpy{t: t, slave: slave, master: master, typed: "yes"}
	term := hostsetup.StdinTerminal(slave, spy)
	prompter, isPrompter := term.(hostsetup.LinePrompter)
	if !isPrompter {
		t.Fatal("the terminal cannot ask a line")
	}
	got, err := prompter.ReadLine("Trust it? ")
	if err != nil || string(got) != "yes" {
		t.Fatalf("got %q, %v", got, err)
	}
	if len(spy.echoAtAsk) != 1 || !spy.echoAtAsk[0] {
		t.Fatalf("echo at the prompt: %v", spy.echoAtAsk)
	}
	if !strings.Contains(spy.promptSeen.String(), "Trust it? ") {
		t.Fatalf("prompt %q", spy.promptSeen.String())
	}
}

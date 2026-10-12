// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build linux

package hostsetup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"
)

// TTY is the operator's terminal, read without echo. The echo is turned
// off with the ioctl of the standard library's syscall package: no
// dependency for it (ADR 0027, CLAUDE.md rule 7).
type TTY struct {
	// In is the terminal; Out receives the prompts.
	In  *os.File
	Out io.Writer
}

// StdinTerminal is the terminal of f, or nil when f is not a terminal.
func StdinTerminal(f *os.File, out io.Writer) Terminal {
	if _, err := tcget(f.Fd()); err != nil {
		return nil
	}
	return TTY{In: f, Out: out}
}

func tcget(fd uintptr) (*syscall.Termios, error) {
	var t syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&t))); errno != 0 {
		return nil, errno
	}
	return &t, nil
}

func tcset(fd uintptr, t *syscall.Termios) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(t))); errno != 0 {
		return errno
	}
	return nil
}

// ReadSecret prints the prompt, reads a line with the echo off and puts
// the terminal back as it was, however the read ends.
func (t TTY) ReadSecret(prompt string) (value []byte, err error) {
	fd := t.In.Fd()
	saved, err := tcget(fd)
	if err != nil {
		return nil, err
	}
	quiet := *saved
	quiet.Lflag &^= syscall.ECHO
	quiet.Lflag |= syscall.ECHONL
	if err := tcset(fd, &quiet); err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, tcset(fd, saved)) }()
	_, _ = fmt.Fprint(t.Out, prompt)
	return readLine(t.In)
}

// ReadLine prints the prompt and reads a line with the echo on: for an
// answer the operator should see, such as the confirmation of a host key.
func (t TTY) ReadLine(prompt string) ([]byte, error) {
	_, _ = fmt.Fprint(t.Out, prompt)
	return readLine(t.In)
}

// readLine reads up to the line break, one byte at a time: nothing past
// the line is consumed.
// It stops one byte past MaxSecretSize: more is refused anyway, and a
// stream that never breaks its line must not fill the memory.
func readLine(r io.Reader) ([]byte, error) {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n == 1 {
			var done bool
			if line, done = take(line, b[0]); done {
				return line, nil
			}
			continue
		}
		if err != nil {
			return endOfInput(line, err)
		}
	}
}

// take adds a byte to the line; done is true at the line break and when
// the line is longer than a secret may be.
func take(line []byte, c byte) (_ []byte, done bool) {
	if c == '\n' {
		return line, true
	}
	line = append(line, c)
	return line, len(line) > MaxSecretSize
}

// endOfInput: a last line without its break is a line; nothing at all is
// the error.
func endOfInput(line []byte, err error) ([]byte, error) {
	if len(line) > 0 && errors.Is(err, io.EOF) {
		return line, nil
	}
	return nil, err
}

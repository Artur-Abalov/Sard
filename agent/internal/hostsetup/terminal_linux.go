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

// readLine reads up to the line break, one byte at a time: nothing past
// the line is consumed.
func readLine(r io.Reader) ([]byte, error) {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		switch {
		case n == 1 && b[0] == '\n':
			return line, nil
		case n == 1:
			line = append(line, b[0])
		case err != nil:
			return endOfInput(line, err)
		}
	}
}

// endOfInput: a last line without its break is a line; nothing at all is
// the error.
func endOfInput(line []byte, err error) ([]byte, error) {
	if len(line) > 0 && errors.Is(err, io.EOF) {
		return line, nil
	}
	return nil, err
}

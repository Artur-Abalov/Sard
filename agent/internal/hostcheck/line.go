// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// Status is the outcome of one check (Ч1, Ч2).
type Status int

// The statuses; the zero Status is none.
const (
	OK Status = iota + 1
	Warn
	Error
	Skip
)

// Label is the status as the text output shows it.
func (s Status) Label() string {
	return [...]string{"", "OK", "WARN", "ERROR", "SKIP"}[s]
}

// jsonName is the status as the JSON output shows it (Ч26).
func (s Status) jsonName() string {
	return [...]string{"", "ok", "warning", "error", "skipped"}[s]
}

// Line is one check: the stable identifier, the subject (a repository, a
// secret or a plugin; empty for a check of the host), the status, what was
// found and, for anything but OK, what to do.
type Line struct {
	Check   string
	Subject string
	Status  Status
	Message string
	Hint    string
}

// Tally counts the statuses of the lines printed.
type Tally struct{ OK, Warnings, Errors, Skipped int }

func (t *Tally) add(s Status) {
	switch s {
	case OK:
		t.OK++
	case Warn:
		t.Warnings++
	case Error:
		t.Errors++
	case Skip:
		t.Skipped++
	}
}

// Summary is the last line of the text output (Ч1).
func (t Tally) Summary() string {
	return fmt.Sprintf("summary: %d ok, %d warnings, %d errors, %d skipped", t.OK, t.Warnings, t.Errors, t.Skipped)
}

// Printer prints the lines as they come, in text or as JSON Lines, and
// counts them.
type Printer struct {
	Tally
	w      io.Writer
	asJSON bool
}

// NewPrinter prints to w; asJSON picks JSON Lines.
func NewPrinter(w io.Writer, asJSON bool) *Printer {
	return &Printer{w: w, asJSON: asJSON}
}

// Worst is the status that decides the exit code (Ч9): Error, else Warn,
// else OK. Skipped lines do not count.
func (p *Printer) Worst() Status {
	switch {
	case p.Errors > 0:
		return Error
	case p.Warnings > 0:
		return Warn
	}
	return OK
}

// Print prints one line and its hint.
func (p *Printer) Print(l Line) {
	p.add(l.Status)
	l.Message, l.Hint = oneLine(l.Message), oneLine(l.Hint)
	if p.asJSON {
		p.printJSON(l)
		return
	}
	subject := l.Subject
	if subject == "" {
		subject = "-"
	}
	_, _ = fmt.Fprintf(p.w, "%s %s %s %s\n", l.Status.Label(), l.Check, subject, l.Message)
	if l.Hint != "" {
		_, _ = fmt.Fprintf(p.w, "hint: %s\n", l.Hint)
	}
}

// Finish prints the summary of the text output; JSON Lines have none.
func (p *Printer) Finish() {
	if !p.asJSON {
		_, _ = fmt.Fprintln(p.w, p.Summary())
	}
}

func (p *Printer) printJSON(l Line) {
	data, _ := json.Marshal(struct {
		Check   string  `json:"check"`
		Subject *string `json:"subject"`
		Status  string  `json:"status"`
		Message string  `json:"message"`
		Hint    *string `json:"hint"`
	}{l.Check, nullIfEmpty(l.Subject), l.Status.jsonName(), l.Message, nullIfEmpty(l.Hint)})
	_, _ = p.w.Write(append(data, '\n'))
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// oneLine keeps a text, which may come from restic, on one output line and
// harmless to a terminal: control characters are written as Go does ("\n" is
// two characters), the same as in the refusals of the transport.
func oneLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// HelpURL is the section of the operator's troubleshooting guide for a check
// (Ч25): the anchor is the identifier without its dots.
func HelpURL(check string) string {
	return "https://github.com/Artur-Abalov/sard/blob/main/docs/operator/09-troubleshooting.md#" + strings.ReplaceAll(check, ".", "")
}

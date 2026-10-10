// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect

import "strings"

// ManagedBlock is the block of ~/.ssh/config for a host (Р41): ssh asks
// nothing at a terminal, trusts only known_hosts, uses only the key of the
// service user and drops a connection that went silent after a minute.
func ManagedBlock(host string) string {
	return "# sard-agent begin " + host + "\n" +
		"Host " + host + "\n" +
		"    StrictHostKeyChecking yes\n" +
		"    BatchMode yes\n" +
		"    IdentityFile ~/.ssh/id_ed25519\n" +
		"    IdentitiesOnly yes\n" +
		"    ServerAliveInterval 15\n" +
		"    ServerAliveCountMax 4\n" +
		"    ConnectTimeout 30\n" +
		"# sard-agent end " + host + "\n"
}

// ApplyBlock puts the managed block of the host into the config: the same
// block leaves it as it is, another block of the host is replaced where it
// stands, no block is put before everything else (ssh takes the first
// value of an option, so a "Host *" below cannot undo it). The rest stays
// byte for byte. changed says whether the content differs.
func ApplyBlock(content []byte, host string) (out []byte, changed bool) {
	block := ManagedBlock(host)
	text := string(content)
	begin, end := blockBounds(text, host)
	switch {
	case begin < 0 && text == "":
		return []byte(block), true
	case begin < 0:
		return []byte(block + "\n" + text), true
	case text[begin:end] == block:
		return content, false
	}
	return []byte(text[:begin] + block + text[end:]), true
}

// blockBounds are the offsets of the managed block of the host in text,
// from its begin line to the end of its end line; -1 if there is none.
func blockBounds(text, host string) (begin, end int) {
	beginLine, endLine := "# sard-agent begin "+host+"\n", "# sard-agent end "+host+"\n"
	start := indexOfLine(text, beginLine)
	if start < 0 {
		return -1, -1
	}
	stop := indexOfLine(text[start:], endLine)
	if stop < 0 {
		return -1, -1
	}
	return start, start + stop + len(endLine)
}

// indexOfLine is the offset of the first line of text that is exactly line
// (with its line break), -1 if none is.
func indexOfLine(text, line string) int {
	offset := 0
	for l := range strings.Lines(text) {
		if l == line {
			return offset
		}
		offset += len(l)
	}
	return -1
}

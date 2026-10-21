// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// The fake of restic key (V3): the keys of a repository, the calls that add
// and remove them, and the failures a scenario injects.

// fakeKey is a key of a repository; password is what opens it.
type fakeKey struct {
	id, user, host, password string
	created                  time.Time
}

// keyAdd is one call of restic key add: what the new key was made with and
// the arguments of the call.
type keyAdd struct {
	user, host, password string
	args                 []string
}

// fakeKeyring is the keys of a repository behind the fake restic.
type fakeKeyring struct {
	keys    []fakeKey
	adds    []keyAdd
	removes []string
	// fail maps the operation (list, add, remove) to the stderr line of a
	// restic that exits with 1; hang is the operation that never ends.
	fail map[string]string
	hang string
	// nonJSON makes key list print what is not JSON.
	nonJSON bool
	// listed counts the calls of key list.
	listed int
	// entered, if not nil, gets a signal when a call of the hang operation starts.
	entered chan struct{}
	// onAdd, if not nil, runs when restic key add starts: what the host looks
	// like at that moment.
	onAdd func()
}

// keyID is the 64-character id of the key called name in the tests.
func keyID(name string) string {
	sum := sha256.Sum256([]byte("key-" + name))
	return hex.EncodeToString(sum[:])
}

// at is a moment of the tests, minutes after 2026-10-20T10:00:00Z.
func at(minutes int) time.Time {
	return time.Date(2026, 10, 20, 10, minutes, 0, 0, time.UTC)
}

// The keys of the conventions of restic-keys.feature: H opens the repository
// base from this host, R is its recovery key, V a verifier's key.
func keyH() fakeKey {
	return fakeKey{id: keyID("H"), user: "sard-agent", host: "src1", password: passMarker, created: at(-60)}
}

func keyR() fakeKey {
	return fakeKey{id: keyID("R"), user: "sard:recovery", host: "src1", password: "R-PASSWORD", created: at(-30)}
}

func keyV() fakeKey {
	return fakeKey{id: keyID("V"), user: "sard:verifier:vrf-1", host: "src1", password: "V-PASSWORD", created: at(0)}
}

// idOf is the first 8 characters of the id, as key list prints them.
func idOf(k fakeKey) string { return k.id[:8] }

func (f *fakeRestic) key(ctx context.Context, cmd restic.Command, r *fakeRepo) (int, error) {
	op := cmd.Args[1]
	kr := &r.keyring
	f.mu.Lock()
	if op == "add" {
		kr.adds = append(kr.adds, f.parseKeyAdd(cmd))
	}
	f.mu.Unlock()
	if op == "add" && kr.onAdd != nil {
		kr.onAdd()
	}
	if code, done := f.keyRefusal(ctx, cmd, r, op); done {
		return code, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch op {
	case "list":
		return kr.list(f, cmd)
	case "add":
		return kr.add()
	}
	return kr.remove(cmd)
}

// keyRefusal is a restic key that does not do its work: it hangs, fails as the
// scenario says, or does not accept the password.
func (f *fakeRestic) keyRefusal(ctx context.Context, cmd restic.Command, r *fakeRepo, op string) (int, bool) {
	kr := &r.keyring
	switch {
	case kr.hang == op:
		if kr.entered != nil {
			kr.entered <- struct{}{}
		}
		code, _ := f.hang(ctx, cmd, r, "")
		return code, true
	case kr.fail[op] != "":
		cmd.Stderr([]byte(kr.fail[op]))
		return 1, true
	case r.wrongPassword || (r.password != "" && r.password != passwordOf(cmd)):
		lines(cmd.Stderr, golden(f.t, "wrong-password.stderr"))
		return 12, true
	}
	return 0, false
}

func (kr *fakeKeyring) list(f *fakeRestic, cmd restic.Command) (int, error) {
	kr.listed++
	if kr.nonJSON {
		cmd.Stdout([]byte("this is not json"))
		return 0, nil
	}
	current := passwordOf(cmd)
	type entry struct {
		Current  bool      `json:"current"`
		ID       string    `json:"id"`
		UserName string    `json:"userName"`
		HostName string    `json:"hostName"`
		Created  time.Time `json:"created"`
	}
	out := []entry{}
	for _, k := range kr.keys {
		out = append(out, entry{current != "" && k.password == current, k.id, k.user, k.host, k.created})
	}
	data, err := json.Marshal(out)
	if err != nil {
		f.t.Fatal(err)
	}
	cmd.Stdout(data)
	return 0, nil
}

// parseKeyAdd reads the call of restic key add, and the password from the
// descriptor --new-password-file names.
func (f *fakeRestic) parseKeyAdd(cmd restic.Command) keyAdd {
	call := keyAdd{args: slices.Clone(cmd.Args), user: flagValue(cmd.Args, "--user"), host: flagValue(cmd.Args, "--host")}
	if file := flagValue(cmd.Args, "--new-password-file"); file != "" {
		call.password = f.readInherited(cmd, file)
	}
	return call
}

// flagValue is the argument after the flag, "" if there is none.
func flagValue(args []string, flag string) string {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

// readInherited reads the descriptor "/proc/self/fd/N" names, which in the
// fake is the Nth inherited file of the command.
func (f *fakeRestic) readInherited(cmd restic.Command, path string) string {
	var n int
	if _, err := fmt.Sscanf(path, "/proc/self/fd/%d", &n); err != nil || n-3 >= len(cmd.ExtraFiles) {
		f.t.Fatalf("restic key add: --new-password-file %q is not an inherited descriptor", path)
	}
	data, err := io.ReadAll(cmd.ExtraFiles[n-3])
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.TrimRight(string(data), "\r\n")
}

// add makes the key of the call parseKeyAdd recorded last.
func (kr *fakeKeyring) add() (int, error) {
	call := kr.adds[len(kr.adds)-1]
	sum := sha256.Sum256([]byte("added-" + call.user + call.host + call.password))
	kr.keys = append(kr.keys, fakeKey{id: hex.EncodeToString(sum[:]), user: call.user, host: call.host, password: call.password, created: at(10 + len(kr.adds))})
	return 0, nil
}

func (kr *fakeKeyring) remove(cmd restic.Command) (int, error) {
	id := cmd.Args[len(cmd.Args)-1]
	kr.removes = append(kr.removes, id)
	kr.keys = slices.DeleteFunc(kr.keys, func(k fakeKey) bool { return k.id == id })
	return 0, nil
}

// lastAdded is the key restic key add made last.
func (r *fakeRepo) lastAdded() keyAdd {
	if len(r.keyring.adds) == 0 {
		return keyAdd{}
	}
	return r.keyring.adds[len(r.keyring.adds)-1]
}

// lastAddedOrCall is the key restic key add made last, or the one a refused
// call tried to make: the password is known to the fake either way.
func (r *fakeRepo) lastAddedOrCall() keyAdd { return r.lastAdded() }

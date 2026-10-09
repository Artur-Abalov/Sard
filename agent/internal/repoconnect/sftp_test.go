// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
)

// fakeClient is the OpenSSH client: it records what it is asked and
// answers from a script; ssh-keygen really writes the key files, as the
// service user would.
type fakeClient struct {
	home  string
	calls []clientCall
	// keyscan, login and keygen answer; nil: the defaults.
	keyscan, login func(args []string) (repoconnect.Output, error)
	keygenFails    string
	noPub          bool
	hang           map[string]chan struct{}
	started        chan string
}

type clientCall struct {
	program string
	args    []string
}

func (c *fakeClient) Run(ctx context.Context, program string, args []string) (repoconnect.Output, error) {
	c.calls = append(c.calls, clientCall{program, args})
	if wait := c.hang[program]; wait != nil {
		c.started <- program
		select {
		case <-ctx.Done():
			return repoconnect.Output{Code: -1}, ctx.Err()
		case <-wait:
		}
	}
	switch program {
	case repoconnect.ProgKeyscan:
		return answer(c.keyscan, args, repoconnect.Output{Stdout: line(nasHost, edKey) + line(nasHost, ecKey)})
	case repoconnect.ProgSFTP:
		return answer(c.login, args, repoconnect.Output{})
	}
	return c.keygen(args)
}

func answer(script func([]string) (repoconnect.Output, error), args []string, def repoconnect.Output) (repoconnect.Output, error) {
	if script != nil {
		return script(args)
	}
	return def, nil
}

func (c *fakeClient) keygen(args []string) (repoconnect.Output, error) {
	if c.keygenFails != "" {
		return repoconnect.Output{Code: 1, Stderr: c.keygenFails}, nil
	}
	if slices.Contains(args, "-y") {
		return repoconnect.Output{Stdout: "ssh-ed25519 AAAAderived sard-agent@host1\n"}, nil
	}
	priv := args[slices.Index(args, "-f")+1]
	if err := os.WriteFile(priv, []byte("PRIVATE"), 0o600); err != nil {
		return repoconnect.Output{}, err
	}
	if !c.noPub {
		if err := os.WriteFile(priv+".pub", []byte("ssh-ed25519 AAAAnew sard-agent@host1\n"), 0o644); err != nil {
			return repoconnect.Output{}, err
		}
	}
	return repoconnect.Output{}, nil
}

func (c *fakeClient) programs() []string {
	var names []string
	for _, call := range c.calls {
		names = append(names, call.program)
	}
	return names
}

// latchClock gives every timer its own channel and fires the latest one:
// a timer of a call that ended cannot take the firing meant for a later one.
type latchClock struct {
	mu     sync.Mutex
	timers []chan time.Time
}

func (c *latchClock) After(time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, ch)
	return ch
}

func (c *latchClock) fireLast() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timers[len(c.timers)-1] <- time.Now()
}

// sftpWorld is a service user's home, the client, and a command to run.
type sftpWorld struct {
	t       *testing.T
	home    string
	ssh     string
	client  *fakeClient
	audit   []string
	asked   []string
	answers []string
	sftp    *repoconnect.SFTP
	clock   *latchClock
}

func newSFTPWorld(t *testing.T, address string) *sftpWorld {
	t.Helper()
	home := t.TempDir()
	w := &sftpWorld{t: t, home: home, ssh: filepath.Join(home, ".ssh"), client: &fakeClient{home: home}, clock: &latchClock{}}
	if err := os.Mkdir(w.ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	a, f := hostsetup.CheckSFTPAddress(address)
	if f != nil {
		t.Fatal(f)
	}
	w.sftp = &repoconnect.SFTP{
		Address:  a,
		Service:  hostsetup.User{Name: "sard-agent", UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Home: home},
		Runner:   w.client,
		Bound:    repoconnect.Bound{Clock: w.clock, Timeout: 30 * time.Second},
		Hostname: func() (string, error) { return "host1", nil },
		Audit:    func(kind, name, action string) { w.audit = append(w.audit, kind+" "+name+" "+action) },
		Scrub:    func(s string) string { return strings.ReplaceAll(s, "SECRET", "[x]") },
	}
	return w
}

const nasAddress = "sftp:backup@nas.example.com:/srv/extra"

func (w *sftpWorld) put(name, content string) {
	w.t.Helper()
	if err := os.WriteFile(filepath.Join(w.ssh, name), []byte(content), 0o600); err != nil {
		w.t.Fatal(err)
	}
}

func (w *sftpWorld) read(name string) string {
	w.t.Helper()
	data, err := os.ReadFile(filepath.Join(w.ssh, name))
	if err != nil {
		return "<absent>"
	}
	return string(data)
}

// terminal makes the operator answer with answers.
func (w *sftpWorld) terminal(answers ...string) {
	w.answers = answers
	w.sftp.Confirm = func(prompt string) (string, error) {
		w.asked = append(w.asked, prompt)
		if len(w.answers) == 0 {
			return "", errors.New("end of input")
		}
		a := w.answers[0]
		w.answers = w.answers[1:]
		return a, nil
	}
}

func (w *sftpWorld) prepare() (repoconnect.SFTPResult, *refusal.Failure) {
	w.t.Helper()
	home, f := hostsetup.OpenSSHHome(hostsetup.OS{}, w.sftp.Service)
	if f != nil {
		w.t.Fatal(f)
	}
	w.t.Cleanup(home.Close)
	w.sftp.Home = home
	return w.sftp.Prepare(w.t.Context())
}

// ready is a world with the host key known, the key and the block in place.
func (w *sftpWorld) ready() {
	w.put("known_hosts", line(nasHost, edKey))
	w.put("id_ed25519", "PRIVATE")
	w.put("id_ed25519.pub", "ssh-ed25519 AAAAold sard-agent@host1\n")
	w.put("config", repoconnect.ManagedBlock(nasHost))
}

func assertFail(t *testing.T, f *refusal.Failure, reason refusal.Reason, class refusal.Class, mentions ...string) {
	t.Helper()
	if f == nil || f.Reason != reason || f.Class != class {
		t.Fatalf("failure %+v, want %s class %d", f, reason, class)
	}
	for _, m := range mentions {
		if !strings.Contains(f.Detail, m) {
			t.Errorf("%s does not mention %q: %s", reason, m, f.Detail)
		}
	}
}

// Р45: a host known, a key present and the block in place need no change.
func TestAKnownHostWithAKeyAndTheBlockInPlaceChangesNothingAndAsksNothing(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.ready()
	w.terminal()
	res, f := w.prepare()
	if f != nil || res.Changed || res.PublicKey != "ssh-ed25519 AAAAold sard-agent@host1" || res.HostKey != edKey {
		t.Fatalf("%+v %v", res, f)
	}
	if len(w.asked) != 0 || len(w.audit) != 0 {
		t.Fatalf("asked %q, audit %q", w.asked, w.audit)
	}
	if got := strings.Join(w.client.programs(), " "); got != "ssh-keyscan sftp" {
		t.Fatalf("programs: %s", got)
	}
}

func TestAnyKeyOfTheServerThatIsKnownMakesTheHostKnown(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.ready()
	w.put("known_hosts", line(nasHost, ecKey))
	res, f := w.prepare()
	if f != nil || res.Changed || res.HostKey != ecKey {
		t.Fatalf("%+v %v", res, f)
	}
}

// Р40: the fingerprint of the flag, of any key type.
func TestAFingerprintOfTheFlagThatMatchesAKeyOfAnyTypeTrustsThatKeyWithoutAQuestion(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.put("known_hosts", "other.example.com ssh-ed25519 AAAA\n")
	w.put("id_ed25519", "PRIVATE")
	w.put("id_ed25519.pub", "ssh-ed25519 AAAAold sard-agent@host1\n")
	w.sftp.Fingerprint = ecKey.Fingerprint()
	w.terminal()
	res, f := w.prepare()
	if f != nil || !res.Changed || res.HostKey != ecKey {
		t.Fatalf("%+v %v", res, f)
	}
	if want := "other.example.com ssh-ed25519 AAAA\n" + line(nasHost, ecKey); w.read("known_hosts") != want {
		t.Fatalf("known_hosts %q", w.read("known_hosts"))
	}
	wantAudit := "ssh host key of nas.example.com trusted ecdsa-sha2-nistp256 " + ecKey.Fingerprint()
	if len(w.audit) != 1 || w.audit[0] != wantAudit {
		t.Fatalf("audit %q", w.audit)
	}
	if len(w.asked) != 0 {
		t.Fatalf("asked %q", w.asked)
	}
	if w.read("config") != repoconnect.ManagedBlock(nasHost) {
		t.Fatalf("config %q", w.read("config"))
	}
}

func TestTheHostKeyOfAServerOnAnotherPortIsWrittenAsHostAndPort(t *testing.T) {
	w := newSFTPWorld(t, "sftp://backup@nas.example.com:2222//srv/extra")
	w.sftp.Fingerprint = edKey.Fingerprint()
	if _, f := w.prepare(); f != nil {
		t.Fatal(f)
	}
	if w.read("known_hosts") != line(nasPort, edKey) {
		t.Fatalf("known_hosts %q", w.read("known_hosts"))
	}
	if len(w.audit) == 0 || !strings.HasPrefix(w.audit[len(w.audit)-1], "ssh host key of nas.example.com:2222 trusted ssh-ed25519 ") {
		t.Fatalf("audit %q", w.audit)
	}
	scan := w.client.calls[0]
	if scan.program != repoconnect.ProgKeyscan || !slices.Contains(scan.args, "2222") || scan.args[len(scan.args)-1] != "nas.example.com" {
		t.Fatalf("keyscan %v", scan)
	}
}

func TestAFingerprintThatMatchesNoKeyIsAMismatchNamingAllFingerprintsAndWritesNothing(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.sftp.Fingerprint = otherKey.Fingerprint()
	w.terminal("yes")
	_, f := w.prepare()
	assertFail(t, f, refusal.HostKeyMismatch, refusal.ClassTrust, otherKey.Fingerprint(), edKey.Fingerprint(), ecKey.Fingerprint())
	w.assertNothingWritten()
	if len(w.asked) != 0 {
		t.Fatalf("asked %q", w.asked)
	}
}

func (w *sftpWorld) assertNothingWritten() {
	w.t.Helper()
	entries, _ := os.ReadDir(w.ssh)
	if len(entries) != 0 || len(w.audit) != 0 {
		w.t.Fatalf("written %v, audit %q", entries, w.audit)
	}
	if slices.Contains(w.client.programs(), repoconnect.ProgKeygen) {
		w.t.Fatal("ssh-keygen ran")
	}
}

func TestWithoutAFlagTheTerminalShowsOneKeyByPreferenceAndYesTrustsIt(t *testing.T) {
	for name, c := range map[string]struct {
		keys    string
		wantKey repoconnect.HostKey
		advice  string
	}{
		"ed25519 first":    {line(nasHost, rsaKey) + line(nasHost, ecKey) + line(nasHost, edKey), edKey, "ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub"},
		"ecdsa without ed": {line(nasHost, rsaKey) + line(nasHost, ecKey), ecKey, "ssh-keygen -lf /etc/ssh/ssh_host_ecdsa_key.pub"},
		"rsa alone":        {line(nasHost, rsaKey), rsaKey, "ssh-keygen -lf /etc/ssh/ssh_host_rsa_key.pub"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newSFTPWorld(t, nasAddress)
			w.client.keyscan = func([]string) (repoconnect.Output, error) { return repoconnect.Output{Stdout: c.keys}, nil }
			w.terminal("yes")
			res, f := w.prepare()
			if f != nil || res.HostKey != c.wantKey {
				t.Fatalf("%+v %v", res, f)
			}
			if len(w.asked) != 1 {
				t.Fatalf("asked %q", w.asked)
			}
			for _, want := range []string{"nas.example.com", "22", c.wantKey.Type, c.wantKey.Fingerprint(), c.advice} {
				if !strings.Contains(w.asked[0], want) {
					t.Errorf("the prompt lacks %q:\n%s", want, w.asked[0])
				}
			}
			if strings.Count(w.asked[0], "SHA256:") != 1 {
				t.Errorf("the prompt shows more than one key:\n%s", w.asked[0])
			}
			if !strings.Contains(w.read("known_hosts"), c.wantKey.Blob) {
				t.Errorf("known_hosts %q", w.read("known_hosts"))
			}
		})
	}
}

func TestAnAnswerOtherThanExactlyYesIsRejectedAndWritesNothing(t *testing.T) {
	for _, answer := range []string{"no", "y", "YES", "Yes", "", " yes", "yes ", "yes\n"} {
		w := newSFTPWorld(t, nasAddress)
		w.terminal(answer)
		_, f := w.prepare()
		assertFail(t, f, refusal.HostKeyRejected, refusal.ClassTrust)
		w.assertNothingWritten()
	}
	w := newSFTPWorld(t, nasAddress)
	w.terminal()
	_, f := w.prepare()
	assertFail(t, f, refusal.HostKeyRejected, refusal.ClassTrust)
	w.assertNothingWritten()
}

func TestWithoutATerminalAndWithoutAFlagTheHostKeyIsUnconfirmed(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	_, f := w.prepare()
	assertFail(t, f, refusal.HostKeyUnconfirmed, refusal.ClassUsage, edKey.Fingerprint(), ecKey.Fingerprint(), "--host-key-fingerprint")
	w.assertNothingWritten()
}

// Р40: the entries of the host that match no key are a change.
func TestAChangedHostKeyIsRefusedWithThePathAndTheLinesAndWritesNothing(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	old := "other.example.com ssh-ed25519 AAAA\n\n" + line(nasHost, oldEd)
	w.put("known_hosts", old)
	w.sftp.Fingerprint = edKey.Fingerprint()
	w.terminal("yes")
	_, f := w.prepare()
	assertFail(t, f, refusal.HostKeyChanged, refusal.ClassTrust, filepath.Join(w.ssh, "known_hosts"), "3", "--replace-host-key", "substitution", "reinstall")
	if w.read("known_hosts") != old || len(w.audit) != 0 || len(w.client.calls) != 1 {
		t.Fatalf("known_hosts %q, audit %q, calls %v", w.read("known_hosts"), w.audit, w.client.programs())
	}
}

func TestReplacingAChangedHostKeyStillNeedsTheConfirmation(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.put("known_hosts", line(nasHost, oldEd))
	w.sftp.Replace = true
	_, f := w.prepare()
	assertFail(t, f, refusal.HostKeyUnconfirmed, refusal.ClassUsage)
	if w.read("known_hosts") != line(nasHost, oldEd) {
		t.Fatalf("known_hosts %q", w.read("known_hosts"))
	}
}

func TestReplacingAChangedHostKeyDropsTheOldEntriesAndAuditsAReplacement(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.put("known_hosts", line(nasHost, oldEd)+line("other.example.com", rsaKey))
	w.sftp.Replace = true
	w.sftp.Fingerprint = edKey.Fingerprint()
	res, f := w.prepare()
	if f != nil || !res.Changed {
		t.Fatalf("%+v %v", res, f)
	}
	if want := line("other.example.com", rsaKey) + line(nasHost, edKey); w.read("known_hosts") != want {
		t.Fatalf("known_hosts %q", w.read("known_hosts"))
	}
	var replaced bool
	for _, a := range w.audit {
		replaced = replaced || a == "ssh host key of nas.example.com replaced ssh-ed25519 "+edKey.Fingerprint()
	}
	if !replaced {
		t.Fatalf("audit %q", w.audit)
	}
}

func TestTheFlagToReplaceChangesNothingWhenTheHostKeyIsKnown(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.ready()
	w.sftp.Replace = true
	res, f := w.prepare()
	if f != nil || res.Changed || len(w.audit) != 0 {
		t.Fatalf("%+v %v audit %q", res, f, w.audit)
	}
}

// Р40: ssh-keyscan.
func TestTheScanRunsWithThePortTheTimeoutAndTheHostAndNoKeysIsUnavailable(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.client.keyscan = func([]string) (repoconnect.Output, error) {
		return repoconnect.Output{Stderr: "getaddrinfo nas.example.com: Name or service not known"}, nil
	}
	_, f := w.prepare()
	assertFail(t, f, refusal.BackendUnavailable, refusal.ClassTemporary, "nas.example.com", "22", "Name or service not known")
	args := strings.Join(w.client.calls[0].args, " ")
	if args != "-T 30 -p 22 -t ed25519,ecdsa,rsa nas.example.com" {
		t.Fatalf("args %s", args)
	}
	w.assertNothingWritten()
}

func TestAScanThatFailsOrCannotStartIsAnErrorOfTheClient(t *testing.T) {
	for name, script := range map[string]func([]string) (repoconnect.Output, error){
		"exit 1": func([]string) (repoconnect.Output, error) {
			return repoconnect.Output{Code: 1, Stderr: "usage: ssh-keyscan SECRET"}, nil
		},
		"no program": func([]string) (repoconnect.Output, error) {
			return repoconnect.Output{Code: -1}, errors.New("fork/exec: no such file")
		},
	} {
		w := newSFTPWorld(t, nasAddress)
		w.client.keyscan = script
		_, f := w.prepare()
		assertFail(t, f, refusal.SSHClientFailed, refusal.ClassAgentError, "ssh-keyscan")
		if strings.Contains(f.Detail, "SECRET") {
			t.Errorf("%s: the message is not scrubbed: %s", name, f.Detail)
		}
		w.assertNothingWritten()
	}
}

func TestAScanThatDoesNotEndInTimeIsUnavailableWithTheHostAndPort(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.client.hang = map[string]chan struct{}{repoconnect.ProgKeyscan: make(chan struct{})}
	w.client.started = make(chan string, 1)
	done := make(chan *refusal.Failure, 1)
	go func() { _, f := w.prepare(); done <- f }()
	<-w.client.started
	w.clock.fireLast()
	f := <-done
	assertFail(t, f, refusal.BackendUnavailable, refusal.ClassTemporary, "nas.example.com", "22", "--connect-timeout", "30s")
	w.assertNothingWritten()
}

func TestACommandThatIsInterruptedDuringAProgramIsInterruptedNotAClientError(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.client.hang = map[string]chan struct{}{repoconnect.ProgKeyscan: make(chan struct{})}
	w.client.started = make(chan string, 1)
	ctx, cancel := context.WithCancel(t.Context())
	home, f := hostsetup.OpenSSHHome(hostsetup.OS{}, w.sftp.Service)
	if f != nil {
		t.Fatal(f)
	}
	defer home.Close()
	w.sftp.Home = home
	done := make(chan *refusal.Failure, 1)
	go func() { _, f := w.sftp.Prepare(ctx); done <- f }()
	<-w.client.started
	cancel()
	assertFail(t, <-done, refusal.Interrupted, refusal.ClassTemporary)
}

// Р39: the key is made by ssh-keygen as the service user, after the host
// key is trusted, and its public part is read back.
func TestWithoutAKeyOneIsMadeAfterTheHostKeyAndItsPublicPartIsReturned(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.sftp.Fingerprint = edKey.Fingerprint()
	res, f := w.prepare()
	if f != nil || !res.Changed || res.PublicKey != "ssh-ed25519 AAAAnew sard-agent@host1" {
		t.Fatalf("%+v %v", res, f)
	}
	var keygen clientCall
	for _, c := range w.client.calls {
		if c.program == repoconnect.ProgKeygen {
			keygen = c
		}
	}
	want := []string{"-q", "-t", "ed25519", "-N", "", "-C", "sard-agent@host1", "-f", filepath.Join(w.ssh, "id_ed25519")}
	if !slices.Equal(keygen.args, want) {
		t.Fatalf("ssh-keygen %q, want %q", keygen.args, want)
	}
	wantAudit := []string{"ssh key of service user sard-agent created", "ssh host key of nas.example.com trusted ssh-ed25519 " + edKey.Fingerprint()}
	if !slices.Equal(w.audit, wantAudit) {
		t.Fatalf("audit %q", w.audit)
	}
	if w.read("config") != repoconnect.ManagedBlock(nasHost) {
		t.Fatalf("config %q", w.read("config"))
	}
}

func TestAKeyThatCannotBeMadeIsAnErrorOfTheClientAndTheHostKeyIsNotWritten(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.sftp.Fingerprint = edKey.Fingerprint()
	w.client.keygenFails = "Saving key failed"
	_, f := w.prepare()
	assertFail(t, f, refusal.SSHClientFailed, refusal.ClassAgentError, "ssh-keygen", "Saving key failed")
	if w.read("known_hosts") != "<absent>" || len(w.audit) != 0 {
		t.Fatalf("known_hosts %q audit %q", w.read("known_hosts"), w.audit)
	}
}

func TestAKeyWhosePublicPartWasNotWrittenIsAnErrorOfTheClient(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.sftp.Fingerprint = edKey.Fingerprint()
	w.client.noPub = true
	_, f := w.prepare()
	assertFail(t, f, refusal.SSHClientFailed, refusal.ClassAgentError, "id_ed25519.pub")
}

func TestAPrivateKeyWithoutAPublicPartYieldsItFromTheKeygenWithoutWritingAFile(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.ready()
	if err := os.Remove(filepath.Join(w.ssh, "id_ed25519.pub")); err != nil {
		t.Fatal(err)
	}
	res, f := w.prepare()
	if f != nil || res.Changed || res.PublicKey != "ssh-ed25519 AAAAderived sard-agent@host1" {
		t.Fatalf("%+v %v", res, f)
	}
	if w.read("id_ed25519.pub") != "<absent>" {
		t.Fatal("the public part was written")
	}
	var args []string
	for _, c := range w.client.calls {
		if c.program == repoconnect.ProgKeygen {
			args = c.args
		}
	}
	if !slices.Equal(args, []string{"-y", "-f", filepath.Join(w.ssh, "id_ed25519")}) {
		t.Fatalf("ssh-keygen %q", args)
	}
}

func TestAPrivateKeyThatTheKeygenCannotReadIsAnErrorOfTheClient(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.ready()
	if err := os.Remove(filepath.Join(w.ssh, "id_ed25519.pub")); err != nil {
		t.Fatal(err)
	}
	w.client.keygenFails = "incorrect passphrase supplied to decrypt private key"
	_, f := w.prepare()
	assertFail(t, f, refusal.SSHClientFailed, refusal.ClassAgentError, "incorrect passphrase")
}

// Р43: the files written before a failed login stay, the key is shown.
func TestTheFilesWrittenBeforeAFailedLoginStayAndTheKeyIsReturned(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.sftp.Fingerprint = edKey.Fingerprint()
	w.client.login = func([]string) (repoconnect.Output, error) {
		return repoconnect.Output{Code: 255, Stderr: "backup@nas.example.com: Permission denied (publickey)."}, nil
	}
	res, f := w.prepare()
	assertFail(t, f, refusal.SSHKeyNotAuthorized, refusal.ClassUsage, "Permission denied (publickey)", "backup", "nas.example.com", "authorized_keys")
	if res.PublicKey != "ssh-ed25519 AAAAnew sard-agent@host1" || !res.Changed {
		t.Fatalf("%+v", res)
	}
	for _, name := range []string{"id_ed25519", "id_ed25519.pub", "known_hosts", "config"} {
		if w.read(name) == "<absent>" {
			t.Errorf("%s is gone", name)
		}
	}
	if len(w.audit) != 2 {
		t.Fatalf("audit %q", w.audit)
	}
}

// Р34: the class of a refusal of the login, from the stderr of ssh.
func TestTheStderrOfTheLoginSaysWhichClassOfRefusalItIs(t *testing.T) {
	for _, c := range []struct {
		stderr string
		reason refusal.Reason
		class  refusal.Class
		text   string
	}{
		{"backup@nas.example.com: Permission denied (publickey).", refusal.SSHKeyNotAuthorized, refusal.ClassUsage, "Permission denied (publickey)"},
		{"backup@nas.example.com: Permission denied (publickey,password).", refusal.SSHKeyNotAuthorized, refusal.ClassUsage, "Permission denied"},
		{"Host key verification failed.", refusal.HostKeyMismatch, refusal.ClassTrust, "Host key verification failed"},
		{"ssh: Could not resolve hostname nas.example.com: Name or service not known", refusal.BackendUnavailable, refusal.ClassTemporary, "Could not resolve hostname"},
		{"ssh: connect to host nas.example.com port 22: Connection refused", refusal.BackendUnavailable, refusal.ClassTemporary, "Connection refused"},
		{"ssh: connect to host nas.example.com port 22: Connection timed out", refusal.BackendUnavailable, refusal.ClassTemporary, "Connection timed out"},
		{"ssh: connect to host nas.example.com port 22: No route to host", refusal.BackendUnavailable, refusal.ClassTemporary, "No route to host"},
		{"ssh: connect to host nas.example.com port 22: Network is unreachable", refusal.BackendUnavailable, refusal.ClassTemporary, "Network is unreachable"},
		{"Connection closed by 192.0.2.1 port 22", refusal.BackendUnavailable, refusal.ClassTemporary, "Connection closed"},
		{"subsystem request failed on channel 0 SECRET", refusal.BackendRefused, refusal.ClassAgentError, "subsystem request failed on channel 0 [x]"},
	} {
		w := newSFTPWorld(t, nasAddress)
		w.ready()
		w.client.login = func([]string) (repoconnect.Output, error) {
			return repoconnect.Output{Code: 255, Stderr: c.stderr}, nil
		}
		_, f := w.prepare()
		assertFail(t, f, c.reason, c.class, c.text)
		if w.read("known_hosts") != line(nasHost, edKey) {
			t.Errorf("%s: known_hosts changed", c.stderr)
		}
	}
}

func TestALoginThatDoesNotEndInTimeIsUnavailable(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.ready()
	w.client.hang = map[string]chan struct{}{repoconnect.ProgSFTP: make(chan struct{})}
	w.client.started = make(chan string, 1)
	done := make(chan *refusal.Failure, 1)
	go func() { _, f := w.prepare(); done <- f }()
	<-w.client.started
	w.clock.fireLast()
	assertFail(t, <-done, refusal.BackendUnavailable, refusal.ClassTemporary, "nas.example.com", "--connect-timeout")
}

// Р41, Р47: every program of the client gets the options of the block
// on its command line too; none gets a secret.
func TestEveryNetworkProgramGetsStrictHostKeyCheckingBatchModeAndTheTimeout(t *testing.T) {
	w := newSFTPWorld(t, "sftp://backup@[2001:db8::1]:2222//srv/extra")
	w.sftp.Fingerprint = edKey.Fingerprint()
	w.put("id_ed25519", "PRIVATE")
	w.put("id_ed25519.pub", "ssh-ed25519 AAAAold x\n")
	w.client.keyscan = func([]string) (repoconnect.Output, error) {
		return repoconnect.Output{Stdout: line("[2001:db8::1]:2222", edKey)}, nil
	}
	if _, f := w.prepare(); f != nil {
		t.Fatal(f)
	}
	login := w.client.calls[len(w.client.calls)-1]
	want := []string{"-b", "/dev/null", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=30", "-P", "2222", "backup@[2001:db8::1]"}
	if login.program != repoconnect.ProgSFTP || !slices.Equal(login.args, want) {
		t.Fatalf("login %s %q, want %q", login.program, login.args, want)
	}
	if got := strings.Join(w.client.calls[0].args, " "); got != "-T 30 -p 2222 -t ed25519,ecdsa,rsa 2001:db8::1" {
		t.Fatalf("keyscan %s", got)
	}
}

func TestAConnectTimeoutOfAFractionOfASecondRoundsUpToASecond(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.ready()
	w.sftp.Bound.Timeout = 1500 * time.Millisecond
	if _, f := w.prepare(); f != nil {
		t.Fatal(f)
	}
	if got := strings.Join(w.client.calls[0].args, " "); !strings.HasPrefix(got, "-T 2 ") {
		t.Fatalf("keyscan %s", got)
	}
}

// Р41: the block of the host is written when it differs.
func TestTheManagedBlockIsWrittenBeforeTheFormerConfigOnlyWhenItDiffers(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	w.ready()
	w.put("config", "Host *\n    ServerAliveInterval 0\n")
	res, f := w.prepare()
	if f != nil || !res.Changed {
		t.Fatalf("%+v %v", res, f)
	}
	if want := repoconnect.ManagedBlock(nasHost) + "\nHost *\n    ServerAliveInterval 0\n"; w.read("config") != want {
		t.Fatalf("config %q", w.read("config"))
	}
	again, f := w.prepare()
	if f != nil || again.Changed {
		t.Fatalf("a repeat: %+v %v", again, f)
	}
}

// Р38: a file of ~/.ssh that cannot be read is refused before the server is asked.
func TestAFileOfTheSSHDirectoryThatIsRejectedStopsBeforeAnyProgramRuns(t *testing.T) {
	w := newSFTPWorld(t, nasAddress)
	if err := os.Symlink("/etc/passwd", filepath.Join(w.ssh, "known_hosts")); err != nil {
		t.Fatal(err)
	}
	_, f := w.prepare()
	assertFail(t, f, refusal.SSHFileRejected, refusal.ClassUsage, "known_hosts")
	if len(w.client.calls) != 0 {
		t.Fatalf("programs ran: %v", w.client.programs())
	}
}

func TestTheClientIsCheckedForEveryProgramInOrder(t *testing.T) {
	has := func(missing string) func(string) bool { return func(p string) bool { return p != missing } }
	if f := repoconnect.CheckClient(has("")); f != nil {
		t.Fatal(f)
	}
	for _, program := range []string{"ssh", "sftp", "ssh-keygen", "ssh-keyscan"} {
		f := repoconnect.CheckClient(has(program))
		assertFail(t, f, refusal.SSHClientMissing, refusal.ClassAgentError, program, "sudo apt-get install openssh-client", "sudo dnf install openssh-clients", "dpkg -i", "rpm -U")
	}
	none := func(string) bool { return false }
	if f := repoconnect.CheckClient(none); f == nil || !strings.Contains(f.Detail, "ssh ") && !strings.Contains(f.Detail, "ssh,") {
		t.Fatalf("%+v", f)
	}
}

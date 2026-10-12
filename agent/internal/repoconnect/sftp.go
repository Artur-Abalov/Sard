// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// The programs of the OpenSSH client that restic and repo add need (Р36).
const (
	ProgSSH     = "ssh"
	ProgSFTP    = "sftp"
	ProgKeygen  = "ssh-keygen"
	ProgKeyscan = "ssh-keyscan"
)

// Output is what a program of the client printed and its exit code.
type Output struct {
	Code           int
	Stdout, Stderr string
}

// Runner runs a program of the OpenSSH client as the service user, with a
// fixed environment that holds no secret, until it ends or ctx ends (the
// process gets SIGTERM, then SIGKILL). An error means it did not run.
type Runner interface {
	Run(ctx context.Context, program string, args []string) (Output, error)
}

// CheckClient: all four programs of the client are there (Р36). has says
// whether one is in the PATH restic gets. SSH_CLIENT_MISSING names the
// first that is not and how to install the client.
func CheckClient(has func(program string) bool) *refusal.Failure {
	for _, program := range []string{ProgSSH, ProgSFTP, ProgKeygen, ProgKeyscan} {
		if !has(program) {
			return refusal.Fail(refusal.SSHClientMissing,
				"the OpenSSH client program %s is not in the PATH of restic; install the client: sudo apt-get install openssh-client (Debian, Ubuntu) or sudo dnf install openssh-clients (RHEL family); a package installed with dpkg -i or rpm -U brings no dependencies, install the client yourself", program)
		}
	}
	return nil
}

// SFTP sets up the ssh side of an sftp: repository for the service user
// (Р37-Р43): the host key, the key of the service user, known_hosts, the
// block of ~/.ssh/config, and a check that the login is accepted.
type SFTP struct {
	// FS is the file system; ~/.ssh of the service user is opened from it,
	// and held by its descriptors for the run.
	FS      hostsetup.FS
	Address hostsetup.SFTPAddress
	Service hostsetup.User
	Runner  Runner
	// Bound limits every network call (Р33).
	Bound Bound
	// Fingerprint is --host-key-fingerprint; Replace is --replace-host-key.
	Fingerprint string
	Replace     bool
	// Confirm asks the operator at the terminal and returns the line they
	// typed; nil when there is no terminal.
	Confirm func(prompt string) (string, error)
	// Hostname is the name of this host, for the comment of a new key.
	Hostname func() (string, error)
	// Audit records a change: kind, name and action as hostsetup.Audit has them.
	Audit func(kind, name, action string)
	// Scrub removes secrets from text taken from the programs.
	Scrub func(string) string
}

// SFTPResult is what the setup found or made.
type SFTPResult struct {
	// PublicKey is the line of the public part of the key of the service
	// user; set whenever the key exists, even when the setup then refuses.
	PublicKey string
	// HostKey is the key of the server that is trusted.
	HostKey HostKey
	// Changed: something was written (the key, known_hosts, the config).
	Changed bool
	// Written names the files of ~/.ssh that were written, in order.
	Written []string
	// KeptPatterns are the lines of known_hosts with a pattern or a list
	// that match the host and were left when its key was replaced.
	KeptPatterns []int
}

const (
	privateKeyFile = "id_ed25519"
	publicKeyFile  = "id_ed25519.pub"
)

// sshRun is one run of the setup.
type sshRun struct {
	*SFTP
	// Home is ~/.ssh of the service user.
	Home *hostsetup.SSHHome
	res  SFTPResult
	// What ~/.ssh held when the run began.
	known, config []byte
	pub           string
	keyPresent    bool
}

// Prepare is the setup, in the order of Р42: the files of ~/.ssh are
// looked at, the server is scanned for its keys, the host key is settled,
// then the key, known_hosts and the config are written, then the login is
// tried. Nothing is written before the host key is trusted; what is
// written stays when a later step refuses (Р43).
func (s *SFTP) Prepare(ctx context.Context) (SFTPResult, *refusal.Failure) {
	home, f := hostsetup.OpenSSHHome(s.FS, s.Service)
	if f != nil {
		return SFTPResult{}, f
	}
	defer home.Close()
	r := &sshRun{SFTP: s, Home: home}
	if f := r.load(); f != nil {
		return r.res, f
	}
	keys, f := r.scan(ctx)
	if f != nil {
		return r.res, f
	}
	trust, f := r.decide(keys)
	if f != nil {
		return r.res, f
	}
	if f := r.write(ctx, trust); f != nil {
		return r.res, f
	}
	return r.res, r.login(ctx)
}

func (r *sshRun) scrub(text string) string {
	if r.Scrub == nil {
		return text
	}
	return r.Scrub(text)
}

// load reads what ~/.ssh holds; a file that may not be read is refused here.
func (r *sshRun) load() *refusal.Failure {
	var f *refusal.Failure
	if r.keyPresent, f = r.Home.Present(privateKeyFile); f != nil {
		return f
	}
	var pub []byte
	for _, file := range []struct {
		name string
		to   *[]byte
	}{{"known_hosts", &r.known}, {"config", &r.config}, {publicKeyFile, &pub}} {
		if *file.to, _, f = r.Home.Read(file.name); f != nil {
			return f
		}
	}
	r.pub = strings.TrimSpace(string(pub))
	return nil
}

// seconds is the connect timeout in whole seconds, rounded up; 0: none.
func (r *sshRun) seconds() int {
	return int(math.Ceil(r.Bound.Timeout.Seconds()))
}

// timeoutOption is the options that limit a connection to the timeout.
func (r *sshRun) timeoutOption(flag string) []string {
	if r.seconds() <= 0 {
		return nil
	}
	return []string{flag, strconv.Itoa(r.seconds())}
}

// where names the server in messages.
func (r *sshRun) where() string {
	return fmt.Sprintf("%s (port %d)", r.Address.Host, r.Address.Port)
}

// network runs a program that talks to the server within the connect
// timeout; timedOut says it did not end in time.
func (r *sshRun) network(ctx context.Context, program string, args []string) (out Output, timedOut bool, f *refusal.Failure) {
	var err error
	timedOut = r.Bound.Within(ctx, func(limited context.Context) { out, err = r.Runner.Run(limited, program, args) })
	if f := repoinit.Interruption(ctx); f != nil {
		return out, false, f
	}
	if err != nil && !timedOut {
		return out, false, r.notRun(program, err)
	}
	return out, timedOut, nil
}

// local runs a program that does not talk to the server.
func (r *sshRun) local(ctx context.Context, program string, args []string) (Output, *refusal.Failure) {
	out, err := r.Runner.Run(ctx, program, args)
	if f := repoinit.Interruption(ctx); f != nil {
		return out, f
	}
	if err != nil {
		return out, r.notRun(program, err)
	}
	if out.Code != 0 {
		return out, r.exitFailure(program, out)
	}
	return out, nil
}

// exitFailure is the refusal for a program of the client that ended with a
// code other than 0.
func (r *sshRun) exitFailure(program string, out Output) *refusal.Failure {
	return refusal.Fail(refusal.SSHClientFailed, "%s failed (exit code %d): %s", program, out.Code, r.scrub(lastLine(out.Stderr)))
}

func (r *sshRun) notRun(program string, err error) *refusal.Failure {
	return refusal.Fail(refusal.SSHClientFailed, "%s could not be run: %s", program, r.scrub(err.Error()))
}

// lastLine is the last line of text that says something.
func lastLine(text string) string {
	last := ""
	for l := range strings.Lines(text) {
		if l = strings.TrimSpace(l); l != "" {
			last = l
		}
	}
	return last
}

// scan asks the server for its host keys (Р40) as the service user.
func (r *sshRun) scan(ctx context.Context) ([]HostKey, *refusal.Failure) {
	args := append(r.timeoutOption("-T"), "-p", strconv.Itoa(r.Address.Port), "-t", "ed25519,ecdsa,rsa", r.Address.Host)
	out, timedOut, f := r.network(ctx, ProgKeyscan, args)
	switch {
	case f != nil:
		return nil, f
	case timedOut:
		return nil, refusal.Fail(refusal.BackendUnavailable, "ssh-keyscan of %s did not answer within %s (--connect-timeout); the command can be repeated", r.where(), r.Bound.Timeout)
	}
	keys := ParseKeyscan(out.Stdout)
	switch {
	case len(keys) == 0:
		return nil, refusal.Fail(refusal.BackendUnavailable, "the server %s presented no host key: %s; the command can be repeated", r.where(), r.scrub(lastLine(out.Stderr)))
	case out.Code != 0:
		return nil, r.exitFailure(ProgKeyscan, out)
	}
	return keys, nil
}

// trust is the decision about the host key.
type trust struct {
	key HostKey
	// write: the key goes into known_hosts; replace: the host's old
	// entries go first.
	write, replace bool
}

// decide settles the host key (Р40): a key of the server that known_hosts
// holds is trusted as it is; entries that match none are a change, to be
// replaced only on request; a new key is trusted when the operator
// confirms it, never silently.
func (r *sshRun) decide(keys []HostKey) (trust, *refusal.Failure) {
	known := FindKnown(r.known, r.Address.KnownHostsName())
	if key, ok := known.Key(keys); ok {
		r.res.HostKey = key
		return trust{}, nil
	}
	if len(known.Lines) > 0 && !r.Replace {
		return trust{}, r.changed(known, keys)
	}
	key, f := r.confirm(keys)
	if f != nil {
		return trust{}, f
	}
	r.res.HostKey = key
	replace := len(known.Lines) > 0
	if replace {
		r.res.KeptPatterns = known.Patterns
	}
	return trust{key: key, write: true, replace: replace}, nil
}

func (r *sshRun) changed(known Known, keys []HostKey) *refusal.Failure {
	own := slices.DeleteFunc(slices.Clone(known.Lines), func(n int) bool { return slices.Contains(known.Patterns, n) })
	where := fmt.Sprintf("on line %s", joinInts(own))
	if len(own) == 0 {
		where = "in no entry of its own"
	}
	if len(known.Patterns) > 0 {
		where += fmt.Sprintf(", and the entries with a pattern or a list on line %s match it too", joinInts(known.Patterns))
	}
	return refusal.Fail(refusal.HostKeyChanged,
		"%s holds a key of %s %s that the server does not present now (it presents %s): this can be a substitution of the server or its reinstallation; if you know it was reinstalled, repeat the command with --replace-host-key (entries with a pattern or a list are kept)",
		r.path("known_hosts"), r.Address.KnownHostsName(), where, describeKeys(keys))
}

// joinInts lists numbers for a message.
func joinInts(numbers []int) string {
	parts := make([]string, len(numbers))
	for i, n := range numbers {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

func (r *sshRun) path(name string) string {
	return filepath.Join(r.Service.Home, ".ssh", name)
}

// describeKeys lists the keys with their fingerprints.
func describeKeys(keys []HostKey) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.Type + " " + k.Fingerprint()
	}
	return strings.Join(parts, ", ")
}

// confirm: the flag, else the terminal, else nothing is trusted.
func (r *sshRun) confirm(keys []HostKey) (HostKey, *refusal.Failure) {
	switch {
	case r.Fingerprint != "":
		return r.byFingerprint(keys)
	case r.Confirm == nil:
		return HostKey{}, refusal.Fail(refusal.HostKeyUnconfirmed,
			"the host key of %s is not known and nothing confirmed it: the server presents %s; compare them with the fingerprint the administrator of the server gives, then repeat with --host-key-fingerprint <fingerprint>, or run the command at a terminal", r.where(), describeKeys(keys))
	}
	return r.ask(keys)
}

func (r *sshRun) byFingerprint(keys []HostKey) (HostKey, *refusal.Failure) {
	for _, k := range keys {
		if k.Fingerprint() == r.Fingerprint {
			return k, nil
		}
	}
	return HostKey{}, refusal.Fail(refusal.HostKeyMismatch,
		"the fingerprint %s given with --host-key-fingerprint is none of the keys %s presents: %s; the server may have been replaced, or the fingerprint is wrong; nothing was written", r.Fingerprint, r.where(), describeKeys(keys))
}

// ask shows one key and trusts it on the answer "yes", exactly.
func (r *sshRun) ask(keys []HostKey) (HostKey, *refusal.Failure) {
	key := preferred(keys)
	prompt := fmt.Sprintf("The host key of %s is not known yet.\n  host: %s\n  port: %d\n  type: %s\n  fingerprint: %s\nCompare the fingerprint with the one the administrator of the server gives (on the server: ssh-keygen -lf /etc/ssh/%s).\nType yes to trust this key: ",
		r.Address.Host, r.Address.Host, r.Address.Port, key.Type, key.Fingerprint(), hostKeyFile(key.Type))
	answer, err := r.Confirm(prompt)
	if err != nil || answer != "yes" {
		return HostKey{}, refusal.Fail(refusal.HostKeyRejected, "the host key of %s was not accepted (only the answer yes does); nothing was written", r.where())
	}
	return key, nil
}

// preferred is the key to show: ed25519, then ecdsa, then rsa.
func preferred(keys []HostKey) HostKey {
	best := keys[0]
	for _, k := range keys[1:] {
		if keyRank(k.Type) < keyRank(best.Type) {
			best = k
		}
	}
	return best
}

func keyRank(keyType string) int {
	switch {
	case keyType == "ssh-ed25519":
		return 0
	case strings.HasPrefix(keyType, "ecdsa-"):
		return 1
	case keyType == "ssh-rsa":
		return 2
	}
	return 3
}

// hostKeyFile is the file of the key on the server that holds the public part.
func hostKeyFile(keyType string) string {
	switch keyRank(keyType) {
	case 0:
		return "ssh_host_ed25519_key.pub"
	case 1:
		return "ssh_host_ecdsa_key.pub"
	case 2:
		return "ssh_host_rsa_key.pub"
	}
	return "ssh_host_*_key.pub"
}

// write makes what is missing, in the order of Р42: the key of the service
// user (only now that the host key is trusted), known_hosts, the block of
// the config; each change is audited when it is made.
func (r *sshRun) write(ctx context.Context, t trust) *refusal.Failure {
	for _, step := range []func() *refusal.Failure{
		func() *refusal.Failure { return r.publicKey(ctx) },
		func() *refusal.Failure { return r.trustHostKey(t) },
		r.writeConfig,
	} {
		if f := step(); f != nil {
			return f
		}
	}
	return nil
}

// writeConfig puts the block of the host into the config when it differs.
func (r *sshRun) writeConfig() *refusal.Failure {
	config, changed := ApplyBlock(r.config, r.Address.Host)
	if !changed {
		return nil
	}
	f := r.Home.Write("config", config)
	r.wroteIf(f == nil, "config")
	return f
}

// publicKey makes the key if there is none and settles its public part.
func (r *sshRun) publicKey(ctx context.Context) *refusal.Failure {
	keyPath := r.path(privateKeyFile)
	switch {
	case !r.keyPresent:
		return r.createKey(ctx, keyPath)
	case r.pub != "":
		r.res.PublicKey = r.pub
		return nil
	}
	out, f := r.local(ctx, ProgKeygen, []string{"-y", "-f", keyPath})
	if f != nil {
		return f
	}
	r.res.PublicKey = strings.TrimSpace(out.Stdout)
	return nil
}

// createKey makes an ed25519 key without a passphrase, as the service
// user: root writes no file of it (Н21).
func (r *sshRun) createKey(ctx context.Context, keyPath string) *refusal.Failure {
	if f := r.Home.Ensure(); f != nil {
		return f
	}
	args := []string{"-q", "-t", "ed25519", "-N", "", "-C", "sard-agent@" + r.hostName(), "-f", keyPath}
	if _, f := r.local(ctx, ProgKeygen, args); f != nil {
		return f
	}
	r.wroteIf(true, "id_ed25519")
	r.Audit("ssh key of service user", r.Service.Name, "created")
	pub, found, f := r.Home.Read(publicKeyFile)
	if !found {
		return orFail(f, refusal.Fail(refusal.SSHClientFailed, "ssh-keygen made no public part of the key: %s is missing", r.path(publicKeyFile)))
	}
	r.res.PublicKey = strings.TrimSpace(string(pub))
	return nil
}

// orFail is f, or other when f is nil.
func orFail(f, other *refusal.Failure) *refusal.Failure {
	if f != nil {
		return f
	}
	return other
}

// wroteIf notes that a file of ~/.ssh was written.
func (r *sshRun) wroteIf(written bool, file string) {
	if written {
		r.res.Changed = true
		r.res.Written = append(r.res.Written, file)
	}
}

// hostName is the name of this host for the comment of the key.
func (r *sshRun) hostName() string {
	if host, err := r.Hostname(); err == nil && host != "" {
		return host
	}
	return "localhost"
}

// trustHostKey writes the key of the server to known_hosts.
func (r *sshRun) trustHostKey(t trust) *refusal.Failure {
	if !t.write {
		return nil
	}
	name := r.Address.KnownHostsName()
	content, action := AddHostKey(r.known, name, t.key), "trusted"
	if t.replace {
		content, action = ReplaceHostKey(r.known, name, t.key), "replaced"
	}
	if f := r.Home.Write("known_hosts", content); f != nil {
		return f
	}
	r.wroteIf(true, "known_hosts")
	r.Audit("ssh host key of", r.hostLabel(), action+" "+t.key.Type+" "+t.key.Fingerprint())
	return nil
}

// hostLabel is host or host:port, for the audit line.
func (r *sshRun) hostLabel() string {
	if r.Address.Port == 22 {
		return r.Address.Host
	}
	return r.Address.Host + ":" + strconv.Itoa(r.Address.Port)
}

// login tries the login as the service user, the way restic will (Р34):
// its refusal is classified by what ssh says, not by what restic passes on.
func (r *sshRun) login(ctx context.Context) *refusal.Failure {
	args := []string{"-b", "/dev/null", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes"}
	if r.seconds() > 0 {
		args = append(args, "-o", "ConnectTimeout="+strconv.Itoa(r.seconds()))
	}
	args = append(args, "-P", strconv.Itoa(r.Address.Port), r.Address.Destination())
	out, timedOut, f := r.network(ctx, ProgSFTP, args)
	switch {
	case f != nil:
		return f
	case timedOut:
		return refusal.Fail(refusal.BackendUnavailable, "the ssh login to %s did not answer within %s (--connect-timeout); the command can be repeated", r.where(), r.Bound.Timeout)
	case out.Code == 0:
		return nil
	}
	return r.loginRefused(out.Stderr)
}

// permissionDenied is the line ssh prints when the server turns the key
// down: "user@host: Permission denied (publickey)." or without the prefix.
var permissionDenied = regexp.MustCompile(`^\S+@\S+: Permission denied \([^)]*\)\.?$`)

// loginClass is what a line of the stderr of ssh says of the login.
type loginClass int

const (
	lineSays loginClass = iota // nothing the command can use
	keyNotAuthorized
	hostKeyRefused
	serverUnreachable
)

// classOfLine looks at the lines ssh itself prints, not at a banner of the
// server, which may hold any words.
func classOfLine(line string) loginClass {
	switch {
	case line == "Host key verification failed.":
		return hostKeyRefused
	case permissionDenied.MatchString(line):
		return keyNotAuthorized
	case strings.HasPrefix(line, "ssh:"), strings.HasPrefix(line, "kex_exchange_identification:"),
		strings.HasPrefix(line, "Connection closed"), strings.HasPrefix(line, "Connection reset"):
		return serverUnreachable
	}
	return lineSays
}

// classified is the first line of stderr that says something, and what.
func classified(stderr string) (string, loginClass) {
	for l := range strings.Lines(stderr) {
		l = strings.TrimSpace(l)
		if class := classOfLine(l); class != lineSays {
			return l, class
		}
	}
	return lastLine(stderr), lineSays
}

// loginRefused classifies the stderr of a login that did not succeed.
func (r *sshRun) loginRefused(stderr string) *refusal.Failure {
	line, class := classified(stderr)
	cause := r.scrub(line)
	user := r.Address.User
	if user == "" {
		user = r.Service.Name
	}
	switch class {
	case hostKeyRefused:
		return refusal.Fail(refusal.HostKeyMismatch, "ssh refused the host key of %s: %s; known_hosts holds another key for it", r.where(), cause)
	case keyNotAuthorized:
		return refusal.Fail(refusal.SSHKeyNotAuthorized,
			"the server %s did not accept the key of the service user %s for the user %s: %s; add the public key printed by this command to ~/.ssh/authorized_keys of the user %s on %s, then repeat the same command",
			r.Address.Host, r.Service.Name, user, cause, user, r.Address.Host)
	case serverUnreachable:
		return refusal.Fail(refusal.BackendUnavailable, "the server %s is unreachable over ssh: %s; the command can be repeated", r.where(), cause)
	}
	return refusal.Fail(refusal.BackendRefused, "the ssh login to %s failed: %s", r.where(), cause)
}

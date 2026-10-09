// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// sftpState is what repo add knows of an sftp: repository besides its address.
type sftpState struct {
	address hostsetup.SFTPAddress
	// res is what the ssh setup found or made.
	res repoconnect.SFTPResult
}

func (st *addState) isSFTP() bool { return st.sftp != nil }

// isLocal: the repository is a directory of this host.
func (st *addState) isLocal() bool { return !st.isS3() && !st.isSFTP() }

// foreignFlags are the flags that belong to one kind of address only (Р29, Р40).
var foreignFlags = []struct{ name, flag, kind string }{
	{"access-key-id", "--access-key-id", "s3"}, {"region", "--region", "s3"},
	{"secret-key-stdin", "--secret-key-stdin", "s3"}, {"secret-key-from-file", "--secret-key-from-file", "s3"},
	{"host-key-fingerprint", "--host-key-fingerprint", "sftp"}, {"replace-host-key", "--replace-host-key", "sftp"},
}

// checkFlagsOfKind: a flag of another kind of address is a usage error that names it.
func (c *hostCmd) checkFlagsOfKind(kind string) *refusal.Failure {
	for _, f := range foreignFlags {
		if f.kind != kind && c.opts.set[f.name] {
			return usageFailureOf("%s is a flag for an %s: address, and the address is %s", f.flag, f.kind, kindName(kind))
		}
	}
	return nil
}

func kindName(kind string) string {
	if kind == "local" {
		return "a local path"
	}
	return "an " + kind + ": address"
}

// checkSFTP is Р32 and Р40: the address, the flags of its kind and the
// format of the fingerprint. The address is written to the fragment as given.
func (c *hostCmd) checkSFTP() (string, *refusal.Failure) {
	addr, f := hostsetup.CheckSFTPAddress(c.opts.address)
	if f != nil {
		return "", f
	}
	if f := c.checkFlagsOfKind("sftp"); f != nil {
		return "", f
	}
	if c.opts.set["host-key-fingerprint"] {
		if f := repoconnect.CheckFingerprint(c.opts.hostKeyFingerprint); f != nil {
			return "", f
		}
	}
	c.sftp = addr
	return c.opts.address, nil
}

// restic's PATH: where its helpers, the OpenSSH client among them, are looked for.
func (c *hostCmd) resticPath() string {
	if c.deps.pathEnv == "" {
		return restic.DefaultPath
	}
	return c.deps.pathEnv
}

// findProgram finds an executable file of that name in the PATH restic gets.
func (c *hostCmd) findProgram(name string) (string, bool) {
	for _, dir := range filepath.SplitList(c.resticPath()) {
		if path, found := c.programIn(dir, name); found {
			return path, true
		}
	}
	return "", false
}

// programIn: the directory (an absolute one) holds an executable file of that name.
func (c *hostCmd) programIn(dir, name string) (string, bool) {
	if !filepath.IsAbs(dir) {
		return "", false
	}
	path := filepath.Join(dir, name)
	info, err := c.deps.fs.Stat(path)
	return path, err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// checkClient is Р36: the OpenSSH client is there, before any change.
func (c *hostCmd) checkClient() *refusal.Failure {
	return repoconnect.CheckClient(func(program string) bool {
		_, found := c.findProgram(program)
		return found
	})
}

// sshRunner runs the programs of the client as the service user with a
// fixed environment: the PATH of restic, the home of passwd, no locale.
// Nothing of the agent's environment, and no secret, reaches them.
type sshRunner struct {
	exec restic.Executor
	find func(name string) (string, bool)
	env  []string
	as   *restic.RunAs
}

func (c *hostCmd) sshRunner() sshRunner {
	return sshRunner{
		exec: c.deps.sshExec,
		find: c.findProgram,
		env:  []string{"PATH=" + c.resticPath(), "HOME=" + c.who.Service.Home, "LC_ALL=C"},
		as:   runAs(c.who),
	}
}

// Run implements repoconnect.Runner.
func (r sshRunner) Run(ctx context.Context, program string, args []string) (repoconnect.Output, error) {
	binary, found := r.find(program)
	if !found {
		return repoconnect.Output{Code: -1}, fmt.Errorf("%s is not in the PATH of restic", program)
	}
	var stdout, stderr lineBuffer
	code, err := r.exec.Run(ctx, restic.Command{
		Path: binary, Args: args, Env: r.env, Stdout: stdout.add, Stderr: stderr.add, RunAs: r.as,
	})
	return repoconnect.Output{Code: code, Stdout: stdout.text(), Stderr: stderr.text()}, err
}

// lineBuffer collects the lines of one stream.
type lineBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (b *lineBuffer) add(line []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, string(line))
}

func (b *lineBuffer) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lines) == 0 {
		return ""
	}
	return strings.Join(b.lines, "\n") + "\n"
}

// setupSSH is the ssh side of the connection (Р37-Р43), under the locks:
// ~/.ssh of the service user, the host key, the key, known_hosts, the
// config and the login check. The public key is shown when the server does
// not know it yet.
func (c *hostCmd) setupSSH(ctx context.Context, st *addState) *refusal.Failure {
	home, f := hostsetup.OpenSSHHome(c.deps.fs, c.who.Service)
	if f != nil {
		return f
	}
	defer home.Close()
	target, _ := c.resticFor(st, repoconnect.Files{}, nil)
	setup := &repoconnect.SFTP{
		Home: home, Address: st.sftp.address, Service: c.who.Service,
		Runner: c.sshRunner(), Bound: c.bound(),
		Fingerprint: c.opts.hostKeyFingerprint, Replace: c.opts.replaceHostKey,
		Confirm:  repoconnect.Confirmation(c.terminal(), c.budget),
		Hostname: c.deps.hostname,
		Audit:    func(kind, name, action string) { c.record(kind, name, action) },
		Scrub:    target.Scrub,
	}
	res, f := setup.Prepare(ctx)
	st.sftp.res = res
	if f != nil {
		c.explainSSHFailure(st, f)
	}
	return f
}

// terminal is the operator's terminal, nil when standard input is none.
func (c *hostCmd) terminal() hostsetup.Terminal {
	if c.deps.terminal == nil {
		return nil
	}
	return c.deps.terminal(c.stderr)
}

// explainSSHFailure tells what the operator needs besides the failure
// itself: the public key to authorize, and that the files written stay (Р39, Р43).
func (c *hostCmd) explainSSHFailure(st *addState, f *refusal.Failure) {
	if f.Reason == refusal.SSHKeyNotAuthorized && st.sftp.res.PublicKey != "" {
		_, _ = fmt.Fprintf(c.stdout, "Public key of the service user %s: add it to ~/.ssh/authorized_keys of the user %s on %s, then repeat the same command:\n  %s\n",
			c.who.Service.Name, c.sftpUser(st), st.sftp.address.Host, st.sftp.res.PublicKey)
	}
	c.noteSSHFilesStay(st)
}

// noteSSHFilesStay says that what the setup wrote stays when the command
// refuses later (Р43).
func (c *hostCmd) noteSSHFilesStay(st *addState) {
	if st.isSFTP() && st.sftp.res.Changed {
		_, _ = fmt.Fprintf(c.stderr, "sard-agent repo add: the ssh files written so far (the key, known_hosts, the config in %s) stay and will be used when the command is repeated\n", c.sshDir())
	}
}

func (c *hostCmd) sshDir() string { return filepath.Join(c.who.Service.Home, ".ssh") }

// sftpUser is the user the server knows the service user as.
func (c *hostCmd) sftpUser(st *addState) string {
	if st.sftp.address.User != "" {
		return st.sftp.address.User
	}
	return c.who.Service.Name
}

// sftpRepeat handles the repeat of a connected sftp: repository whose ssh
// files needed nothing (Р45): its code, and whether it was one.
func (c *hostCmd) sftpRepeat(ctx context.Context, st *addState) (int, bool) {
	if !st.isSFTP() || st.plan != hostsetup.AddUnchanged || st.sftp.res.Changed {
		return exitOK, false
	}
	return c.reportUnchanged(ctx, st)
}

// finishSSHUpdate: the repository was connected already and only the ssh
// files were set up again; nothing of the config changed, the service is
// not restarted (Р43).
func (c *hostCmd) finishSSHUpdate(st *addState) int {
	c.printUnchanged(st, st.ID)
	_, _ = fmt.Fprintf(c.stdout, "The ssh files of the service user were set up again in %s; ssh reads them at every connection, so the service is not restarted.\n", c.sshDir())
	return exitOK
}

// printSFTPAdded adds to the summary what an sftp: connection has: the
// host key and the public key to authorize.
func (c *hostCmd) printSFTPAdded(st *addState) {
	res := st.sftp.res
	_, _ = fmt.Fprintf(c.stdout, "  host key:      %s %s %s\n", st.sftp.address.KnownHostsName(), res.HostKey.Type, res.HostKey.Fingerprint())
	_, _ = fmt.Fprintf(c.stdout, "Public key of the service user %s (the user %s on %s must have it in ~/.ssh/authorized_keys):\n  %s\n",
		c.who.Service.Name, c.sftpUser(st), st.sftp.address.Host, res.PublicKey)
}

// printSFTPRemoved says that repo remove left the ssh files alone (Р48).
func (c *hostCmd) printSFTPRemoved(url string) {
	addr, f := hostsetup.CheckSFTPAddress(url)
	if f != nil {
		return
	}
	_, _ = fmt.Fprintf(c.stdout, "The ssh files of the service user remain: the key %s and the host key of %s in %s; other repositories may use them.\n",
		filepath.Join(c.sshDir(), "id_ed25519"), addr.KnownHostsName(), filepath.Join(c.sshDir(), "known_hosts"))
}

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// addState is what "repo add" knows about the repository it connects.
type addState struct {
	name, url string
	binary    string
	// State is what the connection learns: the passwords and the id.
	repoconnect.State
	// s3 is the access to an s3: repository, nil for another kind.
	s3 *repoconnect.S3Access
	// rotation: the repository is connected already, the keys change (Н17).
	rotation bool
	// sftp is the state of an sftp: repository, nil for another kind.
	sftp *sftpState
	// plan is what the config says of the name before the command acts.
	plan hostsetup.AddPlan
}

func (s *addState) final(c *hostCmd) string { return c.layout.PasswordFile(s.name) }

// passwordSource are the flags that give the password of an existing repository (Н4).
func passwordSource(o hostOptions) hostsetup.SourceOptions {
	return hostsetup.SourceOptions{
		Stdin: o.passwordStdin, File: o.passwordFromFile,
		Flags:   hostsetup.SourceFlags{Stdin: "--password-stdin", File: "--password-from-file"},
		Subject: "the password of repository " + o.name,
	}
}

// checkAddress is Р14 from the name to the address: the name, the kind of
// the address (a local path, s3:) and the address itself with the flags
// of its kind. It returns the address as it will be written to the fragment.
func (c *hostCmd) checkAddress() (string, *refusal.Failure) {
	if f := hostsetup.CheckName("repository", c.opts.name); f != nil {
		return "", f
	}
	switch kind := (config.Repository{URL: c.opts.address}).Backend(); kind {
	case "local":
		return c.checkLocal()
	case "s3":
		return c.checkS3()
	case "sftp":
		return c.checkSFTP()
	default:
		return "", refusal.Fail(refusal.BackendNotSupported, "the address is of kind %q: repo add supports a local path, s3: or sftp:", kind)
	}
}

// checkLocal: a local path takes none of the flags of a remote address.
func (c *hostCmd) checkLocal() (string, *refusal.Failure) {
	if f := c.checkFlagsOfKind("local"); f != nil {
		return "", f
	}
	if hasControlCharacter(c.opts.address) {
		return "", refusal.Fail(refusal.LocalPathInvalid, "%q holds a control character", c.opts.address)
	}
	return c.checkLocalPath(c.opts.address)
}

// checkS3Flags: the key id is required, the region is optional (Р29).
func (c *hostCmd) checkS3Flags() *refusal.Failure {
	if !c.opts.set["access-key-id"] {
		return usageFailureOf("--access-key-id is required for an s3: address")
	}
	if f := hostsetup.CheckAccessKeyID(c.opts.accessKeyID); f != nil {
		return f
	}
	if c.opts.set["region"] {
		return hostsetup.CheckRegion(c.opts.region)
	}
	return nil
}

// checkS3 is Р28 and Р29: the address, then the flags of the key. The
// address is written to the fragment as given, or as --provider expands it (Р50).
func (c *hostCmd) checkS3() (string, *refusal.Failure) {
	if f := c.expandPreset(); f != nil {
		return "", f
	}
	addr, f := hostsetup.CheckS3Address(c.opts.address)
	if f != nil {
		return "", f
	}
	if f := c.checkS3Flags(); f != nil {
		return "", f
	}
	if f := c.checkFlagsOfKind("s3"); f != nil {
		return "", f
	}
	c.s3 = addr
	if addr.Insecure {
		_, _ = fmt.Fprintf(c.stderr, "warning: the address uses http: requests to the storage go without TLS; the data is encrypted by restic, but object names and signed requests are visible on the network\n")
	}
	return c.opts.address, nil
}

// expandPreset replaces the address with the one --provider builds (Р50).
func (c *hostCmd) expandPreset() *refusal.Failure {
	if !c.opts.set["provider"] {
		return nil
	}
	expanded, f := hostsetup.ExpandProvider(c.opts.provider, c.opts.region, c.opts.address)
	if f == nil {
		c.opts.address = expanded
	}
	return f
}

func (c *hostCmd) checkLocalPath(path string) (string, *refusal.Failure) {
	if !filepath.IsAbs(path) {
		return "", refusal.Fail(refusal.LocalPathInvalid, "%q is not an absolute path", path)
	}
	path = filepath.Clean(path)
	d, _, err := hostsetup.OpenDir(c.deps.fs, path, nil)
	if err == nil {
		_ = d.Close()
		return path, nil
	}
	return path, pathRefusal(path, err)
}

// pathRefusal tells why the walk of a path stopped: a component that is
// missing is no refusal (the command creates it); a link or a file in the
// way is, and so is a path that cannot be looked at.
func pathRefusal(path string, err error) *refusal.Failure {
	var notDir *hostsetup.NotDirError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case errors.As(err, &notDir):
		return refusal.Fail(refusal.LocalPathInvalid, "%s", notDir)
	}
	return refusal.Fail(refusal.LocalPathInvalid, "%s cannot be looked at: %v", path, err)
}

// repository is the repository of the config by name.
func (c *hostCmd) repository(name string) config.Repository {
	return hostsetup.RepositoryNamed(c.cfg, name)
}

// runRepoAdd is "sard-agent repo add <name> <address>": args excludes "add".
// The checks come in the order of Р14.
func runRepoAdd(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("add", args, stderr, deps)
	if code != exitOK {
		return code
	}
	if f := checkAddSources(opts); f != nil {
		return report(stderr, "repo add", f)
	}
	who, f := authorize(deps, "repo add", opts, true, true)
	if f != nil {
		return report(stderr, "repo add", f)
	}
	c := newHostCmd("repo add", opts, who, stdout, stderr, deps)
	url, f := c.checkAddress()
	if f != nil {
		return c.fail(f)
	}
	ctx, c.budget = repoconnect.NewBudget(ctx, deps.clock, opts.timeout)
	defer c.budget.Cancel(nil)
	return c.addChecked(ctx, url)
}

// addChecked goes on once the address is sound: config, conflicts, restic,
// the password flags, and the unchanged case.
func (c *hostCmd) addChecked(ctx context.Context, url string) int {
	if f := c.load(); f != nil {
		return c.fail(f)
	}
	plan, f := hostsetup.PlanRepository(c.cfg, c.layout, c.opts.name, url)
	if f != nil {
		return c.fail(f)
	}
	return c.addPlanned(ctx, url, plan)
}

// addPlanned checks restic, reads the password flags and handles the
// unchanged case; anything else is connected under the locks.
func (c *hostCmd) addPlanned(ctx context.Context, url string, plan hostsetup.AddPlan) int {
	binary, err := checkRestic(ctx, c.cfg, c.opts.configPath, c.deps.executable, c.deps.exec, runAs(c.who))
	if err != nil {
		return reportRepoError(ctx, c.stderr, "add", err)
	}
	st := &addState{name: c.opts.name, url: url, binary: binary, plan: plan}
	if f := c.prepareAccess(st, plan); f != nil {
		return c.fail(f)
	}
	if f := c.readGivenPassword(st); f != nil {
		return c.fail(f)
	}
	if code, done := c.repeatOf(ctx, st, plan); done {
		return code
	}
	return c.addLocked(ctx, st)
}

// prepareAccess settles what a remote repository needs before anything is
// written: the OpenSSH client for sftp: (Р36), the keys for s3: (Р29, Р45, Н17).
func (c *hostCmd) prepareAccess(st *addState, plan hostsetup.AddPlan) *refusal.Failure {
	switch (config.Repository{URL: st.url}).Backend() {
	case "sftp":
		return c.prepareSFTP(st, plan)
	case "s3":
		return c.prepareS3Access(st, plan)
	}
	return nil
}

// prepareSFTP: an sftp: repository needs the OpenSSH client, and a repeat of a
// connected one takes its password from the password file (T1).
func (c *hostCmd) prepareSFTP(st *addState, plan hostsetup.AddPlan) *refusal.Failure {
	st.sftp = &sftpState{address: c.sftp}
	if f := c.checkClient(); f != nil {
		return f
	}
	if plan == hostsetup.AddUnchanged {
		return c.refusePasswordWithKeys("an update of the ssh files of a connected repository")
	}
	return nil
}

// prepareS3Access settles the keys of an s3: repository and whether they change.
func (c *hostCmd) prepareS3Access(st *addState, plan hostsetup.AddPlan) *refusal.Failure {
	if f := c.prepareS3(st, plan); f != nil {
		return f
	}
	st.rotation = plan == hostsetup.AddUnchanged && !st.s3.Same
	if st.rotation {
		return c.refusePasswordWithKeys("a change of the keys of a connected repository")
	}
	return nil
}

// refusePasswordWithKeys: a change of keys takes the password from the
// password file of the repository, never from a flag (П16). It is decided
// as soon as the change is known, before the secret key is read.
func (c *hostCmd) refusePasswordWithKeys(what string) *refusal.Failure {
	var given []string
	if c.opts.passwordStdin {
		given = append(given, "--password-stdin")
	}
	if c.opts.passwordFromFile != "" {
		given = append(given, "--password-from-file")
	}
	if len(given) == 0 {
		return nil
	}
	return usageFailureOf("%s takes the password from the password file of the repository: drop %s", what, strings.Join(given, " and "))
}

// repeatOf handles the command of a connected repository that changes
// nothing: its code, and whether it was one.
func (c *hostCmd) repeatOf(ctx context.Context, st *addState, plan hostsetup.AddPlan) (int, bool) {
	if plan != hostsetup.AddUnchanged || st.rotation || st.isSFTP() {
		return exitOK, false
	}
	return c.reportUnchanged(ctx, st)
}

// readGivenPassword reads the password the flags name; the terminal is
// asked later, and only for a repository that exists.
func (c *hostCmd) readGivenPassword(st *addState) *refusal.Failure {
	o := passwordSource(c.opts)
	if !o.Stdin && o.File == "" {
		return nil
	}
	value, f := c.readSource(o)
	st.Provided = value
	return f
}

// resticFor is restic for the repository opened with the files, and the
// target its failures are described for. Its stderr is also written to
// stderr, if given.
func (c *hostCmd) resticFor(st *addState, files repoconnect.Files, stderr io.Writer) (repoinit.Target, restic.Repository) {
	repo := config.Repository{Name: st.name, URL: st.url, PasswordFile: files.Password, EnvFile: files.Env}
	checked := repoinit.Checked{EnvAssignments: st.secretAssignments()}
	// restic takes the password without the white space around it in the file.
	password := strings.TrimSpace(string(st.Provided))
	target := repoTarget(repo, checked, string(st.Provided), password, st.Generated)
	target.Where = config.RedactURL(st.url)
	switch {
	case st.isS3():
		target.Remote, target.Bucket = true, c.s3.Bucket
	case st.isSFTP():
		target.Remote, target.Directory = true, st.sftp.address.Path
	}
	cli := newRestic(c.cfg, st.binary, c.deps, repo, runAs(c.who))
	if stderr != nil {
		cli = cli.WithStderr(stderr)
	}
	return target, cli
}

// hasControlCharacter: a line break in a path would start a new line of the
// systemd drop-in.
func hasControlCharacter(path string) bool {
	return strings.ContainsFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

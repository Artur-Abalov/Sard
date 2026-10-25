// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restickeys"
)

// repoSteps are the checks of one repository (Ч3), in their order.
type repoSteps struct {
	*repoCheck
	// ctx is the context of this repository, limited by --timeout.
	ctx   context.Context
	repo  config.Repository
	index int
	// cli reads the repository without lock and cache, and keeps the tail of
	// restic's stderr in log.
	cli *restic.CLI
	log repoconnect.Log
	// target describes the repository to the messages; set once the files of
	// the repository are checked.
	target repoinit.Target
	// checks are the identifiers of the checks of this run, in order.
	checks []string
}

func newRepoSteps(c *repoCheck, ctx context.Context, index int) *repoSteps {
	r := &repoSteps{repoCheck: c, ctx: ctx, repo: c.cfg.Repositories[index], index: index}
	r.cli = newRestic(c.cfg, c.binary, c.deps, r.repo, runAs(c.who)).WithReadOnly().WithStderr(&r.log)
	r.checks = hostcheck.RepositoryChecks()
	if c.opts.readData > 0 {
		r.checks = append(r.checks, hostcheck.CheckRepositoryIntegrity)
	}
	if isVerifier(c.deps, c.cfg) {
		r.checks = append(r.checks, hostcheck.CheckRepositoryReadOnly)
	}
	return r
}

// openChecks are the checks the files of the repository and one restic cat
// config answer (Ч3).
var openChecks = hostcheck.RepositoryOpenChecks()

func (r *repoSteps) run() {
	id, failed := r.open()
	if failed == "" {
		failed = r.laterSteps(id)
	}
	if failed != "" {
		r.skipAfter(failed)
	}
}

// laterSteps runs the checks after the open ones until one is an ERROR (Ч3);
// it returns that one.
func (r *repoSteps) laterSteps(id string) string {
	steps := []struct {
		check string
		do    func() hostcheck.Line
	}{{hostcheck.CheckRepositoryID, func() hostcheck.Line { return r.idStep(id) }}, {hostcheck.CheckRepositoryKeys, r.keysStep}, {hostcheck.CheckRepositoryLocks, r.locksStep}, {hostcheck.CheckRepositoryIntegrity, r.integrityStep}, {hostcheck.CheckRepositoryReadOnly, r.readOnlyStep}}
	for _, s := range steps {
		if !slices.Contains(r.checks, s.check) {
			continue
		}
		l := s.do()
		if r.interrupted() {
			return ""
		}
		r.printer.Print(l)
		if l.Status == hostcheck.Error {
			return s.check
		}
	}
	return ""
}

// skipAfter prints the checks that follow the failed one as skipped.
func (r *repoSteps) skipAfter(failed string) {
	for _, check := range r.checks[slices.Index(r.checks, failed)+1:] {
		r.printer.Print(skipLine(check, r.repo.Name, failed))
	}
}

// openResult is how far the first four checks got: the position of the one
// that failed (-1 for none) and why, or the id of the repository.
type openResult struct {
	at      int
	failure *refusal.Failure
	id      string
}

// open runs the first four checks and prints them; it returns the id of the
// repository, and the identifier of the check that failed, if one did.
func (r *repoSteps) open() (id, failed string) {
	res := r.openResult()
	if r.interrupted() {
		return "", openChecks[0]
	}
	for i, check := range openChecks {
		switch {
		case res.failure == nil || i < res.at:
			r.printer.Print(hostcheck.Line{Check: check, Subject: r.repo.Name, Status: hostcheck.OK, Message: r.okMessage(check)})
		case i == res.at:
			f := res.failure
			r.printer.Print(failureLine(check, r.repo.Name, f, r.hint(check, f)))
			failed = check
		}
	}
	return res.id, failed
}

// okMessage is the message of an OK line; the storage is named by its address
// with the password of the address hidden.
func (r *repoSteps) okMessage(check string) string {
	return hostcheck.RepositoryOKMessage(check, config.RedactURL(r.repo.URL))
}

func (r *repoSteps) openResult() openResult {
	env, res := r.localChecks()
	if res != nil {
		return *res
	}
	r.target = repoconnect.TargetFor(r.repo, env, passwordValues(r.repo, r.who, r.deps)...)
	id, initialized, f := r.bound().Inspect(r.ctx, r.cli, r.target, &r.log)
	switch {
	case f != nil:
		f = repoinit.Reclassify(f)
		return openResult{at: slices.Index(openChecks, hostcheck.RepositoryStepOf(f.Reason)), failure: f}
	case !initialized:
		return openResult{at: 2, failure: refusal.Fail(refusal.RepositoryNotInitialized, "nothing is stored at the address of repository %q", r.repo.Name)}
	}
	return openResult{at: -1, id: id}
}

func (r *repoSteps) bound() repoconnect.Bound {
	return repoconnect.Bound{Clock: r.deps.clock, Timeout: r.opts.connectTimeout}
}

// localChecks are the checks of the files of the repository, which need no
// restic: the env_file belongs to the credentials, the password file and the
// encryption to the password.
func (r *repoSteps) localChecks() ([]string, *openResult) {
	host := r.deps.host(r.who.Service.UID)
	env, f := repoinit.CheckEnvFile(host, r.repo, r.index)
	if f != nil {
		return nil, &openResult{at: 1, failure: f}
	}
	if f := repoinit.CheckCryptoProvider(r.repo); f != nil {
		return nil, &openResult{at: 3, failure: f}
	}
	if f := repoinit.CheckPasswordFile(host, r.repo, r.index); f != nil {
		return nil, &openResult{at: 3, failure: f}
	}
	return env, nil
}

// hint says what to do about a failed check (Ч25): a command to run, or the
// section of the guide.
func (r *repoSteps) hint(check string, f *refusal.Failure) string {
	name := r.repo.Name
	switch f.Reason {
	case refusal.SecretFileRejected:
		path := r.repo.PasswordFile
		if check == hostcheck.CheckRepositoryCredentials {
			path = r.repo.EnvFile
		}
		return fmt.Sprintf("sudo chown %s %s && sudo chmod 0600 %s", r.who.Service.Name, path, path)
	case refusal.PasswordFileMissing:
		return fmt.Sprintf("put the password of the repository into %s (owner %s, mode 0600), or make a new repository with `sudo sard-agent repo add %s <address>`", r.repo.PasswordFile, r.who.Service.Name, name)
	case refusal.RepositoryNotInitialized:
		return fmt.Sprintf("create it with `sudo sard-agent repo init %s`, or point the repository at the right address with `sudo sard-agent repo add %s <address>`", name, name)
	case refusal.SSHKeyNotAuthorized:
		return "add the public key of the service user to authorized_keys on the SFTP server; see " + hostcheck.HelpURL(check)
	}
	return "see " + hostcheck.HelpURL(check)
}

// idStep is repository.id (Ч4).
func (r *repoSteps) idStep(id string) hostcheck.Line {
	announced, err := hostcheck.ReadAnnounced(r.deps.readFile, executorStateDir(r.cfg.Executor.StateDir))
	return hostcheck.IDLine(r.repo.Name, id, announced, err)
}

// keysStep is repository.keys (V3, K16): the repository has a key issued for
// recovery, or the operator is told to issue one.
func (r *repoSteps) keysStep() hostcheck.Line {
	var keys []restic.Key
	f := r.bound().Attempt(r.ctx, r.target, &r.log, func(limited context.Context) (err error) {
		keys, err = r.cli.Keys(limited)
		return err
	}, func(ctx context.Context, err error) *refusal.Failure { return repoinit.FromRestic(ctx, err, r.target) })
	if f != nil {
		return failureLine(hostcheck.CheckRepositoryKeys, r.repo.Name, f, r.hint(hostcheck.CheckRepositoryKeys, f))
	}
	return hostcheck.KeysLine(r.repo.Name, len(keys), restickeys.Warnings(keys))
}

// locksStep is repository.locks (Ch5); the whole step is one access to the
// storage as far as --connect-timeout goes.
func (r *repoSteps) locksStep() hostcheck.Line {
	env, err := lockEnv(r.deps)
	if err != nil {
		return hostcheck.Line{Check: hostcheck.CheckRepositoryLocks, Subject: r.repo.Name, Status: hostcheck.Error, Message: fmt.Sprintf("the name of this host is unknown: %v", err), Hint: "see " + hostcheck.HelpURL(hostcheck.CheckRepositoryLocks)}
	}
	var locks []hostcheck.Lock
	var gone int
	f := r.bound().Attempt(r.ctx, r.target, &r.log, func(limited context.Context) (err error) {
		locks, gone, err = readLocks(limited, r.cli)
		return err
	}, func(ctx context.Context, err error) *refusal.Failure { return repoinit.FromRestic(ctx, err, r.target) })
	if f != nil {
		return failureLine(hostcheck.CheckRepositoryLocks, r.repo.Name, f, r.hint(hostcheck.CheckRepositoryLocks, f))
	}
	return hostcheck.LockLine(r.repo.Name, env, locks, gone)
}

// integrityStep is repository.integrity (Ч7): restic check of a part of the
// data; a verifier's takes no lock, as its verifications (V5).
func (r *repoSteps) integrityStep() hostcheck.Line {
	var log repoconnect.Log
	cli := newRestic(r.cfg, r.binary, r.deps, r.repo, runAs(r.who)).WithStderr(&log)
	err := r.checkData(cli)
	line := hostcheck.Line{Check: hostcheck.CheckRepositoryIntegrity, Subject: r.repo.Name, Hint: "see " + hostcheck.HelpURL(hostcheck.CheckRepositoryIntegrity)}
	if err == nil {
		line.Status, line.Hint = hostcheck.OK, ""
		line.Message = fmt.Sprintf("restic read %d%% of the data and found no errors", r.opts.readData)
		return line
	}
	line.Status = hostcheck.Error
	line.Message = r.integrityMessage(err, &log)
	if r.ctx.Err() == nil && errors.Is(err, restic.ErrLocked) {
		// a backup or a restore holds the repository: nothing is wrong with it
		line.Status = hostcheck.Warn
		line.Hint = fmt.Sprintf("run `sudo sard-agent repo check %s --read-data %d` again when it has ended", r.repo.Name, r.opts.readData)
	}
	return line
}

func (r *repoSteps) checkData(cli *restic.CLI) error {
	if isVerifier(r.deps, r.cfg) {
		_, err := cli.VerifyData(r.ctx, r.opts.readData)
		return err
	}
	return cli.CheckData(r.ctx, r.opts.readData)
}

// readOnlyStep is repository.readonly (V5, K2): a verifier's credentials
// should only read. The storage is asked for a lock, which only credentials
// that can write get, and which restic removes at once.
func (r *repoSteps) readOnlyStep() hostcheck.Line {
	line := hostcheck.Line{Check: hostcheck.CheckRepositoryReadOnly, Subject: r.repo.Name, Status: hostcheck.OK, Message: "the credentials only read: the storage refuses a lock"}
	cli := newRestic(r.cfg, r.binary, r.deps, r.repo, runAs(r.who))
	if cli.CheckLock(r.ctx) == nil {
		line.Status, line.Message = hostcheck.Warn, "these storage credentials can write: a verifier needs none that do, and a leak of them would let an attacker spoil the backups"
		line.Hint = "give the verifier storage credentials that only read, see " + hostcheck.HelpURL(hostcheck.CheckRepositoryReadOnly)
	}
	return line
}

// integrityMessage says why restic check failed; what restic printed is
// scrubbed and, for a lock it could not create, cut to its cause (Ч7).
func (r *repoSteps) integrityMessage(err error, log *repoconnect.Log) string {
	said := r.target.Scrub(lastLine(log.String()))
	switch {
	case r.ctx.Err() != nil:
		return r.refusalMessage(err)
	case errors.Is(err, restic.ErrLocked):
		return "restic check needs a lock of its own and the repository is locked by another process (a backup, a restore or another check is running); repeat when it has ended"
	case repoinit.CannotLock(log.String()):
		return "restic check needs to create a lock in the repository and these credentials cannot write; checking with read-only credentials is not supported yet; restic said: " + causeOf(said)
	case repoinit.FoundErrors(err):
		return "restic check found errors in the repository: " + said
	}
	return r.refusalMessage(err)
}

func (r *repoSteps) refusalMessage(err error) string {
	return named(repoinit.FromRestic(r.ctx, err, r.target)).Error()
}

// causeOf is the last part of a message of restic, after its last ": ".
func causeOf(line string) string {
	if _, cause, found := strings.Cut(line, ": "); found {
		return causeOf(cause)
	}
	return strings.TrimSpace(line)
}

// lastLine is the last line of the text that holds anything, cut to a length
// that fits a line of the output.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if len(last) > maxSaid {
		return last[:maxSaid]
	}
	return last
}

const maxSaid = 200

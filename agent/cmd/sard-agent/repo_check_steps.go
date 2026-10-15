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
	r.checks = []string{hostcheck.CheckRepositoryStorage, hostcheck.CheckRepositoryCredentials, hostcheck.CheckRepositoryInitialized, hostcheck.CheckRepositoryPassword, hostcheck.CheckRepositoryID, hostcheck.CheckRepositoryLocks}
	if c.opts.readData > 0 {
		r.checks = append(r.checks, hostcheck.CheckRepositoryIntegrity)
	}
	return r
}

// openChecks are the checks the files of the repository and one restic cat
// config answer (Ч3).
var openChecks = [...]string{hostcheck.CheckRepositoryStorage, hostcheck.CheckRepositoryCredentials, hostcheck.CheckRepositoryInitialized, hostcheck.CheckRepositoryPassword}

// okMessages are what an OK line of the first checks says.
var okMessages = map[string]string{
	hostcheck.CheckRepositoryStorage:     "the storage at %s answers",
	hostcheck.CheckRepositoryCredentials: "the storage accepts the credentials",
	hostcheck.CheckRepositoryInitialized: "a repository is initialized at the address",
	hostcheck.CheckRepositoryPassword:    "the password file opens the repository",
}

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
	}{{hostcheck.CheckRepositoryID, func() hostcheck.Line { return r.idStep(id) }}, {hostcheck.CheckRepositoryLocks, r.locksStep}, {hostcheck.CheckRepositoryIntegrity, r.integrityStep}}
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
	if check == hostcheck.CheckRepositoryStorage {
		return fmt.Sprintf(okMessages[check], config.RedactURL(r.repo.URL))
	}
	return okMessages[check]
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
		f = reclassify(f)
		return openResult{at: openStepOf(f.Reason), failure: f}
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

// openStepOf is the position in openChecks of the check a refusal ends.
func openStepOf(reason refusal.Reason) int {
	switch reason {
	case refusal.S3KeyRejected, refusal.StorageAccessDenied, refusal.BucketNotFound, refusal.SSHKeyNotAuthorized,
		refusal.HostKeyMismatch, refusal.HostKeyRejected, refusal.HostKeyChanged, refusal.BackendRefused:
		return 1
	case refusal.RepositoryNotInitialized, refusal.ResticOutputUnexpected:
		return 2
	case refusal.WrongPassword, refusal.PasswordFileEmpty:
		return 3
	}
	return 0
}

// A restic that prints what restic does not (a certificate, ssh) is told
// from the generic refusals by its words (Ч3).
var refusalsByWords = []struct {
	words string
	from  []refusal.Reason
	to    refusal.Reason
}{
	{"x509", []refusal.Reason{refusal.BackendRefused}, refusal.BackendUnavailable},
	{"permission denied (publickey", []refusal.Reason{refusal.BackendRefused, refusal.StorageAccessDenied}, refusal.SSHKeyNotAuthorized},
	{"host key verification failed", []refusal.Reason{refusal.BackendRefused, refusal.StorageAccessDenied}, refusal.HostKeyMismatch},
}

// reclassify gives a refusal the reason its words call for.
func reclassify(f *refusal.Failure) *refusal.Failure {
	text := strings.ToLower(f.Detail)
	for _, c := range refusalsByWords {
		if strings.Contains(text, c.words) && slices.Contains(c.from, f.Reason) {
			return refusal.Fail(c.to, "%s", f.Detail)
		}
	}
	return f
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

// integrityStep is repository.integrity (Ч7): restic check of a part of the data.
func (r *repoSteps) integrityStep() hostcheck.Line {
	var log repoconnect.Log
	cli := newRestic(r.cfg, r.binary, r.deps, r.repo, runAs(r.who)).WithStderr(&log)
	err := cli.CheckData(r.ctx, r.opts.readData)
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

// integrityMessage says why restic check failed; what restic printed is
// scrubbed and, for a lock it could not create, cut to its cause (Ч7).
func (r *repoSteps) integrityMessage(err error, log *repoconnect.Log) string {
	said := r.target.Scrub(lastLine(log.String()))
	switch {
	case r.ctx.Err() != nil:
		return r.refusalMessage(err)
	case errors.Is(err, restic.ErrLocked):
		return "restic check needs a lock of its own and the repository is locked by another process (a backup, a restore or another check is running); repeat when it has ended"
	case cannotLock(log.String()):
		return "restic check needs to create a lock in the repository and these credentials cannot write; checking with read-only credentials is not supported yet; restic said: " + causeOf(said)
	case foundErrors(err):
		return "restic check found errors in the repository: " + said
	}
	return r.refusalMessage(err)
}

func (r *repoSteps) refusalMessage(err error) string {
	return named(repoinit.FromRestic(r.ctx, err, r.target)).Error()
}

// foundErrors: restic check ended with exit code 1, which it uses for the
// errors it found in the repository (not for a storage it could not reach).
func foundErrors(err error) bool {
	var exit *restic.ExitError
	return errors.As(err, &exit) && exit.Code == 1 && !errors.Is(err, restic.ErrNetwork)
}

// cannotLock: restic could not create its lock because the credentials may not write.
func cannotLock(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "unable to create lock") &&
		(strings.Contains(text, "access denied") || strings.Contains(text, "forbidden") || strings.Contains(text, "permission denied"))
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

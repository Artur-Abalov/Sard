// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Command sard-agent runs on a backed-up host. It dials sard-server,
// receives commands and streams backup data straight to storage.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/app"
	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/identity"
	"github.com/Artur-Abalov/sard/agent/internal/machineid"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restorejournal"
	"github.com/Artur-Abalov/sard/agent/internal/role"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
	"github.com/Artur-Abalov/sard/agent/internal/stepsecrets"
	"github.com/Artur-Abalov/sard/agent/internal/transport"
	"github.com/Artur-Abalov/sard/agent/internal/verifier"
	"github.com/Artur-Abalov/sard/agent/plugins"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// version is set at build time: -ldflags "-X main.version=...".
var version = "dev"

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
	// exitRefused: the server refused the agent for good (EX_CONFIG).
	// sard-agent.service has RestartPreventExitStatus=78.
	exitRefused = 78
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Hostname)
	stop() // equivalent mutant: os.Exit follows immediately
	os.Exit(code)
}

// hostnameFunc is os.Hostname, injectable for tests.
type hostnameFunc func() (string, error)

func run(ctx context.Context, args []string, stdout, stderr io.Writer, hostname hostnameFunc) int {
	if isEnrollCommand(args) {
		return runEnroll(ctx, args[1:], stdout, stderr, hostname)
	}
	if isCheckCommand(args) {
		return runCheckCommand(ctx, args[1:], stdout, stderr)
	}
	if isStatusCommand(args) {
		return runStatus(args[1:], stdout, stderr, realStatusDeps())
	}
	if isRepoCommand(args) {
		return runRepo(ctx, args[1:], stdout, stderr)
	}
	if isSecretCommand(args) {
		return runSecret(ctx, args[1:], stdout, stderr)
	}
	return runAgentCmd(ctx, args, stdout, stderr, hostname, realSelfEnrollDeps(hostname))
}

func isEnrollCommand(args []string) bool {
	return len(args) > 0 && args[0] == "enroll"
}

// runAgentCmd is "sard-agent --config ..." — running the agent itself,
// A2b's "enroll" subcommand dispatched away above.
func runAgentCmd(ctx context.Context, args []string, stdout, stderr io.Writer, hostname hostnameFunc, self selfEnrollDeps) int {
	fs := flag.NewFlagSet("sard-agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage of sard-agent:")
		fs.PrintDefaults()
		_, _ = fmt.Fprint(stderr, commandList)
	}
	showVersion := fs.Bool("version", false, "print the version and exit")
	configPath := fs.String("config", "", "path to the agent YAML config")
	tokenFile := fs.String("enroll-token-file", "", "file in which the server leaves an enrollment token; the agent enrolls itself with it")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *showVersion {
		_, _ = fmt.Fprintf(stdout, "sard-agent %s\n", version)
		return exitOK
	}
	if *configPath == "" {
		_, _ = fmt.Fprintln(stderr, "sard-agent: --config <path> is required")
		fs.Usage()
		return exitUsage
	}
	if proceed, code := selfEnrollIfAsked(ctx, *configPath, *tokenFile, stdout, stderr, self); !proceed {
		return code
	}
	return runUntilStopped(ctx, *configPath, *tokenFile, stdout, stderr, hostname, self)
}

// runUntilStopped starts the agent. The built-in agent (started with
// --enroll-token-file) does not exit on a refusal that a restart cannot cure:
// it waits for a new token and starts again (П24).
func runUntilStopped(ctx context.Context, configPath, tokenFile string, stdout, stderr io.Writer, hostname hostnameFunc, self selfEnrollDeps) int {
	for {
		err := startWith(ctx, configPath, stdout, hostname, os.Executable, self.runtime.withDefaults())
		var refused *refusedError
		if tokenFile == "" || !errors.As(err, &refused) {
			return exitCode(stderr, err)
		}
		if proceed, code := rejectedUntilNewToken(ctx, configPath, tokenFile, refused, stdout, stderr, self); !proceed {
			return code
		}
	}
}

func exitCode(stderr io.Writer, err error) int {
	if err == nil {
		return exitOK
	}
	return reportFailure(stderr, err)
}

// commandList is what the usage of the agent names besides running it (Ч29).
const commandList = `
Commands (each has --help):
  sard-agent check            check that this host is ready to back up and restore
  sard-agent enroll           register this host with sard-server
  sard-agent repo ...         the repositories of this host (init, list, add, show, remove, password, snapshots, restore, check, unlock)
  sard-agent secret ...       the secrets of this host (set, list, remove)
`

// reportFailure prints why the agent stopped and picks the exit code: a
// refusal that no restart cures is told apart from any other error.
func reportFailure(stderr io.Writer, err error) int {
	var refused *refusedError
	if errors.As(err, &refused) {
		_, _ = fmt.Fprintln(stderr, "sard-agent:", refused.line(false))
		return exitRefused
	}
	_, _ = fmt.Fprintln(stderr, "sard-agent:", err)
	return exitError
}

// startWith is the composition root: the only place that knows concrete types.
func startWith(ctx context.Context, configPath string, stdout io.Writer, hostnameOf hostnameFunc, executable func() (string, error), rt agentRuntime) error {
	local, err := loadLocal(configPath, rt)
	if err != nil {
		return err
	}
	cfg, me := local.cfg, local.host
	removeStale(local, rt)
	resticBinary, hostname, err := checkHostRuntime(ctx, cfg, configPath, hostnameOf, executable)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "sard-agent %s: connecting to %s as %s\n", version, cfg.Server.Address, me.role)
	renewal := newRenewal(cfg, hostname, rt)
	if stop, err := adoptPending(ctx, renewal, local, configPath, rt); stop || err != nil {
		return err
	}
	// R2a: before any step is accepted, what a killed restore left is removed.
	engine := newRestoreEngine(cfg)
	journal := newRestoreJournal(cfg, slog.Default())
	// V5: what a killed verification left is removed before any step, too.
	verify, err := newVerifying(cfg, me, verifier.OSDisk{}, slog.Default())
	if err != nil {
		return err
	}
	agent := newAgentWith(cfg, hostname, resticBinary, me.machineID, engine, journal, rt, verify.readsOnly(), verify.options()...)
	verify.attach(agent)
	go verify.warnIfWritable(ctx, lockers(openRepositories(cfg, resticOptions(cfg, resticBinary))), slog.Default())
	// R2c: the plugins recover their entries of the restore journal now, before
	// the first step and before the first connection to the server.
	recovered := recoverJournal(cfg, journal, rt.journalPlugins, agent.Plugins, slog.Default())
	return refuse(serve(ctx, cfg, me.role, agent, newRecoveryWith(engine, journal, recovered), renewal, rt), configPath, cfg)
}

// newRestoreJournal is the restore journal of the plugins that restore
// themselves: a directory of executor.state_dir, written as the service user.
// The directory of the previous release, <state_dir>/pg-restore, is read as the
// entries of the plugin postgresql for one release (ADR 0066 point 4) and never written.
func newRestoreJournal(cfg config.Config, log *slog.Logger) *pluginhost.Journal {
	store := restorejournal.New(restorejournal.Options{
		Dir:    restoreJournalDir(cfg),
		FS:     hostsetup.OS{},
		Owner:  hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid()},
		Legacy: []restorejournal.Legacy{{Dir: legacyJournalDir(cfg), Plugin: "postgresql"}},
	})
	return pluginhost.NewJournal(store, log)
}

func restoreJournalDir(cfg config.Config) string {
	return filepath.Join(executorStateDir(cfg.Executor.StateDir), "restore-journal")
}

// legacyJournalDir is the journal of the rollback of restores of databases of the previous release.
func legacyJournalDir(cfg config.Config) string {
	return filepath.Join(executorStateDir(cfg.Executor.StateDir), "pg-restore")
}

// recoverJournal gives the entries of the journal to the plugins that keep
// them: those of the test, if it names some, else the agent's own.
func recoverJournal(cfg config.Config, journal *pluginhost.Journal, test, builtin *sdk.Registry, log *slog.Logger) *pluginhost.JournalRecovery {
	reg := builtin
	if test != nil {
		reg = test
	}
	return pluginhost.RecoverJournal(pluginhost.RecoveryOptions{
		Journal: journal, Plugins: reg, Secrets: pluginhost.NewSecrets(cfg.Secrets, os.ReadFile), Logger: log,
	})
}

// checkHostRuntime finds restic and the host name, before the network.
func checkHostRuntime(ctx context.Context, cfg config.Config, configPath string, hostnameOf hostnameFunc, executable func() (string, error)) (resticBinary, hostname string, err error) {
	// A5b: no usable restic, no start — before the network (С5).
	resticBinary, err = checkRestic(ctx, cfg, configPath, executable, restic.ProcessExecutor{}, nil)
	if err != nil {
		return "", "", err
	}
	hostname, err = hostnameOf()
	if err != nil {
		return "", "", fmt.Errorf("hostname: %w", err)
	}
	return resticBinary, hostname, nil
}

// removeStale deletes the renewal files that belong to no usable pair and
// says which (П21).
func removeStale(local localState, rt agentRuntime) {
	if removed := certpair.Remove(rt.fs, local.pair.Stale); len(removed) > 0 {
		rt.logger.Warn("removed renewal files that belong to no usable key and certificate", "files", removed)
	}
}

// localState is what the checks that need no network found out.
type localState struct {
	cfg  config.Config
	host hostIdentity
	// pair is the key and certificate to start with.
	pair certpair.Selection
}

// loadLocal loads the config and runs every check that needs no network, in
// the order of П29: the crypto providers, the pair of key and certificate
// (a renewal may have left a second one), the files' modes, the expiry, then
// V1: the role from the certificate, and the machine id (С1).
func loadLocal(configPath string, rt agentRuntime) (localState, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return localState{}, err
	}
	sel, err := selectPair(cfg, configPath, rt)
	if err != nil {
		return localState{}, err
	}
	me, err := identifyHost(cfg, sel)
	return localState{cfg: cfg, host: me, pair: sel}, err
}

// selectPair runs the checks of loadLocal that come before the identity: the
// crypto providers, the pair, the files' modes and the expiry.
func selectPair(cfg config.Config, configPath string, rt agentRuntime) (certpair.Selection, error) {
	if err := checkCryptoProviders(cfg); err != nil {
		return certpair.Selection{}, err
	}
	sel, err := certpair.Select(cfg.TLS, rt.fs, rt.clock.Now())
	if err != nil {
		return certpair.Selection{}, err
	}
	if err := checkHostFiles(cfg, sel); err != nil {
		return certpair.Selection{}, err
	}
	if sel.Expired != nil {
		return certpair.Selection{}, expiredRefusal(sel.Expired, configPath, cfg)
	}
	return sel, nil
}

// hostIdentity is what the agent knows about itself before it connects.
type hostIdentity struct {
	role      role.Role
	machineID string
}

// machineIDSources are the host's machine-id files; tests replace them.
var machineIDSources = machineid.DefaultSources

// identifyHost reads the role from the certificate of the selected pair and
// computes the machine id keyed by the certificate's tenant. Nothing here
// touches the network.
func identifyHost(cfg config.Config, sel certpair.Selection) (hostIdentity, error) {
	if err := transport.CheckFiles(cfg.TLS); err != nil {
		return hostIdentity{}, err
	}
	id, err := identity.Read(sel.Pair.CertFile, os.ReadFile)
	if err != nil {
		return hostIdentity{}, err
	}
	host := machineid.OS(slog.Default())
	host.Sources = machineIDSources
	machine, err := machineid.Compute(host, executorStateDir(cfg.Executor.StateDir), id.TenantID)
	if err != nil {
		return hostIdentity{}, err
	}
	return hostIdentity{role: id.Role, machineID: machine}, nil
}

// newRestoreEngine restores for the console into restore.dir, as the
// service user: it makes the directories it needs with the owner of this
// process and mode 0700.
func newRestoreEngine(cfg config.Config) *consolerestore.Engine {
	return consolerestore.New(consolerestore.Options{
		Dir:    cfg.RestoreDir(),
		FS:     hostsetup.OS{},
		Owner:  hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700},
		Logger: slog.Default(),
		Target: restoreTargets(cfg, os.ReadFile),
	})
}

// pluginOptions are the settings of the host the built-in plugins get from the
// configuration of the agent: they read none themselves.
func pluginOptions(cfg config.Config) []plugins.Option {
	var opts []plugins.Option
	if share := cfg.PostgresqlLockShare(); share != nil {
		opts = append(opts, plugins.WithPostgresLockShare(*share))
	}
	return opts
}

// restoreTargets describes the repositories of the host for the messages of
// a failed restore, with their secrets hidden.
func restoreTargets(cfg config.Config, readFile func(string) ([]byte, error)) func(name string) repoinit.Target {
	return func(name string) repoinit.Target {
		for _, repo := range cfg.Repositories {
			if repo.Name == name {
				return repoconnect.TargetReading(repo, readFile)
			}
		}
		return repoinit.Named(name)
	}
}

// checkHostFiles runs the checks of local files that need no network. A1:
// refuse to start with a secret file readable beyond its owner, or owned by
// someone else, before touching the network (В20); the key is the one of the
// selected pair. That the key belongs to the certificate (OQ-027) was
// settled by the selection.
func checkHostFiles(cfg config.Config, sel certpair.Selection) error {
	if sel.Pair.KeyFile != "" {
		cfg.TLS.KeyFile = sel.Pair.KeyFile
	}
	return secrets.CheckAll(cfg, uint32(os.Getuid()), secrets.RealStat)
}

// newAgentWith is buildAgent whose steps a test may run with its own handlers.
func newAgentWith(cfg config.Config, hostname, resticBinary, machineID string, restores *consolerestore.Engine, journal *pluginhost.Journal, rt agentRuntime, readOnly bool, extra ...pluginhost.Option) *app.Agent {
	agent := buildAgent(cfg, hostname, resticBinary, machineID, restores, journal, readOnly, extra...)
	if rt.handlers != nil {
		agent.Handlers = rt.handlers
	}
	return agent
}

// buildAgent wires the built-in plugins to restic and the host's secrets.
// A verifier (readOnly) opens every repository without lock and cache: it
// never writes to one, and restic takes a lock even to read the config.
func buildAgent(cfg config.Config, hostname, resticBinary, machineID string, restores *consolerestore.Engine, journal *pluginhost.Journal, readOnly bool, extra ...pluginhost.Option) *app.Agent {
	opts := resticOptions(cfg, resticBinary)
	opts.ReadOnly = readOnly
	repos := openRepositories(cfg, opts)
	registry := plugins.Registry(version, pluginOptions(cfg)...)
	handlerOpts := append([]pluginhost.Option{pluginhost.WithConsoleRestore(restores), pluginhost.WithRestoreJournal(journal), pluginhost.WithRepositoryWords(consolerestore.NewClassifier(restoreTargets(cfg, os.ReadFile))), pluginhost.WithLogger(slog.Default())}, extra...)
	return &app.Agent{
		Plugins:   registry,
		Handlers:  plugins.Handlers(registry, pluginhost.NewSecrets(cfg.Secrets, os.ReadFile), repos.get, restoreDir(cfg.Executor.StateDir), handlerOpts...),
		Hostname:  hostname,
		MachineID: machineID,
		Version:   version,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Local:     cfg,
		// Only the operator of the host says where the console may restore.
		RestoreDir: cfg.RestoreDir(),
		RepositoryID: func(ctx context.Context, r config.Repository) (string, error) {
			return repos[r.Name].ID(ctx)
		},
		Announced: recordAnnounced(executorStateDir(cfg.Executor.StateDir), slog.Default()),
		// V3: whether each repository has a recovery key, read again at every Register.
		RecoveryKey: recoveryKeyReader(repos, os.ReadFile, registerKeysLimit),
		Logger:      slog.Default(),
	}
}

// resticOptions run the shipped restic as this process; repository keys stay
// on this host (ADR 0008).
func resticOptions(cfg config.Config, resticBinary string) restic.Options {
	return restic.Options{
		Binary:   resticBinary,
		CacheDir: cfg.Restic.CacheDir,
		Path:     os.Getenv("PATH"),
		Exec:     restic.ProcessExecutor{},
		Keys:     crypto.NewResticAES(cfg.PasswordFiles()),
		ReadFile: os.ReadFile,
		Logger:   slog.Default(),
	}
}

// restoreDir holds restored copies, inside the executor's 0700 state dir
// (the executor reads only its own subdirectories).
func restoreDir(stateDir string) string {
	return filepath.Join(executorStateDir(stateDir), "restore")
}

// serve connects the agent and runs steps until ctx ends. The executor and
// the transport need each other: the transport feeds the executor commands,
// the executor reports through the transport. The transport is built first
// (it checks TLS before anything touches the disk); ref points it at the
// executor before it starts.
func serve(ctx context.Context, cfg config.Config, role role.Role, agent *app.Agent, recovery *recovery, renewal *certrenew.Manager, rt agentRuntime) error {
	ref := &executorRef{}
	// A7b: the step's secrets are masked in its logs and result.
	masking := stepsecrets.New(cfg, os.ReadFile, slog.Default())
	masking.Audit()
	link, err := transport.New(transport.Options{
		Address:  cfg.Server.Address,
		TLS:      cfg.TLS,
		Register: agent.RegisterRequest,
		Verifier: agent.VerifierChanged,
		Commands: ref,
		State:    ref,
		Logger:   slog.Default(),
		Clock:    rt.clock,
		Rand:     rt.rand,
		// The certificate is renewed after the first Register; a Register
		// refused because the certificate was replaced may go on with the
		// pair that replaced it (П17, П23).
		OnRegistered: renewal.Registered,
		OnSuperseded: renewal.Superseded,
	})
	if err != nil {
		return err
	}
	exec, err := executor.New(executor.Options{
		Handlers:     agent.Handlers,
		Sink:         link,
		StateDir:     executorStateDir(cfg.Executor.StateDir),
		Role:         role,
		Repositories: repositoryNames(cfg.Repositories),
		Secrets:      masking,
		OutputLevel:  pluginhost.OutputLevel,
		Interrupted:  recovery.interrupted,
		Journaled:    recovery.journaled,
		MaxParallel:  cfg.Executor.MaxParallel,
		Logger:       slog.Default(),
	})
	if err != nil {
		return err
	}
	recovery.executorStarted()
	ref.Executor = exec
	agent.Link = link
	err = runRenewing(ctx, renewal, agent.Run)
	return errors.Join(err, stopExecutor(exec))
}

// executorRef breaks the transport ↔ executor construction cycle.
type executorRef struct{ *executor.Executor }

// shutdownTimeout bounds the stop of running steps; it outlasts the
// executor's own 30 s of cancellation checks and stays under systemd's 90 s.
const shutdownTimeout = 45 * time.Second

// stopExecutor fails queued steps and cancels running ones (SHUTDOWN
// IMMEDIATE). Their results stay on disk and are sent on the next start.
func stopExecutor(exec *executor.Executor) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return exec.Close(ctx)
}

// defaultExecutorStateDir lives under the unit's StateDirectory=sard-agent,
// which systemd creates 0755; the executor makes its own subdirectory 0700.
const defaultExecutorStateDir = "/var/lib/sard-agent/executor"

func executorStateDir(configured string) string {
	if configured != "" {
		return configured
	}
	return defaultExecutorStateDir
}

// resticRepositories opens every configured repository with restic.
type resticRepositories map[string]*restic.CLI

func openRepositories(cfg config.Config, opts restic.Options) resticRepositories {
	repos := make(resticRepositories, len(cfg.Repositories))
	for _, r := range cfg.Repositories {
		repos[r.Name] = restic.New(opts, r)
	}
	return repos
}

// get returns the repository of a step: its stderr copied to the step's
// log, its runs of restic logged with the step's command_id.
func (r resticRepositories) get(name, commandID string, stderr io.Writer) (restic.Repository, bool) {
	repo, ok := r[name]
	if !ok {
		return nil, false
	}
	return repo.ForStep(commandID, stderr), true
}

func repositoryNames(repos []config.Repository) []string {
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	return names
}

// resticPath is restic.path, or the restic shipped next to sard-agent
// (docs/adr/0017-restic-shipped-with-agent.md).
func resticPath(configured string, executable func() (string, error)) (string, error) {
	if configured != "" {
		return configured, nil
	}
	self, err := executable()
	if err != nil {
		return "", fmt.Errorf("restic.path: %w", err)
	}
	// The restic next to the real file, not next to a symlink to it.
	if real, err := filepath.EvalSymlinks(self); err == nil {
		self = real
	}
	return filepath.Join(filepath.Dir(self), "restic"), nil
}

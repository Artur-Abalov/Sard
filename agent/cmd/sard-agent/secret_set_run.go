// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
)

// runSecretSet is "sard-agent secret set <name>": args excludes "set". The
// checks come in the order of Р14: flags, rights, name, config, conflicts,
// the value, lock, writing, audit, applying.
func runSecretSet(_ context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseSecretFlags("set", args, stderr, deps)
	if code != exitOK {
		return code
	}
	if f := hostsetup.CheckSources(sourceOptions(opts)); f != nil {
		return report(stderr, "secret set", f)
	}
	who, f := authorize(deps, "secret set", opts, true, true)
	if f != nil {
		return report(stderr, "secret set", f)
	}
	return newHostCmd("secret set", opts, who, stdout, stderr, deps).setSecret()
}

// setSecret is runSecretSet from the name on: the config, the conflicts,
// the value, then the lock and the writing.
func (c *hostCmd) setSecret() int {
	name := c.opts.name
	if f := firstFailure(hostsetup.CheckName("secret", name), c.knownPlugin(), c.load()); f != nil {
		return c.fail(f)
	}
	if _, f := hostsetup.PlanSecret(c.cfg, c.layout, name); f != nil {
		return c.fail(f)
	}
	value, f := c.readSource(sourceOptions(c.opts))
	if f != nil {
		return c.fail(f)
	}
	return c.setSecretLocked(name, value)
}

// knownPlugin: the secret is bound to a plugin of this agent (S11).
func (c *hostCmd) knownPlugin() *refusal.Failure {
	plugin := c.opts.binding.Plugin
	if _, ok := c.deps.plugins.Get(plugin); ok {
		return nil
	}
	return refusal.Fail(refusal.PluginUnknown, "the agent has no plugin %q to bind the secret to; it has: %s", plugin, strings.Join(c.deps.plugins.Names(), ", "))
}

// firstFailure is the first of the failures that is not nil.
func firstFailure(fs ...*refusal.Failure) *refusal.Failure {
	for _, f := range fs {
		if f != nil {
			return f
		}
	}
	return nil
}

// setSecretLocked takes the lock, looks at the config once more and
// writes.
func (c *hostCmd) setSecretLocked(name string, value []byte) int {
	var plan hostsetup.SecretPlan
	return c.underConfigLock(
		func() (f *refusal.Failure) { plan, f = hostsetup.PlanSecret(c.cfg, c.layout, name); return f },
		func() int { return c.storeSecret(name, value, plan) })
}

func (c *hostCmd) storeSecret(name string, value []byte, plan hostsetup.SecretPlan) int {
	s := secretWrite{
		valueFile: c.layout.SecretFile(name), fragment: c.layout.SecretFragment(name), bindingFile: c.layout.SecretBinding(name),
		value: value, binding: c.opts.binding.Marshal(),
	}
	s.yamlBody = hostsetup.SecretYAML(name, s.valueFile)
	s.sameValue = c.fileHolds(s.valueFile, value)
	if plan.OwnFragment && s.sameValue && c.fileHolds(s.fragment, s.yamlBody) && c.fileHolds(s.bindingFile, s.binding) {
		_, _ = fmt.Fprintf(c.stdout, "Secret %q unchanged: the value file %s already holds this value and the binding is the same.\n", name, s.valueFile)
		return exitOK
	}
	if err := c.writeSecret(s); err != nil {
		return c.fail(writeFailed(err))
	}
	action := secretAction(plan)
	c.record("secret", name, action)
	_, _ = fmt.Fprintf(c.stdout, "Secret %q %s.\n  value file: %s\n  defined in: %s\n  bound to:   %s\n  binding:    %s\n",
		name, action, s.valueFile, s.fragment, describeBinding(c.opts.binding), s.bindingFile)
	return c.finish()
}

// secretWrite are the files of a secret and what they are to hold.
type secretWrite struct {
	valueFile, fragment, bindingFile string
	value, yamlBody, binding         []byte
	sameValue                        bool
}

// describeBinding is the binding in a line: the plugin, its targets and what
// it allows beyond the plugin's rules.
func describeBinding(b secretbind.Binding) string {
	targets := make([]string, len(b.Targets))
	for i, t := range b.Targets {
		targets[i] = t.String()
	}
	line := b.Plugin + " at " + strings.Join(targets, ", ")
	if allowed := allowedWords(b); len(allowed) > 0 {
		line += " (" + strings.Join(allowed, "; ") + ")"
	}
	return line
}

// allowedWords say what the binding allows beyond the plugin's rules.
func allowedWords(b secretbind.Binding) []string {
	var out []string
	for _, w := range []struct {
		set  bool
		word string
	}{
		{len(b.Auth) > 0, "auth " + strings.Join(b.Auth, ",")},
		{b.AllowTLS != "", "tls " + b.AllowTLS},
		{b.CAFile != "", "CA " + b.CAFile},
		{b.AllowNoChannelBinding, "no channel binding"},
	} {
		if w.set {
			out = append(out, w.word)
		}
	}
	return out
}

// secretAction names what the command did to a secret it wrote.
func secretAction(plan hostsetup.SecretPlan) string {
	if plan.OwnFragment {
		return "updated"
	}
	return "added"
}

// finish applies the change: its failure is the command's.
func (c *hostCmd) finish() int {
	if f := c.apply(); f != nil {
		return c.fail(f)
	}
	return exitOK
}

// fileHolds says whether the file exists and holds exactly data.
func (c *hostCmd) fileHolds(path string, data []byte) bool {
	got, err := c.deps.readFile(path)
	return err == nil && bytes.Equal(got, data)
}

// writeSecret writes the value file, the binding, then the fragment (Р4,
// S11): until the fragment is there the agent does not see the secret, and
// it never sees it unbound. The binding is root's, as a fragment: the service
// does not change where its secrets go. An unchanged file is not rewritten.
func (c *hostCmd) writeSecret(s secretWrite) error {
	if _, err := hostsetup.EnsureDir(c.deps.fs, c.layout.SecretsDir(), c.serviceOwner(0o700)); err != nil {
		return err
	}
	if !s.sameValue {
		if err := hostsetup.WriteFile(c.deps.fs, s.valueFile, s.value, c.serviceOwner(0o600)); err != nil {
			return err
		}
	}
	if err := c.writeUnlessHeld(s.bindingFile, s.binding, c.layout.BindingDir()); err != nil {
		return err
	}
	return c.writeUnlessHeld(s.fragment, s.yamlBody, "")
}

// writeUnlessHeld writes a file of root's that the service reads, in dir if
// given (made like agent.d), unless it holds data already.
func (c *hostCmd) writeUnlessHeld(path string, data []byte, dir string) error {
	if c.fileHolds(path, data) {
		return nil
	}
	if dir != "" {
		if _, err := hostsetup.EnsureDir(c.deps.fs, dir, hostsetup.Attrs{UID: 0, GID: int(c.who.Service.GID), Mode: 0o750}); err != nil {
			return err
		}
	}
	return hostsetup.WriteFile(c.deps.fs, path, data, c.fragmentOwner())
}

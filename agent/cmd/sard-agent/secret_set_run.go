// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// secretPlan is what the config says about the secret a command is
// about to change.
type secretPlan struct {
	// ownFragment is true if the secret is defined in the fragment this
	// command writes.
	ownFragment bool
}

// planSecret is the decision of Р12: a name defined elsewhere is refused,
// and so is a value file that another key of the config uses.
func (c *hostCmd) planSecret(name string) (secretPlan, *refusal.Failure) {
	source := c.cfg.SecretSource(name)
	own := c.layout.SecretFragment(name)
	if source != "" && source != own {
		return secretPlan{}, refusal.Fail(refusal.DefinedInConfig, "secret %q is defined in %s; commands never change it, edit that file instead", name, source)
	}
	if key := hostsetup.ReferencedBy(c.cfg, c.layout.SecretFile(name), hostsetup.SecretKey(name)); key != "" {
		return secretPlan{}, refusal.Fail(refusal.PathInUse, "%s is used by %s and would be overwritten", c.layout.SecretFile(name), key)
	}
	return secretPlan{ownFragment: source == own}, nil
}

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
	if f := firstFailure(hostsetup.CheckName("secret", name), c.load()); f != nil {
		return c.fail(f)
	}
	if _, f := c.planSecret(name); f != nil {
		return c.fail(f)
	}
	value, f := c.readSource(sourceOptions(c.opts))
	if f != nil {
		return c.fail(f)
	}
	return c.setSecretLocked(name, value)
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
	var plan secretPlan
	return c.underConfigLock(
		func() (f *refusal.Failure) { plan, f = c.planSecret(name); return f },
		func() int { return c.storeSecret(name, value, plan) })
}

func (c *hostCmd) storeSecret(name string, value []byte, plan secretPlan) int {
	valueFile, fragment := c.layout.SecretFile(name), c.layout.SecretFragment(name)
	yamlBody := hostsetup.SecretYAML(name, valueFile)
	sameValue := c.fileHolds(valueFile, value)
	if plan.ownFragment && sameValue && c.fileHolds(fragment, yamlBody) {
		_, _ = fmt.Fprintf(c.stdout, "Secret %q unchanged: the value file %s already holds this value.\n", name, valueFile)
		return exitOK
	}
	if err := c.writeSecret(valueFile, fragment, value, yamlBody, sameValue); err != nil {
		return c.fail(writeFailed(err))
	}
	action := secretAction(plan)
	c.record("secret", name, action)
	_, _ = fmt.Fprintf(c.stdout, "Secret %q %s.\n  value file: %s\n  defined in: %s\n", name, action, valueFile, fragment)
	return c.finish()
}

// secretAction names what the command did to a secret it wrote.
func secretAction(plan secretPlan) string {
	if plan.ownFragment {
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

// writeSecret writes the value file, then the fragment (Р4): until the
// fragment is there the agent does not see the secret. An unchanged value
// file is not rewritten, an unchanged fragment neither.
func (c *hostCmd) writeSecret(valueFile, fragment string, value, yamlBody []byte, sameValue bool) error {
	if _, err := hostsetup.EnsureDir(c.deps.fs, c.layout.SecretsDir(), c.serviceOwner(0o700)); err != nil {
		return err
	}
	if !sameValue {
		if err := hostsetup.WriteFile(c.deps.fs, valueFile, value, c.serviceOwner(0o600)); err != nil {
			return err
		}
	}
	if c.fileHolds(fragment, yamlBody) {
		return nil
	}
	return hostsetup.WriteFile(c.deps.fs, fragment, yamlBody, c.fragmentOwner())
}

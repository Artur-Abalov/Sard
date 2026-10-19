// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

func isPermission(err error) bool { return errors.Is(err, fs.ErrPermission) }

// configLines are config.load and config.names: one reading of the config
// answers both. A name defined twice is not a failure to load, but there is
// no config to go on with either way.
func (c *hostCheck) configLines() {
	path := c.opts.configPath
	cfg, err := c.deps.loadConfig(path)
	fragments, _ := config.FragmentPaths(path)
	if err == nil {
		err = c.serviceCannotRead(path, fragments)
	}
	switch {
	case err == nil:
		c.cfg = cfg
		c.emit(hostcheck.ConfigLoadLine(path, len(fragments), nil))
		c.emit(hostcheck.ConfigNamesLine(nil))
	case hostcheck.IsDuplicateName(err):
		c.keepPartial(path)
		c.emit(hostcheck.ConfigLoadLine(path, len(fragments), nil))
		var dup *config.DuplicateNameError
		_ = errors.As(err, &dup)
		c.emit(hostcheck.ConfigNamesLine(dup))
	default:
		c.keepPartial(path)
		c.loadFailed(path, err)
	}
}

// loadFailed is config.load ending in an ERROR; for the service user a file it
// is refused is a matter of root (Ч20).
func (c *hostCheck) loadFailed(path string, err error) {
	if c.who.Role == hostsetup.RoleService && isPermission(err) {
		c.needsRoot = true
		c.emit(hostcheck.NeedsRootLine(hostcheck.CheckConfigLoad, "", "cannot read "+deniedPath(err, path)))
	} else {
		c.emit(hostcheck.ConfigLoadLine(path, 0, err))
	}
	c.emit(hostcheck.NotCheckedLine(hostcheck.CheckConfigNames, "", hostcheck.CheckConfigLoad))
	c.failed[hostcheck.CheckConfigLoad] = true
}

func deniedPath(err error, fallback string) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Path
	}
	return fallback
}

// keepPartial reads what it can of the main file: restic.path and the state
// dir are good for the checks that do not need the whole config.
func (c *hostCheck) keepPartial(path string) {
	if data, err := c.deps.readFile(path); err == nil {
		c.partial, _ = config.Parse(data)
	}
}

// serviceCannotRead: the config is read by root, but the service reads it as
// itself; the files it could not read are the failure of config.load (Ч20).
func (c *hostCheck) serviceCannotRead(path string, fragments []string) error {
	svc := c.who.Service
	if c.who.Role != hostsetup.RoleRoot || svc.Name == "" {
		return nil
	}
	dir := filepath.Join(filepath.Dir(path), config.FragmentDir)
	for _, f := range append([]string{path, dir}, fragments...) {
		if c.unreadableBy(svc, f, f == dir) {
			return &fs.PathError{Op: "read", Path: f, Err: errors.New("the service user " + svc.Name + " cannot read it")}
		}
	}
	return nil
}

// unreadableBy: the file exists, and by its permission bits the user cannot read it.
func (c *hostCheck) unreadableBy(user hostsetup.User, path string, isDir bool) bool {
	info, err := c.deps.stat(path)
	facts := hostcheck.FileFacts{Mode: info.Mode, UID: info.UID, GID: info.GID}
	return err == nil && !hostcheck.CanRead(facts, isDir, user.UID, user.GID)
}

func (c *hostCheck) references() hostcheck.Line {
	return hostcheck.ReferencesLine(hostcheck.References(c.cfg), func(path string) error {
		_, err := c.deps.stat(path)
		return err
	})
}

// permissions is files.permissions (Ч11): the facts of the files the service
// uses, judged by hostcheck.
func (c *hostCheck) permissions() hostcheck.Line {
	svc := hostcheck.ServiceUser{Name: c.who.Service.Name, UID: c.who.Service.UID}
	return hostcheck.PermissionsLine(svc, c.permFiles())
}

// permFiles are the secret files, the directories of the secrets and the
// files of the config that exist, with what stat says about them.
func (c *hostCheck) permFiles() []hostcheck.PermFile {
	layout := hostsetup.Layout{Config: c.opts.configPath}
	fragments, _ := config.FragmentPaths(c.opts.configPath)
	var files []hostcheck.PermFile
	add := func(kind hostcheck.PermKind, paths ...string) {
		for _, path := range paths {
			if info, err := c.deps.stat(path); err == nil && path != "" {
				files = append(files, hostcheck.PermFile{Path: path, Kind: kind, Facts: hostcheck.FileFacts{Mode: info.Mode, UID: info.UID, GID: info.GID}})
			}
		}
	}
	add(hostcheck.PermSecret, uniqueOf(secretFiles(c.cfg))...)
	add(hostcheck.PermSecretDir, layout.SecretsDir(), keyDir(c.cfg.TLS))
	add(hostcheck.PermConfig, c.opts.configPath, layout.FragmentDir())
	add(hostcheck.PermFragment, fragments...)
	return files
}

// keyDir is the directory of the key of the certificate, "" if the config has none.
func keyDir(tls config.TLS) string {
	if tls.KeyFile == "" {
		return ""
	}
	return filepath.Dir(tls.KeyFile)
}

// uniqueOf is the paths without the repeats, in their order.
func uniqueOf(paths []string) []string {
	var got []string
	for _, p := range paths {
		if !slices.Contains(got, p) {
			got = append(got, p)
		}
	}
	return got
}

// secretFiles are the secret files of A1 the config names, in order.
func secretFiles(cfg config.Config) []string {
	files := []string{cfg.TLS.KeyFile}
	for _, r := range cfg.Repositories {
		files = append(files, r.PasswordFile, r.EnvFile)
	}
	for _, name := range cfg.SecretNames() {
		files = append(files, cfg.Secrets[name])
	}
	return files
}

// tlsLines are tls.identity, tls.chain, tls.expiry and tls.role.
func (c *hostCheck) tlsLines() {
	tls := c.cfg.TLS
	c.step(hostcheck.CheckTLSIdentity, needConfig, func() hostcheck.Line {
		return c.guarded(hostcheck.CheckTLSIdentity, "", func() hostcheck.Line {
			return hostcheck.TLSIdentityLine(tls, c.cfg.Server.Address, c.read)
		})
	})
	c.step(hostcheck.CheckTLSChain, needConfig, func() hostcheck.Line {
		return c.guarded(hostcheck.CheckTLSChain, "", func() hostcheck.Line { return hostcheck.TLSChainLine(tls, c.now, c.read) })
	})
	c.step(hostcheck.CheckTLSExpiry, needConfig, func() hostcheck.Line {
		return c.guarded(hostcheck.CheckTLSExpiry, "", func() hostcheck.Line { return hostcheck.TLSExpiryLine(tls, c.now, c.read) })
	})
	c.emit(hostcheck.TLSRoleLine())
}

// secretLines is secret.readable for every secret of the config (Ч17).
func (c *hostCheck) secretLines() {
	if failed := c.configBlock(); failed != "" {
		c.emit(hostcheck.NotCheckedLine(hostcheck.CheckSecretReadable, "", failed))
		return
	}
	for _, name := range c.cfg.SecretNames() {
		path := c.cfg.Secrets[name]
		if c.failed[hostcheck.CheckServiceUser] {
			c.emit(hostcheck.NotCheckedLine(hostcheck.CheckSecretReadable, name, hostcheck.CheckServiceUser))
			continue
		}
		c.emit(c.guarded(hostcheck.CheckSecretReadable, name, func() hostcheck.Line { return c.secretLine(name, path) }))
	}
}

// secretLine reads the secret as the service does and says only whether it
// can be read; the value is neither printed nor kept.
func (c *hostCheck) secretLine(name, path string) hostcheck.Line {
	data, err := c.deps.readOwned(path, c.who.Service.UID)
	c.noteDenied(path, err)
	return hostcheck.SecretLine(name, path, len(data), err)
}

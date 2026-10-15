// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// repoChecks are the identifiers of the checks of one repository, in order.
var repoChecks = []string{hostcheck.CheckRepositoryStorage, hostcheck.CheckRepositoryCredentials, hostcheck.CheckRepositoryInitialized, hostcheck.CheckRepositoryPassword, hostcheck.CheckRepositoryID, hostcheck.CheckRepositoryLocks}

// repositoryLines are the six checks of every repository (Ч3), the same
// steps as repo check without --read-data. Offline none is made, and the
// record of the ids the service announced is not read (Ч32): the id to
// compare it with comes from the storage.
func (c *hostCheck) repositoryLines() {
	if failed := c.configBlock(); failed != "" {
		c.skipRepositories("", failed)
		return
	}
	if len(c.cfg.Repositories) == 0 {
		c.emit(noRepositoriesLine())
		return
	}
	for index, repo := range c.cfg.Repositories {
		if c.ctx.Err() != nil {
			return
		}
		c.repositoryChecks(index, repo.Name)
	}
}

// repositoryChecks makes the checks of one repository, or leaves them out:
// offline, or because the service user or restic is not good.
func (c *hostCheck) repositoryChecks(index int, name string) {
	failed := c.blocked([]string{hostcheck.CheckServiceUser, hostcheck.CheckResticBinary})
	switch {
	case failed != "":
		c.skipRepositories(name, failed)
	case c.opts.offline:
		for _, check := range repoChecks {
			c.emit(hostcheck.OfflineLine(check, name))
		}
	default:
		c.checkRepository(index)
	}
}

func (c *hostCheck) skipRepositories(subject, failed string) {
	for _, check := range repoChecks {
		c.emit(hostcheck.NotCheckedLine(check, subject, failed))
	}
}

// checkRepository runs the steps of repo check for one repository, within the
// time --timeout allows (2 minutes here).
func (c *hostCheck) checkRepository(index int) {
	opts := c.opts
	opts.timeout, opts.readData = defaultRepoTimeout, 0
	rc := &repoCheck{ctx: c.ctx, opts: opts, who: c.who, deps: c.deps, cfg: c.cfg, binary: c.binary, printer: c.printer}
	rc.checkOne(index)
}

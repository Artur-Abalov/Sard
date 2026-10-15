// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew

import (
	"context"
	"errors"
	"fmt"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/transport"
)

// ErrDiscarded: the server refused the renewed pair for good (П20), so its
// ".new" files were removed and the current pair stays.
var ErrDiscarded = errors.New("the server refused the renewed key and certificate; they were removed")

// Adopt confirms the selected pair that still has ".new" files (certpair.Select)
// and renames it into place. Until the server answers the agent registers with
// no pair: a temporary refusal is repeated at the reconnect delays of the
// transport until ctx ends (ctx's error); a final one removes the ".new"
// files (ErrDiscarded) (П21).
func (m *Manager) Adopt(ctx context.Context, sel certpair.Selection) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.adopt(ctx, sel)
}

func (m *Manager) adopt(ctx context.Context, sel certpair.Selection) error {
	for attempt := 0; ; attempt++ {
		err := m.o.Caller.Confirm(ctx, sel.Pair.CertFile, sel.Pair.KeyFile)
		if err == nil {
			return certpair.Promote(m.o.FS, m.o.Files, sel)
		}
		if finalRefusal(err) {
			removed := certpair.Discard(m.o.FS, m.o.Files)
			m.o.Logger.Warn("the server refused the renewed certificate; removed it", "files", removed, "error", transport.Describe(err))
			return fmt.Errorf("%w: %s", ErrDiscarded, transport.Describe(err))
		}
		m.o.Logger.Warn("confirming the renewed certificate failed", "error", transport.Describe(err))
		if !m.sleep(ctx, transport.Backoff(attempt, m.o.Rand)) {
			return ctx.Err()
		}
	}
}

// Superseded is the transport's answer to a Register refused with
// CERT_SUPERSEDED (П23): true when the agent now holds another pair and should
// reconnect at once. That is the case when a renewed pair lies next to the
// current one and the server confirms it, or when this process has renamed a
// renewed pair into place since the last such refusal.
func (m *Manager) Superseded(ctx context.Context) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	sel, err := certpair.Select(m.o.Files, m.o.FS, m.o.Clock.Now())
	if err == nil && sel.Pending() {
		return m.adopt(ctx, sel) == nil
	}
	if m.renamed == m.reported {
		return false
	}
	m.reported = m.renamed
	return true
}

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certpair

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/identity"
	"github.com/Artur-Abalov/sard/agent/internal/tlsid"
)

// ExpiredError: every consistent pair has expired by the agent's clock (П22).
type ExpiredError struct {
	// CertFile is tls.cert_file; NotAfter is when its certificate expired.
	CertFile string
	NotAfter time.Time
}

func (e *ExpiredError) Error() string {
	return fmt.Sprintf("CERT_EXPIRED: tls.cert_file %q expired at %s", e.CertFile, e.NotAfter.UTC().Format(time.RFC3339))
}

// candidate is one of the four ways to combine the files.
type candidate struct {
	pair      Pair
	newFiles  []string
	keyIsNew  bool
	certIsNew bool
	cert      Cert
	expired   bool
}

// Select chooses the pair to use at start (П21): among the pairs of
// {key, key.new} x {cert, cert.new} whose files parse, whose key belongs to
// the certificate and whose certificate names the same agent as tls.cert_file,
// the one not expired by now with the latest NotBefore; the current pair
// wins a tie. Renewal files that belong to no such pair are listed as stale.
//
// Without any consistent pair the error is the one of the current pair (the
// incomplete enrollment of agent-tls-identity.feature); when they are all
// expired, Selection.Expired is set and Pair is the current pair.
func Select(files config.TLS, fsys FS, now time.Time) (Selection, error) {
	if files.CertFile == "" || files.KeyFile == "" {
		return Selection{Pair: Pair{KeyFile: files.KeyFile, CertFile: files.CertFile}}, nil
	}
	owner, err := agentOf(files.CertFile, fsys)
	if err != nil {
		return Selection{}, err
	}
	var consistent []candidate
	for _, c := range candidates(files) {
		if err := c.read(owner, fsys, now); err == nil {
			consistent = append(consistent, c)
		}
	}
	return decide(files, fsys, now, consistent)
}

func decide(files config.TLS, fsys FS, now time.Time, consistent []candidate) (Selection, error) {
	if len(consistent) == 0 {
		return Selection{}, currentPairError(files, fsys)
	}
	var best *candidate
	for i := range consistent {
		c := &consistent[i]
		if !c.expired && (best == nil || c.cert.NotBefore.After(best.cert.NotBefore)) {
			best = c
		}
	}
	if best == nil {
		return expiredSelection(files, fsys), nil
	}
	return Selection{
		Pair: best.pair, KeyIsNew: best.keyIsNew, CertIsNew: best.certIsNew, Cert: best.cert,
		Stale: stale(files, fsys, consistent),
	}, nil
}

// currentPairError is why the current pair is not a pair of its own.
func currentPairError(files config.TLS, fsys FS) error {
	if err := tlsid.Check(files, fsys.ReadFile); err != nil {
		return err
	}
	return tlsid.ErrIncompleteEnrollment
}

func expiredSelection(files config.TLS, fsys FS) Selection {
	cert, _ := tlsid.ReadCertificate(files.CertFile, fsys.ReadFile)
	return Selection{
		Pair:    Pair{KeyFile: files.KeyFile, CertFile: files.CertFile},
		Expired: &ExpiredError{CertFile: files.CertFile, NotAfter: cert.NotAfter},
	}
}

// agentOf is the agent tls.cert_file names; the other pairs must name the same.
func agentOf(certFile string, fsys FS) (string, error) {
	id, err := identity.Read(certFile, fsys.ReadFile)
	var roleErr *identity.RoleError
	if err != nil && !errors.As(err, &roleErr) {
		return "", err
	}
	return id.TenantID + "/" + id.AgentID, nil
}

func candidates(files config.TLS) []candidate {
	keyNew, certNew := files.KeyFile+NewSuffix, files.CertFile+NewSuffix
	return []candidate{
		{pair: Pair{files.KeyFile, files.CertFile}},
		{pair: Pair{keyNew, certNew}, newFiles: []string{keyNew, certNew}, keyIsNew: true, certIsNew: true},
		{pair: Pair{files.KeyFile, certNew}, newFiles: []string{certNew}, certIsNew: true},
		{pair: Pair{keyNew, files.CertFile}, newFiles: []string{keyNew}, keyIsNew: true},
	}
}

// read fills c from its files; an error means the pair is not consistent.
func (c *candidate) read(owner string, fsys FS, now time.Time) error {
	files := config.TLS{KeyFile: c.pair.KeyFile, CertFile: c.pair.CertFile}
	if err := tlsid.Check(files, fsys.ReadFile); err != nil {
		return err
	}
	cert, err := tlsid.ReadCertificate(c.pair.CertFile, fsys.ReadFile)
	if err != nil {
		return err
	}
	who, err := agentOf(c.pair.CertFile, fsys)
	if err != nil || who != owner {
		return errors.New("another agent")
	}
	c.cert = Cert{Serial: cert.SerialNumber.Text(16), NotBefore: cert.NotBefore.UTC(), NotAfter: cert.NotAfter.UTC()}
	c.expired = !now.Before(cert.NotAfter)
	return nil
}

// stale are the renewal files in the directories of the pair that no usable
// pair needs: a usable pair that was not selected keeps its files, because
// the next renewal overwrites them.
func stale(files config.TLS, fsys FS, consistent []candidate) []string {
	keep := map[string]bool{}
	for _, c := range consistent {
		if !c.expired {
			for _, f := range c.newFiles {
				keep[f] = true
			}
		}
	}
	var out []string
	for _, dir := range dirsOf(files) {
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			if isRenewalFile(files, path) && !keep[path] {
				out = append(out, path)
			}
		}
	}
	return out
}

func isRenewalFile(files config.TLS, path string) bool {
	return path == files.KeyFile+NewSuffix || path == files.CertFile+NewSuffix ||
		strings.HasPrefix(filepath.Base(path), TempPrefix)
}

func dirsOf(files config.TLS) []string {
	keyDir, certDir := filepath.Dir(files.KeyFile), filepath.Dir(files.CertFile)
	if keyDir == certDir {
		return []string{keyDir}
	}
	return []string{keyDir, certDir}
}

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// The words and numbers of the lines, asserted whole: a line is what the
// operator reads, so a part of it that goes missing is a defect.

func TestTheRecordOfAnnouncedRepositoriesCarriesItsVersion(t *testing.T) {
	dir := t.TempDir()
	if err := hostcheck.WriteAnnounced(dir, []hostcheck.AnnouncedRepository{{Name: "base", RepositoryID: "A"}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, hostcheck.AnnouncedFile))
	if want := `{"version":1,"repositories":[{"name":"base","repository_id":"A"}]}`; string(data) != want {
		t.Fatalf("record %s, want %s", data, want)
	}
}

func TestAnotherRepositoryThanTheServerKnowsSaysWhatToDo(t *testing.T) {
	l := hostcheck.IDLine("base", "REPO-A", []hostcheck.AnnouncedRepository{{Name: "base", RepositoryID: "REPO-B"}}, nil)
	if want := "sudo systemctl restart sard-agent, and check the address of the repository"; l.Hint != want {
		t.Fatalf("hint %q, want %q", l.Hint, want)
	}
}

type bareError struct{}

func (bareError) Error() string { return "bare" }
func (bareError) Unwrap() error { return nil }

func TestTheReasonOfAReferenceThatCannotBeLookedAtIsTheAnswerOfTheSystemWithoutThePath(t *testing.T) {
	refs := []hostcheck.Ref{{Key: "secrets.pg", Path: "/s/pg", Fix: "fix"}}
	for _, c := range []struct {
		err  error
		want string
	}{
		{&fs.PathError{Op: "stat", Path: "/s/pg", Err: syscall.EACCES}, "secrets.pg: /s/pg (permission denied)"},
		{bareError{}, "secrets.pg: /s/pg (bare)"},
		{errors.New("plain"), "secrets.pg: /s/pg (plain)"},
	} {
		l := hostcheck.ReferencesLine(refs, func(string) error { return c.err })
		if l.Message != c.want {
			t.Errorf("message %q, want %q", l.Message, c.want)
		}
	}
}

func TestAStaleLockIsDescribedWithEveryFactAboutIt(t *testing.T) {
	excl := lock("db1", 30*h, 5)
	excl.Exclusive = true
	for _, c := range []struct {
		name  string
		env   hostcheck.LockEnv
		lock  hostcheck.Lock
		wants string
	}{
		{"exclusive and dead", env(), excl, "stale lock of db1, 30h old, exclusive"},
		{"exclusive and alive", env(5), excl, "stale lock of db1, 30h old, exclusive, held by running process 5"},
		{"shared and alive", env(5), lock("db1", 30*h, 5), "stale lock of db1, 30h old, held by running process 5"},
		{"foreign and alive here", env(5), lock("db2", 30*h, 5), "stale lock of db2, 30h old"},
		{"own and dead", env(), lock("db1", 30*h, 5), "stale lock of db1, 30h old"},
	} {
		if l := hostcheck.LockLine("base", c.env, []hostcheck.Lock{c.lock}, 0); l.Message != c.wants {
			t.Errorf("%s: %q, want %q", c.name, l.Message, c.wants)
		}
	}
}

func TestTheSameCommandAndTheSameHostAreNamedOnce(t *testing.T) {
	foreign := hostcheck.LockLine("base", env(), []hostcheck.Lock{lock("db2", 30*h, 1), lock("db2", 40*h, 2)}, 0)
	if want := "remove the lock on db2 with the same command (sudo sard-agent repo unlock base --remove-stale), not here: the agent never removes locks of other hosts"; foreign.Hint != want {
		t.Errorf("hint %q, want %q", foreign.Hint, want)
	}
	own := hostcheck.LockLine("base", env(), []hostcheck.Lock{lock("db1", 30*h, 1), lock("db1", 40*h, 2)}, 0)
	if want := "sudo sard-agent repo unlock base --remove-stale"; own.Hint != want {
		t.Errorf("hint %q, want %q", own.Hint, want)
	}
}

func TestLocksAreNamedByHostAndAgeInOrder(t *testing.T) {
	got := hostcheck.DescribeLocks(env(), []hostcheck.Lock{lock("db1", 30*h, 1), lock("db2", 2*h, 2), lock("db3", 5*time.Minute, 3)})
	if want := "db1 30h, db2 2h, db3 5m"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

func TestResticRemovesALockOnlyWhenItIsOlderThanThirtyMinutes(t *testing.T) {
	at := func(d time.Duration, host string, pid int) []hostcheck.Lock {
		return []hostcheck.Lock{lock(host, d, pid)}
	}
	for _, c := range []struct {
		name    string
		lock    []hostcheck.Lock
		foreign int
	}{
		{"foreign at exactly thirty minutes", at(30*time.Minute, "db2", 1), 0},
		{"foreign a second beyond it", at(30*time.Minute+time.Second, "db2", 1), 1},
		{"foreign a second short of it", at(30*time.Minute-time.Second, "db2", 1), 0},
		{"own, young, process dead", at(10*time.Minute, "db1", 1), 1},
		{"own, young, process alive", at(10*time.Minute, "db1", 2), 0},
	} {
		if plan := hostcheck.PlanUnlock(env(2), c.lock); len(plan.Foreign) != c.foreign || len(plan.Remove) != 0 {
			t.Errorf("%s: %+v", c.name, plan)
		}
	}
}

func TestAFileOfNoKnownKindIsJudgedAsRootsAndNotAsASecret(t *testing.T) {
	// owned by the service user, not writable by others: a secret would be fine, a setting of root's is not
	l := hostcheck.PermissionsLine(svc, []hostcheck.PermFile{{Path: "/x", Facts: facts(0o640, 990)}})
	if l.Status != hostcheck.Warn || !strings.Contains(l.Hint, "sudo chown root /x") {
		t.Errorf("%+v", l)
	}
}

func TestAFileOfRootsThatItsOwnerCannotWriteIsNotAFault(t *testing.T) {
	// owned by another user who cannot write it: nobody but root can change it
	l := hostcheck.PermissionsLine(svc, []hostcheck.PermFile{{Path: "/c.yaml", Kind: hostcheck.PermConfig, Facts: facts(0o440, 990)}})
	if l.Status != hostcheck.OK || l.Hint != "" {
		t.Errorf("%+v", l)
	}
}

func TestEveryWayAFileOfASecretCannotBeUsedHasItsOwnWords(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&fs.PathError{Op: "open", Path: "/s/pg", Err: syscall.ELOOP}, "/s/pg is a symbolic link; the service does not follow links"},
		{&fs.PathError{Op: "open", Path: "/s/pg", Err: fs.ErrNotExist}, "/s/pg does not exist"},
		{&fs.PathError{Op: "open", Path: "/s/pg", Err: fs.ErrPermission}, "/s/pg cannot be read: permission denied"},
		{errors.New("secret file /s/pg is not a regular file"), "/s/pg is not a regular file"},
		{&fs.PathError{Op: "read", Path: "/s/pg", Err: syscall.EIO}, "/s/pg cannot be used: input/output error"},
	} {
		if l := hostcheck.SecretLine("pg", "/s/pg", 0, c.err); l.Message != c.want {
			t.Errorf("%q, want %q", l.Message, c.want)
		}
	}
}

func TestADenialIsDescribedWithItsPermissionProgramAndFields(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	d := hostcheck.ParseDenials(avc("restic", 7, now.Unix()), now.Add(-time.Hour))
	want := "restic was denied read name=pg7 scontext=system_u:system_r:sard_t:s0 tcontext=system_u:object_r:tmp_t:s0"
	if d.Total != 1 || d.First[0] != want {
		t.Fatalf("%+v, want %q", d, want)
	}
}

func TestLinesThatAreNotDenialsOfTheAgentAreLeftAlone(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	since := now.Add(-time.Hour)
	for name, line := range map[string]string{
		"no denial":      `type=SYSCALL msg=audit(1792000000.1:5): comm="restic" exit=0`,
		"no program":     `type=AVC msg=audit(1792000000.1:5): avc:  denied  { read } for pid=1 name="x"`,
		"another comm":   `type=AVC msg=audit(1792000000.1:5): avc:  denied  { read } for pid=1 comm="cat" name="x"`,
		"empty":          ``,
		"neither of two": `restic`,
	} {
		if d := hostcheck.ParseDenials(line, since); d.Total != 0 {
			t.Errorf("%s: %+v", name, d)
		}
	}
	noTime := `type=AVC avc:  denied  { read } for pid=1 comm="restic" name="x"`
	if d := hostcheck.ParseDenials(noTime, since); d.Total != 1 {
		t.Errorf("a record without a time is counted: %+v", d)
	}
}

func TestADenialAtTheMomentOfSinceIsCountedAndOneBeforeItIsNot(t *testing.T) {
	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	line := avc("restic", 1, at.Unix())
	for name, c := range map[string]struct {
		since time.Time
		total int
	}{
		"at since":         {at, 1},
		"a nanosecond old": {at.Add(time.Nanosecond), 0},
		"a second old":     {at.Add(time.Second), 0},
		"just after":       {at.Add(-time.Nanosecond), 1},
	} {
		if d := hostcheck.ParseDenials(line, c.since); d.Total != c.total {
			t.Errorf("%s: %+v", name, d)
		}
	}
}

func TestARefusalNamesTheReasonAfterTheWords(t *testing.T) {
	f := serverFacts()
	f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerRefused, Reason: "agent revoked"}
	if l := hostcheck.ServerAcceptedLine(f); l.Message != "the server refuses this agent: agent revoked" {
		t.Errorf("%q", l.Message)
	}
}

func TestAnAddressWithoutAPortIsTheNameOfTheHost(t *testing.T) {
	f := serverFacts()
	f.Address = "sard.example.org"
	f.TLS = hostcheck.Failure{Kind: hostcheck.FailName}
	if l := hostcheck.ServerTLSLine(f); l.Message != "the name in the certificate of the server does not match sard.example.org" {
		t.Errorf("%q", l.Message)
	}
}

func TestAServiceThatRestartedOnceIsAWarning(t *testing.T) {
	s, _ := hostcheck.ParseShow(strings.Replace(showActive, "NRestarts=0", "NRestarts=1", 1))
	if l := hostcheck.ServiceRunningLine(s, tlsNow); l.Status != hostcheck.Warn {
		t.Errorf("%+v", l)
	}
}

func TestKeysOfSystemctlThatAreNotNeededAreIgnored(t *testing.T) {
	s, err := hostcheck.ParseShow("Id=sard-agent.service\nnot a pair\n\nActiveState=active\nMainPID=1\nSubState=running\n")
	if err != nil || s.ActiveState != "active" || s.SubState != "running" {
		t.Errorf("%+v, %v", s, err)
	}
}

// --- tls

func certFile(parts ...[]byte) []byte { return slices.Concat(parts...) }

func TestTheDaysLeftAreTheDaysOfAValidCertificate(t *testing.T) {
	l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow, validPKI(t, 200*24*time.Hour).read)
	if want := "the certificate is valid until 2027-05-02 (200 days)"; l.Message != want {
		t.Errorf("%q, want %q", l.Message, want)
	}
	l = hostcheck.TLSExpiryLine(tlsFiles, tlsNow, newPKI(t, tlsNow.Add(-364*24*time.Hour), tlsNow.Add(36*time.Hour)).read)
	want := "the certificate expires in 1 days (2026-10-16) and has not been renewed: see renewal_last_error in sudo sard-agent status"
	if l.Message != want {
		t.Errorf("%q, want %q", l.Message, want)
	}
}

func TestTheExpiryIsJudgedAtTheLastMomentOfTheCertificate(t *testing.T) {
	p := newPKI(t, tlsNow.Add(-355*24*time.Hour), tlsNow.Add(10*24*time.Hour))
	end := tlsNow.Add(10 * 24 * time.Hour)
	for name, c := range map[string]struct {
		now    time.Time
		status hostcheck.Status
	}{
		"at the end":            {end, hostcheck.Error},
		"a nanosecond before":   {end.Add(-time.Nanosecond), hostcheck.Warn},
		"a second before":       {end.Add(-time.Second), hostcheck.Warn},
		"a nanosecond after it": {end.Add(time.Nanosecond), hostcheck.Error},
	} {
		if l := hostcheck.TLSExpiryLine(tlsFiles, c.now, p.read); l.Status != c.status {
			t.Errorf("%s: %+v", name, l)
		}
	}
}

func TestAFileThatCannotBeReadIsReportedWithTheReason(t *testing.T) {
	boom := func(string) ([]byte, error) { return nil, errors.New("boom") }
	if l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow, boom); l.Message != "/tls/agent.pem: boom" {
		t.Errorf("%q", l.Message)
	}
}

func TestAFileWithoutACertificateSaysSo(t *testing.T) {
	garbage := func(string) ([]byte, error) { return []byte("garbage"), nil }
	if l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow, garbage); l.Message != "/tls/agent.pem: no certificate in the file" {
		t.Errorf("%q", l.Message)
	}
}

func TestACertificateThatCannotBeParsedSaysSo(t *testing.T) {
	broken := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("not der")})
	l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow, func(string) ([]byte, error) { return broken, nil })
	if l.Status != hostcheck.Error || !strings.HasPrefix(l.Message, "/tls/agent.pem: a certificate that cannot be parsed: ") {
		t.Errorf("%q", l.Message)
	}
}

func TestBlocksOfAPEMFileThatAreNoCertificatesAreSkipped(t *testing.T) {
	p := validPKI(t, 200*24*time.Hour)
	mixed := certFile(p.keyPEM, p.certPEM)
	l := hostcheck.TLSExpiryLine(tlsFiles, tlsNow, func(string) ([]byte, error) { return mixed, nil })
	if l.Status != hostcheck.OK {
		t.Errorf("%+v", l)
	}
}

// intermediate issues a CA certificate, signed by the parent.
func intermediate(t *testing.T, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, name string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: name},
		NotBefore: tlsNow.Add(-1000 * 24 * time.Hour), NotAfter: tlsNow.Add(1000 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert, key
}

func TestACertificateIssuedThroughIntermediateCAsIsOK(t *testing.T) {
	root, rootKey := newCA(t, "root")
	one, oneKey := intermediate(t, root, rootKey, "one")
	two, twoKey := intermediate(t, one, oneKey, "two")
	leaf, _ := issue(t, two, twoKey, tlsNow.Add(-time.Hour), tlsNow.Add(time.Hour))
	pemOf := func(c *x509.Certificate) []byte {
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	chain := certFile(leaf, pemOf(two), pemOf(one))
	l := hostcheck.TLSChainLine(tlsFiles, tlsNow, func(name string) ([]byte, error) {
		if name == "/tls/ca.pem" {
			return pemOf(root), nil
		}
		return chain, nil
	})
	if l.Status != hostcheck.OK {
		t.Errorf("%+v", l)
	}
}

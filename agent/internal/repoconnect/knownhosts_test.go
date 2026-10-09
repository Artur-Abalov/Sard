// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect_test

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
)

// wire is an OpenSSH public key blob of the type with some payload.
func wire(keyType string, payload byte) string {
	var b []byte
	for _, part := range []string{keyType, strings.Repeat(string([]byte{payload}), 32)} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(part)))
		b = append(b, part...)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// fingerprintOf is what ssh-keygen -l prints for a key: SHA256 of the blob
// in unpadded base64.
func fingerprintOf(blob string) string {
	raw, _ := base64.StdEncoding.DecodeString(blob)
	sum := sha256.Sum256(raw)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

var (
	edKey    = repoconnect.HostKey{Type: "ssh-ed25519", Blob: wire("ssh-ed25519", 'a')}
	oldEd    = repoconnect.HostKey{Type: "ssh-ed25519", Blob: wire("ssh-ed25519", 'z')}
	ecKey    = repoconnect.HostKey{Type: "ecdsa-sha2-nistp256", Blob: wire("ecdsa-sha2-nistp256", 'b')}
	rsaKey   = repoconnect.HostKey{Type: "ssh-rsa", Blob: wire("ssh-rsa", 'c')}
	nasHost  = "nas.example.com"
	nasPort  = "[nas.example.com]:2222"
	otherKey = repoconnect.HostKey{Type: "ssh-ed25519", Blob: wire("ssh-ed25519", 'o')}
	wildKey  = repoconnect.HostKey{Type: "ssh-ed25519", Blob: wire("ssh-ed25519", 'w')}
)

func line(host string, k repoconnect.HostKey) string {
	return host + " " + k.Type + " " + k.Blob + "\n"
}

func TestTheFingerprintOfAKeyIsSHA256OfItsBlobInUnpaddedBase64(t *testing.T) {
	fp := edKey.Fingerprint()
	if fp != fingerprintOf(edKey.Blob) || len(fp) != len("SHA256:")+43 {
		t.Fatalf("fingerprint %q", fp)
	}
}

func TestTheKeysOfASSHKeyscanAreReadWithoutItsComments(t *testing.T) {
	out := "# nas.example.com:22 SSH-2.0-OpenSSH_9.2p1\n" + line(nasHost, edKey) + "# nas.example.com:22 SSH-2.0-OpenSSH_9.2p1\n" +
		line(nasHost, ecKey) + "\nnot a key line\n" + nasHost + " ssh-rsa !!!notbase64!!!\n"
	keys := repoconnect.ParseKeyscan(out)
	if len(keys) != 2 || keys[0] != edKey || keys[1] != ecKey {
		t.Fatalf("keys %+v", keys)
	}
	if got := repoconnect.ParseKeyscan("# only comments\n"); len(got) != 0 {
		t.Fatalf("keys %+v", got)
	}
}

func TestOnlyAFingerprintOfSHA256AndFortyThreeCharactersIsAccepted(t *testing.T) {
	good := edKey.Fingerprint()
	if f := repoconnect.CheckFingerprint(good); f != nil {
		t.Fatal(f)
	}
	for _, bad := range []string{
		"MD5:16:27:ac:a5:76:28:2d:36:63:1b:56:4d:eb:df:a6:48",
		"SHA256:short",
		good + "=",
		strings.TrimPrefix(good, "SHA256:"),
		"SHA256:" + strings.Repeat("!", 43),
		"",
	} {
		f := repoconnect.CheckFingerprint(bad)
		if f == nil || f.Class != refusal.ClassUsage || !strings.Contains(f.Detail, "--host-key-fingerprint") || !strings.Contains(f.Detail, "SHA256:") {
			t.Errorf("%q: %+v", bad, f)
		}
	}
}

// Р40: the entries of known_hosts for a host.
func TestKnownHostsFindsPlainListedHashedAndPortEntriesOfAHostAndNoOthers(t *testing.T) {
	hashed := hashedName("nas.example.com", "c2FsdHNhbHRzYWx0c2FsdHM=")
	content := "# a comment\n" +
		line("other.example.com", edKey) + // 2
		line(nasHost, oldEd) + // 3
		line("a.example.com,"+nasHost+",10.0.0.5", ecKey) + // 4
		hashed + " " + rsaKey.Type + " " + rsaKey.Blob + "\n" + // 5
		line(nasPort, otherKey) + // 6
		line("*.example.com", wildKey) + // 7
		"@revoked " + line(nasHost, edKey) + // 8
		"@cert-authority " + line(nasHost, edKey) + // 9
		line("!"+nasHost+",*.example.com", otherKey) + // 10
		line("NAS.Example.COM", rsaKey) + // 11
		line("!other.example.com,"+nasHost, rsaKey) // 12 (a negation of another host does not matter)
	m := repoconnect.FindKnown([]byte(content), nasHost)
	if !slices.Equal(m.Lines, []int{3, 4, 5, 7, 11, 12}) {
		t.Fatalf("lines %v", m.Lines)
	}
	for _, k := range []repoconnect.HostKey{rsaKey, ecKey, oldEd, wildKey} {
		if !m.Matches([]repoconnect.HostKey{k}) {
			t.Errorf("the key %s of an entry is not matched", k.Type)
		}
	}
	for _, keys := range [][]repoconnect.HostKey{{edKey}, {otherKey}, nil} {
		if m.Matches(keys) {
			t.Errorf("the keys %v that are no entry's are matched", keys)
		}
	}
}

func TestKnownHostsFindsTheEntriesOfAPortOnly(t *testing.T) {
	content := line(nasHost, oldEd) + line(nasPort, otherKey) + line("*.example.com", wildKey)
	port := repoconnect.FindKnown([]byte(content), nasPort)
	if !slices.Equal(port.Lines, []int{2}) || !port.Matches([]repoconnect.HostKey{otherKey}) {
		t.Errorf("the entries of the port: %+v", port)
	}
}

// hashedName is the |1|salt|hash form ssh writes with HashKnownHosts.
func hashedName(name, salt string) string {
	raw, _ := base64.StdEncoding.DecodeString(salt)
	mac := hmac.New(sha1.New, raw)
	mac.Write([]byte(name))
	return "|1|" + salt + "|" + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestAHashedEntryOfAnotherHostIsNotTheHostsAndABrokenOneIsSkipped(t *testing.T) {
	content := hashedName("other.example.com", "c2FsdHNhbHRzYWx0c2FsdHM=") + " " + edKey.Type + " " + edKey.Blob + "\n" +
		"|1|!!!|!!! " + edKey.Type + " " + edKey.Blob + "\n" +
		"|2|x|y " + edKey.Type + " " + edKey.Blob + "\n" +
		"|1|only-one-part " + edKey.Type + " " + edKey.Blob + "\n"
	if m := repoconnect.FindKnown([]byte(content), nasHost); len(m.Lines) != 0 {
		t.Fatalf("lines %v", m.Lines)
	}
}

func TestMalformedLinesOfKnownHostsAreNobodysEntries(t *testing.T) {
	content := "nas.example.com\n" + "nas.example.com ssh-ed25519\n" + "nas.example.com ssh-ed25519 !!!\n" + "   \n" + "\t# indented comment\n"
	if m := repoconnect.FindKnown([]byte(content), nasHost); len(m.Lines) != 0 {
		t.Fatalf("lines %v", m.Lines)
	}
}

func TestATrustedKeyIsAppendedAsOneLineAndTheRestStaysByteForByte(t *testing.T) {
	old := "other.example.com ssh-ed25519 AAAA comment\n\n# keep\n"
	got := repoconnect.AddHostKey([]byte(old), nasHost, edKey)
	if string(got) != old+line(nasHost, edKey) {
		t.Fatalf("got %q", got)
	}
	if got := repoconnect.AddHostKey(nil, nasPort, ecKey); string(got) != line(nasPort, ecKey) {
		t.Fatalf("an empty file: %q", got)
	}
	noBreak := "other.example.com ssh-ed25519 AAAA"
	if got := repoconnect.AddHostKey([]byte(noBreak), nasHost, edKey); string(got) != noBreak+"\n"+line(nasHost, edKey) {
		t.Fatalf("a last line without its break: %q", got)
	}
}

func TestReplacingAHostsKeyDropsItsEntriesAndKeepsEveryOtherByteAndEveryOtherHost(t *testing.T) {
	content := "# comment\n" +
		line("other.example.com", edKey) +
		line(nasHost, oldEd) +
		line("a.example.com,"+nasHost, ecKey) +
		hashedName(nasHost, "c2FsdHNhbHRzYWx0c2FsdHM=") + " " + rsaKey.Type + " " + rsaKey.Blob + "\n" +
		line(nasPort, otherKey) +
		line("*.example.com", rsaKey) +
		"@revoked " + line(nasHost, otherKey)
	got := repoconnect.ReplaceHostKey([]byte(content), nasHost, edKey)
	want := "# comment\n" +
		line("other.example.com", edKey) +
		line("a.example.com,"+nasHost, ecKey) +
		line(nasPort, otherKey) +
		line("*.example.com", rsaKey) +
		"@revoked " + line(nasHost, otherKey) +
		line(nasHost, edKey)
	if string(got) != want {
		t.Fatalf("got:\n%swant:\n%s", got, want)
	}
}

// П22: entries with patterns, lists or negations are not the host's own.
func TestTheEntriesThatArePatternsOrListsAreNamedAndNotTheHostsOwn(t *testing.T) {
	content := line("*.example.com", wildKey) + line(nasHost, oldEd) + line("a.example.com,"+nasHost, ecKey) +
		hashedName(nasHost, "c2FsdHNhbHRzYWx0c2FsdHM=") + " " + rsaKey.Type + " " + rsaKey.Blob + "\n" + line("NAS.example.com", rsaKey)
	m := repoconnect.FindKnown([]byte(content), nasHost)
	if !slices.Equal(m.Lines, []int{1, 2, 3, 4, 5}) || !slices.Equal(m.Patterns, []int{1, 3}) {
		t.Fatalf("lines %v, patterns %v", m.Lines, m.Patterns)
	}
}

// O1: ssh hashes the host name in lower case.
func TestAHashedEntryIsFoundForTheHostWrittenInAnyCase(t *testing.T) {
	content := hashedName("nas.example.com", "c2FsdHNhbHRzYWx0c2FsdHM=") + " " + edKey.Type + " " + edKey.Blob + "\n"
	if m := repoconnect.FindKnown([]byte(content), "NAS.Example.com"); len(m.Lines) != 1 {
		t.Fatalf("lines %v", m.Lines)
	}
}

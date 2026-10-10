// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// Р32: the forms of an sftp: address restic knows.
func TestTheFormsOfAnSFTPAddressAreAccepted(t *testing.T) {
	for address, want := range map[string]hostsetup.SFTPAddress{
		"sftp:backup@nas.example.com:/srv/extra":          {User: "backup", Host: "nas.example.com", Port: 22, Path: "/srv/extra"},
		"sftp:nas.example.com:/srv/extra":                 {Host: "nas.example.com", Port: 22, Path: "/srv/extra"},
		"sftp:backup@nas.example.com:relative/dir":        {User: "backup", Host: "nas.example.com", Port: 22, Path: "relative/dir"},
		"sftp:backup@[2001:db8::1]:/srv/extra":            {User: "backup", Host: "2001:db8::1", Port: 22, Path: "/srv/extra"},
		"sftp://backup@nas.example.com//srv/extra":        {User: "backup", Host: "nas.example.com", Port: 22, Path: "/srv/extra"},
		"sftp://backup@nas.example.com:2222//srv/extra":   {User: "backup", Host: "nas.example.com", Port: 2222, Path: "/srv/extra"},
		"sftp://backup@nas.example.com/relative":          {User: "backup", Host: "nas.example.com", Port: 22, Path: "relative"},
		"sftp://nas.example.com:22//srv/extra":            {Host: "nas.example.com", Port: 22, Path: "/srv/extra"},
		"sftp://backup@[2001:db8::1]//srv/extra":          {User: "backup", Host: "2001:db8::1", Port: 22, Path: "/srv/extra"},
		"sftp://backup@[2001:db8::1]:2222//srv/extra":     {User: "backup", Host: "2001:db8::1", Port: 2222, Path: "/srv/extra"},
		"sftp://backup@nas.example.com:65535//srv/extra":  {User: "backup", Host: "nas.example.com", Port: 65535, Path: "/srv/extra"},
		"sftp:backup@nas.example.com:/srv/a@b:c":          {User: "backup", Host: "nas.example.com", Port: 22, Path: "/srv/a@b:c"},
		"sftp://backup@nas.example.com:1//srv/extra":      {User: "backup", Host: "nas.example.com", Port: 1, Path: "/srv/extra"},
		"sftp:nas.example.com:/srv/extra with spaces/dir": {Host: "nas.example.com", Port: 22, Path: "/srv/extra with spaces/dir"},
	} {
		got, f := hostsetup.CheckSFTPAddress(address)
		if f != nil || got != want {
			t.Errorf("%s: got %+v, %v; want %+v", address, got, f, want)
		}
	}
}

func TestAnUnusableSFTPAddressIsAddressInvalidAndNeverShowsACredential(t *testing.T) {
	for _, address := range []string{
		"sftp:backup@nas.example.com",
		"sftp:backup@nas.example.com:",
		"sftp:",
		"sftp:@nas.example.com:/srv/extra",
		"sftp::/srv/extra",
		"sftp://backup:URL-MARKER@nas.example.com//srv/extra",
		"sftp://backup@nas.example.com:0//srv/extra",
		"sftp://backup@nas.example.com:65536//srv/extra",
		"sftp://backup@nas.example.com:port//srv/extra",
		"sftp://backup@nas.example.com",
		"sftp://backup@nas.example.com/",
		"sftp://backup@//srv/extra",
		"sftp://backup@[2001:db8::1//srv/extra",
		"sftp:-oProxyCommand=touch-pwned:/srv/extra",
		"sftp:-lroot@nas.example.com:/srv/extra",
		"sftp://-lroot@nas.example.com//srv/extra",
		"sftp://-oProxyCommand=x//srv/extra",
		"sftp:backup@nas.example.com:/srv/ex\ntra",
		"sftp:backup@nas.example.com\t:/srv/extra",
		"sftp:backup@nas.example.com:/srv/extra\x7f",
	} {
		_, f := hostsetup.CheckSFTPAddress(address)
		if f == nil || f.Reason != refusal.AddressInvalid || f.Class != refusal.ClassUsage {
			t.Errorf("%q: %+v", address, f)
			continue
		}
		if strings.Contains(f.Detail, "URL-MARKER") {
			t.Errorf("%q: the message shows the credential: %s", address, f.Detail)
		}
	}
}

// Р40: ssh writes the host of a known_hosts entry bare for port 22 and
// as [host]:port for another; the user is the service user's when the
// address names none.
func TestTheNamesOfAnSFTPServerAsSSHKnowsThem(t *testing.T) {
	for _, c := range []struct {
		address string
		known   string
		target  string
	}{
		{"sftp:backup@nas.example.com:/srv", "nas.example.com", "backup@nas.example.com"},
		{"sftp://backup@nas.example.com:2222//srv", "[nas.example.com]:2222", "backup@nas.example.com"},
		{"sftp://backup@[2001:db8::1]//srv", "2001:db8::1", "backup@[2001:db8::1]"},
		{"sftp://backup@[2001:db8::1]:2222//srv", "[2001:db8::1]:2222", "backup@[2001:db8::1]"},
		{"sftp:nas.example.com:/srv", "nas.example.com", "nas.example.com"},
	} {
		a, f := hostsetup.CheckSFTPAddress(c.address)
		if f != nil {
			t.Fatal(f)
		}
		if a.KnownHostsName() != c.known || a.Destination() != c.target {
			t.Errorf("%s: %q, %q; want %q, %q", c.address, a.KnownHostsName(), a.Destination(), c.known, c.target)
		}
	}
}

// A host or user that ssh would read as a pattern, a list or a negation widens
// the trust of known_hosts and of the Host block.
func TestAnSFTPHostOrUserThatSSHWouldReadAsAPatternOrAListIsInvalid(t *testing.T) {
	for _, address := range []string{
		"sftp:a,b:/x", "sftp:*:/x", "sftp:*.example.com:/x", "sftp:h?:/x", "sftp:!h:/x",
		"sftp:u@h@x:/x", "sftp:a b:/x", "sftp:h/x:/x", "sftp:h#c:/x", "sftp:u,v@h:/x",
		"sftp://u@a,b:2222/x", "sftp://u@*.example.com//x", "sftp://u v@h//x", "sftp:u@[2001:db8::*]:/x",
		"sftp:u@[]:/x", "sftp:u@[host.example.com]:/x",
	} {
		_, f := hostsetup.CheckSFTPAddress(address)
		if f == nil || f.Reason != refusal.AddressInvalid {
			t.Errorf("%q: %+v", address, f)
		}
	}
	for _, address := range []string{"sftp:u.v-w_x@h-1.example.com:/x", "sftp:u@[2001:db8::1]:/x", "sftp:u@10.0.0.5:/x"} {
		if _, f := hostsetup.CheckSFTPAddress(address); f != nil {
			t.Errorf("%q: %+v", address, f)
		}
	}
}

// Each wrong address is told what is wrong with it, not with another rule.
func TestAnUnusableSFTPAddressSaysWhatIsWrongWithIt(t *testing.T) {
	for address, why := range map[string]string{
		"sftp://backup:URL-MARKER@nas.example.com//srv/extra": "holds a password",
		"sftp:@nas.example.com:/srv/extra":                    "names no user before @",
		"sftp:-nas.example.com:/srv/extra":                    "must not start with -",
		"sftp:-u@nas.example.com:/srv/extra":                  "must not start with -",
		"sftp:u v@nas.example.com:/srv/extra":                 "the user may hold only",
		"sftp::/srv/extra":                                    "names no valid host",
		"sftp://backup@//srv/extra":                           "names no valid host",
		"sftp://u@[2001:db8::1//srv/extra":                    "names no valid host",
		"sftp://u@nas.example.com:99999//srv/extra":           "the port must be 1-65535",
		"sftp:u@nas*.example.com:/srv/extra":                  "the host must be a DNS name",
		"sftp:u@[fe80::1%eth0]:/srv/extra":                    "the host must be a DNS name",
		"sftp:u@[10.0.0.5]:/srv/extra":                        "the host must be a DNS name",
		"sftp:u@[nas.example.com]:/srv/extra":                 "the host must be a DNS name",
	} {
		_, f := hostsetup.CheckSFTPAddress(address)
		if f == nil || f.Reason != refusal.AddressInvalid || !strings.Contains(f.Detail, why) {
			t.Errorf("%q: %+v, want %q", address, f, why)
		}
	}
}

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// Р28: the forms of an S3 address restic knows.
func TestTheFormsOfAnS3AddressAreAccepted(t *testing.T) {
	for address, want := range map[string]hostsetup.S3Address{
		"s3:https://s3.example.com/bucket-b/extra":  {Host: "s3.example.com", Bucket: "bucket-b"},
		"s3:https://s3.example.com:9000/bucket-b":   {Host: "s3.example.com:9000", Bucket: "bucket-b"},
		"s3:s3.example.com/bucket-b/extra":          {Host: "s3.example.com", Bucket: "bucket-b"},
		"s3:http://10.0.0.5:3900/bucket-b/extra":    {Host: "10.0.0.5:3900", Bucket: "bucket-b", Insecure: true},
		"s3:https://[2001:db8::1]:9000/bucket-b":    {Host: "[2001:db8::1]:9000", Bucket: "bucket-b"},
		"s3:s3.example.com:9000/bucket-b/a/b/c/d/e": {Host: "s3.example.com:9000", Bucket: "bucket-b"},
		"s3:https://[::1]/bucket-b":                 {Host: "[::1]", Bucket: "bucket-b"},
	} {
		got, f := hostsetup.CheckS3Address(address)
		if f != nil || got != want {
			t.Errorf("%s: got %+v, %v; want %+v", address, got, f, want)
		}
	}
}

func TestAnUnusableS3AddressIsAddressInvalidAndNeverShowsACredential(t *testing.T) {
	for _, address := range []string{
		"s3:https://s3.example.com",
		"s3:https://s3.example.com/",
		"s3:https:///bucket-b/extra",
		"s3:ftp://s3.example.com/bucket-b/extra",
		"s3:https://u:URL-MARKER@s3.example.com/bucket-b/extra",
		"s3:u:URL-MARKER@s3.example.com/bucket-b/extra",
		"s3:https://-s3.example.com/bucket-b/extra",
		"s3:-s3.example.com/bucket-b/extra",
		"s3:https://s3.example.com/bucket-b/ex\ntra",
		"s3:https://s3.example.com\t/bucket-b",
		"s3:https://s3.example.com:port/bucket-b",
		"s3:https://s3.example.com:0/bucket-b",
		"s3:https://s3.example.com:65536/bucket-b",
		"s3:",
		"s3:/bucket-b",
		"s3:https://[2001:db8::1/bucket-b",
	} {
		_, f := hostsetup.CheckS3Address(address)
		if f == nil || f.Reason != refusal.AddressInvalid || f.Class != refusal.ClassUsage {
			t.Errorf("%q: %+v", address, f)
			continue
		}
		if strings.Contains(f.Detail, "URL-MARKER") {
			t.Errorf("%q: the message shows the credential: %s", address, f.Detail)
		}
	}
}

func TestTheMessageForAnUnusableS3AddressSaysWhatIsWrong(t *testing.T) {
	for address, want := range map[string]string{
		"s3:https://s3.example.com/":       "bucket",
		"s3:https:///bucket-b/extra":       "host",
		"s3:ftp://s3.example.com/b/extra":  "https or http",
		"s3:https://u:p@s3.example.com/b":  "credentials",
		"s3:-s3.example.com/b":             "-",
		"s3:https://s3.example.com:0/b":    "port",
		"s3:https://s3.example.com/b/x\ny": "control character",
	} {
		_, f := hostsetup.CheckS3Address(address)
		if f == nil || !strings.Contains(f.Detail, want) {
			t.Errorf("%q: want %q in %+v", address, want, f)
		}
	}
}

func TestAnAccessKeyIDIsPrintableASCIIWithoutSpacesUpTo128Characters(t *testing.T) {
	for _, id := range []string{"KEY-ID-1", "A", strings.Repeat("A", 128), "GK31c2f218a2e44f485b94239e"} {
		if f := hostsetup.CheckAccessKeyID(id); f != nil {
			t.Errorf("%q: %v", id, f)
		}
	}
	for _, id := range []string{"", "KEY ID", strings.Repeat("A", 129), "KEY\tID", "KEY\x7f", "ключ", "KEY\n"} {
		f := hostsetup.CheckAccessKeyID(id)
		if f == nil || f.Class != refusal.ClassUsage || !strings.Contains(f.Detail, "--access-key-id") {
			t.Errorf("%q: %+v", id, f)
		}
	}
}

func TestARegionIs1To64CharactersOfLowercaseLettersDigitsAndHyphens(t *testing.T) {
	for _, r := range []string{"ru-central1", "us-west-004", "a", strings.Repeat("a", 64)} {
		if f := hostsetup.CheckRegion(r); f != nil {
			t.Errorf("%q: %v", r, f)
		}
	}
	for _, r := range []string{"", "RU Central", "ru_central", strings.Repeat("a", 65), "eu\n", "EU"} {
		f := hostsetup.CheckRegion(r)
		if f == nil || f.Class != refusal.ClassUsage || !strings.Contains(f.Detail, "--region") {
			t.Errorf("%q: %+v", r, f)
		}
	}
}

// Н16: one trailing line break of the secret key goes; the rest is a line of the env file.
func TestOneTrailingLineBreakOfTheSecretKeyIsDropped(t *testing.T) {
	for in, want := range map[string]string{
		"S3-MARKER":     "S3-MARKER",
		"S3-MARKER\n":   "S3-MARKER",
		"S3-MARKER\r\n": "S3-MARKER",
		"a b/+=":        "a b/+=",
	} {
		got, f := hostsetup.S3SecretKey([]byte(in))
		if f != nil || string(got) != want {
			t.Errorf("%q: %q, %v", in, got, f)
		}
	}
}

func TestASecretKeyWithAnotherControlCharacterIsSecretInvalid(t *testing.T) {
	for _, in := range []string{"S3-MARKER\nX", "S3-MARKER\n\n", "S3-MARKER\rX", "S3-MARKER\x00X", "S3\tMARKER", "S3-MARKER\x7f", "S3-MARKER\r\r\n"} {
		_, f := hostsetup.S3SecretKey([]byte(in))
		if f == nil || f.Reason != refusal.SecretInvalid || f.Class != refusal.ClassUsage {
			t.Errorf("%q: %+v", in, f)
			continue
		}
		if strings.Contains(f.Detail, "S3-MARKER") {
			t.Errorf("%q: the message shows the key: %s", in, f.Detail)
		}
	}
}

func TestASecretKeyThatIsOnlyALineBreakIsEmpty(t *testing.T) {
	for _, in := range []string{"\n", "\r\n", ""} {
		_, f := hostsetup.S3SecretKey([]byte(in))
		if f == nil || f.Reason != refusal.SecretEmpty {
			t.Errorf("%q: %+v", in, f)
		}
	}
}

// Р30: exactly these lines, in this order.
func TestTheEnvFileHoldsTheKeyAndTheRegionInOrder(t *testing.T) {
	got := string(hostsetup.S3EnvFile("KEY-ID-1", []byte("S3-MARKER"), "ru-central1"))
	want := "AWS_ACCESS_KEY_ID=KEY-ID-1\nAWS_SECRET_ACCESS_KEY=S3-MARKER\nAWS_DEFAULT_REGION=ru-central1\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
	got = string(hostsetup.S3EnvFile("KEY-ID-1", []byte("S3-MARKER"), ""))
	if want := "AWS_ACCESS_KEY_ID=KEY-ID-1\nAWS_SECRET_ACCESS_KEY=S3-MARKER\n"; got != want {
		t.Fatalf("without a region: got %q", got)
	}
}

func TestTheKeyOfAnEnvFileIsFoundAgain(t *testing.T) {
	id, region := hostsetup.S3EnvIdentity([]byte("AWS_ACCESS_KEY_ID=KEY-ID-1\nAWS_SECRET_ACCESS_KEY=S3-MARKER\nAWS_DEFAULT_REGION=ru-central1\n"))
	if id != "KEY-ID-1" || region != "ru-central1" {
		t.Fatalf("id %q region %q", id, region)
	}
	id, region = hostsetup.S3EnvIdentity([]byte("# a comment\nAWS_SECRET_ACCESS_KEY=x\n"))
	if id != "" || region != "" {
		t.Fatalf("id %q region %q", id, region)
	}
}

func TestThePortsAtTheEdgesOfTheRangeAreAccepted(t *testing.T) {
	for _, address := range []string{"s3:https://s3.example.com:1/b", "s3:https://s3.example.com:65535/b", "s3:https://s3.example.com:2/b", "s3:https://s3.example.com:65534/b"} {
		if _, f := hostsetup.CheckS3Address(address); f != nil {
			t.Errorf("%s: %v", address, f)
		}
	}
	for _, address := range []string{"s3:https://s3.example.com:0/b", "s3:https://s3.example.com:65536/b", "s3:https://s3.example.com:/b", "s3:https://s3.example.com:-1/b"} {
		if _, f := hostsetup.CheckS3Address(address); f == nil || !strings.Contains(f.Detail, "port") {
			t.Errorf("%s: %v", address, f)
		}
	}
}

func TestAnAddressWithoutAHostSaysSo(t *testing.T) {
	_, f := hostsetup.CheckS3Address("s3:https:///bucket-b/extra")
	if f == nil || !strings.Contains(f.Detail, "names no host") {
		t.Fatalf("%v", f)
	}
}

func TestATextAfterTheBracketOfAnIPv6HostIsNotAPort(t *testing.T) {
	for _, address := range []string{"s3:https://[::1]x/b", "s3:https://[::1/b", "s3:https://[]/b"} {
		if _, f := hostsetup.CheckS3Address(address); f == nil || !strings.Contains(f.Detail, "host") {
			t.Errorf("%s: %v", address, f)
		}
	}
	if got, f := hostsetup.CheckS3Address("s3:https://[::1]:9000/b"); f != nil || got.Host != "[::1]:9000" {
		t.Errorf("%+v %v", got, f)
	}
}

func TestTheMessageHidesWhatItCannotRedactAndRedactsWhatItCan(t *testing.T) {
	for _, c := range []struct{ address, shown, hidden string }{
		{"s3:backup@s3.example.com/b", "credentials", "backup@"},
		{"s3:https://u:SECRET@s3.example.com/b", "u:***@s3.example.com", "SECRET"},
		{"s3:https://s3.example.com/", "s3:https://s3.example.com/", "\x00"},
	} {
		_, f := hostsetup.CheckS3Address(c.address)
		if f == nil || !strings.Contains(f.Detail, c.shown) || strings.Contains(f.Detail, c.hidden) {
			t.Errorf("%s: %v", c.address, f)
		}
	}
}

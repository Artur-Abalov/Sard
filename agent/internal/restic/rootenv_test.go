// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// A8c-2 (Р72): restic that runs as root gets from the env_file, which
// belongs to the service user, only the variables of its kind of storage.

func (f *fixture) asRoot(url, envFile string) *restic.CLI {
	f.root = true
	f.repo.URL, f.repo.EnvFile = url, "/etc/sard/main.env"
	f.files["/etc/sard/main.env"], f.files["/etc/sard/main.pass"] = envFile, "pw\n"
	return f.build()
}

func TestARootRestoreRefusesEnvTheServiceUserCouldTurnAgainstRoot(t *testing.T) {
	for _, tc := range []struct{ url, line, name string }{
		{"s3:https://s3.example.com/b", "RCLONE_PASSWORD_COMMAND=touch /pwned", "RCLONE_PASSWORD_COMMAND"},
		{"s3:https://s3.example.com/b", "AWS_SHARED_CREDENTIALS_FILE=/etc/shadow", "AWS_SHARED_CREDENTIALS_FILE"},
		{"s3:https://s3.example.com/b", "HTTPS_PROXY=http://evil.example.com:3128", "HTTPS_PROXY"},
		{"s3:https://s3.example.com/b", "SSL_CERT_FILE=/etc/shadow", "SSL_CERT_FILE"},
		{"sftp://u@h/x", "AWS_ACCESS_KEY_ID=x", "AWS_ACCESS_KEY_ID"},
		{"/srv/restic/main", "HTTPS_PROXY=http://evil.example.com:3128", "HTTPS_PROXY"},
		{"rest:https://rest.example.com/x", "HTTP_PROXY=http://evil.example.com", "HTTP_PROXY"},
	} {
		f := newFixture(t, map[string]reply{"restore": {}})
		cli := f.asRoot(tc.url, "# keys\n\n"+tc.line+"\n")
		_, err := cli.RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Staging: stagingFile(t)})
		if !errors.Is(err, restic.ErrInvalidEnvFile) {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		for _, want := range []string{"/etc/sard/main.env", "line 3", tc.name} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %q lacks %q", tc.name, err, want)
			}
		}
		if value := tc.line[strings.Index(tc.line, "=")+1:]; strings.Contains(err.Error(), value) {
			t.Errorf("%s: the value is in %q", tc.name, err)
		}
		if len(f.exec.calls) != 0 {
			t.Errorf("%s: restic was started: %v", tc.name, f.exec.calls)
		}
	}
}

func TestARootRestoreOfARepositoryWithoutAnAllowlistIsRefusedBeforeResticStarts(t *testing.T) {
	for url, kind := range map[string]string{
		"rclone:remote:x": "rclone", "azure:container:/x": "azure", "b2:bucket:x": "b2", "gs:bucket:/x": "gs", "swift:container:/x": "swift",
	} {
		f := newFixture(t, map[string]reply{"restore": {}})
		f.root = true
		f.repo.URL = url
		_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Staging: stagingFile(t)})
		var unsupported *restic.BackendNotSupportedError
		if !errors.As(err, &unsupported) || unsupported.Kind != kind {
			t.Errorf("%s: err = %v", url, err)
		}
		if len(f.exec.calls) != 0 {
			t.Errorf("%s: restic was started", url)
		}
	}
}

func TestARootRestorePassesTheS3KeysThrough(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	env := "# keys\nAWS_ACCESS_KEY_ID=K\n\nAWS_SECRET_ACCESS_KEY=S\nAWS_SESSION_TOKEN=T\nAWS_DEFAULT_REGION=eu-west-1\n"
	_, err := f.asRoot("s3:https://s3.example.com/b", env).RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Staging: stagingFile(t)})
	if err != nil {
		t.Fatal(err)
	}
	got := f.exec.call("restore").Env
	for _, want := range []string{"AWS_ACCESS_KEY_ID=K", "AWS_SECRET_ACCESS_KEY=S", "AWS_SESSION_TOKEN=T", "AWS_DEFAULT_REGION=eu-west-1"} {
		if !slices.Contains(got, want) {
			t.Errorf("env lacks %s: %q", want, got)
		}
	}
}

// Р72: the service user's own restic (the list of snapshots, the backup of
// the service) keeps the denylist: a proxy is fine there.
func TestResticOfTheServiceUserKeepsTheDenylist(t *testing.T) {
	f := newFixture(t, map[string]reply{"snapshots": {inline: "[]"}})
	f.repo.URL, f.repo.EnvFile = "s3:https://s3.example.com/b", "/etc/sard/main.env"
	f.files["/etc/sard/main.env"] = "AWS_ACCESS_KEY_ID=K\nHTTPS_PROXY=http://proxy:3128\n"
	if _, err := f.build().Snapshots(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if env := f.exec.call("snapshots").Env; !slices.Contains(env, "HTTPS_PROXY=http://proxy:3128") {
		t.Errorf("env %q", env)
	}
}

// A8c-2 (Р78): the password of the repository reaches restic that runs as
// root through a pipe the agent fills from the file it has read itself; no
// path to a file of the service user is given to root.
func TestARootRestoreGetsThePasswordThroughAnInheritedPipe(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	f.files["/etc/sard/main.pass"] = "hunter2\n"
	f.root = true
	if _, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Staging: stagingFile(t)}); err != nil {
		t.Fatal(err)
	}
	c := f.exec.call("restore")
	if !slices.Equal(c.Args[:3], []string{"restore", "--password-file", "/proc/self/fd/4"}) {
		t.Errorf("args %q", c.Args)
	}
	if !slices.Equal(f.exec.extra, []string{"hunter2\n"}) {
		t.Errorf("the pipe held %q", f.exec.extra)
	}
	for _, kv := range c.Env {
		if strings.HasPrefix(kv, "RESTIC_PASSWORD") {
			t.Errorf("env %q", kv)
		}
	}
}

func TestARootRestoreWithAPasswordFileTheAgentCannotReadDoesNotStartRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	f.root = true // the password file is not among the files
	_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Staging: stagingFile(t)})
	if !errors.Is(err, fs.ErrNotExist) || len(f.exec.calls) != 0 {
		t.Fatalf("err = %v, calls %d", err, len(f.exec.calls))
	}
}

// Constraint 6 (crypto.Provider): the key comes from the provider, not from
// the config; the file the provider names is read by the agent and piped.
type providerKeys struct{ file string }

func (providerKeys) Name() string { return "test-provider" }
func (k providerKeys) RepositoryKey(context.Context, string) (crypto.Key, error) {
	return crypto.Key{File: k.file}, nil
}

func TestARootRestoreTakesTheKeyFromTheProvider(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	f.root = true
	f.files["/from-config"], f.files["/from-provider"] = "pw-config", "pw-provider"
	f.repo.PasswordFile = "/from-config"
	f.provider = providerKeys{file: "/from-provider"}
	if _, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Staging: stagingFile(t)}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.exec.extra, []string{"pw-provider"}) {
		t.Errorf("the pipe held %q", f.exec.extra)
	}
}

func TestARootRestoreWithAProviderThatGivesNoFileNamesTheProvider(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	f.root = true
	f.provider = providerKeys{}
	_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Staging: stagingFile(t)})
	if err == nil || !strings.Contains(err.Error(), "test-provider") || len(f.exec.calls) != 0 {
		t.Fatalf("err = %v, calls %d", err, len(f.exec.calls))
	}
}

// stagingFile is a directory to hand to restic as the staging directory.
func stagingFile(t *testing.T) *os.File {
	t.Helper()
	d, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

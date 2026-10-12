// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
)

func TestTargetForDescribesTheStorage(t *testing.T) {
	for _, c := range []struct {
		name, url                string
		remote                   bool
		bucket, directory, where string
	}{
		{"local", "/srv/restic/main", false, "", "", "/srv/restic/main"},
		{"s3", "s3:https://s3.example.com/bucket-b/extra", true, "bucket-b", "", "s3:https://s3.example.com/bucket-b/extra"},
		{"sftp", "sftp://backup@sftp.example.com/srv/restic", true, "", "srv/restic", "sftp://backup@sftp.example.com/srv/restic"},
		{"s3", "s3:", false, "", "", "s3:"},
		{"sftp", "sftp:", false, "", "", "sftp:"},
	} {
		repo := config.Repository{Name: "r", URL: c.url, PasswordFile: "/p"}
		got := repoconnect.TargetFor(repo, nil)
		shown := fmt.Sprintf("%s %s %s %v %q %q %q", got.Name, got.Backend, got.PasswordFile, got.Remote, got.Bucket, got.Directory, got.Where)
		want := fmt.Sprintf("r %s /p %v %q %q %q", repo.Backend(), c.remote, c.bucket, c.directory, c.where)
		if shown != want {
			t.Errorf("%s %q: %s, want %s", c.name, c.url, shown, want)
		}
	}
}

func TestTargetForHidesTheSecretsAndThePasswordOfTheAddress(t *testing.T) {
	repo := config.Repository{Name: "r", URL: "s3:https://u:URL-SECRET@s3.example.com/b/x"}
	got := repoconnect.TargetFor(repo, []string{"AWS_SECRET_ACCESS_KEY=ENV-SECRET"}, "EXTRA-SECRET")
	if strings.Contains(got.Where, "URL-SECRET") {
		t.Errorf("where %q", got.Where)
	}
	if out := got.Scrub("ENV-SECRET EXTRA-SECRET URL-SECRET"); strings.Contains(out, "SECRET") {
		t.Errorf("scrubbed %q", out)
	}
}

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// V5: without a verify section a verifier works in the state directory of
// the service, reads 10% of the data, needs twice the copy plus 1 GiB and
// gives its stages the limits of the owner's decision.
func TestWithoutAVerifySectionTheVerifierHasTheDefaults(t *testing.T) {
	c, err := config.Parse([]byte(restoreBase))
	if err != nil {
		t.Fatal(err)
	}
	want := config.VerifySettings{
		WorkDir: "/var/lib/sard-agent/verify", CheckPercent: 10, SpaceFactor: 2, SpaceHeadroom: 1 << 30,
		Limits:  config.VerifyLimits{Prepare: 10 * time.Minute, Check: 2 * time.Hour, Restore: 4 * time.Hour, Fingerprint: time.Hour},
		Pull:    true,
		Sandbox: config.SandboxLimits{CPUs: "2", Memory: "4g", Pids: 1024},
	}
	if got := c.VerifySettings(); !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %+v", got)
	}
}

// Owner's decision 8: a closed network gives its own images, pulled from
// nowhere; the list of the release is replaced, not merged.
func TestTheImagesOfTheConfigReplaceTheReleaseListAndPullCanBeOff(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	body := "verify:\n  pull: false\n  images:\n    - {name: postgres, version: '17.6', digest: '" + digest + "'}\n" +
		"  sandbox: {cpus: '0.5', memory: 1g, pids: 64}\n"
	c, err := config.Parse([]byte(restoreBase + body))
	if err != nil {
		t.Fatal(err)
	}
	s := c.VerifySettings()
	if s.Pull || !reflect.DeepEqual(s.Images, []config.VerifyImage{{Name: "postgres", Version: "17.6", Digest: digest}}) ||
		s.Sandbox != (config.SandboxLimits{CPUs: "0.5", Memory: "1g", Pids: 64}) {
		t.Fatalf("settings = %+v", s)
	}
}

func TestAnImageOfTheConfigThatIsNoImageStopsTheAgent(t *testing.T) {
	_, err := config.Parse([]byte(restoreBase + "verify:\n  images:\n    - {name: postgres, version: '17.6'}\n"))
	if !errors.Is(err, config.ErrInvalidVerify) || !strings.Contains(err.Error(), "verify.images[0]") {
		t.Fatalf("err = %v", err)
	}
	_, err = config.Parse([]byte(restoreBase + "verify:\n  sandbox: {pids: -1}\n"))
	if !errors.Is(err, config.ErrInvalidVerify) || !strings.Contains(err.Error(), "verify.sandbox.pids") {
		t.Fatalf("err = %v", err)
	}
}

func TestTheVerifySectionSetsEachSettingOnItsOwn(t *testing.T) {
	body := "verify:\n  work_dir: /srv/verify\n  check_percent: 100\n  space_factor: 1.5\n  space_headroom: 0\n" +
		"  stage_limits:\n    restore: 12h\n    check: 30m\n"
	c, err := config.Parse([]byte(restoreBase + body))
	if err != nil {
		t.Fatal(err)
	}
	want := config.VerifySettings{
		WorkDir: "/srv/verify", CheckPercent: 100, SpaceFactor: 1.5, SpaceHeadroom: 0,
		Limits:  config.VerifyLimits{Prepare: 10 * time.Minute, Check: 30 * time.Minute, Restore: 12 * time.Hour, Fingerprint: time.Hour},
		Pull:    true,
		Sandbox: config.SandboxLimits{CPUs: "2", Memory: "4g", Pids: 1024},
	}
	if got := c.VerifySettings(); !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %+v", got)
	}
}

func TestAnInvalidVerifySettingStopsTheAgent(t *testing.T) {
	tests := map[string]string{
		"relative work dir":           "verify:\n  work_dir: verify\n",
		"root":                        "verify:\n  work_dir: /\n",
		"not clean":                   "verify:\n  work_dir: /srv/a/../b\n",
		"the restore dir":             "verify:\n  work_dir: /var/lib/sard-agent/restore\n",
		"holding the restore dir":     "verify:\n  work_dir: /srv\nrestore:\n  dir: /srv/restore\n",
		"inside the executor's state": "verify:\n  work_dir: /var/lib/sard-agent/executor/v\n",
		"inside a local repository":   "verify:\n  work_dir: /srv/repo/v\nrepositories:\n  - {name: r, url: /srv/repo, password_file: /p}\n",
		"a negative percent":          "verify:\n  check_percent: -1\n",
		"more than all the data":      "verify:\n  check_percent: 101\n",
		"a factor below one":          "verify:\n  space_factor: 0.5\n",
		"a negative limit":            "verify:\n  stage_limits:\n    check: -1m\n",
		"a nanosecond below zero":     "verify:\n  stage_limits:\n    prepare: -1ns\n",
		"a negative factor":           "verify:\n  space_factor: -1\n",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := config.Parse([]byte(restoreBase + body))
			if !errors.Is(err, config.ErrInvalidVerify) || !strings.Contains(err.Error(), "verify.") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// The boundaries are settings, a zero one is the default.
func TestTheEdgesOfTheVerifySettingsAreValid(t *testing.T) {
	for _, body := range []string{
		"verify:\n  check_percent: 1\n", "verify:\n  check_percent: 100\n", "verify:\n  check_percent: 0\n",
		"verify:\n  space_factor: 1\n", "verify:\n  space_factor: 0\n",
		"verify:\n  stage_limits:\n    check: 0s\n    restore: 1ns\n",
	} {
		if _, err := config.Parse([]byte(restoreBase + body)); err != nil {
			t.Errorf("%q: %v", body, err)
		}
	}
}

func TestTheRootAsAWorkDirectoryIsRefusedAsTheRoot(t *testing.T) {
	_, err := config.Parse([]byte(restoreBase + "verify:\n  work_dir: /\n"))
	if err == nil || !strings.Contains(err.Error(), "and not /") {
		t.Fatalf("err = %v", err)
	}
}

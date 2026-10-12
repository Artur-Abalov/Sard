// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect_test

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
)

const nasBlock = `# sard-agent begin nas.example.com
Host nas.example.com
    StrictHostKeyChecking yes
    BatchMode yes
    IdentityFile ~/.ssh/id_ed25519
    IdentitiesOnly yes
    ServerAliveInterval 15
    ServerAliveCountMax 4
    ConnectTimeout 30
# sard-agent end nas.example.com
`

func TestTheManagedBlockOfAHostIsTheTenLinesOfR41(t *testing.T) {
	if got := repoconnect.ManagedBlock("nas.example.com"); got != nasBlock {
		t.Fatalf("block:\n%s", got)
	}
}

func TestWithoutAConfigItIsMadeOfOneBlock(t *testing.T) {
	got, changed := repoconnect.ApplyBlock(nil, "nas.example.com")
	if !changed || string(got) != nasBlock {
		t.Fatalf("changed %v:\n%s", changed, got)
	}
}

func TestTheBlockStandsBeforeTheFormerContentWhichStaysByteForByte(t *testing.T) {
	old := "Host *\n    ServerAliveInterval 0\n"
	got, changed := repoconnect.ApplyBlock([]byte(old), "nas.example.com")
	if !changed || string(got) != nasBlock+"\n"+old {
		t.Fatalf("changed %v:\n%s", changed, got)
	}
}

func TestTheSameBlockIsNotAChange(t *testing.T) {
	content := "# mine\n" + nasBlock + "\nHost *\n    Compression yes\n"
	got, changed := repoconnect.ApplyBlock([]byte(content), "nas.example.com")
	if changed || string(got) != content {
		t.Fatalf("changed %v:\n%s", changed, got)
	}
}

func TestAnOutdatedBlockOfTheSameHostIsReplacedInPlace(t *testing.T) {
	old := strings.Replace(nasBlock, "ServerAliveInterval 15", "ServerAliveInterval 60", 1)
	content := "# before\n" + old + "Host mine\n    User me\n"
	got, changed := repoconnect.ApplyBlock([]byte(content), "nas.example.com")
	if !changed || string(got) != "# before\n"+nasBlock+"Host mine\n    User me\n" {
		t.Fatalf("changed %v:\n%s", changed, got)
	}
}

func TestBlocksOfDifferentHostsLiveSideBySideEachOnce(t *testing.T) {
	other, _ := repoconnect.ApplyBlock(nil, "other.example.com")
	got, changed := repoconnect.ApplyBlock(other, "nas.example.com")
	if !changed || strings.Count(string(got), "# sard-agent begin") != 2 || !strings.HasPrefix(string(got), nasBlock) || !strings.HasSuffix(string(got), string(other)) {
		t.Fatalf("changed %v:\n%s", changed, got)
	}
	again, changed := repoconnect.ApplyBlock(got, "nas.example.com")
	if changed || string(again) != string(got) {
		t.Fatalf("a repeat changed the file:\n%s", again)
	}
}

func TestABlockWithoutItsEndIsNotTakenForTheHostsBlock(t *testing.T) {
	content := "# sard-agent begin nas.example.com\nHost nas.example.com\n    User x\n"
	got, changed := repoconnect.ApplyBlock([]byte(content), "nas.example.com")
	if !changed || string(got) != nasBlock+"\n"+content {
		t.Fatalf("changed %v:\n%s", changed, got)
	}
}

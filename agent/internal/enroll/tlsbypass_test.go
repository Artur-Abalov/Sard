// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F10: InsecureSkipVerify only ever belongs in agent/internal/enroll/trust.go,
// where it is paired with a real VerifyPeerCertificate (DialTOFU) — every
// other agent-side TLS config must let crypto/tls do its own verification.
// ClientSessionCache belongs nowhere in agent/: TLS session resumption
// skips VerifyPeerCertificate on the resumed handshake, which would let a
// server that passed the TOFU check once bypass it on every later
// connection this process makes.
func TestNoTLSVerificationBypassOutsideTrustGo(t *testing.T) {
	root := findAgentRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		checkFileForTLSBypass(t, fset, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func checkFileForTLSBypass(t *testing.T, fset *token.FileSet, path string) {
	t.Helper()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	isTrustGo := strings.HasSuffix(path, filepath.Join("internal", "enroll", "trust.go"))
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			return true
		}
		switch key.Name {
		case "InsecureSkipVerify":
			if !isTrustGo {
				t.Errorf("%s: InsecureSkipVerify is only allowed in internal/enroll/trust.go", path)
			}
		case "ClientSessionCache":
			t.Errorf("%s: ClientSessionCache is not allowed anywhere in agent/ (session resumption skips VerifyPeerCertificate)", path)
		}
		return true
	})
}

// findAgentRoot walks up from the working directory to the agent module
// root (its go.mod), so the test finds every package regardless of which
// directory `go test` runs it from.
func findAgentRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the agent module root (go.mod)")
		}
		dir = parent
	}
}

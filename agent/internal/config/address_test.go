// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

func TestAddressEqualComparesHostCaseInsensitively(t *testing.T) {
	if !config.AddressEqual("SARD.Example.com:9090", "sard.example.com:9090") {
		t.Fatal("want equal: host differs only by case")
	}
}

func TestAddressEqualComparesIPv6ByValue(t *testing.T) {
	if !config.AddressEqual("[0:0:0:0:0:0:0:1]:9090", "[::1]:9090") {
		t.Fatal("want equal: same IPv6 address written differently")
	}
}

func TestAddressEqualRequiresTheSamePort(t *testing.T) {
	if config.AddressEqual("sard.example.com:9090", "sard.example.com:19090") {
		t.Fatal("want not equal: different ports")
	}
}

func TestAddressEqualRejectsUnparsableAddresses(t *testing.T) {
	if config.AddressEqual("sard.example.com", "sard.example.com:9090") {
		t.Fatal("want not equal: the first address has no port")
	}
}

func TestAddressEqualComparesDNSNamesLiterally(t *testing.T) {
	if config.AddressEqual("backup.example.org:9090", "sard.example.com:9090") {
		t.Fatal("want not equal: different hosts")
	}
}

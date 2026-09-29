// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config

import (
	"net"
	"strings"
)

// AddressEqual compares two host:port addresses the way "sard-agent
// enroll" must (В10, docs/specs/agent/agent-enroll.feature): the host
// compared case-insensitively, an IPv6 host compared by value, and the
// ports equal. Either address failing to parse as host:port means "not
// equal".
func AddressEqual(a, b string) bool {
	hostA, portA, errA := net.SplitHostPort(a)
	hostB, portB, errB := net.SplitHostPort(b)
	if errA != nil || errB != nil {
		return false
	}
	if portA != portB {
		return false
	}
	return hostEqual(hostA, hostB)
}

func hostEqual(a, b string) bool {
	if ipA, ipB := net.ParseIP(a), net.ParseIP(b); ipA != nil && ipB != nil {
		return ipA.Equal(ipB)
	}
	return strings.EqualFold(a, b)
}

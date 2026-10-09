// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"strconv"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// SFTPAddress is what a command needs to know of an sftp: address (Р32).
type SFTPAddress struct {
	// User is the user of the address; "" when it names none.
	User string
	// Host is the host without the brackets of an IPv6 address.
	Host string
	// Port is the port of the address, 22 when it names none.
	Port int
	// Path is the directory on the server, as restic gets it.
	Path string
}

// defaultSSHPort is the port ssh uses when the address names none.
const defaultSSHPort = 22

// KnownHostsName is how ssh writes this server in known_hosts: the bare
// host for port 22, [host]:port for another.
func (a SFTPAddress) KnownHostsName() string {
	if a.Port == defaultSSHPort {
		return a.Host
	}
	return "[" + a.Host + "]:" + strconv.Itoa(a.Port)
}

// Destination is [user@]host as ssh and sftp take it.
func (a SFTPAddress) Destination() string {
	host := a.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if a.User == "" {
		return host
	}
	return a.User + "@" + host
}

// CheckSFTPAddress reads the forms of restic: sftp:[user@]host:path and
// sftp://[user@]host[:port]/path. ADDRESS_INVALID names what is wrong and
// never shows a credential.
func CheckSFTPAddress(address string) (SFTPAddress, *refusal.Failure) {
	rest := strings.TrimPrefix(address, "sftp:")
	if hasControl(rest) {
		return SFTPAddress{}, addressInvalid(address, "it holds a control character")
	}
	var authority, path string
	var hasPath bool
	if after, isURL := strings.CutPrefix(rest, "//"); isURL {
		authority, path, hasPath = strings.Cut(after, "/")
	} else {
		authority, path, hasPath = cutHostPath(rest)
	}
	if !hasPath || path == "" {
		return SFTPAddress{}, addressInvalid(address, "it names no directory: sftp:[user@]host:path")
	}
	a, f := sftpAuthority(address, authority)
	a.Path = path
	return a, f
}

// cutHostPath cuts [user@]host from path at the first colon outside the
// brackets of an IPv6 address.
func cutHostPath(rest string) (authority, path string, found bool) {
	depth := 0
	for i, r := range rest {
		switch {
		case r == '[':
			depth++
		case r == ']':
			depth--
		case r == ':' && depth == 0:
			return rest[:i], rest[i+1:], true
		}
	}
	return rest, "", false
}

// sftpAuthority reads [user@]host[:port].
func sftpAuthority(address, authority string) (SFTPAddress, *refusal.Failure) {
	user, hostPort, f := sftpUser(address, authority)
	if f != nil {
		return SFTPAddress{}, f
	}
	if strings.HasPrefix(user, "-") || strings.HasPrefix(hostPort, "-") {
		return SFTPAddress{}, addressInvalid(address, "the user and the host must not start with -")
	}
	host, port, f := sftpHostPort(address, hostPort)
	return SFTPAddress{User: user, Host: host, Port: port}, f
}

// sftpHostPort reads host[:port]; the port is 22 when there is none.
func sftpHostPort(address, hostPort string) (host string, port int, f *refusal.Failure) {
	host, portText, hasPort, ok := splitHost(hostPort)
	switch {
	case !ok || host == "":
		return "", 0, addressInvalid(address, "it names no valid host")
	case hasPort && !validPort(portText):
		return "", 0, addressInvalid(address, "the port must be 1-65535")
	}
	return host, portOf(portText, hasPort), nil
}

// sftpUser cuts the user from [user@]host[:port]; a password is refused.
func sftpUser(address, authority string) (user, hostPort string, f *refusal.Failure) {
	user, hostPort, hasUser := strings.Cut(authority, "@")
	switch {
	case !hasUser:
		return "", authority, nil
	case strings.Contains(user, ":"):
		return "", "", addressInvalid(address, "it holds a password (user:password@): ssh uses a key, never a password")
	case user == "":
		return "", "", addressInvalid(address, "it names no user before @")
	}
	return user, hostPort, nil
}

func portOf(port string, hasPort bool) int {
	if !hasPort {
		return defaultSSHPort
	}
	n, _ := strconv.Atoi(port)
	return n
}

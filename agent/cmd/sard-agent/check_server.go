// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/serverprobe"
)

// serverLines are server.reachable, server.tls, server.accepted and
// clock.skew (A8d-3, Ч14-Ч16): the server is asked once, read-only, and each
// line judges its part of the answer.
func (c *hostCheck) serverLines() {
	needs := append([]string{hostcheck.CheckTLSIdentity}, needConfig...)
	chain := []struct {
		check string
		line  func(hostcheck.ServerFacts) hostcheck.Line
	}{
		{hostcheck.CheckServerReachable, hostcheck.ServerReachableLine},
		{hostcheck.CheckServerTLS, hostcheck.ServerTLSLine},
		{hostcheck.CheckServerAccepted, hostcheck.ServerAcceptedLine},
		{hostcheck.CheckClockSkew, hostcheck.ClockSkewLine},
	}
	for _, link := range chain {
		c.step(link.check, needs, func() hostcheck.Line {
			if c.opts.offline {
				return hostcheck.OfflineLine(link.check, "")
			}
			facts := c.serverFacts()
			if c.serverDenied != "" {
				c.needsRoot = true
				return hostcheck.NeedsRootLine(link.check, "", "cannot read "+c.serverDenied)
			}
			return link.line(facts)
		})
		needs = append(needs, link.check)
	}
}

// serverFacts asks the server the first time it is needed.
func (c *hostCheck) serverFacts() hostcheck.ServerFacts {
	if c.server == nil {
		tlsConfig, err := c.clientTLS()
		facts := c.deps.probeServer(c.ctx, serverprobe.Request{Address: c.cfg.Server.Address, TLS: tlsConfig, TLSErr: err,
			Timeout: c.opts.connectTimeout, Now: c.deps.clock.Now})
		c.server = &facts
	}
	return *c.server
}

// clientTLS is the configuration of the connection of the service: the CA of
// Sard, the name of the server and the certificate of the agent.
func (c *hostCheck) clientTLS() (*tls.Config, error) {
	files := c.selectedTLS()
	c.denied = ""
	var pem [3][]byte
	for i, f := range []struct{ key, path string }{{"tls.ca_file", files.CAFile}, {"tls.cert_file", files.CertFile}, {"tls.key_file", files.KeyFile}} {
		data, err := c.read(f.path)
		if err != nil {
			return nil, c.denyOr(f.path, fmt.Errorf("%s: %w", f.key, err))
		}
		pem[i] = data
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem[0]) {
		return nil, fmt.Errorf("tls.ca_file: no PEM certificates in %q", files.CAFile)
	}
	pair, err := tls.X509KeyPair(pem[1], pem[2])
	if err != nil {
		return nil, fmt.Errorf("tls.cert_file, tls.key_file: %w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: serverName(c.cfg.Server), Certificates: []tls.Certificate{pair}}, nil
}

// denyOr remembers a file the service user may not read (Ч20) and returns err.
func (c *hostCheck) denyOr(path string, err error) error {
	if c.denied != "" && c.who.Role == hostsetup.RoleService {
		c.serverDenied = path
	}
	return err
}

// serverName is the host of server.address: the name in its certificate.
func serverName(s config.Server) string {
	host, _, err := net.SplitHostPort(s.Address)
	if err != nil {
		return s.Address
	}
	return host
}

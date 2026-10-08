// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config

import (
	"net/url"
	"strings"
)

// URLPassword is the password in a repository URL of the form
// "backend:scheme://user:password@host/path"; "" if there is none.
func URLPassword(repoURL string) string {
	_, rest, _ := strings.Cut(repoURL, ":")
	u, err := url.Parse(rest)
	if err != nil {
		return ""
	}
	password, _ := u.User.Password() // nil User: no password
	return password
}

// RedactURL is the repository URL with the password of its userinfo
// replaced by ***, for the places that show an address.
func RedactURL(repoURL string) string {
	_, rest, found := strings.Cut(repoURL, ":")
	if !found {
		return repoURL
	}
	_, afterScheme, found := strings.Cut(rest, "://")
	if !found {
		return repoURL
	}
	authority, _, _ := strings.Cut(afterScheme, "/")
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return repoURL
	}
	user, _, hasPassword := strings.Cut(authority[:at], ":")
	if !hasPassword {
		return repoURL
	}
	return strings.Replace(repoURL, authority[:at], user+":***", 1)
}

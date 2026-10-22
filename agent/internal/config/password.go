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
	return replaceUserinfo(repoURL, func(user string) string { return user + ":***" })
}

// StripPassword is the repository URL without the password of its userinfo:
// an address that can be given to another host (V3, K11).
func StripPassword(repoURL string) string {
	return replaceUserinfo(repoURL, func(user string) string { return user })
}

// replaceUserinfo replaces the userinfo of a URL that has a password by what
// with(user) says; a URL without a password is returned as it is.
func replaceUserinfo(repoURL string, with func(user string) string) string {
	_, rest, found := strings.Cut(repoURL, ":")
	if !found {
		return repoURL
	}
	afterScheme, found := strings.CutPrefix(rest, "//") // sftp://host/path
	if !found {
		_, afterScheme, found = strings.Cut(rest, "://") // rest:https://host/path
	}
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
	return strings.Replace(repoURL, authority[:at], with(user), 1)
}

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

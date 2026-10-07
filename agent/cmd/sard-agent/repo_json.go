// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"encoding/json"
	"io"
)

// printJSON writes one JSON object and a line break; the value is built
// from strings and pointers to strings, which always marshal.
func printJSON(stdout io.Writer, v any) {
	data, _ := json.Marshal(v)
	_, _ = stdout.Write(append(data, '\n'))
}

// nullIfDash is the JSON value of a field that the text shows as "-".
func nullIfDash(s string) *string {
	if s == "-" || s == "" {
		return nil
	}
	return &s
}

type listJSONRow struct {
	Name         string  `json:"name"`
	Backend      string  `json:"backend"`
	Status       string  `json:"status"`
	RepositoryID *string `json:"repository_id"`
	DefinedIn    string  `json:"defined_in"`
}

// printListJSON is Р21: {"repositories":[...]}.
func printListJSON(stdout io.Writer, rows []listRow) {
	out := struct {
		Repositories []listJSONRow `json:"repositories"`
	}{Repositories: []listJSONRow{}}
	for _, r := range rows {
		out.Repositories = append(out.Repositories, listJSONRow{
			Name: r.name, Backend: r.backend, Status: r.status(),
			RepositoryID: nullIfDash(r.repositoryID()), DefinedIn: r.definedIn,
		})
	}
	printJSON(stdout, out)
}

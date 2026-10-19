// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// AnnouncedFile is the record the service writes into executor.state_dir at
// every Register: the repositories and the ids it told the server (Ч4). It
// holds names and ids, no key, no address.
const AnnouncedFile = "announced-repositories.json"

const announcedVersion = 1

// AnnouncedRepository is one repository as the service announced it. An
// empty RepositoryID means it could not read the id when it started.
type AnnouncedRepository struct {
	Name         string `json:"name"`
	RepositoryID string `json:"repository_id"`
}

type announcedRecord struct {
	Version      int                   `json:"version"`
	Repositories []AnnouncedRepository `json:"repositories"`
}

// WriteAnnounced replaces the record in dir atomically: a temporary file in
// the same directory (mode 0600, the owner is the caller), written, synced
// and renamed. A failure leaves no temporary file.
func WriteAnnounced(dir string, repos []AnnouncedRepository) error {
	data, err := json.Marshal(announcedRecord{Version: announcedVersion, Repositories: repos})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-announced-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // fails harmlessly once renamed
	_, err = tmp.Write(data)
	if err = errors.Join(err, tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, AnnouncedFile)); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// ReadAnnounced reads the record in dir; a record that is not there is an
// error that matches fs.ErrNotExist.
func ReadAnnounced(read func(string) ([]byte, error), dir string) ([]AnnouncedRepository, error) {
	data, err := read(filepath.Join(dir, AnnouncedFile))
	if err != nil {
		return nil, err
	}
	var rec announcedRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("%s: %w", AnnouncedFile, err)
	}
	return rec.Repositories, nil
}

const restartHint = "sudo systemctl restart sard-agent"

// IDLine is the line of repository.id (Ч4): the id restic reports for the
// repository against the record of the service; readErr is why the record
// could not be read.
func IDLine(repo, current string, announced []AnnouncedRepository, readErr error) Line {
	line := Line{Check: "repository.id", Subject: repo, Status: Warn, Hint: restartHint}
	switch {
	case errors.Is(readErr, fs.ErrNotExist):
		line.Status, line.Message = Skip, "the service has not registered yet: there is no record of the repositories it announced"
		line.Hint = "start the service with `sudo systemctl start sard-agent`, then run `sudo sard-agent repo check " + repo + "` again"
	case readErr != nil:
		line.Message = fmt.Sprintf("the record of the repositories the service announced cannot be read: %v", readErr)
	default:
		return compareID(line, current, announced)
	}
	return line
}

func compareID(line Line, current string, announced []AnnouncedRepository) Line {
	known := ""
	found := false
	for _, a := range announced {
		if a.Name == line.Subject {
			known, found = a.RepositoryID, true
		}
	}
	switch {
	case !found:
		line.Message = "this repository was not announced to the server yet: the service has not started since it was added"
	case known == "":
		line.Message = "the service could not read the repository id when it started, so the server does not know it"
	case known != current:
		line.Status = Error
		line.Message = fmt.Sprintf("the storage holds repository %s, but the server was told %s: it is another repository than the server knows", current, known)
		line.Hint = restartHint + ", and check the address of the repository"
	default:
		line.Status, line.Hint = OK, ""
		line.Message = fmt.Sprintf("repository id %s is the one announced to the server", current)
	}
	return line
}

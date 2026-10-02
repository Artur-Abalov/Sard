// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package stepsecrets_test

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/stepsecrets"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

var _ executor.Secrets = (*stepsecrets.Source)(nil)

// files is a fake file system; a missing file is fs.ErrNotExist with its path.
type files map[string]string

func (f files) read(name string) ([]byte, error) {
	data, ok := f[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return []byte(data), nil
}

func cfg() config.Config {
	return config.Config{
		TLS:     config.TLS{KeyFile: "/etc/sard/agent.key"},
		Secrets: map[string]string{"pg": "/etc/sard/pg", "api": "/etc/sard/api", "blank": "/etc/sard/blank"},
		Repositories: []config.Repository{
			{Name: "main", URL: "rest:http://u:url-pass@host/x", PasswordFile: "/etc/sard/main.pass", EnvFile: "/etc/sard/main.env"},
			{Name: "plain", URL: "/srv/restic", PasswordFile: "/etc/sard/plain.pass"},
			{Name: "other", URL: "/srv/other", PasswordFile: "/etc/sard/other.pass", EnvFile: "/etc/sard/other.env"},
		},
	}
}

func allFiles() files {
	return files{
		"/etc/sard/agent.key":  "-----BEGIN KEY-----\nabc\n-----END KEY-----\n",
		"/etc/sard/pg":         "pg-secret\n",
		"/etc/sard/api":        "api-token\r\n",
		"/etc/sard/blank":      "\n",
		"/etc/sard/main.env":   "# creds\nAWS_ACCESS_KEY_ID=AKIAEXAMPLE\nAWS_SECRET_ACCESS_KEY=aws-secret\nEMPTY=\n",
		"/etc/sard/other.env":  "OTHER=other-secret\n",
		"/etc/sard/main.pass":  "never-read",
		"/etc/sard/plain.pass": "never-read",
	}
}

func step(repo string) *agentv1.RunStep {
	return &agentv1.RunStep{CommandId: "c1", RepositoryName: repo}
}

func values(t *testing.T, got []executor.Secret) []string {
	t.Helper()
	var out []string
	for _, s := range got {
		out = append(out, s.Name+"="+string(s.Value))
	}
	return out
}

func newSource(c config.Config, read func(string) ([]byte, error)) *stepsecrets.Source {
	return stepsecrets.New(c, read, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestEverySecretEveryEnvFileTheAgentKeyAndTheStepRepositorysURLPassword(t *testing.T) {
	got, err := newSource(cfg(), allFiles().read).For(step("main"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"secret api=api-token",
		"secret blank=",
		"secret pg=pg-secret",
		"tls.key_file=-----BEGIN KEY-----\nabc\n-----END KEY-----",
		`env_file of repository "main": AWS_ACCESS_KEY_ID=AKIAEXAMPLE`,
		`env_file of repository "main": AWS_SECRET_ACCESS_KEY=aws-secret`,
		`env_file of repository "main": EMPTY=`,
		`env_file of repository "other": OTHER=other-secret`,
		`url of repository "main"=url-pass`,
	}
	if got := values(t, got); !slices.Equal(got, want) {
		t.Fatalf("values =\n%q\nwant\n%q", got, want)
	}
}

func TestARepositoryWithoutEnvFileOrPasswordAddsNothingOfItsOwn(t *testing.T) {
	got, err := newSource(cfg(), allFiles().read).For(step("plain"))
	if err != nil || len(got) != 8 {
		t.Fatalf("values = %q, err = %v", values(t, got), err)
	}
}

func TestAStepWithoutRepositoryGetsNoURLPassword(t *testing.T) {
	got, err := newSource(cfg(), allFiles().read).For(step(""))
	if err != nil || len(got) != 8 {
		t.Fatalf("values = %q, err = %v", values(t, got), err)
	}
}

func TestTheRepositoryPasswordFileIsNeverRead(t *testing.T) {
	read := allFiles()
	var opened []string
	_, err := newSource(cfg(), func(name string) ([]byte, error) {
		opened = append(opened, name)
		return read.read(name)
	}).For(step("main"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range opened {
		if strings.HasSuffix(name, ".pass") {
			t.Fatalf("read %s (ADR 0008)", name)
		}
	}
}

func TestAnUnreadableSecretIsNamedWithoutItsPath(t *testing.T) {
	f := allFiles()
	delete(f, "/etc/sard/pg")
	_, err := newSource(cfg(), f.read).For(step("main"))
	if err == nil || err.Error() != `cannot read secret "pg": file does not exist` || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestAnUnreadableEnvFileIsNamedByItsRepository(t *testing.T) {
	f := allFiles()
	delete(f, "/etc/sard/main.env")
	_, err := newSource(cfg(), f.read).For(step("main"))
	if err == nil || err.Error() != `cannot read env_file of repository "main": file does not exist` {
		t.Fatalf("err = %v", err)
	}
}

func TestAnInvalidEnvFileNamesTheLineNeverAValue(t *testing.T) {
	f := allFiles()
	f["/etc/sard/main.env"] = "TOKEN=fine\nnot an assignment s3cr3t\n"
	_, err := newSource(cfg(), f.read).For(step("main"))
	if err == nil || strings.Contains(err.Error(), "s3cr3t") || !strings.Contains(err.Error(), `env_file of repository "main"`) || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v", err)
	}
}

func TestAReadErrorWithoutAPathKeepsItsText(t *testing.T) {
	_, err := newSource(cfg(), func(string) ([]byte, error) { return nil, errors.New("boom") }).For(step(""))
	if err == nil || err.Error() != `cannot read secret "api": boom` {
		t.Fatalf("err = %v", err)
	}
}

func TestAnUnknownRepositoryAddsNoURLPassword(t *testing.T) {
	got, err := newSource(cfg(), allFiles().read).For(step("nas"))
	if err != nil || len(got) != 8 {
		t.Fatalf("values = %q, err = %v", values(t, got), err)
	}
}

func TestAnAgentWithoutAKeyFileMasksNoKey(t *testing.T) {
	c := cfg()
	c.TLS.KeyFile = ""
	got, err := newSource(c, allFiles().read).For(step("plain"))
	if err != nil || len(got) != 7 {
		t.Fatalf("values = %q, err = %v", values(t, got), err)
	}
}

// --- short values: one warning at start, then only when a value turns short

type warned struct {
	buf bytes.Buffer
	src *stepsecrets.Source
	fs  files
}

func warnSetup(t *testing.T, secrets map[string]string, env string) *warned {
	t.Helper()
	w := &warned{fs: files{}}
	c := config.Config{Secrets: map[string]string{}, Repositories: []config.Repository{{Name: "main", URL: "/srv/r", EnvFile: "/e"}}}
	for name, value := range secrets {
		c.Secrets[name] = "/s/" + name
		w.fs["/s/"+name] = value + "\n"
	}
	w.fs["/e"] = env
	w.src = stepsecrets.New(c, w.fs.read, slog.New(slog.NewTextHandler(&w.buf, nil)))
	return w
}

func (w *warned) lines() []string {
	return strings.Split(strings.TrimSpace(w.buf.String()), "\n")
}

func (w *warned) count(substr string) int { return strings.Count(w.buf.String(), substr) }

func TestAShortSecretIsWarnedAboutAtStartByNameNeverByValue(t *testing.T) {
	w := warnSetup(t, map[string]string{"short": "ab7", "long": "a-long-secret"}, "")
	w.src.Audit()
	lines := w.lines()
	if len(lines) != 1 || !strings.Contains(lines[0], "level=WARN") || !strings.Contains(lines[0], "short") ||
		!strings.Contains(lines[0], "less readable") || strings.Contains(w.buf.String(), "ab7") || strings.Contains(w.buf.String(), "long") {
		t.Fatalf("log:\n%s", w.buf.String())
	}
}

func TestTheShortValueLimitIsFourCharacters(t *testing.T) {
	for value, want := range map[string]int{"abc": 1, "abcd": 0, "пар": 1, "паро": 0} {
		w := warnSetup(t, map[string]string{"s": value}, "")
		w.src.Audit()
		if got := w.count("level=WARN"); got != want {
			t.Errorf("%q: %d warnings, want %d", value, got, want)
		}
	}
}

func TestAShortEnvFileValueIsWarnedAboutByRepositoryAndVariable(t *testing.T) {
	w := warnSetup(t, nil, "AWS_DEFAULT_REGION=eu\nAWS_SECRET_ACCESS_KEY=a-long-secret\n")
	w.src.Audit()
	log := w.buf.String()
	if w.count("level=WARN") != 1 || !strings.Contains(log, "main") || !strings.Contains(log, "AWS_DEFAULT_REGION") ||
		strings.Contains(log, "=eu") || strings.Contains(log, "AWS_SECRET_ACCESS_KEY") {
		t.Fatalf("log:\n%s", log)
	}
}

func TestAnEmptyValueIsNotWarnedAbout(t *testing.T) {
	w := warnSetup(t, map[string]string{"empty": ""}, "EMPTY=\n")
	w.src.Audit()
	if w.buf.Len() != 0 {
		t.Fatalf("log:\n%s", w.buf.String())
	}
}

func TestAShortSecretIsNotWarnedAboutAgainOnSteps(t *testing.T) {
	w := warnSetup(t, map[string]string{"short": "ab7"}, "")
	w.src.Audit()
	for range 3 {
		if _, err := w.src.For(step("main")); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.count("level=WARN"); got != 1 {
		t.Fatalf("%d warnings:\n%s", got, w.buf.String())
	}
}

func TestAValueThatTurnedShortAfterStartIsWarnedAboutOnce(t *testing.T) {
	w := warnSetup(t, map[string]string{"tok": "a-long-secret"}, "")
	w.src.Audit()
	w.fs["/s/tok"] = "ab7\n"
	for range 2 {
		if _, err := w.src.For(step("main")); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.count("level=WARN"); got != 1 || !strings.Contains(w.buf.String(), "tok") {
		t.Fatalf("%d warnings:\n%s", got, w.buf.String())
	}
}

func TestAValueThatTurnsShortAgainIsWarnedAboutAgain(t *testing.T) {
	w := warnSetup(t, map[string]string{"tok": "ab7"}, "")
	w.src.Audit()
	for _, v := range []string{"a-long-secret", "xy9"} {
		w.fs["/s/tok"] = v + "\n"
		if _, err := w.src.For(step("main")); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.count("level=WARN"); got != 2 {
		t.Fatalf("%d warnings:\n%s", got, w.buf.String())
	}
}

func TestAnUnreadableFileAtStartIsNamedWithoutItsPathAndStopsNothing(t *testing.T) {
	w := warnSetup(t, map[string]string{"tok": "a-long-secret", "short": "ab7"}, "")
	delete(w.fs, "/s/tok")
	w.src.Audit()
	log := w.buf.String()
	if w.count("level=WARN") != 2 || !strings.Contains(log, "tok") || strings.Contains(log, "/s/tok") || !strings.Contains(log, "short") {
		t.Fatalf("log:\n%s", log)
	}
}

// OQ-085: the executor hands the plugin the content it read for masking.
func TestAConfiguredSecretCarriesItsNameAndTheFileContentForThePlugin(t *testing.T) {
	got, err := newSource(cfg(), allFiles().read).For(step("main"))
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]string{}
	for _, s := range got {
		if s.Ref != "" {
			refs[s.Ref] = string(s.Content)
		}
	}
	want := map[string]string{"pg": "pg-secret\n", "api": "api-token\r\n", "blank": "\n"}
	if len(refs) != len(want) || refs["pg"] != want["pg"] || refs["api"] != want["api"] || refs["blank"] != want["blank"] {
		t.Fatalf("refs = %q", refs)
	}
}

func TestAReplacedSecretFileIsPickedUpByTheNextStep(t *testing.T) {
	f := allFiles()
	src := newSource(cfg(), f.read)
	if _, err := src.For(step("main")); err != nil {
		t.Fatal(err)
	}
	f["/etc/sard/pg"] = "new-pg-secret\n"
	got, err := src.For(step("main"))
	if err != nil || !slices.Contains(values(t, got), "secret pg=new-pg-secret") {
		t.Fatalf("values = %q, err = %v", values(t, got), err)
	}
}

// --- OQ-088: a source the step does not depend on warns and does not fail it

// foreign is cfg() with a log; the step runs on "plain", so other.env and the
// agent key are sources it does not depend on.
func foreign(t *testing.T) (*stepsecrets.Source, files, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	f := allFiles()
	return stepsecrets.New(cfg(), f.read, slog.New(slog.NewTextHandler(&buf, nil))), f, &buf
}

func TestAnUnreadableEnvFileOfAnotherRepositoryWarnsOnceWithoutThePathAndFailsNothing(t *testing.T) {
	src, f, buf := foreign(t)
	delete(f, "/etc/sard/other.env")
	got, err := src.For(step("plain"))
	log := buf.String()
	if err != nil || strings.Count(log, "level=WARN") != 1 || !strings.Contains(log, `repository \"other\"`) ||
		!strings.Contains(log, "env_file") || strings.Contains(log, "other.env") {
		t.Fatalf("err = %v, log:\n%s", err, log)
	}
	for _, v := range values(t, got) {
		if strings.Contains(v, "other") {
			t.Fatalf("masks %q of an unreadable file", v)
		}
	}
}

func TestAnInvalidEnvFileOfAnotherRepositoryWarnsWithoutItsContent(t *testing.T) {
	src, f, buf := foreign(t)
	f["/etc/sard/other.env"] = "ENV2-MARKER no equals sign\n"
	_, err := src.For(step("plain"))
	log := buf.String()
	if err != nil || strings.Count(log, "level=WARN") != 1 || !strings.Contains(log, "env_file") || strings.Contains(log, "ENV2-MARKER") {
		t.Fatalf("err = %v, log:\n%s", err, log)
	}
}

func TestAnUnreadableEnvFileOfTheStepRepositoryFailsTheStepWhateverItsName(t *testing.T) {
	src, f, buf := foreign(t)
	delete(f, "/etc/sard/other.env")
	_, err := src.For(step("other"))
	if err == nil || err.Error() != `cannot read env_file of repository "other": file does not exist` || buf.Len() != 0 {
		t.Fatalf("err = %v, log:\n%s", err, buf.String())
	}
}

func TestAnUnreadableAgentKeyWarnsOnceWithoutThePathAndFailsNothing(t *testing.T) {
	src, f, buf := foreign(t)
	delete(f, "/etc/sard/agent.key")
	got, err := src.For(step("main"))
	log := buf.String()
	if err != nil || strings.Count(log, "level=WARN") != 1 || !strings.Contains(log, "tls.key_file") || strings.Contains(log, "agent.key") {
		t.Fatalf("err = %v, log:\n%s", err, log)
	}
	for _, v := range values(t, got) {
		if strings.HasPrefix(v, "tls.key_file") {
			t.Fatalf("masks %q of an unreadable file", v)
		}
	}
}

func TestTheWarningAboutAForeignFileIsNotRepeatedOnLaterSteps(t *testing.T) {
	src, f, buf := foreign(t)
	delete(f, "/etc/sard/other.env")
	for range 3 {
		if _, err := src.For(step("plain")); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(buf.String(), "level=WARN"); got != 1 {
		t.Fatalf("%d warnings:\n%s", got, buf.String())
	}
}

func TestAForeignFileUnreadableAtStartIsNotWarnedAboutAgainOnSteps(t *testing.T) {
	src, f, buf := foreign(t)
	delete(f, "/etc/sard/other.env")
	src.Audit()
	for range 2 {
		if _, err := src.For(step("plain")); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(buf.String(), `repository \"other\"`); got != 1 {
		t.Fatalf("%d warnings:\n%s", got, buf.String())
	}
}

func TestParallelStepsGiveOneWarningAboutAForeignFile(t *testing.T) {
	src, f, buf := foreign(t)
	delete(f, "/etc/sard/other.env")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = src.For(step("plain"))
		}()
	}
	wg.Wait()
	if got := strings.Count(buf.String(), "level=WARN"); got != 1 {
		t.Fatalf("%d warnings:\n%s", got, buf.String())
	}
}

func TestAForeignFileThatIsReadableAgainIsMaskedSilentlyFromTheNextStep(t *testing.T) {
	src, f, buf := foreign(t)
	saved := f["/etc/sard/other.env"]
	delete(f, "/etc/sard/other.env")
	if _, err := src.For(step("plain")); err != nil {
		t.Fatal(err)
	}
	warnings := strings.Count(buf.String(), "level=WARN")
	f["/etc/sard/other.env"] = saved
	got, err := src.For(step("plain"))
	if err != nil || !slices.Contains(values(t, got), `env_file of repository "other": OTHER=other-secret`) ||
		strings.Count(buf.String(), "level=WARN") != warnings {
		t.Fatalf("values = %q, err = %v, log:\n%s", values(t, got), err, buf.String())
	}
}

func TestAForeignFileThatBreaksAgainIsWarnedAboutAgain(t *testing.T) {
	src, f, buf := foreign(t)
	saved := f["/etc/sard/other.env"]
	for _, content := range []string{"", saved, ""} {
		if content == "" {
			delete(f, "/etc/sard/other.env")
		} else {
			f["/etc/sard/other.env"] = content
		}
		if _, err := src.For(step("plain")); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(buf.String(), `repository \"other\"`); got != 2 {
		t.Fatalf("%d warnings:\n%s", got, buf.String())
	}
}

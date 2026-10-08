// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

// docs/specs/agent/self-agent.feature

// tickClock hands out a tick only when the test asks for one.
type tickClock struct {
	mu     sync.Mutex
	waited []time.Duration
	ch     chan time.Time
}

func newTickClock() *tickClock { return &tickClock{ch: make(chan time.Time, 16)} }

func (c *tickClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.waited = append(c.waited, d)
	c.mu.Unlock()
	return c.ch
}

func (c *tickClock) tick() { c.ch <- time.Now() }
func (c *tickClock) timers() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waited...)
}

type selfRun struct {
	proceed bool
	code    int
	out     string
	errOut  string
}

func selfDepsFor(clk clock) selfEnrollDeps {
	return selfEnrollDeps{
		readFile:  os.ReadFile,
		writeFile: os.WriteFile,
		clock:     clk,
		inspect:   enroll.InspectIdentity,
		enroll:    testEnrollDeps(func() (string, error) { return "sard-self", nil }),
	}
}

func runSelfStep(ctx context.Context, cfgPath, tokenPath string, d selfEnrollDeps) selfRun {
	var out, errOut bytes.Buffer
	proceed, code := selfEnrollStep(ctx, cfgPath, tokenPath, &out, &errOut, d)
	return selfRun{proceed, code, out.String(), errOut.String()}
}

func markerOf(h *host) string { return h.certFile + ".token-sha256" }

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func writeTokenFile(t *testing.T, h *host, content string) string {
	t.Helper()
	p := filepath.Join(h.dir, "enroll-token")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func requireNoLeak(t *testing.T, token string, r selfRun, h *host) {
	t.Helper()
	secret := strings.TrimPrefix(strings.SplitN(token, ".", 2)[0], "sard_")
	for _, s := range []string{r.out, r.errOut} {
		if strings.Contains(s, token) || strings.Contains(s, secret) {
			t.Fatalf("token leaked: %q", s)
		}
	}
	if m, err := os.ReadFile(markerOf(h)); err == nil && strings.Contains(string(m), secret) {
		t.Fatal("token leaked into the marker")
	}
}

// Агент без идентичности регистрируется по файлу токена и подключается;
// После успешной регистрации маркер хранит SHA-256 токена
func TestAgentWithoutIdentityEnrollsByTheTokenFileAndWritesTheMarker(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := writeTokenFile(t, f.h, f.token)
	r := runSelfStep(context.Background(), f.h.configPath, p, selfDepsFor(newTickClock()))
	if !r.proceed || r.code != exitOK {
		t.Fatalf("proceed=%v code=%d stderr=%q", r.proceed, r.code, r.errOut)
	}
	if n := f.srv.calls.Load(); n != 1 {
		t.Fatalf("Enroll calls = %d, want 1", n)
	}
	requireCertKeyMatch(t, f.h.certFile, f.h.keyFile)
	m, err := os.ReadFile(markerOf(f.h))
	if err != nil || string(m) != sha(f.token) {
		t.Fatalf("marker = %q (%v), want %s", m, err, sha(f.token))
	}
	if st, _ := os.Stat(markerOf(f.h)); st.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode = %v", st.Mode().Perm())
	}
	if r.out != "sard-agent: enrolled as agent a1\n" {
		t.Fatalf("stdout = %q", r.out)
	}
	requireNoLeak(t, f.token, r, f.h)
}

// Завершающий перевод строки в файле токена не мешает
func TestATrailingLineEndingInTheTokenFileIsIgnored(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		f := newSucceedingFakeFixture(t, "a1")
		p := writeTokenFile(t, f.h, f.token+ending)
		r := runSelfStep(context.Background(), f.h.configPath, p, selfDepsFor(newTickClock()))
		if !r.proceed {
			t.Fatalf("ending %q: code=%d stderr=%q", ending, r.code, r.errOut)
		}
		if m, _ := os.ReadFile(markerOf(f.h)); string(m) != sha(f.token) {
			t.Fatalf("ending %q: marker = %q", ending, m)
		}
	}
}

// Израсходованный токен в файле не регистрирует агента повторно
func TestASpentTokenWithAnIdentityStartsNormally(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := writeTokenFile(t, f.h, f.token)
	d := selfDepsFor(newTickClock())
	if r := runSelfStep(context.Background(), f.h.configPath, p, d); !r.proceed {
		t.Fatalf("first run: %+v", r)
	}
	r := runSelfStep(context.Background(), f.h.configPath, p, d)
	if !r.proceed || r.code != exitOK {
		t.Fatalf("second run: %+v", r)
	}
	if n := f.srv.calls.Load(); n != 1 {
		t.Fatalf("Enroll calls = %d, want 1", n)
	}
}

// Новый токен при существующей идентичности заменяет личность;
// Идентичность без маркера заменяется по токену из файла
func TestANewTokenReplacesTheExistingIdentity(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	d := selfDepsFor(newTickClock())
	if code := runEnrollWithDeps(context.Background(), []string{"--config", f.h.configPath, "--token", f.token}, &bytes.Buffer{}, &bytes.Buffer{}, d.enroll); code != exitOK {
		t.Fatalf("setup enroll: %d", code)
	}
	old, _ := os.ReadFile(f.h.certFile)
	t2 := newToken(t, f.ca.fingerprint())
	p := writeTokenFile(t, f.h, t2)
	r := runSelfStep(context.Background(), f.h.configPath, p, d)
	if !r.proceed {
		t.Fatalf("%+v", r)
	}
	if now, _ := os.ReadFile(f.h.certFile); bytes.Equal(old, now) {
		t.Fatal("the certificate was not replaced")
	}
	if m, _ := os.ReadFile(markerOf(f.h)); string(m) != sha(t2) {
		t.Fatalf("marker = %q", m)
	}
}

// Неудачная регистрация по файлу завершает агента кодом конвейера
func TestAFailedEnrollmentExitsWithThePipelineCode(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	f.srv.answer = failingAnswer(codes.FailedPrecondition, "TOKEN_USED")
	p := writeTokenFile(t, f.h, f.token)
	r := runSelfStep(context.Background(), f.h.configPath, p, selfDepsFor(newTickClock()))
	if r.proceed || r.code != exitTokenRefused || !strings.Contains(r.errOut, "TOKEN_USED") {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(markerOf(f.h)); err == nil {
		t.Fatal("marker written after a failure")
	}
	requireNoLeak(t, f.token, r, f.h)
}

// Повреждённая строка и пустой файл — ошибка использования без обращения к серверу
func TestAMalformedOrEmptyTokenFileIsAUsageError(t *testing.T) {
	for content, want := range map[string]string{"sard_broken": "TOKEN_MALFORMED", "": "empty"} {
		f := newSucceedingFakeFixture(t, "a1")
		p := writeTokenFile(t, f.h, content)
		r := runSelfStep(context.Background(), f.h.configPath, p, selfDepsFor(newTickClock()))
		if r.proceed || r.code != exitUsage || !strings.Contains(r.errOut, want) || f.srv.calls.Load() != 0 {
			t.Fatalf("content %q: %+v", content, r)
		}
		requireNoLeak(t, "sard_broken", r, f.h)
	}
}

// Нечитаемый файл токена — ошибка использования
func TestAnUnreadableTokenFileIsAUsageError(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := filepath.Join(f.h.dir, "a-directory")
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	r := runSelfStep(context.Background(), f.h.configPath, p, selfDepsFor(newTickClock()))
	if r.proceed || r.code != exitUsage || f.srv.calls.Load() != 0 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.errOut, "reading the enrollment token file "+p) || strings.Contains(r.errOut, "is empty") {
		t.Fatalf("stderr = %q", r.errOut)
	}
}

// Сбой записи маркера после успешной регистрации — ошибка записи
func TestAMarkerWriteFailureIsAWriteError(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := writeTokenFile(t, f.h, f.token)
	d := selfDepsFor(newTickClock())
	d.writeFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	r := runSelfStep(context.Background(), f.h.configPath, p, d)
	if r.proceed || r.code != exitWrite {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.errOut, markerOf(f.h)) || !strings.Contains(r.errOut, "a1") {
		t.Fatalf("stderr = %q", r.errOut)
	}
	requireCertKeyMatch(t, f.h.certFile, f.h.keyFile)
	requireNoLeak(t, f.token, r, f.h)
}

// Регистрация прошла, но личность после неё не читается — ошибка агента,
// сообщение называет файл сертификата, id не печатается пустым
func TestAnIdentityUnreadableAfterEnrollingIsAnAgentErrorNamingTheCertFile(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := writeTokenFile(t, f.h, f.token)
	d := selfDepsFor(newTickClock())
	d.inspect = func(config.TLS) (enroll.IdentityStatus, error) {
		return enroll.IdentityStatus{}, errors.New("stat failed")
	}
	r := runSelfStep(context.Background(), f.h.configPath, p, d)
	if r.proceed || r.code != exitAgentError {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.errOut, f.h.certFile) || strings.Contains(r.out, "enrolled as agent") {
		t.Fatalf("stdout=%q stderr=%q", r.out, r.errOut)
	}
	requireNoLeak(t, f.token, r, f.h)
}

// Личность читается без ошибки, но id в сертификате нет — то же самое
func TestAnEmptyAgentIdAfterEnrollingIsAnAgentErrorNamingTheCertFile(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := writeTokenFile(t, f.h, f.token)
	d := selfDepsFor(newTickClock())
	d.inspect = func(config.TLS) (enroll.IdentityStatus, error) {
		return enroll.IdentityStatus{Exists: true}, nil
	}
	r := runSelfStep(context.Background(), f.h.configPath, p, d)
	if r.proceed || r.code != exitAgentError || !strings.Contains(r.errOut, f.h.certFile) {
		t.Fatalf("%+v", r)
	}
}

// Без файла токена агент с идентичностью стартует как обычно
func TestWithoutATokenFileAnAgentWithAnIdentityStartsNormally(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	d := selfDepsFor(newTickClock())
	if code := runEnrollWithDeps(context.Background(), []string{"--config", f.h.configPath, "--token", f.token}, &bytes.Buffer{}, &bytes.Buffer{}, d.enroll); code != exitOK {
		t.Fatalf("setup enroll: %d", code)
	}
	r := runSelfStep(context.Background(), f.h.configPath, filepath.Join(f.h.dir, "absent"), d)
	if !r.proceed || r.code != exitOK || r.errOut != "" || f.srv.calls.Load() != 1 {
		t.Fatalf("%+v", r)
	}
}

// waiter runs the step in the background so a test can drive the polling.
type waiter struct {
	cancel context.CancelFunc
	done   chan selfRun
	out    *syncBuffer
	errOut *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func startWaiting(t *testing.T, cfgPath, tokenPath string, d selfEnrollDeps) *waiter {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w := &waiter{cancel: cancel, done: make(chan selfRun, 1), out: &syncBuffer{}, errOut: &syncBuffer{}}
	go func() {
		proceed, code := selfEnrollStep(ctx, cfgPath, tokenPath, w.out, w.errOut, d)
		w.done <- selfRun{proceed, code, w.out.String(), w.errOut.String()}
	}()
	t.Cleanup(cancel)
	return w
}

func (w *waiter) mustBeRunning(t *testing.T) {
	t.Helper()
	select {
	case r := <-w.done:
		t.Fatalf("the agent finished: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

func (w *waiter) result(t *testing.T) selfRun {
	t.Helper()
	select {
	case r := <-w.done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("the agent did not finish")
		return selfRun{}
	}
}

func waitForTimers(t *testing.T, c *tickClock, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for len(c.timers()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("timers = %d, want %d", len(c.timers()), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// Агент без файла и без идентичности ждёт и пишет одну строку ожидания;
// Агент опрашивает файл токена каждые 2 секунды
func TestWithoutAFileAndAnIdentityTheAgentWaitsAndPollsEveryTwoSeconds(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := filepath.Join(f.h.dir, "no-such-dir", "token")
	clk := newTickClock()
	d := selfDepsFor(clk)
	var mu sync.Mutex
	reads := 0
	d.readFile = func(path string) ([]byte, error) {
		mu.Lock()
		reads++
		mu.Unlock()
		return os.ReadFile(path)
	}
	w := startWaiting(t, f.h.configPath, p, d)
	for i := 1; i <= 5; i++ {
		waitForTimers(t, clk, i)
		clk.tick()
	}
	waitForTimers(t, clk, 6)
	w.mustBeRunning(t)
	for _, got := range clk.timers() {
		if got != 2*time.Second {
			t.Fatalf("poll interval = %v", got)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if reads != 6 {
		t.Fatalf("reads = %d, want 6", reads)
	}
	if want := "sard-agent: waiting for the enrollment token in " + p + "\n"; w.errOut.String() != want {
		t.Fatalf("stderr = %q, want %q", w.errOut.String(), want)
	}
	if f.srv.calls.Load() != 0 {
		t.Fatal("the server was contacted")
	}
}

// SIGTERM во время ожидания завершает агента с кодом 0
func TestSIGTERMWhileWaitingEndsTheAgentWithCodeZero(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	clk := newTickClock()
	w := startWaiting(t, f.h.configPath, filepath.Join(f.h.dir, "token"), selfDepsFor(clk))
	waitForTimers(t, clk, 1)
	w.cancel()
	r := w.result(t)
	if r.proceed || r.code != exitOK {
		t.Fatalf("%+v", r)
	}
	for _, path := range []string{f.h.keyFile, f.h.certFile, markerOf(f.h)} {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("%s exists", path)
		}
	}
}

// Появившийся файл токена подхватывается следующим опросом
func TestATokenFileThatAppearsIsPickedUpByTheNextPoll(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := filepath.Join(f.h.dir, "enroll-token")
	clk := newTickClock()
	w := startWaiting(t, f.h.configPath, p, selfDepsFor(clk))
	waitForTimers(t, clk, 1)
	if err := os.WriteFile(p, []byte(f.token), 0o600); err != nil {
		t.Fatal(err)
	}
	clk.tick()
	r := w.result(t)
	if !r.proceed || r.code != exitOK || f.srv.calls.Load() != 1 {
		t.Fatalf("%+v", r)
	}
	if strings.Count(r.errOut, "waiting for the enrollment token") != 1 {
		t.Fatalf("stderr = %q", r.errOut)
	}
}

// Израсходованный токен без идентичности — ожидание
func TestASpentTokenWithoutAnIdentityWaits(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := writeTokenFile(t, f.h, f.token)
	if err := os.WriteFile(markerOf(f.h), []byte(sha(f.token)), 0o600); err != nil {
		t.Fatal(err)
	}
	clk := newTickClock()
	w := startWaiting(t, f.h.configPath, p, selfDepsFor(clk))
	waitForTimers(t, clk, 1)
	w.mustBeRunning(t)
	if f.srv.calls.Load() != 0 {
		t.Fatal("the server was contacted")
	}
}

// Флаг --enroll-token-file у команды enroll — ошибка использования
func TestEnrollTokenFileFlagOnTheEnrollCommandIsAUsageError(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := writeTokenFile(t, f.h, f.token)
	code, _, _ := runEnrollCmdTest("--config", f.h.configPath, "--enroll-token-file", p)
	if code != exitUsage || f.srv.calls.Load() != 0 {
		t.Fatalf("code = %d", code)
	}
}

// Без флага агент без идентичности не ждёт файла токена; с флагом после шага
// идёт обычный старт
func TestTheEnrollTokenFileFlagIsWiredIntoTheAgentCommand(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	var out, errOut bytes.Buffer
	code := runAgentCmd(context.Background(), []string{"--config", f.h.configPath}, &out, &errOut, fixedHostname, selfDepsFor(newTickClock()))
	if code != exitError || !strings.Contains(errOut.String(), "tls.cert_file") || f.srv.calls.Load() != 0 {
		t.Fatalf("without the flag: code=%d stderr=%q", code, errOut.String())
	}
	p := writeTokenFile(t, f.h, f.token)
	out.Reset()
	errOut.Reset()
	code = runAgentCmd(context.Background(), []string{"--config", f.h.configPath, "--enroll-token-file", p}, &out, &errOut, fixedHostname, selfDepsFor(newTickClock()))
	if f.srv.calls.Load() != 1 {
		t.Fatalf("Enroll calls = %d (code %d, stderr %q)", f.srv.calls.Load(), code, errOut.String())
	}
	if _, err := os.Stat(f.h.certFile); err != nil {
		t.Fatal(err)
	}
}

// Ошибка конфига — как без флага
func TestAConfigErrorWithTheFlagIsReportedLikeWithoutIt(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runAgentCmd(context.Background(), []string{"--config", filepath.Join(t.TempDir(), "none.yaml"), "--enroll-token-file", "x"}, &out, &errOut, fixedHostname, selfDepsFor(newTickClock()))
	if code != exitError || errOut.Len() == 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
}

// Когда шаг самоорегистрации не даёт продолжить, команда агента завершается
// его кодом и обычный старт не выполняется
func TestAgentCommandExitsWithTheCodeOfAStepThatDoesNotProceed(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := writeTokenFile(t, f.h, "")
	var out, errOut bytes.Buffer
	code := runAgentCmd(context.Background(), []string{"--config", f.h.configPath, "--enroll-token-file", p}, &out, &errOut, fixedHostname, selfDepsFor(newTickClock()))
	if code != exitUsage || !strings.Contains(errOut.String(), "is empty") || strings.Contains(errOut.String(), "tls.cert_file") {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
	if out.Len() != 0 || f.srv.calls.Load() != 0 {
		t.Fatalf("stdout=%q calls=%d", out.String(), f.srv.calls.Load())
	}
}

// Конфиг, который не читается, — ошибка запуска, продолжать нечем
func TestAConfigThatCannotBeLoadedStopsTheStepWithTheStartErrorCode(t *testing.T) {
	r := runSelfStep(context.Background(), filepath.Join(t.TempDir(), "none.yaml"), "x", selfDepsFor(newTickClock()))
	if r.proceed || r.code != exitError || r.errOut == "" || r.out != "" {
		t.Fatalf("%+v", r)
	}
}

// Без cert_file или без key_file регистрироваться некуда: шаг ничего не
// делает и не мешает обычному старту, который назовёт недостающий ключ
func TestWithoutACertOrKeyFileInTheConfigTheStepDoesNothing(t *testing.T) {
	for _, missing := range []string{"cert_file", "key_file"} {
		f := newSucceedingFakeFixture(t, "a1")
		p := writeTokenFile(t, f.h, f.token)
		if missing == "cert_file" {
			f.h.certFile = ""
		} else {
			f.h.keyFile = ""
		}
		cfg := f.h.writeConfig(t, f.h.address)
		r := runSelfStep(context.Background(), cfg, p, selfDepsFor(newTickClock()))
		if !r.proceed || r.code != exitOK || r.out != "" || r.errOut != "" || f.srv.calls.Load() != 0 {
			t.Fatalf("without %s: %+v (calls %d)", missing, r, f.srv.calls.Load())
		}
	}
}

// Личность, которую нельзя проверить, и нет токена — ошибка агента
func TestAnIdentityThatCannotBeInspectedWithoutATokenIsAnAgentError(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	blocker := filepath.Join(f.h.dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.h.certFile = filepath.Join(blocker, "agent.pem")
	cfg := f.h.writeConfig(t, f.h.address)
	r := runSelfStep(context.Background(), cfg, filepath.Join(f.h.dir, "no-token"), selfDepsFor(newTickClock()))
	if r.proceed || r.code != exitAgentError || !strings.Contains(r.errOut, "checking the existing identity") {
		t.Fatalf("%+v", r)
	}
}

// Ошибка чтения личности после регистрации перевешивает известный id
func TestAnInspectionErrorAfterEnrollingIsAnAgentErrorEvenWithAnId(t *testing.T) {
	f := newSucceedingFakeFixture(t, "a1")
	p := writeTokenFile(t, f.h, f.token)
	d := selfDepsFor(newTickClock())
	d.inspect = func(config.TLS) (enroll.IdentityStatus, error) {
		return enroll.IdentityStatus{Exists: true, AgentID: "a1"}, errors.New("stat failed")
	}
	r := runSelfStep(context.Background(), f.h.configPath, p, d)
	if r.proceed || r.code != exitAgentError || !strings.Contains(r.errOut, "stat failed") || r.out != "" {
		t.Fatalf("%+v", r)
	}
}

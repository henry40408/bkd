package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// logWait bounds a wedged binary rather than pacing the tests; generous so a
// loaded machine never reaches it.
const logWait = 30 * time.Second

var bkdBinary string

// coverDir is where a `-cover` binary writes its profile; empty means the
// binary is built plain. `go test -coverprofile` does not see the exec'd binary.
var coverDir = os.Getenv("BKD_COVERDIR")

func TestMain(m *testing.M) {
	os.Exit(buildAndRun(m))
}

// buildAndRun builds the binary once, so every test drives the real thing.
func buildAndRun(m *testing.M) int {
	dir, err := os.MkdirTemp("", "bkd-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	bkdBinary = filepath.Join(dir, "bkd")
	buildArgs := []string{"build", "-mod=vendor"}
	if coverDir != "" {
		if err := os.MkdirAll(coverDir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		buildArgs = append(buildArgs, "-cover", "-coverpkg=github.com/henry40408/bkd/...")
	}
	build := exec.Command("go", append(buildArgs, "-o", bkdBinary, ".")...)
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build failed: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

func command(ctx context.Context, env []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bkdBinary, args...)
	cmd.Env = env
	if coverDir != "" {
		cmd.Env = append(append([]string(nil), env...), "GOCOVERDIR="+coverDir)
	}
	return cmd
}

func runToCompletion(t *testing.T, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), logWait)
	defer cancel()
	cmd := command(ctx, env, args...)
	var out, errw bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errw
	if err := cmd.Run(); err != nil && cmd.ProcessState == nil {
		t.Fatalf("failed to run bkd: %v", err)
	}
	return out.String(), errw.String(), cmd.ProcessState.ExitCode()
}

func TestVersionFlagPrintsTheVersion(t *testing.T) {
	stdout, stderr, code := runToCompletion(t, nil, "--version")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "bkd ") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestRustLogIsARetiredVariable(t *testing.T) {
	for _, value := range []string{"debug", ""} {
		stdout, stderr, code := runToCompletion(t, []string{"RUST_LOG=" + value, "BKD_ADDR=127.0.0.1:0"})
		want := "Error: these environment variables no longer exist; rename (or unset) them to continue:\n" +
			"  RUST_LOG -> BKD_LOG_LEVEL\n"
		if code != 1 || stdout != "" || stderr != want {
			t.Fatalf("RUST_LOG=%q: exit %d, stdout %q, stderr %q", value, code, stdout, stderr)
		}
	}
}

func TestRetiredVariableStopsEvenTheVersionFlag(t *testing.T) {
	_, stderr, code := runToCompletion(t, []string{"RUST_LOG=info"}, "--version")
	if code != 1 || !strings.Contains(stderr, "RUST_LOG -> BKD_LOG_LEVEL") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestBadValuesExitWithUsageCode(t *testing.T) {
	cases := map[string][]string{
		"bad log level":    {"BKD_LOG_LEVEL=trace"},
		"hostname addr":    {"BKD_ADDR=localhost:8080"},
		"interval too big": {"BKD_LINK_CHECK_INTERVAL=2592001"},
		"batch zero":       {"BKD_LINK_CHECK_BATCH=0"},
		"bad bool":         {"BKD_SECURE_COOKIES=yes"},
		"bad proxy":        {"BKD_FORWARD_AUTH_TRUSTED_PROXIES=nonsense"},
	}
	for name, env := range cases {
		stdout, stderr, code := runToCompletion(t, env)
		if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "Error: ") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, stdout, stderr)
		}
	}
}

func TestAnOutOfRangeFlagExitsWithUsageCode(t *testing.T) {
	_, _, code := runToCompletion(t, nil, "--link-check-batch", "201")
	if code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestAnUnopenableDatabaseExitsWithFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no", "such", "dir", "bkd.sqlite3")
	_, stderr, code := runToCompletion(t, nil, "--db", missing, "--addr", "127.0.0.1:0")
	if code != 1 || !strings.HasPrefix(stderr, "Error: cannot open database") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

// runningServer is a bkd process whose stderr, where the log goes, is read line by line.
type runningServer struct {
	cmd   *exec.Cmd
	lines chan string
	logs  []string
	addr  string
	db    string
}

func startServer(t *testing.T, env []string, args ...string) *runningServer {
	t.Helper()
	db := filepath.Join(t.TempDir(), "bkd.sqlite3")
	args = append([]string{"--db", db, "--addr", "127.0.0.1:0"}, args...)
	cmd := command(context.Background(), env, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	server := &runningServer{cmd: cmd, lines: make(chan string, 64), db: db}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start bkd: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			server.lines <- scanner.Text()
		}
		close(server.lines)
	}()
	return server
}

// waitFor reads log lines until one contains marker, recording every line.
func (s *runningServer) waitFor(t *testing.T, marker string) {
	t.Helper()
	timeout := time.After(logWait)
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				t.Fatalf("stderr closed before %q; logs:\n%s", marker, s.joined())
			}
			s.logs = append(s.logs, line)
			if strings.Contains(line, marker) {
				return
			}
		case <-timeout:
			t.Fatalf("%q was never logged; logs:\n%s", marker, s.joined())
		}
	}
}

var listeningAddr = regexp.MustCompile(`listening on http://([^ "]+)`)

// waitListening returns once the server announces its address.
func (s *runningServer) waitListening(t *testing.T) {
	t.Helper()
	s.waitFor(t, "listening on http://")
	match := listeningAddr.FindStringSubmatch(s.logs[len(s.logs)-1])
	if match == nil {
		t.Fatalf("no address in %q", s.logs[len(s.logs)-1])
	}
	s.addr = match[1]
}

func (s *runningServer) drain(t *testing.T) {
	t.Helper()
	timeout := time.After(logWait)
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				return
			}
			s.logs = append(s.logs, line)
		case <-timeout:
			t.Fatalf("the server never closed stderr; logs:\n%s", s.joined())
		}
	}
}

func (s *runningServer) joined() string { return strings.Join(s.logs, "\n") }

func (s *runningServer) wait(t *testing.T) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case <-done: //nolint:errcheck // a non-zero exit is expected; the code is read from ProcessState
	case <-time.After(logWait):
		t.Fatalf("the server never exited; logs:\n%s", s.joined())
	}
	return s.cmd.ProcessState.ExitCode()
}

func (s *runningServer) get(t *testing.T, path string, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "http://"+s.addr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestSetupTokenIsLoggedBeforeTheListenerBinds(t *testing.T) {
	server := startServer(t, nil)
	server.waitListening(t)
	tokenLine := ""
	listeningAt := -1
	for index, line := range server.logs {
		if strings.Contains(line, "setup token ") {
			tokenLine = line
		}
		if strings.Contains(line, "listening on") {
			listeningAt = index
		}
	}
	if tokenLine == "" || !strings.Contains(tokenLine, "level=WARN") ||
		!strings.Contains(tokenLine, "no users yet: open the site and create the first admin with setup token ") {
		t.Fatalf("no setup token line:\n%s", server.joined())
	}
	if server.logs[listeningAt] == tokenLine || strings.Index(server.joined(), tokenLine) > strings.Index(server.joined(), "listening on") {
		t.Fatalf("the token came after listening:\n%s", server.joined())
	}
	_, token, _ := strings.Cut(tokenLine, "setup token ")
	token = strings.TrimSuffix(token, `"`)
	if !regexp.MustCompile(`^[0-9A-Z]{5}-[0-9A-Z]{5}$`).MatchString(token) {
		t.Fatalf("token %q", token)
	}
	if server.get(t, "/login", nil).StatusCode != http.StatusOK {
		t.Fatal("the server does not answer")
	}
}

func TestSIGTERMShutsDownGracefullyAndLeavesNoWAL(t *testing.T) {
	server := startServer(t, nil)
	server.waitListening(t)
	server.get(t, "/login", nil)
	if err := server.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	server.drain(t)
	if code := server.wait(t); code != 0 {
		t.Fatalf("exit %d; logs:\n%s", code, server.joined())
	}
	if !strings.Contains(server.joined(), "shutting down") {
		t.Fatalf("no shutdown line:\n%s", server.joined())
	}
	for _, leftover := range []string{server.db + "-wal", server.db + "-shm"} {
		if _, err := os.Stat(leftover); err == nil {
			t.Errorf("%s outlived the process", leftover)
		}
	}
	if _, err := os.Stat(server.db); err != nil {
		t.Errorf("database file missing: %v", err)
	}
}

func TestLogLevelFiltersTheLog(t *testing.T) {
	server := startServer(t, []string{"BKD_LOG_LEVEL=error"}, "--forward-auth-trusted-proxies", "127.0.0.1")
	// Nothing below error is logged, so the only proof of life is a clean exit.
	time.Sleep(500 * time.Millisecond)
	_ = server.cmd.Process.Signal(syscall.SIGTERM)
	server.drain(t)
	if code := server.wait(t); code != 0 {
		t.Fatalf("exit %d; logs:\n%s", code, server.joined())
	}
	if len(server.logs) != 0 {
		t.Fatalf("logs at level error:\n%s", server.joined())
	}
}

func TestAListenFailureExitsWithFailure(t *testing.T) {
	first := startServer(t, nil)
	first.waitListening(t)
	stdout, stderr, code := runToCompletion(t, nil,
		"--db", filepath.Join(t.TempDir(), "other.sqlite3"), "--addr", first.addr)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "cannot listen on "+first.addr) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestForwardAuthOptionsReachTheServer(t *testing.T) {
	server := startServer(t, nil,
		"--forward-auth-trusted-proxies", "127.0.0.1",
		"--forward-auth-user-header", "X-Who")
	server.waitListening(t)
	if got := server.get(t, "/api/export?format=json", nil).StatusCode; got != http.StatusUnauthorized {
		t.Errorf("no header: status %d", got)
	}
	if got := server.get(t, "/api/export?format=json", map[string]string{"X-Who": "alice"}).StatusCode; got != http.StatusOK {
		t.Errorf("custom header: status %d", got)
	}
	if got := server.get(t, "/api/export?format=json", map[string]string{"Remote-User": "alice"}).StatusCode; got != http.StatusUnauthorized {
		t.Errorf("default header is not the configured one: status %d", got)
	}
}

func TestForwardAuthStaysOffWithoutTrustedProxies(t *testing.T) {
	server := startServer(t, nil)
	server.waitListening(t)
	if got := server.get(t, "/api/export?format=json", map[string]string{"Remote-User": "alice"}).StatusCode; got != http.StatusUnauthorized {
		t.Errorf("status %d", got)
	}
}

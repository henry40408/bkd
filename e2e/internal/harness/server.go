// Package harness drives a bkd server and a browser for the e2e suite.
package harness

import (
	"bufio"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	// Registers the "sqlite" driver the database edit below uses.
	_ "modernc.org/sqlite"
)

// BinaryEnv names the variable that pins the server binary.
const BinaryEnv = "E2E_BINARY"

const (
	startupTimeout    = 30 * time.Second
	setupTokenTimeout = 5 * time.Second
	setupTokenMarker  = "setup token "
)

// Server is one bkd under test: the real binary, a throwaway database and a
// free port. Stop kills it and deletes the database.
type Server struct {
	// BaseURL is where the server listens, without a trailing slash.
	BaseURL string
	// SetupToken is the first-run token the server printed to its log.
	SetupToken string
	// DBPath is the database file, for a scenario that must age a row instead
	// of waiting.
	DBPath string

	cmd     *exec.Cmd
	exited  chan error
	tempDir string
}

// EnsureBinary returns the server binary: $E2E_BINARY, else bin/bkd in the
// repository, built first when it is missing.
//
// An existing bin/bkd is never rebuilt, so rebuild it by hand after changing
// server code.
func EnsureBinary() (string, error) {
	if path := os.Getenv(BinaryEnv); path != "" {
		return path, nil
	}
	binary := filepath.Join(repoRoot(), "bin", "bkd")
	if isFile(binary) {
		return binary, nil
	}
	fmt.Fprintf(os.Stderr, "e2e: %s is missing, building it\n", binary)
	build := exec.Command("go", "build", "-o", binary, "./cmd/bkd")
	build.Dir = repoRoot()
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("running go build: %w", err)
	}
	return binary, nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// repoRoot is the repository root, three levels above e2e/internal/harness.
// It comes from this file's compile-time path, so the suite works from any
// working directory.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// StartServer starts the binary on an empty database and waits until it
// answers.
func StartServer(binary string) (*Server, error) {
	tempDir, err := os.MkdirTemp("", "bkd-e2e-")
	if err != nil {
		return nil, fmt.Errorf("creating the directory for the test database: %w", err)
	}
	address, err := freeAddress()
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, err
	}
	server := &Server{
		BaseURL: "http://" + address,
		DBPath:  filepath.Join(tempDir, "test.sqlite3"),
		tempDir: tempDir,
	}
	if err := server.launch(binary, address); err != nil {
		server.Stop()
		return nil, err
	}
	return server, nil
}

func (s *Server) launch(binary, address string) error {
	cmd := exec.Command(binary, "--db", s.DBPath, "--addr", address)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", binary, err)
	}
	s.cmd = cmd
	tokens := forwardLogAndFindSetupToken(stderr)
	s.exited = make(chan error, 1)
	go func() { s.exited <- cmd.Wait() }()

	if err := s.waitUntilAnswering(); err != nil {
		return err
	}
	// The token is logged before the listener binds, so it has arrived by now.
	select {
	case s.SetupToken = <-tokens:
		return nil
	case <-time.After(setupTokenTimeout):
		return fmt.Errorf("the server did not log a setup token within %v", setupTokenTimeout)
	}
}

// forwardLogAndFindSetupToken echoes the server's log to ours, so startup
// failures show in the test output, and delivers the setup token it logs.
func forwardLogAndFindSetupToken(stderr io.Reader) <-chan string {
	tokens := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(os.Stderr, "bkd: "+line)
			if _, token, found := strings.Cut(line, setupTokenMarker); found {
				// slog quotes the message, so the token ends in a quote.
				select {
				case tokens <- strings.Trim(strings.TrimSpace(token), `"`):
				default:
				}
			}
		}
	}()
	return tokens
}

// Stop kills the server and deletes its database.
func (s *Server) Stop() {
	if s.cmd != nil && s.exited != nil {
		_ = s.cmd.Process.Kill()
		<-s.exited //nolint:errcheck // the process was just killed, so its exit error is expected
		s.cmd = nil
	}
	_ = os.RemoveAll(s.tempDir)
}

// AgeConfirmations makes every session's last password confirmation old. The
// server asks for the password again once the last one is five minutes old;
// waiting that long is not an option.
func (s *Server) AgeConfirmations() error {
	db, err := sql.Open("sqlite", s.DBPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec("UPDATE sessions SET confirmed_at = 0")
	return err
}

// SetLinkCheck stores a link check result on the bookmark with this URL. The
// checker only contacts public addresses, so a scenario cannot make a real
// server answer "moved" or "broken"; this puts the stored result in place.
func (s *Server) SetLinkCheck(url, status, detail string) error {
	db, err := sql.Open("sqlite", s.DBPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	result, err := db.Exec(
		"UPDATE bookmarks SET link_status = ?, link_checked_at = ?, link_detail = ? WHERE url = ?",
		status, time.Now().Unix(), detail, url)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("no bookmark has the URL %s", url)
	}
	return nil
}

// SetSite stores what a page said about itself on the bookmark with this URL.
// The server only contacts public addresses, so a scenario cannot make a real
// server answer with a page; this puts the stored details in place.
func (s *Server) SetSite(url, title, description string) error {
	db, err := sql.Open("sqlite", s.DBPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	result, err := db.Exec(
		"UPDATE bookmarks SET site_title = ?, site_description = ? WHERE url = ?",
		title, description, url)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("no bookmark has the URL %s", url)
	}
	return nil
}

// Backdate makes the bookmark with this URL as old as age says, so a page that
// prints "3 days ago" does so on every run. A scenario cannot wait that long.
func (s *Server) Backdate(url string, age time.Duration) error {
	db, err := sql.Open("sqlite", s.DBPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	then := time.Now().Add(-age).Unix()
	result, err := db.Exec("UPDATE bookmarks SET created_at = ?, updated_at = ? WHERE url = ?", then, then, url)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("no bookmark has the URL %s", url)
	}
	return nil
}

// freeAddress is an unused loopback address. Racy (released before the server
// claims it); a clash makes the new server exit, which waitUntilAnswering
// reports.
func freeAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("probing for a free port: %w", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().String(), nil
}

// waitUntilAnswering fails at once if our child has exited, so a port clash
// cannot pass by reaching another scenario's server.
func (s *Server) waitUntilAnswering() error {
	url := s.BaseURL + "/healthz"
	deadline := time.Now().Add(startupTimeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-s.exited:
			s.exited <- err
			return fmt.Errorf("the server exited (%v) before it answered", err)
		default:
		}
		if answers(url) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("the server did not answer %s within %v", url, startupTimeout)
}

func answers(url string) bool {
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return response.StatusCode == http.StatusOK
}

package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

func envOf(pairs map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := pairs[name]
		return value, ok
	}
}

func mustParse(t *testing.T, args []string, env map[string]string) options {
	t.Helper()
	opts, err := parseOptions(args, envOf(env), io.Discard)
	if err != nil {
		t.Fatalf("parseOptions(%v): %v", args, err)
	}
	return opts
}

func mustFail(t *testing.T, args []string, env map[string]string) error {
	t.Helper()
	_, err := parseOptions(args, envOf(env), io.Discard)
	if err == nil {
		t.Fatalf("parseOptions(%v, %v) succeeded", args, env)
	}
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("error %v is not a usage error", err)
	}
	return err
}

func TestDefaults(t *testing.T) {
	opts := mustParse(t, nil, nil)
	if opts.db != "bkd.sqlite3" || opts.addr != netip.MustParseAddrPort("127.0.0.1:8080") ||
		opts.secureCookies || len(opts.trustedProxies) != 0 ||
		opts.userHeader != "Remote-User" || opts.groupsHeader != "Remote-Groups" || opts.adminGroup != "" ||
		opts.linkCheckInterval != 0 || opts.linkCheckBatch != 20 ||
		opts.logLevel != slog.LevelInfo || opts.showVersion {
		t.Fatalf("unexpected defaults: %+v", opts)
	}
}

func TestEnvironmentSetsEverySetting(t *testing.T) {
	opts := mustParse(t, nil, map[string]string{
		"BKD_DB":                           "/data/x.db",
		"BKD_ADDR":                         "0.0.0.0:9000",
		"BKD_SECURE_COOKIES":               "true",
		"BKD_FORWARD_AUTH_TRUSTED_PROXIES": "127.0.0.1,172.18.0.0/16",
		"BKD_FORWARD_AUTH_USER_HEADER":     "X-User",
		"BKD_FORWARD_AUTH_GROUPS_HEADER":   "X-Groups",
		"BKD_FORWARD_AUTH_ADMIN_GROUP":     "admins",
		"BKD_LINK_CHECK_INTERVAL":          "3600",
		"BKD_LINK_CHECK_BATCH":             "50",
		"BKD_LOG_LEVEL":                    "debug",
	})
	if opts.db != "/data/x.db" || opts.addr.String() != "0.0.0.0:9000" || !opts.secureCookies ||
		strings.Join(opts.trustedProxies, "|") != "127.0.0.1|172.18.0.0/16" ||
		opts.userHeader != "X-User" || opts.groupsHeader != "X-Groups" || opts.adminGroup != "admins" ||
		opts.linkCheckInterval != time.Hour || opts.linkCheckBatch != 50 || opts.logLevel != slog.LevelDebug {
		t.Fatalf("unexpected options: %+v", opts)
	}
}

func TestFlagBeatsEnvBeatsDefault(t *testing.T) {
	env := map[string]string{"BKD_DB": "from-env", "BKD_ADDR": "127.0.0.1:1"}
	opts := mustParse(t, []string{"--db", "from-flag"}, env)
	if opts.db != "from-flag" || opts.addr.Port() != 1 {
		t.Fatalf("db %q, addr %v", opts.db, opts.addr)
	}
}

func TestBooleanSecureCookiesFollowClap(t *testing.T) {
	if !mustParse(t, []string{"--secure-cookies"}, nil).secureCookies {
		t.Error("bare flag did not turn cookies on")
	}
	if mustParse(t, []string{"--secure-cookies"}, map[string]string{"BKD_SECURE_COOKIES": "false"}).secureCookies != true {
		t.Error("flag did not beat a false env")
	}
	for _, value := range []string{"1", "yes", "TRUE", "on", "", "maybe"} {
		mustFail(t, nil, map[string]string{"BKD_SECURE_COOKIES": value})
	}
}

func TestAddrMustBeAnIPAddressAndPort(t *testing.T) {
	for _, value := range []string{"localhost:8080", "127.0.0.1", ":8080", "", "x"} {
		mustFail(t, []string{"--addr", value}, nil)
	}
	if got := mustParse(t, []string{"--addr", "[::1]:8080"}, nil).addr.String(); got != "[::1]:8080" {
		t.Fatalf("addr %q", got)
	}
}

func TestLinkCheckRanges(t *testing.T) {
	cases := []struct {
		flag, value string
		ok          bool
	}{
		{"--link-check-interval", "0", true},
		{"--link-check-interval", "2592000", true},
		{"--link-check-interval", "2592001", false},
		{"--link-check-interval", "-1", false},
		{"--link-check-interval", "soon", false},
		{"--link-check-interval", "", false},
		{"--link-check-batch", "1", true},
		{"--link-check-batch", "200", true},
		{"--link-check-batch", "0", false},
		{"--link-check-batch", "201", false},
	}
	for _, c := range cases {
		_, err := parseOptions([]string{c.flag, c.value}, envOf(nil), io.Discard)
		if (err == nil) != c.ok {
			t.Errorf("%s %q: error %v, want ok=%v", c.flag, c.value, err, c.ok)
		}
	}
	mustFail(t, nil, map[string]string{"BKD_LINK_CHECK_BATCH": "201"})
}

func TestLogLevelComesFromTheEnvironmentOnly(t *testing.T) {
	for text, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError,
	} {
		if got := mustParse(t, nil, map[string]string{"BKD_LOG_LEVEL": text}).logLevel; got != want {
			t.Errorf("%s: %v", text, got)
		}
	}
	for _, bad := range []string{"", "DEBUG", "trace", "bkd=debug"} {
		mustFail(t, nil, map[string]string{"BKD_LOG_LEVEL": bad})
	}
	mustFail(t, []string{"--log-level", "debug"}, nil)
}

func TestTrustedProxiesSplitLikeClap(t *testing.T) {
	opts := mustParse(t, []string{"--forward-auth-trusted-proxies", "10.0.0.0/8, 127.0.0.1"}, nil)
	if len(opts.trustedProxies) != 2 {
		t.Fatalf("proxies %q", opts.trustedProxies)
	}
	// Rust rejected an empty piece, so an empty variable is an error rather than "off".
	for _, bad := range []string{"", ",", "10.0.0.0/8,,127.0.0.1", "nonsense", "10.0.0.0/99"} {
		mustFail(t, []string{"--forward-auth-trusted-proxies", bad}, nil)
	}
}

func TestForwardAuthHeadersAreValidated(t *testing.T) {
	mustFail(t, []string{"--forward-auth-trusted-proxies", "127.0.0.1", "--forward-auth-user-header", "not a header"}, nil)
	// Without a proxy list forward auth is off and the headers are not used.
	mustParse(t, []string{"--forward-auth-user-header", "not a header"}, nil)
}

func TestAdminGroupMayBeEmpty(t *testing.T) {
	opts := mustParse(t, []string{"--forward-auth-trusted-proxies", "127.0.0.1"},
		map[string]string{"BKD_FORWARD_AUTH_ADMIN_GROUP": ""})
	forward, err := opts.forwardAuth(nil)
	if err != nil || forward == nil {
		t.Fatalf("forwardAuth: %v, %v", forward, err)
	}
}

func TestUnknownFlagAndStrayArgumentAreUsageErrors(t *testing.T) {
	mustFail(t, []string{"--nope"}, nil)
	mustFail(t, []string{"serve"}, nil)
}

func TestHelpPrintsUsage(t *testing.T) {
	var usage bytes.Buffer
	_, err := parseOptions([]string{"-h"}, envOf(nil), &usage)
	if err == nil || !strings.Contains(usage.String(), "--forward-auth-admin-group") ||
		!strings.Contains(usage.String(), "BKD_LOG_LEVEL") {
		t.Fatalf("err %v, usage %q", err, usage.String())
	}
}

func TestEveryLiveEnvVarIsASpecAndNeverLegacy(t *testing.T) {
	var opts options
	specEnvs := map[string]bool{}
	for _, spec := range opts.specs() {
		if spec.env != "" {
			specEnvs[spec.env] = true
		}
	}
	if len(specEnvs) != len(liveEnvVars) {
		t.Fatalf("specs read %d variables, liveEnvVars lists %d", len(specEnvs), len(liveEnvVars))
	}
	for _, name := range liveEnvVars {
		if !specEnvs[name] {
			t.Errorf("%s is listed live but no option reads it", name)
		}
		for _, legacy := range legacyEnvVars {
			if legacy.old == name {
				t.Errorf("%s is both live and retired", name)
			}
		}
	}
}

func TestEveryLiveEnvVarIsInTheReadme(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range liveEnvVars {
		if !strings.Contains(string(readme), "`"+name+"`") {
			t.Errorf("README.md does not document %s", name)
		}
	}
}

func TestRetiredVariableIsFoundEvenWhenEmpty(t *testing.T) {
	err := ensureNoLegacyEnvVars(envOf(map[string]string{"RUST_LOG": ""}))
	if err == nil || !strings.Contains(err.Error(), "  RUST_LOG -> BKD_LOG_LEVEL") {
		t.Fatalf("err %v", err)
	}
	if err := ensureNoLegacyEnvVars(envOf(map[string]string{"BKD_LOG_LEVEL": "info"})); err != nil {
		t.Fatalf("err %v", err)
	}
}

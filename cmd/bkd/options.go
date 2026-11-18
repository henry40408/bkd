package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/henry40408/bkd/internal/auth"
)

const (
	defaultDB           = "bkd.sqlite3"
	defaultAddr         = "127.0.0.1:8080"
	defaultUserHeader   = "Remote-User"
	defaultGroupsHeader = "Remote-Groups"
	defaultBatch        = 20
	maxBatch            = 200
	// 30 days, as the Rust server.
	maxIntervalSeconds = 30 * 24 * 60 * 60

	logLevelEnv = "BKD_LOG_LEVEL"
)

// liveEnvVars lists every variable the server reads, next to legacyEnvVars.
// Update both whenever one is added, renamed or removed; a test keeps them
// in step with the specs.
var liveEnvVars = []string{
	"BKD_DB", "BKD_ADDR", "BKD_SECURE_COOKIES",
	"BKD_FORWARD_AUTH_TRUSTED_PROXIES", "BKD_FORWARD_AUTH_USER_HEADER",
	"BKD_FORWARD_AUTH_GROUPS_HEADER", "BKD_FORWARD_AUTH_ADMIN_GROUP",
	"BKD_LINK_CHECK_INTERVAL", "BKD_LINK_CHECK_BATCH", logLevelEnv,
}

// usageError marks a bad command line or environment value; main exits 2.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

type options struct {
	db            string
	addr          netip.AddrPort
	secureCookies bool
	// trustedProxies is empty when forward auth is off.
	trustedProxies    []string
	userHeader        string
	groupsHeader      string
	adminGroup        string
	linkCheckInterval time.Duration
	linkCheckBatch    int
	logLevel          slog.Level
	showVersion       bool
}

// flagSpec describes one setting. Precedence is flag, then env, then default:
// the environment is applied first and a flag parsed afterwards overrides it.
// An empty name means the setting has no flag.
type flagSpec struct {
	name    string
	env     string
	usage   string
	boolean bool
	set     func(value string) error
}

func newOptions() options {
	return options{
		db:             defaultDB,
		addr:           netip.MustParseAddrPort(defaultAddr),
		userHeader:     defaultUserHeader,
		groupsHeader:   defaultGroupsHeader,
		linkCheckBatch: defaultBatch,
		logLevel:       slog.LevelInfo,
	}
}

func (o *options) specs() []flagSpec {
	return []flagSpec{
		{name: "db", env: "BKD_DB", set: setString(&o.db),
			usage: "path of the SQLite database file (default " + defaultDB + ")"},
		{name: "addr", env: "BKD_ADDR", set: o.setAddr,
			usage: "address to listen on, an IP address and port (default " + defaultAddr + ")"},
		{name: "secure-cookies", env: "BKD_SECURE_COOKIES", boolean: true, set: o.setSecureCookies,
			usage: "mark the session cookie Secure; turn it on when served over HTTPS"},
		{name: "forward-auth-trusted-proxies", env: "BKD_FORWARD_AUTH_TRUSTED_PROXIES", set: o.setTrustedProxies,
			usage: "reverse proxies (addresses or CIDR ranges, comma separated) whose user header is believed; empty turns forward auth off"},
		{name: "forward-auth-user-header", env: "BKD_FORWARD_AUTH_USER_HEADER", set: setString(&o.userHeader),
			usage: "header that carries the signed-in user name (default " + defaultUserHeader + ")"},
		{name: "forward-auth-groups-header", env: "BKD_FORWARD_AUTH_GROUPS_HEADER", set: setString(&o.groupsHeader),
			usage: "header that carries the user's groups, comma separated (default " + defaultGroupsHeader + ")"},
		{name: "forward-auth-admin-group", env: "BKD_FORWARD_AUTH_ADMIN_GROUP", set: setString(&o.adminGroup),
			usage: "members of this group are admins; everyone else is a member"},
		{name: "link-check-interval", env: "BKD_LINK_CHECK_INTERVAL", set: o.setLinkCheckInterval,
			usage: "seconds between rounds of the background link check, 0 to " + strconv.Itoa(maxIntervalSeconds) +
				"; 0 turns it off (default 0)"},
		{name: "link-check-batch", env: "BKD_LINK_CHECK_BATCH", set: o.setLinkCheckBatch,
			usage: "bookmarks the background link check visits per round, 1 to " + strconv.Itoa(maxBatch) +
				" (default " + strconv.Itoa(defaultBatch) + ")"},
		// Environment only: debug|info|warn|error, no target or filter syntax.
		{env: logLevelEnv, set: o.setLogLevel},
		{name: "version", boolean: true, set: o.setShowVersion,
			usage: "print the version and exit"},
	}
}

// parseOptions reads the environment, then args. The caller prints the returned
// error; errw receives only the usage text.
func parseOptions(args []string, lookupEnv func(string) (string, bool), errw io.Writer) (options, error) {
	opts := newOptions()
	specs := opts.specs()
	if err := applyEnv(specs, lookupEnv); err != nil {
		return options{}, usageError{err}
	}
	flags := newFlagSet(specs)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// Only a request for help prints usage; a bad flag is reported once,
			// by the caller.
			flags.SetOutput(errw)
			flags.Usage()
		}
		return options{}, usageError{err}
	}
	if flags.NArg() > 0 {
		return options{}, usageError{fmt.Errorf("unexpected argument %q", flags.Arg(0))}
	}
	if err := opts.validateForwardAuth(); err != nil {
		return options{}, usageError{err}
	}
	return opts, nil
}

func applyEnv(specs []flagSpec, lookupEnv func(string) (string, bool)) error {
	for _, spec := range specs {
		if spec.env == "" {
			continue
		}
		value, ok := lookupEnv(spec.env)
		if !ok {
			continue
		}
		if err := spec.set(value); err != nil {
			return fmt.Errorf("%s: %w", spec.env, err)
		}
	}
	return nil
}

func newFlagSet(specs []flagSpec) *flag.FlagSet {
	flags := flag.NewFlagSet("bkd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	for _, spec := range specs {
		if spec.name == "" {
			continue
		}
		flags.Var(funcValue{boolean: spec.boolean, set: spec.set}, spec.name, spec.usage)
	}
	flags.Usage = func() { printUsage(flags) }
	return flags
}

func printUsage(flags *flag.FlagSet) {
	out := flags.Output()
	_, _ = fmt.Fprint(out, "Usage: bkd [OPTIONS]\n\n"+
		"A single-binary bookmark manager. Each option can also be set by the environment\n"+
		"variable BKD_<NAME>, such as BKD_DB; a flag wins. BKD_LOG_LEVEL (debug, info,\n"+
		"warn or error; default info) has no flag.\n\nOptions:\n")
	flags.VisitAll(func(f *flag.Flag) {
		_, _ = fmt.Fprintf(out, "  --%s\n        %s\n", f.Name, f.Usage)
	})
}

// funcValue lets a flag call a setter, so a flag and its environment variable
// share one validation.
type funcValue struct {
	boolean bool
	set     func(string) error
}

func (v funcValue) String() string     { return "" }
func (v funcValue) Set(s string) error { return v.set(s) }
func (v funcValue) IsBoolFlag() bool   { return v.boolean }

func setString(target *string) func(string) error {
	return func(value string) error {
		*target = value
		return nil
	}
}

func (o *options) setAddr(value string) error {
	addr, err := netip.ParseAddrPort(value)
	if err != nil {
		// Hostnames are refused, as with Rust's SocketAddr.
		return fmt.Errorf("invalid address %q: must be an IP address and port such as 127.0.0.1:8080", value)
	}
	o.addr = addr
	return nil
}

// setSecureCookies takes what clap's bool takes: exactly true or false. A bare
// --secure-cookies flag arrives as "true".
func (o *options) setSecureCookies(value string) error {
	switch value {
	case "true":
		o.secureCookies = true
	case "false":
		o.secureCookies = false
	default:
		return fmt.Errorf("invalid value %q (want true or false)", value)
	}
	return nil
}

// setTrustedProxies splits on commas like clap's value_delimiter: nothing is
// trimmed and an empty piece is kept, so "a,,b" and "" fail validation as in Rust.
func (o *options) setTrustedProxies(value string) error {
	o.trustedProxies = strings.Split(value, ",")
	return nil
}

func (o *options) setLinkCheckInterval(value string) error {
	seconds, err := parseBounded(value, 0, maxIntervalSeconds)
	if err != nil {
		return err
	}
	o.linkCheckInterval = time.Duration(seconds) * time.Second
	return nil
}

func (o *options) setLinkCheckBatch(value string) error {
	batch, err := parseBounded(value, 1, maxBatch)
	if err != nil {
		return err
	}
	o.linkCheckBatch = int(batch)
	return nil
}

func (o *options) setLogLevel(value string) error {
	level, err := parseLogLevel(value)
	if err != nil {
		return err
	}
	o.logLevel = level
	return nil
}

func (o *options) setShowVersion(value string) error {
	show, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	o.showVersion = show
	return nil
}

func parseBounded(value string, low, high uint64) (uint64, error) {
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a whole number", value)
	}
	if number < low || number > high {
		return 0, fmt.Errorf("%d is not in %d..=%d", number, low, high)
	}
	return number, nil
}

// validateForwardAuth rejects a bad proxy list or header name before any work
// is done, so a typo cannot leave the site without the protection it was
// meant to have.
func (o *options) validateForwardAuth() error {
	if o.showVersion {
		return nil
	}
	_, err := o.forwardAuth(nil)
	return err
}

// forwardAuth is nil when no trusted proxy is set.
func (o *options) forwardAuth(logger *slog.Logger) (*auth.ForwardAuth, error) {
	if len(o.trustedProxies) == 0 {
		return nil, nil
	}
	forward, err := auth.NewForwardAuth(auth.ForwardAuthConfig{
		TrustedProxies: o.trustedProxies,
		UserHeader:     o.userHeader,
		GroupsHeader:   o.groupsHeader,
		AdminGroup:     o.adminGroup,
		Logger:         logger,
	})
	if err != nil {
		return nil, fmt.Errorf("invalid forward auth setting: %w", err)
	}
	return forward, nil
}

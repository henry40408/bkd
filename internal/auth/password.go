package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	minPasswordLength = 12
	// Argon2 cost grows with input size, so an unbounded password is a cheap way to burn CPU.
	maxPasswordBytes = 256
)

// The messages are shown to people, so they are part of the interface.
var (
	ErrPasswordTooShort        = errors.New("password must be at least 12 characters")
	ErrPasswordTooLong         = errors.New("password must be at most 256 bytes")
	ErrPasswordContainsName    = errors.New("password must not contain the username")
	errUnsupportedPasswordHash = errors.New("unsupported password hash")
)

// ValidatePassword checks a new password for the account username (already
// normalized). Login does not call this: an old password that no longer meets
// the rules must still work.
func ValidatePassword(password, username string) error {
	if len(password) > maxPasswordBytes {
		return ErrPasswordTooLong
	}
	if utf8.RuneCountInString(password) < minPasswordLength {
		return ErrPasswordTooShort
	}
	if strings.Contains(strings.ToLower(password), username) {
		return ErrPasswordContainsName
	}
	return nil
}

// Argon2id parameters of every new hash (OWASP's second option).
const (
	hashMemoryKiB = 19456
	hashTime      = 2
	hashThreads   = 1
	saltBytes     = 16
	digestBytes   = 32
)

// Limits for hashes read from the database. The parameters sit inside the
// stored string, so a damaged or hostile row must not choose unbounded cost.
const (
	maxMemoryKiB   = 256 * 1024
	maxTime        = 10
	maxThreads     = 16
	minSaltBytes   = 8
	minDigestBytes = 10
	maxDigestBytes = 64
	argon2Version  = 19
)

func hashPassword(password string) string {
	salt := randomBytes(saltBytes)
	digest := argon2.IDKey([]byte(password), salt, hashTime, hashMemoryKiB, hashThreads, digestBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version, hashMemoryKiB, hashTime, hashThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest))
}

type passwordHash struct {
	memory  uint32
	time    uint32
	threads uint8
	salt    []byte
	digest  []byte
}

// parsePasswordHash reads $argon2id$[v=19$]m=..,t=..,p=..$salt$digest with
// standard base64 without padding. A parameter that is missing takes the value
// of the hashes bkd writes.
func parsePasswordHash(stored string) (passwordHash, error) {
	fields := strings.Split(stored, "$")
	if len(fields) < 5 || fields[0] != "" || fields[1] != "argon2id" {
		return passwordHash{}, errUnsupportedPasswordHash
	}
	fields = fields[2:]
	if version, found := strings.CutPrefix(fields[0], "v="); found {
		if version != strconv.Itoa(argon2Version) {
			return passwordHash{}, errUnsupportedPasswordHash
		}
		fields = fields[1:]
	}
	if len(fields) != 3 {
		return passwordHash{}, errUnsupportedPasswordHash
	}
	parsed, err := parseCostParameters(fields[0])
	if err != nil {
		return passwordHash{}, err
	}
	if parsed.salt, err = decodeBounded(fields[1], minSaltBytes, 64); err != nil {
		return passwordHash{}, err
	}
	if parsed.digest, err = decodeBounded(fields[2], minDigestBytes, maxDigestBytes); err != nil {
		return passwordHash{}, err
	}
	return parsed, nil
}

func parseCostParameters(text string) (passwordHash, error) {
	parsed := passwordHash{memory: hashMemoryKiB, time: hashTime, threads: hashThreads}
	for _, pair := range strings.Split(text, ",") {
		name, value, found := strings.Cut(pair, "=")
		number, err := strconv.ParseUint(value, 10, 32)
		if !found || err != nil {
			return passwordHash{}, errUnsupportedPasswordHash
		}
		if err := setCostParameter(&parsed, name, number); err != nil {
			return passwordHash{}, err
		}
	}
	return parsed, nil
}

func setCostParameter(parsed *passwordHash, name string, number uint64) error {
	switch {
	case name == "m" && number >= 8 && number <= maxMemoryKiB:
		parsed.memory = uint32(number)
	case name == "t" && number >= 1 && number <= maxTime:
		parsed.time = uint32(number)
	case name == "p" && number >= 1 && number <= maxThreads:
		parsed.threads = uint8(number)
	default:
		return errUnsupportedPasswordHash
	}
	return nil
}

func decodeBounded(text string, minBytes, maxBytes int) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(text)
	if err != nil || len(decoded) < minBytes || len(decoded) > maxBytes {
		return nil, errUnsupportedPasswordHash
	}
	return decoded, nil
}

// verifyPassword is false for a wrong password and for a stored hash that
// cannot be parsed.
func verifyPassword(password, stored string) bool {
	// Oversized input is refused before hashing, for the same reason as in ValidatePassword.
	if len(password) > maxPasswordBytes {
		return false
	}
	parsed, err := parsePasswordHash(stored)
	if err != nil {
		return false
	}
	digest := argon2.IDKey([]byte(password), parsed.salt, parsed.time, parsed.memory,
		parsed.threads, uint32(len(parsed.digest)))
	return subtle.ConstantTimeCompare(digest, parsed.digest) == 1
}

// Hasher runs Argon2 with a cap on how many runs go at the same time. Each run
// allocates about 19 MiB. Without a cap, a burst of login requests from many
// addresses could use gigabytes at once, so only as many runs as there are CPU
// cores go together and the rest wait their turn.
type Hasher struct {
	permits chan struct{}

	dummyOnce sync.Once
	dummy     string
}

func NewHasher() *Hasher {
	return &Hasher{permits: make(chan struct{}, runtime.NumCPU())}
}

// run waits for a permit, then does the work on its own goroutine. The permit
// goes back only when the work ends: if the caller gives up, the Argon2 run
// still goes on (it cannot be interrupted), so it must still count.
func run[T any](ctx context.Context, hasher *Hasher, work func() T) (T, error) {
	var zero T
	select {
	case hasher.permits <- struct{}{}:
	case <-ctx.Done():
		return zero, ctx.Err()
	}
	done := make(chan T, 1)
	go func() {
		defer func() { <-hasher.permits }()
		done <- work()
	}()
	select {
	case result := <-done:
		return result, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// Hash returns a PHC string for the password.
func (hasher *Hasher) Hash(ctx context.Context, password string) (string, error) {
	return run(ctx, hasher, func() string { return hashPassword(password) })
}

// Verify is false for a wrong password and for a stored hash that cannot be parsed.
func (hasher *Hasher) Verify(ctx context.Context, password, stored string) (bool, error) {
	return run(ctx, hasher, func() bool { return verifyPassword(password, stored) })
}

func (hasher *Hasher) dummyHash() string {
	hasher.dummyOnce.Do(func() { hasher.dummy = hashPassword("dummy password") })
	return hasher.dummy
}

// WarmUp builds the dummy hash now, so the first login attempt is not slower
// than the rest. Call it once at startup.
func (hasher *Hasher) WarmUp(ctx context.Context) error {
	_, err := run(ctx, hasher, hasher.dummyHash)
	return err
}

// VerifyDummy spends the same time as a real check. Call it when the username
// does not exist or the account is disabled, so response time does not reveal
// which usernames exist.
func (hasher *Hasher) VerifyDummy(ctx context.Context, password string) error {
	_, err := run(ctx, hasher, func() bool {
		return verifyPassword(password, hasher.dummyHash())
	})
	return err
}

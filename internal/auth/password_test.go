package auth

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCorrectPasswordVerifies(t *testing.T) {
	stored := hashPassword("correct horse")
	if !verifyPassword("correct horse", stored) {
		t.Fatal("correct password was rejected")
	}
}

func TestWrongPasswordIsRejected(t *testing.T) {
	stored := hashPassword("correct horse")
	if verifyPassword("wrong horse", stored) {
		t.Fatal("wrong password was accepted")
	}
}

func TestSamePasswordGetsADifferentSalt(t *testing.T) {
	first, second := hashPassword("correct horse"), hashPassword("correct horse")
	if first == second {
		t.Fatal("two hashes are identical")
	}
}

func TestGarbageHashNeverVerifies(t *testing.T) {
	for _, stored := range []string{"not a hash", "", "!", "$argon2id$v=19$m=19456,t=2,p=1$$"} {
		if verifyPassword("anything", stored) {
			t.Errorf("%q verified", stored)
		}
	}
}

func TestOversizedPasswordIsRefused(t *testing.T) {
	stored := hashPassword("correct horse")
	long := strings.Repeat("a", maxPasswordBytes+1)
	if verifyPassword(long, stored) {
		t.Fatal("oversized password verified")
	}
	if err := ValidatePassword(long, "alice"); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("got %v", err)
	}
}

func TestShortPasswordIsRefused(t *testing.T) {
	if err := ValidatePassword("12345678901", "alice"); !errors.Is(err, ErrPasswordTooShort) {
		t.Fatalf("got %v", err)
	}
	if err := ValidatePassword("123456789012", "alice"); err != nil {
		t.Fatal(err)
	}
	// Counted in characters, not bytes.
	if err := ValidatePassword("密碼密碼密碼密碼密碼密碼", "alice"); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordWithTheUsernameIsRefused(t *testing.T) {
	for _, bad := range []string{"alice-is-the-best", "my-ALICE-password"} {
		if err := ValidatePassword(bad, "alice"); !errors.Is(err, ErrPasswordContainsName) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if err := ValidatePassword("a different phrase", "alice"); err != nil {
		t.Fatal(err)
	}
}

func TestDummyCheckDoesNotPanic(t *testing.T) {
	if err := NewHasher().VerifyDummy(context.Background(), "whatever"); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordMessagesAreVerbatim(t *testing.T) {
	want := map[error]string{
		ErrPasswordTooShort:     "password must be at least 12 characters",
		ErrPasswordTooLong:      "password must be at most 256 bytes",
		ErrPasswordContainsName: "password must not contain the username",
	}
	for err, text := range want {
		if err.Error() != text {
			t.Errorf("%q != %q", err.Error(), text)
		}
	}
}

// rustPasswordVectors are hashes the Rust server wrote. They are never
// regenerated from Go output.
var rustPasswordVectors = []struct{ password, stored string }{
	{"correct horse battery", "$argon2id$v=19$m=19456,t=2,p=1$plJ2HcKvQ8IC5FndOHYrdQ$59Z60GrXjGgWM5zZu9Hd6Un2YpIW1XrJIytYfB5r3vM"},
	{"sturdy umbrella pass", "$argon2id$v=19$m=19456,t=2,p=1$Zg/+0OcDGvaRLCXwODjOVw$icdzQnw1S4M7dZhJGhcQNTc6Bo9uCPv9k0+F6G4RUPM"},
	{"violet river lantern", "$argon2id$v=19$m=19456,t=2,p=1$dt899HVrkYZEmViQWmaJuw$oy++/tNoseStDzIKKUEeExpCcprspQextR6lIZhtkPo"},
}

func TestGoVerifiesEveryHashTheRustVersionWrote(t *testing.T) {
	for _, vector := range rustPasswordVectors {
		if !verifyPassword(vector.password, vector.stored) {
			t.Errorf("Rust hash of %q does not verify", vector.password)
		}
		if verifyPassword(vector.password+"x", vector.stored) {
			t.Errorf("Rust hash of %q accepts a wrong password", vector.password)
		}
	}
}

func TestGoHashesUseTheSameFormatAsTheRustVersion(t *testing.T) {
	format := regexp.MustCompile(`^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)
	stored, err := NewHasher().Hash(context.Background(), "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !format.MatchString(stored) {
		t.Fatalf("unexpected format %q", stored)
	}
	parsed, err := parsePasswordHash(stored)
	if err != nil || len(parsed.salt) != saltBytes || len(parsed.digest) != digestBytes {
		t.Fatalf("parsed %+v, %v", parsed, err)
	}
	ok, err := NewHasher().Verify(context.Background(), "correct horse battery", stored)
	if err != nil || !ok {
		t.Fatalf("verify = %v, %v", ok, err)
	}
}

func TestCostParametersInAStoredHashAreCapped(t *testing.T) {
	for _, stored := range []string{
		"$argon2id$v=19$m=4194304,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=19456,t=99,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=19456,t=2,p=200$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=18$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		if _, err := parsePasswordHash(stored); err == nil {
			t.Errorf("%q was accepted", stored)
		}
	}
}

func TestWaitingForAPermitEndsWithTheContext(t *testing.T) {
	hasher := &Hasher{permits: make(chan struct{}, 1)}
	hasher.permits <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := hasher.Hash(ctx, "correct horse"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestACancelledRunKeepsItsPermitUntilTheWorkEnds(t *testing.T) {
	hasher := &Hasher{permits: make(chan struct{}, 1)}
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() {
		_, _ = run(ctx, hasher, func() bool {
			close(started)
			<-release
			return true
		})
	}()
	<-started
	cancel()
	// The caller is gone, but the work still holds the only permit.
	time.Sleep(20 * time.Millisecond)
	if len(hasher.permits) != 1 {
		t.Fatal("permit was released before the work ended")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for len(hasher.permits) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("permit was never released")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

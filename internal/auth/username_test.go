package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestUppercaseIsLowercased(t *testing.T) {
	got, err := NormalizeUsername("Henry.W")
	if err != nil || got != "henry.w" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestLengthIsLimited(t *testing.T) {
	if _, err := NormalizeUsername("ab"); !errors.Is(err, ErrUsernameLength) {
		t.Fatalf("2 chars: %v", err)
	}
	if _, err := NormalizeUsername(strings.Repeat("a", 32)); err != nil {
		t.Fatalf("32 chars: %v", err)
	}
	if _, err := NormalizeUsername(strings.Repeat("a", 33)); !errors.Is(err, ErrUsernameLength) {
		t.Fatalf("33 chars: %v", err)
	}
}

func TestOnlyASCIISymbolsAreAllowed(t *testing.T) {
	for _, bad := range []string{"a b c", "ab/c", "mé1", "日本語", " abc", "abc\n"} {
		if _, err := NormalizeUsername(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := NormalizeUsername("a b c"); !errors.Is(err, ErrUsernameCharacter) {
		t.Fatalf("got %v", err)
	}
}

func TestEdgesMustBeAlphanumeric(t *testing.T) {
	for _, bad := range []string{"-abc", "abc.", "_abc", "abc_"} {
		if _, err := NormalizeUsername(bad); !errors.Is(err, ErrUsernameEdge) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestUsernameMessagesAreVerbatim(t *testing.T) {
	want := map[error]string{
		ErrUsernameLength:    "username must be 3 to 32 characters",
		ErrUsernameCharacter: "username may only contain a-z, 0-9, '.', '_' and '-'",
		ErrUsernameEdge:      "username must start and end with a letter or digit",
	}
	for err, text := range want {
		if err.Error() != text {
			t.Errorf("%q != %q", err.Error(), text)
		}
	}
}

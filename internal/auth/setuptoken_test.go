package auth

import (
	"math"
	"strings"
	"testing"
)

func TestDisplayFormIsTwoGroupsOfCrockfordCharacters(t *testing.T) {
	_, display := NewSetupToken()
	first, second, found := strings.Cut(display, "-")
	if !found || len(first) != tokenGroupLength || len(second) != tokenGroupLength {
		t.Fatalf("display = %q", display)
	}
	for _, character := range first + second {
		if !strings.ContainsRune(crockfordAlphabet, character) {
			t.Fatalf("%q is not in the alphabet", character)
		}
	}
}

func TestEncodingUsesTheTopFiftyBits(t *testing.T) {
	cases := []struct {
		name   string
		random uint64
		want   string
	}{
		{"zero", 0, "0000000000"},
		{"all ones", math.MaxUint64, "ZZZZZZZZZZ"},
		// Only the top bit set: the first symbol is 0b10000 = 16 = 'G'.
		{"top bit", 1 << 63, "G000000000"},
		// The lowest kept bit (bit 14) is the last bit of the last symbol.
		{"lowest kept bit", 1 << 14, "0000000001"},
		// The 14 discarded low bits never reach the output.
		{"discarded bits", 1<<14 - 1, "0000000000"},
	}
	for _, tc := range cases {
		if got := encodeCrockford(tc.random); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTheDisplayedTokenMatches(t *testing.T) {
	token, display := NewSetupToken()
	if !token.Matches(display) {
		t.Fatal("displayed token does not match")
	}
}

func TestCaseDashesAndSpacesAreIgnored(t *testing.T) {
	token, display := NewSetupToken()
	sloppy := "  " + strings.ReplaceAll(strings.ToLower(display), "-", " ") + " "
	if !token.Matches(sloppy) {
		t.Fatalf("%q does not match", sloppy)
	}
	if !token.Matches(strings.ReplaceAll(display, "-", "")) {
		t.Fatal("token without the dash does not match")
	}
}

func TestLookAlikeCharactersAreForgiven(t *testing.T) {
	token, display := NewSetupToken()
	misread := strings.NewReplacer("0", "o", "1", "l").Replace(display)
	if !token.Matches(misread) {
		t.Fatalf("%q does not match %q", misread, display)
	}
}

// The test above only reaches a rule when the random token holds the matching
// character, so this one pins every rule.
func TestNormalizeTokenAppliesEachLookAlikeRule(t *testing.T) {
	cases := []struct{ guess, want string }{
		{"o", "0"},
		{"O", "0"},
		{"i", "1"},
		{"I", "1"},
		{"l", "1"},
		{"L", "1"},
		{" ab-c\t", "ABC"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeToken(tc.guess); got != tc.want {
			t.Errorf("normalizeToken(%q) = %q, want %q", tc.guess, got, tc.want)
		}
	}
}

func TestAWrongGuessIsRejected(t *testing.T) {
	token, display := NewSetupToken()
	wrong := []byte(display)
	last := len(wrong) - 1
	wrong[last] = 'A'
	if display[last] == 'A' {
		wrong[last] = 'B'
	}
	for _, guess := range []string{string(wrong), "", "-----"} {
		if token.Matches(guess) {
			t.Errorf("%q matched", guess)
		}
	}
}

func TestATokenDoesNotMatchAnotherInstance(t *testing.T) {
	_, otherDisplay := NewSetupToken()
	token, _ := NewSetupToken()
	if token.Matches(otherDisplay) {
		t.Fatal("token matched another instance's display")
	}
}

func TestExtraCharactersAreRejected(t *testing.T) {
	token, display := NewSetupToken()
	if token.Matches(display + "0") {
		t.Fatal("token with a trailing character matched")
	}
}

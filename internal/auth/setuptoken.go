package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"unicode"
)

// The setup token is the one-time secret that lets the person who can read the
// server log create the first admin. It exists only in memory: it is printed at
// startup and never stored.
//
// The token is 50 random bits written as two groups of five Crockford base32
// characters (XXXXX-XXXXX). Comparison ignores case, dashes and spaces, and runs
// on HMACs so the check takes the same time wherever the guess first differs.

const (
	crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	tokenGroupLength  = 5
	tokenLength       = 2 * tokenGroupLength
)

type SetupToken struct {
	macKey      []byte
	expectedTag []byte
}

// NewSetupToken returns the token to keep in memory and the text to show the operator.
func NewSetupToken() (*SetupToken, string) {
	symbols := encodeCrockford(binary.BigEndian.Uint64(randomBytes(8)))
	display := symbols[:tokenGroupLength] + "-" + symbols[tokenGroupLength:]

	macKey := randomBytes(32)
	return &SetupToken{macKey: macKey, expectedTag: tokenTag(macKey, symbols)}, display
}

// Matches compares in constant time, however the guess is written.
func (token *SetupToken) Matches(guess string) bool {
	return hmac.Equal(tokenTag(token.macKey, normalizeToken(guess)), token.expectedTag)
}

// encodeCrockford turns the top 50 bits of random into ten Crockford symbols.
func encodeCrockford(random uint64) string {
	bits := random >> (64 - 5*tokenLength)
	symbols := make([]byte, tokenLength)
	for index := range symbols {
		shift := 5 * (tokenLength - 1 - index)
		symbols[index] = crockfordAlphabet[(bits>>shift)&0b11111]
	}
	return string(symbols)
}

func tokenTag(key []byte, normalized string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(normalized))
	return mac.Sum(nil)
}

// normalizeToken uppercases, drops dashes and whitespace, and applies Crockford's
// look-alike rules (O is 0; I and L are 1) so a misread character still matches.
func normalizeToken(guess string) string {
	kept := strings.Map(func(character rune) rune {
		if character == '-' || unicode.IsSpace(character) {
			return -1
		}
		return character
	}, guess)
	return strings.Map(func(character rune) rune {
		switch character {
		case 'O':
			return '0'
		case 'I', 'L':
			return '1'
		}
		return character
	}, strings.ToUpper(kept))
}

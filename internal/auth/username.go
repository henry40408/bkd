package auth

import (
	"errors"
	"strings"
)

const (
	minUsernameLength = 3
	maxUsernameLength = 32
)

// The messages are shown to people, so they are part of the interface.
var (
	ErrUsernameLength    = errors.New("username must be 3 to 32 characters")
	ErrUsernameCharacter = errors.New("username may only contain a-z, 0-9, '.', '_' and '-'")
	ErrUsernameEdge      = errors.New("username must start and end with a letter or digit")
)

// NormalizeUsername lowercases the input and checks it against the rules, which
// mirror the CHECK on users.username. Surrounding whitespace is not trimmed: it
// is not an allowed character, so it is an error.
func NormalizeUsername(raw string) (string, error) {
	username := asciiLower(raw)
	if len(username) < minUsernameLength || len(username) > maxUsernameLength {
		return "", ErrUsernameLength
	}
	for index := 0; index < len(username); index++ {
		if !isUsernameByte(username[index]) {
			return "", ErrUsernameCharacter
		}
	}
	if !isAlphanumeric(username[0]) || !isAlphanumeric(username[len(username)-1]) {
		return "", ErrUsernameEdge
	}
	return username, nil
}

// asciiLower folds only A-Z. strings.ToLower would also fold other scripts and
// could change the byte length.
func asciiLower(text string) string {
	return strings.Map(func(character rune) rune {
		if character >= 'A' && character <= 'Z' {
			return character + ('a' - 'A')
		}
		return character
	}, text)
}

func isUsernameByte(char byte) bool {
	return isAlphanumeric(char) || char == '.' || char == '_' || char == '-'
}

func isAlphanumeric(char byte) bool {
	return (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')
}

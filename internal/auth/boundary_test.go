package auth

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPasswordOfExactlyTheMaximumLengthIsAccepted(t *testing.T) {
	exact := strings.Repeat("a", maxPasswordBytes)
	if err := ValidatePassword(exact, "alice"); err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(exact, hashPassword(exact)) {
		t.Fatal("a password of exactly the maximum length did not verify")
	}
}

func hashWith(params string, saltBytes, digestBytes int) string {
	encode := func(size int) string { return base64.RawStdEncoding.EncodeToString(make([]byte, size)) }
	return fmt.Sprintf("$argon2id$v=19$%s$%s$%s", params, encode(saltBytes), encode(digestBytes))
}

func TestStoredHashBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		stored string
		valid  bool
	}{
		{"smallest memory", hashWith("m=8,t=1,p=1", 16, 32), true},
		{"memory below the smallest", hashWith("m=7,t=1,p=1", 16, 32), false},
		{"largest memory", hashWith(fmt.Sprintf("m=%d,t=1,p=1", maxMemoryKiB), 16, 32), true},
		{"memory above the largest", hashWith(fmt.Sprintf("m=%d,t=1,p=1", maxMemoryKiB+1), 16, 32), false},
		{"smallest time", hashWith("m=8,t=1,p=1", 16, 32), true},
		{"time zero", hashWith("m=8,t=0,p=1", 16, 32), false},
		{"largest time", hashWith(fmt.Sprintf("m=8,t=%d,p=1", maxTime), 16, 32), true},
		{"time above the largest", hashWith(fmt.Sprintf("m=8,t=%d,p=1", maxTime+1), 16, 32), false},
		{"threads zero", hashWith("m=8,t=1,p=0", 16, 32), false},
		{"largest threads", hashWith(fmt.Sprintf("m=8,t=1,p=%d", maxThreads), 16, 32), true},
		{"threads above the largest", hashWith(fmt.Sprintf("m=8,t=1,p=%d", maxThreads+1), 16, 32), false},
		{"smallest salt", hashWith("m=8,t=1,p=1", minSaltBytes, 32), true},
		{"salt below the smallest", hashWith("m=8,t=1,p=1", minSaltBytes-1, 32), false},
		{"largest salt", hashWith("m=8,t=1,p=1", 64, 32), true},
		{"salt above the largest", hashWith("m=8,t=1,p=1", 65, 32), false},
		{"smallest digest", hashWith("m=8,t=1,p=1", 16, minDigestBytes), true},
		{"digest below the smallest", hashWith("m=8,t=1,p=1", 16, minDigestBytes-1), false},
		{"largest digest", hashWith("m=8,t=1,p=1", 16, maxDigestBytes), true},
		{"digest above the largest", hashWith("m=8,t=1,p=1", 16, maxDigestBytes+1), false},
		{"no version field", strings.Replace(hashWith("m=8,t=1,p=1", 16, 32), "v=19$", "", 1), true},
		{"four fields", "$argon2id$m=8,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parsePasswordHash(c.stored)
			if (err == nil) != c.valid {
				t.Fatalf("valid = %v, err = %v", c.valid, err)
			}
		})
	}
}

func TestUsernameLengthAndCharacterBoundaries(t *testing.T) {
	cases := map[string]bool{
		"ab":                           false,
		"abc":                          true,
		strings.Repeat("a", 32):        true,
		strings.Repeat("a", 33):        false,
		"a`b":                          false,
		"a{b":                          false,
		"a/b":                          false,
		"a:b":                          false,
		"a.b":                          true,
		"a_b":                          true,
		"a-b":                          true,
		"a9b":                          true,
		"azb":                          true,
		"0zz":                          true,
		"zz9":                          true,
		"`zz":                          false,
		"zz{":                          false,
		"/zz":                          false,
		"zz:":                          false,
		"-zz":                          false,
		"zz.":                          false,
		"ABC":                          true,
		"a@b":                          false,
		"a[b":                          false,
		"a\x00b":                       false,
		"aéb":                          false,
		strings.Repeat("é", 16):        false,
		strings.Repeat("a", 31) + "é":  false,
		strings.Repeat("a", 30) + "éa": false,
	}
	for name, valid := range cases {
		_, err := NormalizeUsername(name)
		if (err == nil) != valid {
			t.Errorf("%q: valid = %v, err = %v", name, valid, err)
		}
	}
}

func TestNetworkPrefixLengthBounds(t *testing.T) {
	for text, valid := range map[string]bool{
		"10.0.0.0/32": true, "10.0.0.0/33": false, "10.0.0.0/0": true,
		"::1/128": true, "::1/129": false, "::ffff:10.0.0.1/32": true, "::ffff:10.0.0.1/33": false,
		"10.0.0.1": true, "10.0.0.1/x": false, "fe80::1%eth0": false,
	} {
		_, err := ParseNetwork(text)
		if (err == nil) != valid {
			t.Errorf("%q: valid = %v, err = %v", text, valid, err)
		}
	}
}

func TestHeaderValueAndNameCharacterBoundaries(t *testing.T) {
	for value, valid := range map[string]bool{
		"": true, " ": true, "~": true, "\t": true, "a\tb": true,
		"\x1f": false, "\x7f": false, "\x00": false, "é": false,
	} {
		if got := isVisibleASCII(value); got != valid {
			t.Errorf("isVisibleASCII(%q) = %v", value, got)
		}
	}
	for name, valid := range map[string]bool{
		"": false, "a": true, "z": true, "A": true, "Z": true, "0": true, "9": true,
		"x-y_z.w": true, "`": true, "{": false, "@": false, "[": false, "/": false, ":": false,
		"a b": false, "a\x00": false,
	} {
		if got := isHeaderName(name); got != valid {
			t.Errorf("isHeaderName(%q) = %v", name, got)
		}
	}
}

func TestRefundNeverGoesBelowZero(t *testing.T) {
	limiter := newTestLimiter()
	ip := address("203.0.113.1")
	now := time.Unix(1_000_000, 0)
	mustAcquire(t, limiter, ip, now)
	limiter.Refund(ip)
	limiter.Refund(ip)
	useUp(t, limiter, ip, now)
	mustRefuse(t, limiter, ip, now)
}

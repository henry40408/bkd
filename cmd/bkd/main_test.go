package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionFlagPrintsTheVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d, stderr %q", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "bkd ") {
		t.Fatalf("stdout %q", stdout.String())
	}
}

func TestMissingFlagIsAUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit code %d", code)
	}
}

func TestUnknownFlagIsAUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--nope"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit code %d", code)
	}
}

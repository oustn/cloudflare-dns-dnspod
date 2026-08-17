package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHelpDoesNotLoadConfiguration(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := (Runner{EnvPath: "/definitely/not/present"}).Run(context.Background(), []string{"--help"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "set-edge") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestParserAcceptsPositionalHostnameAndOriginModes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		args        []string
		originSet   bool
		originValue string
	}{
		{name: "fallback", args: []string{"add", "test.example.com"}},
		{name: "default custom origin", args: []string{"add", "test.example.com", "--origin"}, originSet: true},
		{name: "host custom origin", args: []string{"add", "test.example.com", "--origin=backend.example.org"}, originSet: true, originValue: "backend.example.org"},
		{name: "IP custom origin", args: []string{"add", "test.example.com", "--origin=1.1.1.1"}, originSet: true, originValue: "1.1.1.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse(tc.args, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			if got.hostname != "test.example.com" || got.originSet != tc.originSet || got.origin != tc.originValue {
				t.Fatalf("parsed = %+v", got)
			}
		})
	}
}

func TestParserAcceptsSetBackendAndZone(t *testing.T) {
	t.Parallel()
	got, err := parse([]string{"set-backend", "test.example.com", "1.1.1.1", "--zone", "platform.example.net", "--dry-run"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got.hostname != "test.example.com" || got.backend != "1.1.1.1" || got.zone != "platform.example.net" || !got.dryRun {
		t.Fatalf("parsed = %+v", got)
	}
}

func TestParserRejectsLegacyAndAmbiguousOriginForms(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"add", "--subdomain", "test"},
		{"add", "test.example.com", "--origin", "backend.example.org"},
		{"add"},
	} {
		if _, err := parse(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted args %v", args)
		}
	}
}

func TestParserRejectsMissingOrMultipleEdgeTargets(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"set-edge", "custom.example.com"},
		{"set-edge", "custom.example.com", "--host", "edge.example.com", "--ip", "1.1.1.1"},
	} {
		parsed, err := parse(args, &bytes.Buffer{})
		if err == nil {
			err = validateParsed(parsed)
		}
		if err == nil {
			t.Fatalf("accepted args %v", args)
		}
	}
}

func TestParserRejectsNonPublicIPv4(t *testing.T) {
	t.Parallel()
	parsed, err := parse([]string{"set-edge", "custom.example.com", "--ip", "192.0.2.1"}, &bytes.Buffer{})
	if err == nil {
		err = validateParsed(parsed)
	}
	if err == nil {
		t.Fatal("accepted documentation-only IPv4")
	}
}

func TestRunValidatesEdgeBeforeLoadingConfig(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := (Runner{EnvPath: "/definitely/not/present"}).Run(
		context.Background(),
		[]string{"set-edge", "custom.example.com", "--ip", "192.0.2.1"},
		&stdout,
		&stderr,
	)
	if code != 2 || !strings.Contains(stderr.String(), "not globally routable") || strings.Contains(stderr.String(), "missing environment") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunRejectsRelativeHostnameBeforeLoadingConfig(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := (Runner{EnvPath: "/definitely/not/present"}).Run(
		context.Background(),
		[]string{"add", "custom"},
		&stdout,
		&stderr,
	)
	if code != 2 || !strings.Contains(stderr.String(), "invalid hostname") || strings.Contains(stderr.String(), "missing environment") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

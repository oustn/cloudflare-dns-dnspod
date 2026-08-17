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

func TestParserRejectsMissingOrMultipleEdgeTargets(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"set-edge", "--subdomain", "custom"},
		{"set-edge", "--subdomain", "custom", "--host", "edge.example.com", "--ip", "1.1.1.1"},
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
	parsed, err := parse([]string{"set-edge", "--subdomain", "custom", "--ip", "192.0.2.1"}, &bytes.Buffer{})
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
		[]string{"set-edge", "--subdomain", "custom", "--ip", "192.0.2.1"},
		&stdout,
		&stderr,
	)
	if code != 2 || !strings.Contains(stderr.String(), "not globally routable") || strings.Contains(stderr.String(), "missing environment") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

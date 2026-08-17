package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func completeValues() map[string]string {
	return map[string]string{
		"CF_API_TOKEN":      "cf-secret",
		"CF_ZONE":           "platform.example.net",
		"DNSPOD_SECRET_ID":  "dns-id",
		"DNSPOD_SECRET_KEY": "dns-secret",
	}
}

func minimalValues() map[string]string {
	return map[string]string{
		"CF_API_TOKEN":      "cf-secret",
		"DNSPOD_SECRET_ID":  "dns-id",
		"DNSPOD_SECRET_KEY": "dns-secret",
	}
}

func TestFromValuesUsesMinimalConfiguration(t *testing.T) {
	t.Parallel()
	values := minimalValues()
	values["CF_ZONE"] = "Platform.Example.Net."
	cfg, err := FromValues(values, CommandAdd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CFZone != "platform.example.net" {
		t.Fatalf("CFZone = %q", cfg.CFZone)
	}
	if cfg.DNSPodRecordLine != "默认" {
		t.Fatalf("DNSPodRecordLine = %q", cfg.DNSPodRecordLine)
	}
}

func TestFromValuesAllowsMissingOptionalZone(t *testing.T) {
	t.Parallel()
	if _, err := FromValues(minimalValues(), CommandStatus); err != nil {
		t.Fatalf("optional CF_ZONE rejected: %v", err)
	}
}

func TestFromValuesRequiresOnlySecrets(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"CF_API_TOKEN", "DNSPOD_SECRET_ID", "DNSPOD_SECRET_KEY"} {
		values := minimalValues()
		delete(values, key)
		if _, err := FromValues(values, CommandAdd); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("missing %s returned %v", key, err)
		}
	}
}

func TestLoadFileThenEnvironmentOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")
	data := strings.Join([]string{
		"CF_API_TOKEN=file-token",
		"CF_ZONE=platform.example.net",
		"DNSPOD_SECRET_ID=dns-id",
		"DNSPOD_SECRET_KEY=dns-secret",
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CF_API_TOKEN", "process-token")
	cfg, err := Load(path, CommandStatus)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CFAPIToken != "process-token" {
		t.Fatalf("token = %q; process environment did not override file", cfg.CFAPIToken)
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()
	cfg, err := FromValues(completeValues(), CommandAdd)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Redact("cf-secret dns-id dns-secret")
	if strings.Contains(got, "secret") || strings.Contains(got, "dns-id") {
		t.Fatalf("secrets remained in %q", got)
	}
}

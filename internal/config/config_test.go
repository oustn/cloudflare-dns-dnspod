package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func completeValues() map[string]string {
	return map[string]string{
		"CF_API_TOKEN":        "cf-secret",
		"CF_PARENT_ZONE_ID":   "parent-id",
		"CF_PARENT_ZONE_NAME": "example.com",
		"CF_SAAS_ZONE_ID":     "saas-id",
		"CF_FALLBACK_HOST":    "fallback.example.com",
		"DNSPOD_SECRET_ID":    "dns-id",
		"DNSPOD_SECRET_KEY":   "dns-secret",
	}
}

func TestFromValuesCommandRequirements(t *testing.T) {
	t.Parallel()
	values := completeValues()
	delete(values, "CF_FALLBACK_HOST")
	if _, err := FromValues(values, CommandAdd); err == nil {
		t.Fatal("add accepted missing fallback host")
	}
	if _, err := FromValues(values, CommandSetEdge); err != nil {
		t.Fatalf("set-edge rejected optional fallback: %v", err)
	}
	if _, err := FromValues(values, CommandStatus); err != nil {
		t.Fatalf("status rejected optional fallback: %v", err)
	}
}

func TestLoadFileThenEnvironmentOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")
	data := strings.Join([]string{
		"CF_API_TOKEN=file-token",
		"CF_PARENT_ZONE_ID=parent-id",
		"CF_PARENT_ZONE_NAME=example.com",
		"CF_SAAS_ZONE_ID=saas-id",
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

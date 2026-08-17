package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
)

func TestSetBackendCreatesOriginAndPromotesFallbackHostname(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	result, err := SetBackend(
		context.Background(), minimalConfig(), "custom.example.com", "backend.example.org",
		Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, BackendOptions{Zone: "platform.example.net"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Wrote || !containsWrite(cf.writes, "origin-dns:CNAME:custom.platform.example.net:backend.example.org") {
		t.Fatalf("result=%+v writes=%v", result, cf.writes)
	}
	if !containsWrite(cf.writes, "hostname-origin:custom.platform.example.net") {
		t.Fatalf("Custom Hostname was not promoted: %v", cf.writes)
	}
	if len(dns.writes) != 0 {
		t.Fatalf("set-backend wrote DNSPod: %v", dns.writes)
	}
}

func TestSetBackendUpdatesOneOriginRecord(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	cf.host.CustomOriginServer = "custom.platform.example.net"
	cf.records["custom.platform.example.net"] = []domain.DNSRecord{{ID: "origin", Name: "custom.platform.example.net", Type: "CNAME", Content: "old.example.org", Proxied: true}}
	result, err := SetBackend(
		context.Background(), minimalConfig(), "custom.example.com", "1.1.1.1",
		Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, BackendOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Wrote || !containsWrite(cf.writes, "origin-update:origin:A:custom.platform.example.net:1.1.1.1") {
		t.Fatalf("result=%+v writes=%v", result, cf.writes)
	}
	if containsWrite(cf.writes, "hostname-origin:custom.platform.example.net") || len(dns.writes) != 0 {
		t.Fatalf("unexpected writes: cf=%v dns=%v", cf.writes, dns.writes)
	}
}

func TestSetBackendUnchangedHasZeroWrites(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	cf.host.CustomOriginServer = "custom.platform.example.net"
	cf.records["custom.platform.example.net"] = []domain.DNSRecord{{ID: "origin", Name: "custom.platform.example.net", Type: "CNAME", Content: "backend.example.org", Proxied: true}}
	result, err := SetBackend(
		context.Background(), minimalConfig(), "custom.example.com", "backend.example.org",
		Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, BackendOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Wrote || result.State != "unchanged" || len(cf.writes) != 0 || len(dns.writes) != 0 {
		t.Fatalf("result=%+v cf=%v dns=%v", result, cf.writes, dns.writes)
	}
}

func TestSetBackendBlocksAmbiguousOriginRecords(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	cf.records["custom.platform.example.net"] = []domain.DNSRecord{
		{ID: "a", Name: "custom.platform.example.net", Type: "A", Content: "1.1.1.1", Proxied: true},
		{ID: "c", Name: "custom.platform.example.net", Type: "CNAME", Content: "old.example.org", Proxied: true},
	}
	_, err := SetBackend(
		context.Background(), minimalConfig(), "custom.example.com", "backend.example.org",
		Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, BackendOptions{},
	)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "ambiguous") {
		t.Fatalf("error=%v", err)
	}
	if len(cf.writes) != 0 || len(dns.writes) != 0 {
		t.Fatalf("blocked set-backend wrote: cf=%v dns=%v", cf.writes, dns.writes)
	}
}

func TestSetBackendDryRunPlansWithoutWrites(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	result, err := SetBackend(
		context.Background(), minimalConfig(), "custom.example.com", "backend.example.org",
		Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, BackendOptions{DryRun: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Wrote || !result.DryRun || len(cf.writes) != 0 || len(dns.writes) != 0 {
		t.Fatalf("result=%+v cf=%v dns=%v", result, cf.writes, dns.writes)
	}
	if len(result.Changes) != 2 || result.Changes[0].Action != "would-create" || result.Changes[1].Action != "would-update" {
		t.Fatalf("changes=%+v", result.Changes)
	}
}

func TestSetEdgeWithDiscoveredZonesOnlyWritesDNSPod(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	result, err := SetEdge(
		context.Background(), minimalConfig(), "custom.example.com", EdgeTarget{Host: "preferred.example.net"},
		Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, EdgeOptions{Zone: "platform.example.net"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Wrote || len(cf.writes) != 0 || len(dns.writes) != 1 || !strings.HasPrefix(dns.writes[0], "set-edge:") {
		t.Fatalf("result=%+v cf=%v dns=%v", result, cf.writes, dns.writes)
	}
}

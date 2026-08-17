package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/oustn/cloudflare-dns-dnspod/internal/config"
	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
)

func discoveryServices() *fakeCF {
	zones := []domain.Zone{
		{ID: "parent", Name: "example.com", Status: "active"},
		{ID: "one", Name: "one.example.net", Status: "active"},
		{ID: "two", Name: "two.example.net", Status: "active"},
	}
	return &fakeCF{
		zoneList: zones,
		zones: map[string]domain.Zone{
			"parent": zones[0], "one": zones[1], "two": zones[2],
		},
		fallbacks: map[string]domain.FallbackOrigin{
			"one": {Origin: "fallback.one.example.net", Status: "active"},
			"two": {Origin: "fallback.two.example.net", Status: "active"},
		},
		hosts: map[string]*domain.CustomHostname{},
	}
}

func TestDiscoverUsesCommandZoneBeforeConfiguredZone(t *testing.T) {
	t.Parallel()
	cf := discoveryServices()
	got, err := discoverInfrastructure(context.Background(), config.Config{CFZone: "one.example.net"}, "test.example.com", "two.example.net", cf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Parent.ID != "parent" || got.SaaS.ID != "two" || got.Source != "command" {
		t.Fatalf("infrastructure = %+v", got)
	}
	if got.OriginName != "test.two.example.net" || got.Fallback.Origin != "fallback.two.example.net" {
		t.Fatalf("infrastructure = %+v", got)
	}
}

func TestDiscoverUsesConfiguredZone(t *testing.T) {
	t.Parallel()
	cf := discoveryServices()
	got, err := discoverInfrastructure(context.Background(), config.Config{CFZone: "one.example.net"}, "test.example.com", "", cf)
	if err != nil || got.SaaS.ID != "one" || got.Source != "configuration" {
		t.Fatalf("infrastructure=%+v err=%v", got, err)
	}
}

func TestDiscoverUsesUniqueCustomHostnameOwner(t *testing.T) {
	t.Parallel()
	cf := discoveryServices()
	cf.hosts["two"] = &domain.CustomHostname{ID: "h1", Hostname: "test.example.com", Status: "active", SSLStatus: "active"}
	got, err := discoverInfrastructure(context.Background(), config.Config{}, "test.example.com", "", cf)
	if err != nil || got.SaaS.ID != "two" || got.Source != "custom_hostname" || got.Hostname == nil {
		t.Fatalf("infrastructure=%+v err=%v", got, err)
	}
}

func TestDiscoverUsesUniqueFallbackCandidate(t *testing.T) {
	t.Parallel()
	cf := discoveryServices()
	delete(cf.fallbacks, "two")
	got, err := discoverInfrastructure(context.Background(), config.Config{}, "test.example.com", "", cf)
	if err != nil || got.SaaS.ID != "one" || got.Source != "fallback" {
		t.Fatalf("infrastructure=%+v err=%v", got, err)
	}
}

func TestDiscoverBlocksMultipleFallbackCandidates(t *testing.T) {
	t.Parallel()
	cf := discoveryServices()
	_, err := discoverInfrastructure(context.Background(), config.Config{}, "test.example.com", "", cf)
	if err == nil || !strings.Contains(err.Error(), "one.example.net") || !strings.Contains(err.Error(), "two.example.net") {
		t.Fatalf("error = %v", err)
	}
	if len(cf.writes) != 0 {
		t.Fatalf("discovery wrote provider state: %v", cf.writes)
	}
}

func TestDiscoverRejectsParentAsSaaSZone(t *testing.T) {
	t.Parallel()
	cf := discoveryServices()
	cf.fallbacks["parent"] = domain.FallbackOrigin{Origin: "fallback.example.com", Status: "active"}
	_, err := discoverInfrastructure(context.Background(), config.Config{}, "test.example.com", "example.com", cf)
	if err == nil || !strings.Contains(err.Error(), "parent Zone") {
		t.Fatalf("error = %v", err)
	}
}

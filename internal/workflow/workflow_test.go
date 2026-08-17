package workflow

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/oustn/cloudflare-dns-dnspod/internal/config"
	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
)

type fakeCF struct {
	zones        map[string]domain.Zone
	zoneList     []domain.Zone
	fallback     domain.FallbackOrigin
	fallbacks    map[string]domain.FallbackOrigin
	records      map[string][]domain.DNSRecord
	host         *domain.CustomHostname
	hosts        map[string]*domain.CustomHostname
	hostSequence []*domain.CustomHostname
	getHostCalls int
	writes       []string
}

func (f *fakeCF) GetZone(_ context.Context, id string) (domain.Zone, error) { return f.zones[id], nil }
func (f *fakeCF) ListZones(context.Context) ([]domain.Zone, error) {
	if len(f.zoneList) > 0 {
		return append([]domain.Zone(nil), f.zoneList...), nil
	}
	result := make([]domain.Zone, 0, len(f.zones))
	for _, zone := range f.zones {
		result = append(result, zone)
	}
	return result, nil
}
func (f *fakeCF) GetFallbackOrigin(_ context.Context, zoneID string) (domain.FallbackOrigin, error) {
	if f.fallbacks != nil {
		fallback, ok := f.fallbacks[zoneID]
		if !ok {
			return domain.FallbackOrigin{}, fmt.Errorf("fallback not configured")
		}
		return fallback, nil
	}
	return f.fallback, nil
}
func (f *fakeCF) ListDNSRecords(_ context.Context, _ string, name string) ([]domain.DNSRecord, error) {
	return append([]domain.DNSRecord(nil), f.records[name]...), nil
}
func (f *fakeCF) FindCustomHostname(_ context.Context, zoneID, _ string) (*domain.CustomHostname, error) {
	if f.hosts != nil {
		return f.hosts[zoneID], nil
	}
	return f.host, nil
}
func (f *fakeCF) GetCustomHostname(context.Context, string, string) (*domain.CustomHostname, error) {
	if len(f.hostSequence) > 0 {
		index := f.getHostCalls
		if index >= len(f.hostSequence) {
			index = len(f.hostSequence) - 1
		}
		f.getHostCalls++
		f.host = f.hostSequence[index]
	}
	return f.host, nil
}
func (f *fakeCF) CreateDNSRecord(_ context.Context, _, type_, name, content string) (domain.DNSRecord, error) {
	f.writes = append(f.writes, "dns:"+type_+":"+name+":"+content)
	return domain.DNSRecord{ID: "new", Name: name, Type: type_, Content: content}, nil
}
func (f *fakeCF) CreateProxiedDNSRecord(_ context.Context, _, type_, name, content string) (domain.DNSRecord, error) {
	f.writes = append(f.writes, "origin-dns:"+type_+":"+name+":"+content)
	record := domain.DNSRecord{ID: "origin-new", Name: name, Type: type_, Content: content, Proxied: true}
	f.records[name] = []domain.DNSRecord{record}
	return record, nil
}
func (f *fakeCF) UpdateProxiedDNSRecord(_ context.Context, _, id, type_, name, content string) (domain.DNSRecord, error) {
	f.writes = append(f.writes, "origin-update:"+id+":"+type_+":"+name+":"+content)
	record := domain.DNSRecord{ID: id, Name: name, Type: type_, Content: content, Proxied: true}
	f.records[name] = []domain.DNSRecord{record}
	return record, nil
}
func (f *fakeCF) DeleteDNSRecord(_ context.Context, _, id string) error {
	f.writes = append(f.writes, "delete:"+id)
	return nil
}
func (f *fakeCF) CreateCustomHostname(_ context.Context, _, hostname, customOrigin string) (*domain.CustomHostname, error) {
	f.writes = append(f.writes, "hostname:"+hostname+":"+customOrigin)
	f.host = &domain.CustomHostname{ID: "h1", Hostname: hostname, Status: "active", SSLStatus: "active", CustomOriginServer: customOrigin}
	return f.host, nil
}
func (f *fakeCF) UpdateCustomHostnameOrigin(_ context.Context, _, _, customOrigin string) (*domain.CustomHostname, error) {
	f.writes = append(f.writes, "hostname-origin:"+customOrigin)
	f.host.CustomOriginServer = customOrigin
	return f.host, nil
}

type fakeDNSPod struct {
	zone      *domain.Zone
	records   []domain.DNSRecord
	writes    []string
	setAction string
}

func (f *fakeDNSPod) FindZone(context.Context, string) (*domain.Zone, error) { return f.zone, nil }
func (f *fakeDNSPod) RequestValidation(context.Context, string) (domain.DNSPodValidation, error) {
	f.writes = append(f.writes, "request-validation")
	return domain.DNSPodValidation{Name: "_dnspod.custom.example.com", Value: "secret-validation"}, nil
}
func (f *fakeDNSPod) ValidationReady(context.Context, string) (bool, error) { return true, nil }
func (f *fakeDNSPod) CreateZone(_ context.Context, hostname string) (*domain.Zone, error) {
	f.writes = append(f.writes, "create-zone")
	f.zone = &domain.Zone{ID: "1", Name: hostname, Status: "pause", Nameservers: []string{"f1.dnspod.net", "f2.dnspod.net"}}
	return f.zone, nil
}
func (f *fakeDNSPod) EnableZone(context.Context, *domain.Zone, bool) (string, error) {
	if strings.EqualFold(f.zone.Status, "enable") {
		return "unchanged", nil
	}
	f.writes = append(f.writes, "enable-zone")
	f.zone.Status = "enable"
	return "enabled", nil
}
func (f *fakeDNSPod) ListRecords(context.Context, string, string) ([]domain.DNSRecord, error) {
	return append([]domain.DNSRecord(nil), f.records...), nil
}
func (f *fakeDNSPod) EnsureTXT(_ context.Context, _, name, _ string, dry bool) (string, error) {
	if !dry {
		f.writes = append(f.writes, "txt:"+name)
	}
	return map[bool]string{true: "would-create", false: "created"}[dry], nil
}
func (f *fakeDNSPod) EnsureFallback(context.Context, string, string, string, bool) (string, error) {
	if len(f.records) > 0 {
		return "preserved-existing-edge", nil
	}
	f.writes = append(f.writes, "fallback")
	return "created", nil
}
func (f *fakeDNSPod) SetTraffic(_ context.Context, _, _, type_, value string, dry bool) (string, error) {
	if !dry && f.setAction != "unchanged" {
		f.writes = append(f.writes, "set-edge:"+type_+":"+value)
	}
	if f.setAction != "" {
		return f.setAction, nil
	}
	return "modified", nil
}

type fakeResolver struct {
	ready              bool
	chain              []string
	delegationFailures int
	delegationCalls    int
}

func (f *fakeResolver) Delegation(context.Context, string, []string) ([]string, bool, error) {
	f.delegationCalls++
	if f.delegationCalls <= f.delegationFailures {
		return nil, false, fmt.Errorf("temporary DNS lookup failure")
	}
	return []string{"f1.dnspod.net", "f2.dnspod.net"}, f.ready, nil
}
func (f *fakeResolver) CheckHostTarget(context.Context, string, string, int) ([]string, error) {
	return f.chain, nil
}

func testConfig() config.Config {
	return config.Config{
		CFAPIToken: "token", CFParentZoneID: "parent", CFParentZoneName: "example.com",
		CFSaaSZoneID: "saas", CFFallbackHost: "fallback.platform.example.net",
		DNSPodSecretID: "id", DNSPodSecretKey: "key", DNSPodRecordLine: "默认",
	}
}

func minimalConfig() config.Config {
	return config.Config{
		CFAPIToken: "token", DNSPodSecretID: "id", DNSPodSecretKey: "key", DNSPodRecordLine: "默认",
	}
}

func readyServices() (*fakeCF, *fakeDNSPod, *fakeResolver) {
	hostname := "custom.example.com"
	cf := &fakeCF{
		zones: map[string]domain.Zone{
			"parent": {ID: "parent", Name: "example.com", Status: "active"},
			"saas":   {ID: "saas", Name: "platform.example.net", Status: "active"},
		},
		fallback: domain.FallbackOrigin{Origin: "fallback.platform.example.net", Status: "active"},
		records: map[string][]domain.DNSRecord{hostname: {
			{ID: "ns1", Name: hostname, Type: "NS", Content: "f1.dnspod.net"},
			{ID: "ns2", Name: hostname, Type: "NS", Content: "f2.dnspod.net"},
		}},
		host: &domain.CustomHostname{ID: "h1", Hostname: hostname, Status: "active", SSLStatus: "active"},
	}
	dns := &fakeDNSPod{
		zone:    &domain.Zone{ID: "1", Name: hostname, Status: "enable", Nameservers: []string{"f1.dnspod.net", "f2.dnspod.net"}},
		records: []domain.DNSRecord{{ID: "r1", Name: "@", Type: "CNAME", Content: "fallback.platform.example.net", Line: "默认"}},
	}
	return cf, dns, &fakeResolver{ready: true, chain: []string{"edge.example.com"}}
}

func customOriginServices() (*fakeCF, *fakeDNSPod, *fakeResolver) {
	cf, dns, resolver := readyServices()
	cf.zones["saas"] = domain.Zone{ID: "saas", Name: "platform.example.net", Status: "active"}
	cf.fallback = domain.FallbackOrigin{Origin: "fallback.platform.example.net", Status: "active"}
	dns.records = []domain.DNSRecord{{ID: "r1", Name: "@", Type: "CNAME", Content: "fallback.platform.example.net", Line: "默认"}}
	return cf, dns, resolver
}

func TestAddFallbackModeOmitsCustomOrigin(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := customOriginServices()
	cf.host = nil
	result, err := Add(context.Background(), minimalConfig(), "custom.example.com", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, AddOptions{Zone: "platform.example.net"})
	if err != nil {
		t.Fatal(err)
	}
	for _, write := range cf.writes {
		if strings.HasPrefix(write, "origin-") {
			t.Fatalf("fallback mode wrote origin state: %v", cf.writes)
		}
	}
	if !containsWrite(cf.writes, "hostname:custom.example.com:") || result.Status["backend"] == nil {
		t.Fatalf("writes=%v result=%+v", cf.writes, result)
	}
}

func TestAddBareOriginCreatesFallbackBackedAlias(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := customOriginServices()
	cf.host = nil
	result, err := Add(context.Background(), minimalConfig(), "custom.example.com", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, AddOptions{Zone: "platform.example.net", OriginSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsWrite(cf.writes, "origin-dns:CNAME:custom.platform.example.net:fallback.platform.example.net") {
		t.Fatalf("writes=%v", cf.writes)
	}
	if !containsWrite(cf.writes, "hostname:custom.example.com:custom.platform.example.net") || result.Status["backend"] == nil {
		t.Fatalf("writes=%v result=%+v", cf.writes, result)
	}
}

func TestAddExplicitOriginCreatesProxiedHostOrIPRecord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, target, want string
	}{
		{name: "host", target: "backend.example.org", want: "origin-dns:CNAME:custom.platform.example.net:backend.example.org"},
		{name: "IP", target: "1.1.1.1", want: "origin-dns:A:custom.platform.example.net:1.1.1.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cf, dns, resolver := customOriginServices()
			cf.host = nil
			_, err := Add(context.Background(), minimalConfig(), "custom.example.com", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, AddOptions{Zone: "platform.example.net", OriginSet: true, Origin: tc.target})
			if err != nil {
				t.Fatal(err)
			}
			if !containsWrite(cf.writes, tc.want) {
				t.Fatalf("writes=%v", cf.writes)
			}
		})
	}
}

func TestAddOriginModeMismatchBlocksBeforeWrites(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := customOriginServices()
	cf.host.CustomOriginServer = "other.platform.example.net"
	_, err := Add(context.Background(), minimalConfig(), "custom.example.com", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, AddOptions{Zone: "platform.example.net", OriginSet: true})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "custom origin") {
		t.Fatalf("error=%v", err)
	}
	if len(cf.writes) != 0 || len(dns.writes) != 0 {
		t.Fatalf("blocked add wrote state: cf=%v dns=%v", cf.writes, dns.writes)
	}
}

func TestAddMatchingOriginRerunDoesNotRewriteOrigin(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := customOriginServices()
	cf.host.CustomOriginServer = "custom.platform.example.net"
	cf.records["custom.platform.example.net"] = []domain.DNSRecord{{ID: "origin", Name: "custom.platform.example.net", Type: "CNAME", Content: "fallback.platform.example.net", Proxied: true}}
	_, err := Add(context.Background(), minimalConfig(), "custom.example.com", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, AddOptions{Zone: "platform.example.net", OriginSet: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, write := range cf.writes {
		if strings.HasPrefix(write, "origin-") {
			t.Fatalf("matching origin rewritten: %v", cf.writes)
		}
	}
}

func containsWrite(writes []string, expected string) bool {
	for _, write := range writes {
		if write == expected {
			return true
		}
	}
	return false
}

func TestAddPreflightBlocksExistingTrafficWithZeroWrites(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	cf.records["custom.example.com"] = []domain.DNSRecord{{ID: "worker", Name: "custom.example.com", Type: "AAAA", Content: "100::", Managed: true}}
	_, err := Add(context.Background(), testConfig(), "custom.example.com", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, AddOptions{})
	if err == nil || !strings.Contains(err.Error(), "AAAA") {
		t.Fatalf("expected AAAA blocker, got %v", err)
	}
	if len(cf.writes) != 0 || len(dns.writes) != 0 {
		t.Fatalf("preflight failure wrote provider state: cf=%v dns=%v", cf.writes, dns.writes)
	}
}

func TestAddPreflightBlocksFallbackMismatch(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	cf.fallback.Origin = "wrong.example.com"
	_, err := Add(context.Background(), testConfig(), "custom.example.com", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, AddOptions{})
	if err == nil || !strings.Contains(err.Error(), "Fallback") {
		t.Fatalf("expected Fallback blocker, got %v", err)
	}
	if len(cf.writes)+len(dns.writes) != 0 {
		t.Fatal("preflight failure performed writes")
	}
}

func TestSetEdgeOnlyWritesOneDNSPodTrafficRecord(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	result, err := SetEdge(context.Background(), testConfig(), "custom.example.com", EdgeTarget{Host: "edge.example.com"}, Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, EdgeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Wrote != true || len(dns.writes) != 1 || !strings.HasPrefix(dns.writes[0], "set-edge:CNAME:") {
		t.Fatalf("unexpected DNSPod writes/result: %v %+v", dns.writes, result)
	}
	if len(cf.writes) != 0 {
		t.Fatalf("set-edge wrote Cloudflare: %v", cf.writes)
	}
}

func TestSetEdgeUnchangedDoesNotWrite(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	dns.setAction = "unchanged"
	result, err := SetEdge(context.Background(), testConfig(), "custom.example.com", EdgeTarget{Host: "edge.example.com"}, Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, EdgeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Wrote {
		t.Fatalf("unchanged result reported a write: %+v", result)
	}
	if len(dns.writes) != 0 {
		t.Fatalf("unchanged target wrote DNSPod: %v", dns.writes)
	}
}

func TestAddRerunPreservesExistingEdgeTarget(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	dns.records = []domain.DNSRecord{{ID: "edge", Name: "@", Type: "CNAME", Content: "edge.example.com", Line: "默认"}}
	result, err := Add(context.Background(), testConfig(), "custom.example.com", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, AddOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, change := range result.Changes {
		if change.Resource == "fallback_cname" && change.Action == "preserved-existing-edge" {
			found = true
		}
	}
	if !found {
		t.Fatalf("result did not preserve edge target: %+v", result.Changes)
	}
	for _, write := range dns.writes {
		if write == "fallback" {
			t.Fatalf("add reset existing edge target: %v", dns.writes)
		}
	}
}

func TestSetEdgePreflightBlocksAmbiguousApexWithoutWrite(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	dns.records = []domain.DNSRecord{
		{ID: "a", Name: "@", Type: "A", Content: "1.1.1.1", Line: "默认"},
		{ID: "c", Name: "@", Type: "CNAME", Content: "old.example.com", Line: "默认"},
	}
	_, err := SetEdge(context.Background(), testConfig(), "custom.example.com", EdgeTarget{Host: "edge.example.com"}, Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, EdgeOptions{})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous apex blocker, got %v", err)
	}
	if len(cf.writes) != 0 || len(dns.writes) != 0 {
		t.Fatalf("blocked set-edge wrote state: cf=%v dns=%v", cf.writes, dns.writes)
	}
}

func TestSetEdgePreflightBlocksTrafficOnAnotherLine(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	dns.records = []domain.DNSRecord{{ID: "other", Name: "@", Type: "A", Content: "1.1.1.1", Line: "境外"}}
	_, err := SetEdge(context.Background(), testConfig(), "custom.example.com", EdgeTarget{Host: "edge.example.com"}, Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, EdgeOptions{})
	if err == nil || !strings.Contains(err.Error(), "unexpected line") {
		t.Fatalf("expected non-default line blocker, got %v", err)
	}
	if len(dns.writes) != 0 {
		t.Fatalf("blocked set-edge wrote DNSPod: %v", dns.writes)
	}
}

func TestAddWaitRetriesTransientDelegationLookupErrors(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	resolver.delegationFailures = 2
	result, err := Add(
		context.Background(),
		testConfig(),
		"custom.example.com",
		Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver},
		AddOptions{Wait: true, Timeout: 100 * time.Millisecond, PollInterval: time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready || resolver.delegationCalls < 3 {
		t.Fatalf("result=%+v delegation calls=%d", result, resolver.delegationCalls)
	}
}

func TestAddWaitPublishesValidationRecordsThatAppearDuringPolling(t *testing.T) {
	t.Parallel()
	cf, dns, resolver := readyServices()
	pending := &domain.CustomHostname{ID: "h1", Hostname: "custom.example.com", Status: "active", SSLStatus: "pending_validation"}
	withValidation := &domain.CustomHostname{
		ID: "h1", Hostname: "custom.example.com", Status: "active", SSLStatus: "pending_validation",
		ValidationRecords: []domain.ValidationRecord{{Name: "_acme-challenge.custom.example.com", Value: "cert-value"}},
	}
	active := &domain.CustomHostname{ID: "h1", Hostname: "custom.example.com", Status: "active", SSLStatus: "active"}
	cf.host = pending
	cf.hostSequence = []*domain.CustomHostname{pending, withValidation, active}

	result, err := Add(
		context.Background(),
		testConfig(),
		"custom.example.com",
		Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver},
		AddOptions{Wait: true, Timeout: 100 * time.Millisecond, PollInterval: time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Ready {
		t.Fatalf("result not ready: %+v", result)
	}
	foundTXT := false
	for _, write := range dns.writes {
		if write == "txt:_acme-challenge.custom.example.com" {
			foundTXT = true
		}
	}
	if !foundTXT {
		t.Fatalf("validation TXT appearing during polling was not published: %v", dns.writes)
	}
}

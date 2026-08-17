package dnspod

import (
	"testing"

	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
)

func TestPlanTraffic(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                 string
		current              []domain.DNSRecord
		type_, value, action string
		valid                bool
	}{
		{"create", nil, "A", "1.1.1.1", "create", true},
		{"unchanged", []domain.DNSRecord{{ID: "1", Type: "CNAME", Content: "EDGE.EXAMPLE.COM."}}, "CNAME", "edge.example.com", "unchanged", true},
		{"modify", []domain.DNSRecord{{ID: "1", Type: "A", Content: "8.8.8.8"}}, "A", "1.1.1.1", "modify", true},
		{"conflict", []domain.DNSRecord{{ID: "1", Type: "A"}, {ID: "2", Type: "AAAA"}}, "A", "1.1.1.1", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanTraffic(tc.current, tc.type_, tc.value)
			if tc.valid && (err != nil || plan.Action != tc.action) {
				t.Fatalf("plan=%+v err=%v", plan, err)
			}
			if !tc.valid && err == nil {
				t.Fatal("expected conflict")
			}
		})
	}
}

func TestDescribeDomainRequestIncludesNameAndID(t *testing.T) {
	t.Parallel()
	request := describeDomainRequest("dns-test.example.com", 99786823)
	if request.Domain == nil || *request.Domain != "dns-test.example.com" {
		t.Fatalf("Domain = %v", request.Domain)
	}
	if request.DomainId == nil || *request.DomainId != 99786823 {
		t.Fatalf("DomainId = %v", request.DomainId)
	}
}

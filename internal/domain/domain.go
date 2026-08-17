package domain

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

type Zone struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Nameservers []string `json:"nameservers,omitempty"`
}

type DNSRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Line    string `json:"line,omitempty"`
	Managed bool   `json:"managed,omitempty"`
	Source  string `json:"source,omitempty"`
}

type ValidationRecord struct {
	Name  string `json:"name"`
	Value string `json:"-"`
}

type CustomHostname struct {
	ID                string             `json:"id"`
	Hostname          string             `json:"hostname"`
	Status            string             `json:"status"`
	SSLStatus         string             `json:"ssl_status"`
	ValidationRecords []ValidationRecord `json:"validation_records,omitempty"`
}

func (h *CustomHostname) Active() bool {
	return h != nil && strings.EqualFold(h.Status, "active") && strings.EqualFold(h.SSLStatus, "active")
}

type FallbackOrigin struct {
	Origin string `json:"origin"`
	Status string `json:"status"`
}

type DNSPodValidation struct {
	Name  string
	Value string
}

type Change struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Detail   string `json:"detail,omitempty"`
}

type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type OperationResult struct {
	Operation string         `json:"operation"`
	Hostname  string         `json:"hostname"`
	Ready     bool           `json:"ready"`
	DryRun    bool           `json:"dry_run,omitempty"`
	Wrote     bool           `json:"wrote"`
	State     string         `json:"state,omitempty"`
	Checks    []Check        `json:"checks,omitempty"`
	Changes   []Change       `json:"changes,omitempty"`
	Status    map[string]any `json:"status,omitempty"`
}

func NormalizeHostname(value string) (string, error) {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if len(host) < 3 || len(host) > 253 || strings.ContainsAny(host, ":/") {
		return "", fmt.Errorf("invalid hostname %q", value)
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("invalid hostname %q", value)
	}
	for _, label := range labels {
		if !validLabel(label) {
			return "", fmt.Errorf("invalid hostname %q", value)
		}
	}
	return host, nil
}

func validLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, r := range label {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func BuildHostname(subdomain, parent string) (string, error) {
	rel := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(subdomain), "."))
	if rel == "" || rel == "*" || strings.ContainsAny(rel, ":/") {
		return "", fmt.Errorf("subdomain must be a relative DNS name")
	}
	base, err := NormalizeHostname(parent)
	if err != nil {
		return "", fmt.Errorf("parent zone: %w", err)
	}
	if rel == base || strings.HasSuffix(rel, "."+base) {
		return "", fmt.Errorf("subdomain must be relative, not a fully qualified hostname")
	}
	return NormalizeHostname(rel + "." + base)
}

var nonPublicIPv4 = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
	"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
	"192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
	"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
)

func mustPrefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

func ValidatePublicIPv4(value string) error {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || !addr.Is4() {
		return fmt.Errorf("edge IP must be an IPv4 address")
	}
	for _, prefix := range nonPublicIPv4 {
		if prefix.Contains(addr) {
			return fmt.Errorf("edge IP %s is not globally routable", addr)
		}
	}
	return nil
}

func NormalizeNameservers(values []string) ([]string, error) {
	seen := map[string]bool{}
	for _, value := range values {
		ns, err := NormalizeHostname(value)
		if err != nil {
			return nil, fmt.Errorf("invalid nameserver: %w", err)
		}
		seen[ns] = true
	}
	result := make([]string, 0, len(seen))
	for ns := range seen {
		result = append(result, ns)
	}
	sort.Strings(result)
	if len(result) < 2 {
		return nil, fmt.Errorf("DNSPod must assign at least two distinct nameservers")
	}
	return result, nil
}

func EqualTarget(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(a), "."), strings.TrimSuffix(strings.TrimSpace(b), "."))
}

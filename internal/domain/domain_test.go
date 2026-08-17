package domain

import "testing"

func TestBuildHostname(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, subdomain, parent, want string
		valid                         bool
	}{
		{"simple", "custom", "example.com", "custom.example.com", true},
		{"nested", "a.b", "example.com.", "a.b.example.com", true},
		{"fqdn", "custom.example.com", "example.com", "", false},
		{"wildcard", "*", "example.com", "", false},
		{"url", "https://custom", "example.com", "", false},
		{"empty", "", "example.com", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildHostname(tc.subdomain, tc.parent)
			if tc.valid && (err != nil || got != tc.want) {
				t.Fatalf("BuildHostname() = %q, %v; want %q", got, err, tc.want)
			}
			if !tc.valid && err == nil {
				t.Fatalf("BuildHostname() unexpectedly accepted %q", tc.subdomain)
			}
		})
	}
}

func TestValidatePublicIPv4(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ip    string
		valid bool
	}{
		{"1.1.1.1", true},
		{"8.8.8.8", true},
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"192.0.2.1", false},
		{"224.0.0.1", false},
		{"2001:4860:4860::8888", false},
		{"bad", false},
	} {
		err := ValidatePublicIPv4(tc.ip)
		if (err == nil) != tc.valid {
			t.Errorf("ValidatePublicIPv4(%q) error = %v, valid=%v", tc.ip, err, tc.valid)
		}
	}
}

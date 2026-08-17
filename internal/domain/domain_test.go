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

func TestFindParentZoneUsesLongestProperSuffix(t *testing.T) {
	t.Parallel()
	zones := []Zone{
		{ID: "root", Name: "example.com", Status: "active"},
		{ID: "nested", Name: "eu.example.com", Status: "active"},
		{ID: "inactive", Name: "shop.eu.example.com", Status: "pending"},
	}
	parent, err := FindParentZone("shop.eu.example.com", zones)
	if err != nil {
		t.Fatal(err)
	}
	if parent.ID != "nested" {
		t.Fatalf("parent = %+v", parent)
	}
}

func TestFindParentZoneRejectsExactZone(t *testing.T) {
	t.Parallel()
	_, err := FindParentZone("example.com", []Zone{{ID: "exact", Name: "example.com", Status: "active"}})
	if err == nil {
		t.Fatal("exact hostname Zone accepted as its own parent")
	}
}

func TestRebaseHostnameKeepsRelativeLabels(t *testing.T) {
	t.Parallel()
	got, err := RebaseHostname("shop.eu.example.com", "example.com", "platform.example.net")
	if err != nil {
		t.Fatal(err)
	}
	if got != "shop.eu.platform.example.net" {
		t.Fatalf("RebaseHostname() = %q", got)
	}
}

func TestClassifyTarget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value, wantType, wantValue string
	}{
		{"1.1.1.1", "A", "1.1.1.1"},
		{"Backend.Example.Org.", "CNAME", "backend.example.org"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			recordType, value, err := ClassifyTarget(tc.value, "backend")
			if err != nil || recordType != tc.wantType || value != tc.wantValue {
				t.Fatalf("ClassifyTarget() = %q, %q, %v", recordType, value, err)
			}
		})
	}
}

func TestClassifyTargetRejectsNonPublicIPv4(t *testing.T) {
	t.Parallel()
	if _, _, err := ClassifyTarget("192.0.2.1", "backend"); err == nil {
		t.Fatal("documentation-only IPv4 accepted")
	}
}

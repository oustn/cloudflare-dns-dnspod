package cloudflare

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetZoneAndAuthorization(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.Path != "/zones/parent" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"parent","name":"example.com","status":"active"}}`))
	}))
	defer server.Close()

	client := New("token", server.URL, server.Client())
	zone, err := client.GetZone(context.Background(), "parent")
	if err != nil {
		t.Fatal(err)
	}
	if zone.ID != "parent" || zone.Name != "example.com" || zone.Status != "active" {
		t.Fatalf("unexpected zone: %+v", zone)
	}
}

func TestListExactDNSRecordsKeepsManagedMetadata(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "ebook.example.com" {
			t.Errorf("name filter = %q", r.URL.Query().Get("name"))
		}
		_, _ = w.Write([]byte(`{"success":true,"result":[
			{"id":"1","name":"ebook.example.com","type":"AAAA","content":"100::","meta":{"managed_by_apps":true}},
			{"id":"2","name":"other.example.com","type":"TXT","content":"ignored"}
		]}`))
	}))
	defer server.Close()

	records, err := New("token", server.URL, server.Client()).ListDNSRecords(context.Background(), "parent", "ebook.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || !records[0].Managed || records[0].Type != "AAAA" {
		t.Fatalf("records = %+v", records)
	}
}

func TestListExactDNSRecordsAcceptsUnderscoreTXTName(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "_dnspodcheck.example.com" {
			t.Errorf("name filter = %q", r.URL.Query().Get("name"))
		}
		_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"txt1","name":"_dnspodcheck.example.com","type":"TXT","content":"validation"}]}`))
	}))
	defer server.Close()

	records, err := New("token", server.URL, server.Client()).ListDNSRecords(context.Background(), "parent", "_dnspodcheck.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "TXT" {
		t.Fatalf("records = %+v", records)
	}
}

func TestAPIErrorDoesNotLeakToken(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":9109,"message":"Invalid token"}],"result":null}`))
	}))
	defer server.Close()

	_, err := New("very-secret-token", server.URL, server.Client()).GetZone(context.Background(), "parent")
	if err == nil || !strings.Contains(err.Error(), "9109") || strings.Contains(err.Error(), "very-secret-token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseCustomHostnameValidation(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"id":"h1","hostname":"custom.example.com","status":"active","ownership_verification":{"type":"txt","name":"_cf-custom-hostname.custom.example.com","value":"owner"},"ssl":{"status":"active","validation_records":[{"txt_name":"_acme-challenge.custom.example.com","txt_value":"cert"}]}}`)
	host, err := parseCustomHostname(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !host.Active() || len(host.ValidationRecords) != 2 {
		t.Fatalf("hostname = %+v", host)
	}
}

package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListZonesReadsAllPages(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/zones" || r.URL.Query().Get("status") != "active" || r.URL.Query().Get("per_page") != "50" {
			t.Fatalf("unexpected request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		page := r.URL.Query().Get("page")
		zones := make([]map[string]string, 0, 50)
		if page == "1" {
			for i := 0; i < 50; i++ {
				zones = append(zones, map[string]string{"id": fmt.Sprintf("id-%02d", i), "name": fmt.Sprintf("zone-%02d.example", i), "status": "active"})
			}
		} else if page == "2" {
			zones = append(zones, map[string]string{"id": "last", "name": "last.example", "status": "active"})
		} else {
			t.Fatalf("unexpected page %q", page)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": zones})
	}))
	defer server.Close()

	zones, err := New("token", server.URL, server.Client()).ListZones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) != 51 || requests != 2 || zones[0].Name != "last.example" {
		t.Fatalf("len=%d requests=%d first=%+v", len(zones), requests, zones[0])
	}
}

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
			{"id":"1","name":"ebook.example.com","type":"AAAA","content":"100::","proxied":true,"meta":{"managed_by_apps":true}},
			{"id":"2","name":"other.example.com","type":"TXT","content":"ignored"}
		]}`))
	}))
	defer server.Close()

	records, err := New("token", server.URL, server.Client()).ListDNSRecords(context.Background(), "parent", "ebook.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || !records[0].Managed || records[0].Type != "AAAA" || !records[0].Proxied {
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
	raw := []byte(`{"id":"h1","hostname":"custom.example.com","status":"active","custom_origin_server":"custom.platform.example.net","ownership_verification":{"type":"txt","name":"_cf-custom-hostname.custom.example.com","value":"owner"},"ssl":{"status":"active","validation_records":[{"txt_name":"_acme-challenge.custom.example.com","txt_value":"cert"}]}}`)
	host, err := parseCustomHostname(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !host.Active() || len(host.ValidationRecords) != 2 || host.CustomOriginServer != "custom.platform.example.net" {
		t.Fatalf("hostname = %+v", host)
	}
}

func TestCreateProxiedDNSRecord(t *testing.T) {
	t.Parallel()
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/zones/saas/dns_records" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"r1","name":"custom.platform.example.net","type":"CNAME","content":"backend.example.org","proxied":true}}`))
	}))
	defer server.Close()

	record, err := New("token", server.URL, server.Client()).CreateProxiedDNSRecord(context.Background(), "saas", "CNAME", "custom.platform.example.net", "backend.example.org")
	if err != nil {
		t.Fatal(err)
	}
	if body["proxied"] != true || body["ttl"] != float64(1) || !record.Proxied {
		t.Fatalf("body=%v record=%+v", body, record)
	}
}

func TestUpdateProxiedDNSRecord(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/zones/saas/dns_records/r1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["type"] != "A" || body["content"] != "1.1.1.1" || body["proxied"] != true {
			t.Fatalf("body=%v", body)
		}
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"r1","name":"custom.platform.example.net","type":"A","content":"1.1.1.1","proxied":true}}`))
	}))
	defer server.Close()

	_, err := New("token", server.URL, server.Client()).UpdateProxiedDNSRecord(context.Background(), "saas", "r1", "A", "custom.platform.example.net", "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndUpdateCustomHostnameOrigin(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if requests == 1 {
			if r.Method != http.MethodPost || body["custom_origin_server"] != "custom.platform.example.net" {
				t.Fatalf("create request=%s body=%v", r.Method, body)
			}
			ssl, _ := body["ssl"].(map[string]any)
			if ssl["method"] != "txt" || ssl["type"] != "dv" {
				t.Fatalf("ssl=%v", ssl)
			}
		} else {
			if r.Method != http.MethodPatch || r.URL.Path != "/zones/saas/custom_hostnames/h1" || body["custom_origin_server"] != "custom.platform.example.net" {
				t.Fatalf("update request=%s %s body=%v", r.Method, r.URL.Path, body)
			}
		}
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"h1","hostname":"custom.example.com","status":"active","custom_origin_server":"custom.platform.example.net","ssl":{"status":"active"}}}`))
	}))
	defer server.Close()
	client := New("token", server.URL, server.Client())

	host, err := client.CreateCustomHostname(context.Background(), "saas", "custom.example.com", "custom.platform.example.net")
	if err != nil || host.CustomOriginServer != "custom.platform.example.net" {
		t.Fatalf("host=%+v err=%v", host, err)
	}
	if _, err := client.UpdateCustomHostnameOrigin(context.Background(), "saas", "h1", "custom.platform.example.net"); err != nil {
		t.Fatal(err)
	}
}

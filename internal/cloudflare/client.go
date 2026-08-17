package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
)

type Error struct {
	StatusCode int
	Details    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("Cloudflare API error: %s", e.Details)
}

type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

func New(token, baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{token: token, baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

type envelope struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiError struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any, target any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Cloudflare request: %w", err)
		}
		reader = bytes.NewReader(payload)
	}
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("create Cloudflare request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Cloudflare request failed: %w", err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 4<<20)
	var env envelope
	if err := json.NewDecoder(limited).Decode(&env); err != nil {
		return fmt.Errorf("Cloudflare returned non-JSON HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 || !env.Success {
		parts := make([]string, 0, len(env.Errors))
		for _, item := range env.Errors {
			parts = append(parts, fmt.Sprintf("%v: %s", item.Code, item.Message))
		}
		detail := strings.Join(parts, "; ")
		if detail == "" {
			detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return &Error{StatusCode: resp.StatusCode, Details: detail}
	}
	if target == nil {
		return nil
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return fmt.Errorf("Cloudflare returned a missing result")
	}
	if err := json.Unmarshal(env.Result, target); err != nil {
		return fmt.Errorf("Cloudflare returned malformed result: %w", err)
	}
	return nil
}

func (c *Client) GetZone(ctx context.Context, zoneID string) (domain.Zone, error) {
	var raw struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := c.request(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID), nil, nil, &raw); err != nil {
		return domain.Zone{}, err
	}
	name, err := domain.NormalizeHostname(raw.Name)
	if err != nil || raw.ID == "" || raw.Status == "" {
		return domain.Zone{}, fmt.Errorf("Cloudflare returned malformed Zone data")
	}
	return domain.Zone{ID: raw.ID, Name: name, Status: strings.ToLower(raw.Status)}, nil
}

func (c *Client) ListZones(ctx context.Context) ([]domain.Zone, error) {
	const pageSize = 50
	result := []domain.Zone{}
	seenIDs := map[string]bool{}
	seenNames := map[string]bool{}
	for page := 1; ; page++ {
		var raw []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		}
		query := url.Values{
			"status":   {"active"},
			"per_page": {fmt.Sprint(pageSize)},
			"page":     {fmt.Sprint(page)},
		}
		if err := c.request(ctx, http.MethodGet, "/zones", query, nil, &raw); err != nil {
			return nil, err
		}
		for _, item := range raw {
			name, err := domain.NormalizeHostname(item.Name)
			if err != nil || item.ID == "" || !strings.EqualFold(item.Status, "active") {
				return nil, fmt.Errorf("Cloudflare returned malformed Zone data")
			}
			if seenIDs[item.ID] || seenNames[name] {
				return nil, fmt.Errorf("Cloudflare returned duplicate Zone data")
			}
			seenIDs[item.ID], seenNames[name] = true, true
			result = append(result, domain.Zone{ID: item.ID, Name: name, Status: "active"})
		}
		if len(raw) < pageSize {
			break
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (c *Client) GetFallbackOrigin(ctx context.Context, zoneID string) (domain.FallbackOrigin, error) {
	var raw struct {
		Origin string `json:"origin"`
		Status string `json:"status"`
	}
	path := "/zones/" + url.PathEscape(zoneID) + "/custom_hostnames/fallback_origin"
	if err := c.request(ctx, http.MethodGet, path, nil, nil, &raw); err != nil {
		return domain.FallbackOrigin{}, err
	}
	origin, err := domain.NormalizeHostname(raw.Origin)
	if err != nil || raw.Status == "" {
		return domain.FallbackOrigin{}, fmt.Errorf("Cloudflare returned malformed Fallback Origin data")
	}
	return domain.FallbackOrigin{Origin: origin, Status: strings.ToLower(raw.Status)}, nil
}

type rawDNSRecord struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Type    string         `json:"type"`
	Content string         `json:"content"`
	Meta    map[string]any `json:"meta"`
	Proxied bool           `json:"proxied"`
}

func (c *Client) ListDNSRecords(ctx context.Context, zoneID, name string) ([]domain.DNSRecord, error) {
	expected, err := normalizeRecordName(name)
	if err != nil {
		return nil, err
	}
	var raw []rawDNSRecord
	query := url.Values{"name": {expected}, "per_page": {"5000"}}
	path := "/zones/" + url.PathEscape(zoneID) + "/dns_records"
	if err := c.request(ctx, http.MethodGet, path, query, nil, &raw); err != nil {
		return nil, err
	}
	result := make([]domain.DNSRecord, 0, len(raw))
	for _, item := range raw {
		normalized, err := normalizeRecordName(item.Name)
		if err != nil || normalized != expected {
			continue
		}
		managed := boolMeta(item.Meta, "managed_by_apps") || boolMeta(item.Meta, "managed")
		source, _ := item.Meta["source"].(string)
		if item.ID == "" || item.Type == "" {
			return nil, fmt.Errorf("Cloudflare returned malformed DNS record data")
		}
		result = append(result, domain.DNSRecord{
			ID: item.ID, Name: normalized, Type: strings.ToUpper(item.Type), Content: item.Content,
			Managed: managed, Source: source, Proxied: item.Proxied,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type != result[j].Type {
			return result[i].Type < result[j].Type
		}
		return result[i].Content < result[j].Content
	})
	return result, nil
}

func boolMeta(meta map[string]any, key string) bool {
	value, _ := meta[key].(bool)
	return value
}

func (c *Client) CreateDNSRecord(ctx context.Context, zoneID, recordType, name, content string) (domain.DNSRecord, error) {
	return c.writeDNSRecord(ctx, http.MethodPost, zoneID, "", recordType, name, content, false)
}

func (c *Client) CreateProxiedDNSRecord(ctx context.Context, zoneID, recordType, name, content string) (domain.DNSRecord, error) {
	return c.writeDNSRecord(ctx, http.MethodPost, zoneID, "", recordType, name, content, true)
}

func (c *Client) UpdateProxiedDNSRecord(ctx context.Context, zoneID, recordID, recordType, name, content string) (domain.DNSRecord, error) {
	if strings.TrimSpace(recordID) == "" {
		return domain.DNSRecord{}, fmt.Errorf("Cloudflare DNS record ID is required")
	}
	return c.writeDNSRecord(ctx, http.MethodPut, zoneID, recordID, recordType, name, content, true)
}

func (c *Client) writeDNSRecord(ctx context.Context, method, zoneID, recordID, recordType, name, content string, proxied bool) (domain.DNSRecord, error) {
	var raw rawDNSRecord
	body := map[string]any{"type": strings.ToUpper(recordType), "name": name, "content": content, "ttl": 1}
	if proxied {
		body["proxied"] = true
	}
	path := "/zones/" + url.PathEscape(zoneID) + "/dns_records"
	if recordID != "" {
		path += "/" + url.PathEscape(recordID)
	}
	if err := c.request(ctx, method, path, nil, body, &raw); err != nil {
		return domain.DNSRecord{}, err
	}
	if raw.ID == "" {
		return domain.DNSRecord{}, fmt.Errorf("Cloudflare create DNS response omitted record ID")
	}
	return domain.DNSRecord{ID: raw.ID, Name: raw.Name, Type: strings.ToUpper(raw.Type), Content: raw.Content, Proxied: raw.Proxied}, nil
}

func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	path := "/zones/" + url.PathEscape(zoneID) + "/dns_records/" + url.PathEscape(recordID)
	return c.request(ctx, http.MethodDelete, path, nil, nil, nil)
}

func parseCustomHostname(raw json.RawMessage) (*domain.CustomHostname, error) {
	var payload struct {
		ID        string         `json:"id"`
		Hostname  string         `json:"hostname"`
		Status    string         `json:"status"`
		Ownership map[string]any `json:"ownership_verification"`
		SSL       struct {
			Status  string           `json:"status"`
			Records []map[string]any `json:"validation_records"`
		} `json:"ssl"`
		CustomOriginServer string `json:"custom_origin_server"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("Cloudflare returned malformed Custom Hostname data")
	}
	hostname, err := domain.NormalizeHostname(payload.Hostname)
	if err != nil || payload.ID == "" {
		return nil, fmt.Errorf("Cloudflare returned malformed Custom Hostname data")
	}
	candidates := append([]map[string]any{payload.Ownership}, payload.SSL.Records...)
	seen := map[string]bool{}
	records := make([]domain.ValidationRecord, 0, len(candidates))
	for _, item := range candidates {
		name := stringField(item, "txt_name", "name")
		value := strings.Trim(stringField(item, "txt_value", "value"), "\"'")
		if name == "" || value == "" {
			continue
		}
		normalized, err := normalizeRecordName(name)
		if err != nil {
			return nil, err
		}
		key := normalized + "\x00" + value
		if !seen[key] {
			seen[key] = true
			records = append(records, domain.ValidationRecord{Name: normalized, Value: value})
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	customOrigin := strings.TrimSpace(payload.CustomOriginServer)
	if customOrigin != "" {
		customOrigin, err = domain.NormalizeHostname(customOrigin)
		if err != nil {
			return nil, fmt.Errorf("Cloudflare returned malformed Custom Hostname data")
		}
	}
	return &domain.CustomHostname{
		ID: payload.ID, Hostname: hostname, Status: strings.ToLower(payload.Status),
		SSLStatus: strings.ToLower(payload.SSL.Status), CustomOriginServer: customOrigin, ValidationRecords: records,
	}, nil
}

func normalizeRecordName(value string) (string, error) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if name == "" || len(name) > 253 || strings.ContainsAny(name, ":/") {
		return "", fmt.Errorf("Cloudflare returned invalid validation record name")
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" {
			return "", fmt.Errorf("Cloudflare returned invalid validation record name")
		}
	}
	return name, nil
}

func stringField(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			return value
		}
	}
	return ""
}

func (c *Client) FindCustomHostname(ctx context.Context, zoneID, hostname string) (*domain.CustomHostname, error) {
	expected, err := domain.NormalizeHostname(hostname)
	if err != nil {
		return nil, err
	}
	var raws []json.RawMessage
	path := "/zones/" + url.PathEscape(zoneID) + "/custom_hostnames"
	if err := c.request(ctx, http.MethodGet, path, url.Values{"hostname": {expected}}, nil, &raws); err != nil {
		return nil, err
	}
	for _, raw := range raws {
		item, err := parseCustomHostname(raw)
		if err != nil {
			return nil, err
		}
		if item.Hostname == expected {
			return item, nil
		}
	}
	return nil, nil
}

func (c *Client) GetCustomHostname(ctx context.Context, zoneID, hostnameID string) (*domain.CustomHostname, error) {
	var raw json.RawMessage
	path := "/zones/" + url.PathEscape(zoneID) + "/custom_hostnames/" + url.PathEscape(hostnameID)
	if err := c.request(ctx, http.MethodGet, path, nil, nil, &raw); err != nil {
		return nil, err
	}
	return parseCustomHostname(raw)
}

func (c *Client) CreateCustomHostname(ctx context.Context, zoneID, hostname, customOrigin string) (*domain.CustomHostname, error) {
	var raw json.RawMessage
	body := map[string]any{"hostname": hostname, "ssl": map[string]string{"method": "txt", "type": "dv"}}
	if customOrigin != "" {
		body["custom_origin_server"] = customOrigin
	}
	path := "/zones/" + url.PathEscape(zoneID) + "/custom_hostnames"
	if err := c.request(ctx, http.MethodPost, path, nil, body, &raw); err != nil {
		return nil, err
	}
	return parseCustomHostname(raw)
}

func (c *Client) UpdateCustomHostnameOrigin(ctx context.Context, zoneID, hostnameID, customOrigin string) (*domain.CustomHostname, error) {
	var raw json.RawMessage
	body := map[string]any{"custom_origin_server": customOrigin}
	path := "/zones/" + url.PathEscape(zoneID) + "/custom_hostnames/" + url.PathEscape(hostnameID)
	if err := c.request(ctx, http.MethodPatch, path, nil, body, &raw); err != nil {
		return nil, err
	}
	return parseCustomHostname(raw)
}

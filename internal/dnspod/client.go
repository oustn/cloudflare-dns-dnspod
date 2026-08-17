package dnspod

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	sdkerrors "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	api "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/dnspod/v20210323"
)

type Client struct {
	sdk        *api.Client
	recordLine string
}

func New(secretID, secretKey, recordLine string) (*Client, error) {
	cred := common.NewCredential(secretID, secretKey)
	clientProfile := profile.NewClientProfile()
	clientProfile.HttpProfile.Endpoint = "dnspod.tencentcloudapi.com"
	sdk, err := api.NewClient(cred, "", clientProfile)
	if err != nil {
		return nil, fmt.Errorf("initialize DNSPod SDK: %w", err)
	}
	if strings.TrimSpace(recordLine) == "" {
		recordLine = "默认"
	}
	return &Client{sdk: sdk, recordLine: recordLine}, nil
}

func providerError(err error) error {
	var sdkErr *sdkerrors.TencentCloudSDKError
	if errors.As(err, &sdkErr) {
		return fmt.Errorf("DNSPod API error: %s: %s (request %s)", sdkErr.Code, sdkErr.Message, sdkErr.RequestId)
	}
	return fmt.Errorf("DNSPod request failed: %w", err)
}

func (c *Client) FindZone(ctx context.Context, hostname string) (*domain.Zone, error) {
	expected, err := domain.NormalizeHostname(hostname)
	if err != nil {
		return nil, err
	}
	var foundID uint64
	const limit int64 = 3000
	for offset := int64(0); ; {
		request := api.NewDescribeDomainListRequest()
		request.Type = common.StringPtr("ALL")
		request.Keyword = common.StringPtr(expected)
		request.Offset = common.Int64Ptr(offset)
		request.Limit = common.Int64Ptr(limit)
		response, err := c.sdk.DescribeDomainListWithContext(ctx, request)
		if err != nil {
			return nil, providerError(err)
		}
		if response == nil || response.Response == nil {
			return nil, fmt.Errorf("DNSPod returned malformed domain-list data")
		}
		batch := response.Response.DomainList
		for _, item := range batch {
			if item == nil || item.Name == nil || item.DomainId == nil {
				continue
			}
			name, normalizeErr := domain.NormalizeHostname(*item.Name)
			if normalizeErr == nil && name == expected {
				foundID = *item.DomainId
				break
			}
		}
		if foundID != 0 || len(batch) < int(limit) {
			break
		}
		offset += int64(len(batch))
	}
	if foundID == 0 {
		return nil, nil
	}
	detailRequest := describeDomainRequest(expected, foundID)
	detail, err := c.sdk.DescribeDomainWithContext(ctx, detailRequest)
	if err != nil {
		return nil, providerError(err)
	}
	if detail == nil || detail.Response == nil || detail.Response.DomainInfo == nil {
		return nil, fmt.Errorf("DNSPod returned malformed domain detail data")
	}
	info := detail.Response.DomainInfo
	if info.DomainId == nil || info.Domain == nil || info.Status == nil {
		return nil, fmt.Errorf("DNSPod returned incomplete domain detail data")
	}
	name, err := domain.NormalizeHostname(*info.Domain)
	if err != nil || name != expected || *info.DomainId != foundID {
		return nil, fmt.Errorf("DNSPod domain identity does not match requested Zone")
	}
	nameservers, err := pointerStrings(info.DnspodNsList)
	if err != nil {
		return nil, err
	}
	nameservers, err = domain.NormalizeNameservers(nameservers)
	if err != nil {
		return nil, fmt.Errorf("DNSPod domain nameservers: %w", err)
	}
	return &domain.Zone{ID: strconv.FormatUint(foundID, 10), Name: name, Status: strings.ToLower(*info.Status), Nameservers: nameservers}, nil
}

func describeDomainRequest(domainName string, domainID uint64) *api.DescribeDomainRequest {
	request := api.NewDescribeDomainRequest()
	request.Domain = common.StringPtr(domainName)
	request.DomainId = common.Uint64Ptr(domainID)
	return request
}

func pointerStrings(values []*string) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == nil {
			return nil, fmt.Errorf("DNSPod returned a null string item")
		}
		result = append(result, *value)
	}
	return result, nil
}

func (c *Client) RequestValidation(ctx context.Context, hostname string) (domain.DNSPodValidation, error) {
	host, err := domain.NormalizeHostname(hostname)
	if err != nil {
		return domain.DNSPodValidation{}, err
	}
	request := api.NewCreateSubdomainValidateTXTValueRequest()
	request.DomainZone = common.StringPtr(host)
	response, err := c.sdk.CreateSubdomainValidateTXTValueWithContext(ctx, request)
	if err != nil {
		return domain.DNSPodValidation{}, providerError(err)
	}
	if response == nil || response.Response == nil {
		return domain.DNSPodValidation{}, fmt.Errorf("DNSPod child-zone validation response is malformed")
	}
	raw := response.Response
	parent := firstString(raw.Domain, raw.ParentDomain)
	relative := firstString(raw.SubDomain, raw.Subdomain)
	if parent == "" || relative == "" || raw.RecordType == nil || !strings.EqualFold(*raw.RecordType, "TXT") || raw.Value == nil || strings.TrimSpace(*raw.Value) == "" {
		return domain.DNSPodValidation{}, fmt.Errorf("DNSPod child-zone validation response is malformed")
	}
	parent, err = domain.NormalizeHostname(parent)
	if err != nil {
		return domain.DNSPodValidation{}, fmt.Errorf("DNSPod child-zone validation response is malformed")
	}
	relative = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(relative), "."))
	name := relative
	if relative == "@" {
		name = parent
	} else if relative != parent && !strings.HasSuffix(relative, "."+parent) {
		name = relative + "." + parent
	}
	if strings.ContainsAny(name, ":/") || len(name) > 253 {
		return domain.DNSPodValidation{}, fmt.Errorf("DNSPod child-zone validation response is malformed")
	}
	return domain.DNSPodValidation{Name: name, Value: strings.TrimSpace(*raw.Value)}, nil
}

func firstString(values ...*string) string {
	for _, value := range values {
		if value != nil && strings.TrimSpace(*value) != "" {
			return *value
		}
	}
	return ""
}

func (c *Client) ValidationReady(ctx context.Context, hostname string) (bool, error) {
	host, err := domain.NormalizeHostname(hostname)
	if err != nil {
		return false, err
	}
	request := api.NewDescribeSubdomainValidateStatusRequest()
	request.DomainZone = common.StringPtr(host)
	_, err = c.sdk.DescribeSubdomainValidateStatusWithContext(ctx, request)
	if err == nil {
		return true, nil
	}
	var sdkErr *sdkerrors.TencentCloudSDKError
	if errors.As(err, &sdkErr) && strings.HasSuffix(sdkErr.Code, "QuhuiTxtNotMatch") {
		return false, nil
	}
	return false, providerError(err)
}

func (c *Client) CreateZone(ctx context.Context, hostname string) (*domain.Zone, error) {
	host, err := domain.NormalizeHostname(hostname)
	if err != nil {
		return nil, err
	}
	request := api.NewCreateDomainRequest()
	request.Domain = common.StringPtr(host)
	request.TransferSubDomain = common.BoolPtr(false)
	response, err := c.sdk.CreateDomainWithContext(ctx, request)
	if err != nil {
		return nil, providerError(err)
	}
	if response == nil || response.Response == nil || response.Response.DomainInfo == nil {
		return nil, fmt.Errorf("DNSPod create-domain response is malformed")
	}
	info := response.Response.DomainInfo
	if info.Id == nil || info.Domain == nil {
		return nil, fmt.Errorf("DNSPod create-domain response is incomplete")
	}
	name, err := domain.NormalizeHostname(*info.Domain)
	if err != nil || name != host {
		return nil, fmt.Errorf("DNSPod created an unexpected domain")
	}
	nameservers, err := pointerStrings(info.GradeNsList)
	if err != nil {
		return nil, err
	}
	nameservers, err = domain.NormalizeNameservers(nameservers)
	if err != nil {
		return nil, err
	}
	return &domain.Zone{ID: strconv.FormatUint(*info.Id, 10), Name: name, Status: "pause", Nameservers: nameservers}, nil
}

func (c *Client) EnableZone(ctx context.Context, zone *domain.Zone, dryRun bool) (string, error) {
	if zone == nil {
		return "", fmt.Errorf("DNSPod Zone is required")
	}
	if strings.EqualFold(zone.Status, "enable") {
		return "unchanged", nil
	}
	if dryRun {
		return "would-enable", nil
	}
	id, err := strconv.ParseUint(zone.ID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid DNSPod Zone ID")
	}
	request := api.NewModifyDomainStatusRequest()
	request.Domain = common.StringPtr(zone.Name)
	request.DomainId = common.Uint64Ptr(id)
	request.Status = common.StringPtr("enable")
	if _, err := c.sdk.ModifyDomainStatusWithContext(ctx, request); err != nil {
		return "", providerError(err)
	}
	return "enabled", nil
}

func relativeName(fullName, zoneName string) (string, error) {
	full := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(fullName), "."))
	zone, err := domain.NormalizeHostname(zoneName)
	if err != nil {
		return "", err
	}
	if full == zone {
		return "@", nil
	}
	suffix := "." + zone
	if !strings.HasSuffix(full, suffix) {
		return "", fmt.Errorf("record %s is outside DNSPod Zone %s", full, zone)
	}
	return strings.TrimSuffix(full, suffix), nil
}

func (c *Client) ListRecords(ctx context.Context, zoneName, fullName string) ([]domain.DNSRecord, error) {
	zone, err := domain.NormalizeHostname(zoneName)
	if err != nil {
		return nil, err
	}
	relative, err := relativeName(fullName, zone)
	if err != nil {
		return nil, err
	}
	result := []domain.DNSRecord{}
	const limit uint64 = 3000
	for offset := uint64(0); ; {
		request := api.NewDescribeRecordListRequest()
		request.Domain = common.StringPtr(zone)
		request.SubDomain = common.StringPtr(relative)
		request.Offset = common.Uint64Ptr(offset)
		request.Limit = common.Uint64Ptr(limit)
		request.ErrorOnEmpty = common.StringPtr("no")
		response, err := c.sdk.DescribeRecordListWithContext(ctx, request)
		if err != nil {
			return nil, providerError(err)
		}
		if response == nil || response.Response == nil {
			return nil, fmt.Errorf("DNSPod returned malformed record-list data")
		}
		batch := response.Response.RecordList
		for _, item := range batch {
			if item == nil || item.RecordId == nil || item.Name == nil || item.Type == nil || item.Value == nil || item.Line == nil {
				return nil, fmt.Errorf("DNSPod returned malformed record data")
			}
			if !strings.EqualFold(*item.Name, relative) {
				continue
			}
			result = append(result, domain.DNSRecord{
				ID: strconv.FormatUint(*item.RecordId, 10), Name: *item.Name,
				Type: strings.ToUpper(*item.Type), Content: *item.Value, Line: *item.Line,
			})
		}
		if len(batch) < int(limit) {
			break
		}
		offset += uint64(len(batch))
	}
	return result, nil
}

func (c *Client) createRecord(ctx context.Context, zone, relative, recordType, value string) (domain.DNSRecord, error) {
	request := api.NewCreateRecordRequest()
	request.Domain = common.StringPtr(zone)
	request.SubDomain = common.StringPtr(relative)
	request.RecordType = common.StringPtr(recordType)
	request.RecordLine = common.StringPtr(c.recordLine)
	request.Value = common.StringPtr(value)
	response, err := c.sdk.CreateRecordWithContext(ctx, request)
	if err != nil {
		return domain.DNSRecord{}, providerError(err)
	}
	if response == nil || response.Response == nil || response.Response.RecordId == nil {
		return domain.DNSRecord{}, fmt.Errorf("DNSPod create response omitted record ID")
	}
	return domain.DNSRecord{ID: strconv.FormatUint(*response.Response.RecordId, 10), Name: relative, Type: recordType, Content: value, Line: c.recordLine}, nil
}

func (c *Client) modifyRecord(ctx context.Context, zone string, current domain.DNSRecord, recordType, value string) (domain.DNSRecord, error) {
	id, err := strconv.ParseUint(current.ID, 10, 64)
	if err != nil {
		return domain.DNSRecord{}, fmt.Errorf("invalid DNSPod record ID")
	}
	request := api.NewModifyRecordRequest()
	request.Domain = common.StringPtr(zone)
	request.RecordId = common.Uint64Ptr(id)
	request.SubDomain = common.StringPtr(current.Name)
	request.RecordType = common.StringPtr(recordType)
	request.RecordLine = common.StringPtr(c.recordLine)
	request.Value = common.StringPtr(value)
	if _, err := c.sdk.ModifyRecordWithContext(ctx, request); err != nil {
		return domain.DNSRecord{}, providerError(err)
	}
	return domain.DNSRecord{ID: current.ID, Name: current.Name, Type: recordType, Content: value, Line: c.recordLine}, nil
}

func (c *Client) EnsureTXT(ctx context.Context, zone, fullName, value string, dryRun bool) (string, error) {
	records, err := c.ListRecords(ctx, zone, fullName)
	if err != nil {
		return "", err
	}
	for _, record := range records {
		if record.Type == "TXT" && record.Line == c.recordLine && strings.Trim(record.Content, "\"") == value {
			return "unchanged", nil
		}
	}
	if dryRun {
		return "would-create", nil
	}
	relative, err := relativeName(fullName, zone)
	if err != nil {
		return "", err
	}
	_, err = c.createRecord(ctx, zone, relative, "TXT", value)
	return "created", err
}

func (c *Client) EnsureFallback(ctx context.Context, zone, hostname, target string, dryRun bool) (string, error) {
	records, err := c.ListRecords(ctx, zone, hostname)
	if err != nil {
		return "", err
	}
	for _, record := range records {
		if record.Type == "A" || record.Type == "AAAA" || record.Type == "CNAME" {
			if record.Type == "CNAME" && domain.EqualTarget(record.Content, target) {
				return "unchanged", nil
			}
			return "preserved-existing-edge", nil
		}
	}
	if dryRun {
		return "would-create", nil
	}
	_, err = c.createRecord(ctx, zone, "@", "CNAME", target)
	return "created", err
}

func (c *Client) SetTraffic(ctx context.Context, zone, hostname, recordType, value string, dryRun bool) (string, error) {
	records, err := c.ListRecords(ctx, zone, hostname)
	if err != nil {
		return "", err
	}
	traffic := make([]domain.DNSRecord, 0, len(records))
	for _, record := range records {
		if record.Type != "A" && record.Type != "AAAA" && record.Type != "CNAME" {
			continue
		}
		if record.Line != c.recordLine {
			return "", fmt.Errorf("apex %s record %s uses DNSPod line %q instead of configured line %q", record.Type, record.ID, record.Line, c.recordLine)
		}
		traffic = append(traffic, record)
	}
	plan, err := PlanTraffic(traffic, recordType, value)
	if err != nil {
		return "", err
	}
	if plan.Action == "unchanged" {
		return "unchanged", nil
	}
	if dryRun {
		return "would-" + plan.Action, nil
	}
	if plan.Action == "create" {
		_, err = c.createRecord(ctx, zone, "@", plan.Type, plan.Value)
		return "created", err
	}
	_, err = c.modifyRecord(ctx, zone, *plan.Current, plan.Type, plan.Value)
	return "modified", err
}

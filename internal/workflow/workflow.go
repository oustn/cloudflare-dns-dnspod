package workflow

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/oustn/cloudflare-dns-dnspod/internal/config"
	"github.com/oustn/cloudflare-dns-dnspod/internal/dnspod"
	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
)

type Cloudflare interface {
	ListZones(context.Context) ([]domain.Zone, error)
	GetZone(context.Context, string) (domain.Zone, error)
	GetFallbackOrigin(context.Context, string) (domain.FallbackOrigin, error)
	ListDNSRecords(context.Context, string, string) ([]domain.DNSRecord, error)
	FindCustomHostname(context.Context, string, string) (*domain.CustomHostname, error)
	GetCustomHostname(context.Context, string, string) (*domain.CustomHostname, error)
	CreateDNSRecord(context.Context, string, string, string, string) (domain.DNSRecord, error)
	DeleteDNSRecord(context.Context, string, string) error
	CreateCustomHostname(context.Context, string, string, string) (*domain.CustomHostname, error)
}

type DNSPod interface {
	FindZone(context.Context, string) (*domain.Zone, error)
	RequestValidation(context.Context, string) (domain.DNSPodValidation, error)
	ValidationReady(context.Context, string) (bool, error)
	CreateZone(context.Context, string) (*domain.Zone, error)
	EnableZone(context.Context, *domain.Zone, bool) (string, error)
	ListRecords(context.Context, string, string) ([]domain.DNSRecord, error)
	EnsureTXT(context.Context, string, string, string, bool) (string, error)
	EnsureFallback(context.Context, string, string, string, bool) (string, error)
	SetTraffic(context.Context, string, string, string, string, bool) (string, error)
}

type Resolver interface {
	Delegation(context.Context, string, []string) ([]string, bool, error)
	CheckHostTarget(context.Context, string, string, int) ([]string, error)
}

type Services struct {
	Cloudflare Cloudflare
	DNSPod     DNSPod
	Resolver   Resolver
}

type BlockedError struct {
	Operation string
	Blockers  []string
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("%s preflight blocked: %s", e.Operation, strings.Join(e.Blockers, "; "))
}

type AddOptions struct {
	Wait           bool
	DryRun         bool
	ReplaceStaleNS bool
	Timeout        time.Duration
	PollInterval   time.Duration
}

type EdgeOptions struct {
	DryRun bool
}

type EdgeTarget struct {
	Host string
	IP   string
}

type addSnapshot struct {
	parentZone    domain.Zone
	saasZone      domain.Zone
	fallback      domain.FallbackOrigin
	parentRecords []domain.DNSRecord
	zone          *domain.Zone
	hostname      *domain.CustomHostname
	state         string
	checks        []domain.Check
}

func validateServices(services Services) error {
	if services.Cloudflare == nil || services.DNSPod == nil || services.Resolver == nil {
		return fmt.Errorf("provider services are incomplete")
	}
	return nil
}

func preflightIdentity(ctx context.Context, cfg config.Config, cf Cloudflare) (domain.Zone, domain.Zone, []domain.Check, []string) {
	checks := []domain.Check{}
	blockers := []string{}
	parent, err := cf.GetZone(ctx, cfg.CFParentZoneID)
	if err != nil {
		blockers = append(blockers, "cannot verify Cloudflare Parent Zone: "+err.Error())
	} else if parent.ID != cfg.CFParentZoneID || !domain.EqualTarget(parent.Name, cfg.CFParentZoneName) || !strings.EqualFold(parent.Status, "active") {
		blockers = append(blockers, fmt.Sprintf("Cloudflare Parent Zone ID/name/status mismatch: id=%s name=%s status=%s", parent.ID, parent.Name, parent.Status))
	} else {
		checks = append(checks, domain.Check{Name: "cloudflare_parent_zone", OK: true})
	}
	saas, err := cf.GetZone(ctx, cfg.CFSaaSZoneID)
	if err != nil {
		blockers = append(blockers, "cannot verify Cloudflare SaaS Zone: "+err.Error())
	} else if saas.ID != cfg.CFSaaSZoneID || !strings.EqualFold(saas.Status, "active") {
		blockers = append(blockers, fmt.Sprintf("Cloudflare SaaS Zone ID/status mismatch: id=%s status=%s", saas.ID, saas.Status))
	} else {
		checks = append(checks, domain.Check{Name: "cloudflare_saas_zone", OK: true})
	}
	return parent, saas, checks, blockers
}

func preflightAdd(ctx context.Context, cfg config.Config, hostname string, services Services, replaceStale bool) (addSnapshot, error) {
	var snap addSnapshot
	parent, saas, checks, blockers := preflightIdentity(ctx, cfg, services.Cloudflare)
	snap.parentZone, snap.saasZone, snap.checks = parent, saas, checks

	fallback, err := services.Cloudflare.GetFallbackOrigin(ctx, cfg.CFSaaSZoneID)
	if err != nil {
		blockers = append(blockers, "cannot verify Cloudflare Fallback Origin: "+err.Error())
	} else if !strings.EqualFold(fallback.Status, "active") || !domain.EqualTarget(fallback.Origin, cfg.CFFallbackHost) {
		blockers = append(blockers, fmt.Sprintf("Cloudflare Fallback Origin mismatch/inactive: configured=%s actual=%s status=%s", cfg.CFFallbackHost, fallback.Origin, fallback.Status))
	} else {
		snap.checks = append(snap.checks, domain.Check{Name: "fallback_origin", OK: true})
	}
	snap.fallback = fallback

	records, err := services.Cloudflare.ListDNSRecords(ctx, cfg.CFParentZoneID, hostname)
	if err != nil {
		blockers = append(blockers, "cannot list exact Cloudflare parent records: "+err.Error())
	}
	snap.parentRecords = records
	zone, err := services.DNSPod.FindZone(ctx, hostname)
	if err != nil {
		blockers = append(blockers, "cannot inspect exact DNSPod Zone: "+err.Error())
	}
	snap.zone = zone
	host, err := services.Cloudflare.FindCustomHostname(ctx, cfg.CFSaaSZoneID, hostname)
	if err != nil {
		blockers = append(blockers, "cannot inspect exact Cloudflare Custom Hostname: "+err.Error())
	}
	snap.hostname = host

	expectedNS := map[string]bool{}
	if zone != nil {
		if zone.Name != hostname || zone.ID == "" {
			blockers = append(blockers, "DNSPod returned a contradictory Zone identity")
		}
		for _, ns := range zone.Nameservers {
			expectedNS[strings.ToLower(strings.TrimSuffix(ns, "."))] = true
		}
	}
	for _, record := range records {
		kind := strings.ToUpper(record.Type)
		if record.Managed {
			blockers = append(blockers, fmt.Sprintf("exact %s record %s is application-managed", kind, record.ID))
			continue
		}
		switch kind {
		case "TXT":
			// DNSPod ownership TXT records can safely coexist at the delegation name.
		case "A", "AAAA", "CNAME":
			blockers = append(blockers, fmt.Sprintf("exact %s record already exists at %s", kind, hostname))
		case "NS":
			ns := strings.ToLower(strings.TrimSuffix(record.Content, "."))
			if zone == nil {
				blockers = append(blockers, fmt.Sprintf("existing NS delegation %s is not owned by an accessible DNSPod Zone", ns))
			} else if !expectedNS[ns] && !replaceStale {
				blockers = append(blockers, fmt.Sprintf("unexpected NS delegation %s; use --replace-stale-ns only after verification", ns))
			}
		default:
			blockers = append(blockers, fmt.Sprintf("incompatible exact %s record exists at %s", kind, hostname))
		}
	}
	if len(blockers) > 0 {
		sort.Strings(blockers)
		return snap, &BlockedError{Operation: "add", Blockers: blockers}
	}
	snap.checks = append(snap.checks, domain.Check{Name: "delegation_point_available", OK: true})
	switch {
	case zone == nil && host == nil:
		snap.state = "new"
	case zone != nil && strings.EqualFold(zone.Status, "enable") && host.Active():
		snap.state = "converged"
	default:
		snap.state = "resumable"
	}
	return snap, nil
}

func Add(ctx context.Context, cfg config.Config, subdomain string, services Services, options AddOptions) (domain.OperationResult, error) {
	if err := validateServices(services); err != nil {
		return domain.OperationResult{}, err
	}
	hostname, err := domain.BuildHostname(subdomain, cfg.CFParentZoneName)
	if err != nil {
		return domain.OperationResult{}, err
	}
	if cfg.CFFallbackHost == "" {
		return domain.OperationResult{}, fmt.Errorf("CF_FALLBACK_HOST is required for add")
	}
	if options.Timeout < 0 || options.PollInterval < 0 {
		return domain.OperationResult{}, fmt.Errorf("timeout and poll interval must be non-negative")
	}
	snap, err := preflightAdd(ctx, cfg, hostname, services, options.ReplaceStaleNS)
	if err != nil {
		return domain.OperationResult{}, err
	}
	result := domain.OperationResult{Operation: "add", Hostname: hostname, Ready: true, DryRun: options.DryRun, State: snap.state, Checks: snap.checks}
	zone := snap.zone
	if zone == nil {
		if options.DryRun {
			result.Changes = append(result.Changes,
				domain.Change{Resource: "dnspod_validation", Action: "would-request"},
				domain.Change{Resource: "dnspod_zone", Action: "would-create"},
				domain.Change{Resource: "delegation", Action: "available-after-zone-create"},
			)
			return result, nil
		}
		validation, err := services.DNSPod.RequestValidation(ctx, hostname)
		if err != nil {
			return result, err
		}
		result.Wrote = true
		result.Changes = append(result.Changes, domain.Change{Resource: "dnspod_validation", Action: "requested"})
		action, err := ensureParentTXT(ctx, cfg, services.Cloudflare, validation)
		if err != nil {
			return result, err
		}
		if action == "created" {
			result.Wrote = true
		}
		result.Changes = append(result.Changes, domain.Change{Resource: "parent_validation_txt", Action: action})
		ready, err := services.DNSPod.ValidationReady(ctx, hostname)
		if err != nil {
			return result, err
		}
		if options.Wait && !ready {
			ready, err = waitUntil(ctx, options.Timeout, options.PollInterval, func() (bool, error) {
				return services.DNSPod.ValidationReady(ctx, hostname)
			})
			if err != nil {
				return result, fmt.Errorf("wait for DNSPod ownership validation: %w", err)
			}
		}
		if !ready {
			result.Ready = false
			result.State = "pending_dnspod_validation"
			return result, nil
		}
		zone, err = services.DNSPod.CreateZone(ctx, hostname)
		if err != nil {
			return result, err
		}
		result.Wrote = true
		result.Changes = append(result.Changes, domain.Change{Resource: "dnspod_zone", Action: "created"})
	}

	nsChanges, wrote, err := reconcileNS(ctx, cfg, services.Cloudflare, hostname, zone.Nameservers, options.ReplaceStaleNS, options.DryRun)
	if err != nil {
		return result, err
	}
	result.Changes = append(result.Changes, nsChanges...)
	result.Wrote = result.Wrote || wrote
	action, err := services.DNSPod.EnableZone(ctx, zone, options.DryRun)
	if err != nil {
		return result, err
	}
	result.Changes = append(result.Changes, domain.Change{Resource: "dnspod_zone", Action: action})
	if action == "enabled" {
		result.Wrote = true
		zone.Status = "enable"
	}
	observed, delegated, err := services.Resolver.Delegation(ctx, hostname, zone.Nameservers)
	if err != nil && options.Wait {
		delegated = false
	}
	if options.Wait && !delegated {
		delegated, err = waitUntil(ctx, options.Timeout, options.PollInterval, func() (bool, error) {
			var lookupErr error
			observed, delegated, lookupErr = services.Resolver.Delegation(ctx, hostname, zone.Nameservers)
			if lookupErr != nil {
				return false, nil
			}
			return delegated, nil
		})
		if err != nil {
			return result, fmt.Errorf("wait for public NS delegation: %w", err)
		}
	}
	result.Status = map[string]any{"assigned_nameservers": zone.Nameservers, "observed_nameservers": observed, "delegation_ready": delegated}

	host := snap.hostname
	if host == nil {
		if options.DryRun {
			result.Changes = append(result.Changes, domain.Change{Resource: "custom_hostname", Action: "would-create"})
			result.Ready = false
			result.State = "planned"
			return result, nil
		}
		host, err = services.Cloudflare.CreateCustomHostname(ctx, cfg.CFSaaSZoneID, hostname, "")
		if err != nil {
			return result, err
		}
		result.Wrote = true
		result.Changes = append(result.Changes, domain.Change{Resource: "custom_hostname", Action: "created"})
	} else {
		result.Changes = append(result.Changes, domain.Change{Resource: "custom_hostname", Action: "reused"})
	}
	host, err = services.Cloudflare.GetCustomHostname(ctx, cfg.CFSaaSZoneID, host.ID)
	if err != nil {
		return result, err
	}
	published := map[string]bool{}
	publishValidations := func(current *domain.CustomHostname) error {
		for _, validation := range current.ValidationRecords {
			key := validation.Name + "\x00" + validation.Value
			if published[key] {
				continue
			}
			action, err := services.DNSPod.EnsureTXT(ctx, zone.Name, validation.Name, validation.Value, options.DryRun)
			if err != nil {
				return err
			}
			published[key] = true
			result.Changes = append(result.Changes, domain.Change{Resource: "validation_txt", Action: action, Detail: validation.Name})
			if action == "created" {
				result.Wrote = true
			}
		}
		return nil
	}
	if err := publishValidations(host); err != nil {
		return result, err
	}
	fallbackAction, err := services.DNSPod.EnsureFallback(ctx, zone.Name, hostname, cfg.CFFallbackHost, options.DryRun)
	if err != nil {
		return result, err
	}
	result.Changes = append(result.Changes, domain.Change{Resource: "fallback_cname", Action: fallbackAction})
	if fallbackAction == "created" {
		result.Wrote = true
	}
	if options.Wait && !host.Active() {
		_, err = waitUntil(ctx, options.Timeout, options.PollInterval, func() (bool, error) {
			host, err = services.Cloudflare.GetCustomHostname(ctx, cfg.CFSaaSZoneID, host.ID)
			if err != nil {
				return false, err
			}
			if err := publishValidations(host); err != nil {
				return false, err
			}
			return host.Active(), nil
		})
		if err != nil {
			return result, fmt.Errorf("wait for Cloudflare Custom Hostname activation: %w", err)
		}
	}
	result.Ready = delegated && host.Active()
	result.State = map[bool]string{true: "converged", false: "resumable"}[result.Ready]
	result.Status["cloudflare_status"] = host.Status
	result.Status["ssl_status"] = host.SSLStatus
	return result, nil
}

func ensureParentTXT(ctx context.Context, cfg config.Config, cf Cloudflare, validation domain.DNSPodValidation) (string, error) {
	records, err := cf.ListDNSRecords(ctx, cfg.CFParentZoneID, validation.Name)
	if err != nil {
		return "", err
	}
	for _, record := range records {
		if record.Type == "TXT" && strings.Trim(record.Content, "\"") == validation.Value {
			return "unchanged", nil
		}
		if record.Managed || record.Type != "TXT" {
			return "", fmt.Errorf("validation name %s has an incompatible %s record", validation.Name, record.Type)
		}
	}
	_, err = cf.CreateDNSRecord(ctx, cfg.CFParentZoneID, "TXT", validation.Name, validation.Value)
	if err != nil {
		return "", err
	}
	return "created", nil
}

func reconcileNS(ctx context.Context, cfg config.Config, cf Cloudflare, hostname string, assigned []string, replace, dryRun bool) ([]domain.Change, bool, error) {
	expected, err := domain.NormalizeNameservers(assigned)
	if err != nil {
		return nil, false, err
	}
	records, err := cf.ListDNSRecords(ctx, cfg.CFParentZoneID, hostname)
	if err != nil {
		return nil, false, err
	}
	current := map[string]domain.DNSRecord{}
	for _, record := range records {
		if record.Type != "NS" {
			if record.Type != "TXT" {
				return nil, false, fmt.Errorf("delegation point changed after preflight: found %s", record.Type)
			}
			continue
		}
		key := strings.ToLower(strings.TrimSuffix(record.Content, "."))
		if _, duplicate := current[key]; duplicate {
			return nil, false, fmt.Errorf("duplicate NS record for %s", key)
		}
		current[key] = record
	}
	expectedSet := map[string]bool{}
	changes := []domain.Change{}
	wrote := false
	for _, ns := range expected {
		expectedSet[ns] = true
		if _, exists := current[ns]; exists {
			changes = append(changes, domain.Change{Resource: "delegation_ns", Action: "unchanged", Detail: ns})
			continue
		}
		action := "would-create"
		if !dryRun {
			if _, err := cf.CreateDNSRecord(ctx, cfg.CFParentZoneID, "NS", hostname, ns); err != nil {
				return changes, wrote, err
			}
			action, wrote = "created", true
		}
		changes = append(changes, domain.Change{Resource: "delegation_ns", Action: action, Detail: ns})
	}
	for ns, record := range current {
		if expectedSet[ns] {
			continue
		}
		if !replace {
			return changes, wrote, fmt.Errorf("unexpected NS %s appeared after preflight", ns)
		}
		action := "would-delete"
		if !dryRun {
			if err := cf.DeleteDNSRecord(ctx, cfg.CFParentZoneID, record.ID); err != nil {
				return changes, wrote, err
			}
			action, wrote = "deleted", true
		}
		changes = append(changes, domain.Change{Resource: "delegation_ns", Action: action, Detail: ns})
	}
	return changes, wrote, nil
}

func waitUntil(ctx context.Context, timeout, interval time.Duration, fn func() (bool, error)) (bool, error) {
	if timeout == 0 {
		return false, fmt.Errorf("timed out")
	}
	deadline := time.Now().Add(timeout)
	for {
		ready, err := fn()
		if err != nil {
			return false, err
		}
		if ready {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			return false, fmt.Errorf("timed out after %s", timeout)
		}
		delay := interval
		if delay <= 0 {
			delay = time.Millisecond
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(delay):
		}
	}
}

type edgeSnapshot struct {
	zone     *domain.Zone
	hostname *domain.CustomHostname
	records  []domain.DNSRecord
	checks   []domain.Check
	observed []string
	chain    []string
}

func SetEdge(ctx context.Context, cfg config.Config, subdomain string, target EdgeTarget, services Services, options EdgeOptions) (domain.OperationResult, error) {
	if err := validateServices(services); err != nil {
		return domain.OperationResult{}, err
	}
	hostname, err := domain.BuildHostname(subdomain, cfg.CFParentZoneName)
	if err != nil {
		return domain.OperationResult{}, err
	}
	recordType, value, err := normalizeEdgeTarget(target)
	if err != nil {
		return domain.OperationResult{}, err
	}
	snap, err := preflightEdge(ctx, cfg, hostname, recordType, value, services)
	if err != nil {
		return domain.OperationResult{}, err
	}
	action, err := services.DNSPod.SetTraffic(ctx, snap.zone.Name, hostname, recordType, value, options.DryRun)
	if err != nil {
		return domain.OperationResult{}, err
	}
	wrote := action == "created" || action == "modified"
	return domain.OperationResult{
		Operation: "set-edge", Hostname: hostname, Ready: true, DryRun: options.DryRun,
		Wrote: wrote, State: action, Checks: snap.checks,
		Changes: []domain.Change{{Resource: "apex_traffic", Action: action, Detail: recordType + " " + value}},
		Status:  map[string]any{"observed_nameservers": snap.observed, "cname_chain": snap.chain},
	}, nil
}

func normalizeEdgeTarget(target EdgeTarget) (string, string, error) {
	hostSet := strings.TrimSpace(target.Host) != ""
	ipSet := strings.TrimSpace(target.IP) != ""
	if hostSet == ipSet {
		return "", "", fmt.Errorf("exactly one of --host and --ip is required")
	}
	if ipSet {
		if err := domain.ValidatePublicIPv4(target.IP); err != nil {
			return "", "", err
		}
		return "A", strings.TrimSpace(target.IP), nil
	}
	host, err := domain.NormalizeHostname(target.Host)
	if err != nil {
		return "", "", fmt.Errorf("edge Host: %w", err)
	}
	return "CNAME", host, nil
}

func preflightEdge(ctx context.Context, cfg config.Config, hostname, recordType, value string, services Services) (edgeSnapshot, error) {
	var snap edgeSnapshot
	_, _, checks, blockers := preflightIdentity(ctx, cfg, services.Cloudflare)
	snap.checks = checks
	zone, err := services.DNSPod.FindZone(ctx, hostname)
	if err != nil {
		blockers = append(blockers, "cannot inspect DNSPod Zone: "+err.Error())
	} else if zone == nil {
		blockers = append(blockers, "DNSPod Zone does not exist")
	} else if !strings.EqualFold(zone.Status, "enable") {
		blockers = append(blockers, "DNSPod Zone is not enabled")
	} else if _, err := domain.NormalizeNameservers(zone.Nameservers); err != nil {
		blockers = append(blockers, "DNSPod Zone nameservers are invalid: "+err.Error())
	} else {
		snap.checks = append(snap.checks, domain.Check{Name: "dnspod_zone_enabled", OK: true})
	}
	snap.zone = zone
	if zone != nil && len(zone.Nameservers) >= 2 {
		observed, ready, lookupErr := services.Resolver.Delegation(ctx, hostname, zone.Nameservers)
		snap.observed = observed
		if lookupErr != nil {
			blockers = append(blockers, "cannot verify public NS delegation: "+lookupErr.Error())
		} else if !ready {
			blockers = append(blockers, "public NS delegation does not match DNSPod")
		} else {
			snap.checks = append(snap.checks, domain.Check{Name: "public_delegation", OK: true})
		}
	}
	host, err := services.Cloudflare.FindCustomHostname(ctx, cfg.CFSaaSZoneID, hostname)
	if err != nil {
		blockers = append(blockers, "cannot inspect Cloudflare Custom Hostname: "+err.Error())
	} else if host == nil {
		blockers = append(blockers, "Cloudflare Custom Hostname does not exist")
	} else if !host.Active() {
		blockers = append(blockers, fmt.Sprintf("Cloudflare Custom Hostname/SSL is not active: hostname=%s ssl=%s", host.Status, host.SSLStatus))
	} else {
		snap.checks = append(snap.checks, domain.Check{Name: "custom_hostname_active", OK: true})
	}
	snap.hostname = host
	if recordType == "CNAME" {
		chain, targetErr := services.Resolver.CheckHostTarget(ctx, hostname, value, 16)
		snap.chain = chain
		if targetErr != nil {
			blockers = append(blockers, targetErr.Error())
		} else {
			snap.checks = append(snap.checks, domain.Check{Name: "edge_target_resolves", OK: true})
		}
	} else {
		snap.checks = append(snap.checks, domain.Check{Name: "edge_ip_globally_routable", OK: true})
	}
	if zone != nil {
		records, listErr := services.DNSPod.ListRecords(ctx, zone.Name, hostname)
		snap.records = records
		if listErr != nil {
			blockers = append(blockers, "cannot inspect DNSPod apex records: "+listErr.Error())
		} else {
			traffic := make([]domain.DNSRecord, 0, len(records))
			for _, record := range records {
				if record.Type != "A" && record.Type != "AAAA" && record.Type != "CNAME" {
					continue
				}
				if record.Line != cfg.DNSPodRecordLine {
					blockers = append(blockers, fmt.Sprintf("DNSPod apex %s record %s uses unexpected line %q", record.Type, record.ID, record.Line))
				}
				traffic = append(traffic, record)
			}
			if _, planErr := dnspod.PlanTraffic(traffic, recordType, value); planErr != nil {
				blockers = append(blockers, "DNSPod apex traffic records are ambiguous: "+planErr.Error())
			} else {
				snap.checks = append(snap.checks, domain.Check{Name: "apex_traffic_unambiguous", OK: true})
			}
		}
	}
	if len(blockers) > 0 {
		sort.Strings(blockers)
		return snap, &BlockedError{Operation: "set-edge", Blockers: blockers}
	}
	return snap, nil
}

func Status(ctx context.Context, cfg config.Config, subdomain string, services Services) (domain.OperationResult, error) {
	if err := validateServices(services); err != nil {
		return domain.OperationResult{}, err
	}
	hostname, err := domain.BuildHostname(subdomain, cfg.CFParentZoneName)
	if err != nil {
		return domain.OperationResult{}, err
	}
	parent, saas, checks, blockers := preflightIdentity(ctx, cfg, services.Cloudflare)
	zone, err := services.DNSPod.FindZone(ctx, hostname)
	if err != nil {
		blockers = append(blockers, "DNSPod Zone: "+err.Error())
	}
	parentRecords, err := services.Cloudflare.ListDNSRecords(ctx, cfg.CFParentZoneID, hostname)
	if err != nil {
		blockers = append(blockers, "Cloudflare records: "+err.Error())
	}
	host, err := services.Cloudflare.FindCustomHostname(ctx, cfg.CFSaaSZoneID, hostname)
	if err != nil {
		blockers = append(blockers, "Custom Hostname: "+err.Error())
	}
	var observed []string
	delegated := false
	var apex []domain.DNSRecord
	if zone != nil {
		observed, delegated, _ = services.Resolver.Delegation(ctx, hostname, zone.Nameservers)
		apex, err = services.DNSPod.ListRecords(ctx, zone.Name, hostname)
		if err != nil {
			blockers = append(blockers, "DNSPod records: "+err.Error())
		}
	}
	if len(blockers) > 0 {
		return domain.OperationResult{}, &BlockedError{Operation: "status", Blockers: blockers}
	}
	ready := zone != nil && strings.EqualFold(zone.Status, "enable") && delegated && host.Active()
	return domain.OperationResult{
		Operation: "status", Hostname: hostname, Ready: ready, State: map[bool]string{true: "converged", false: "incomplete"}[ready], Checks: checks,
		Status: map[string]any{
			"parent_zone": parent, "saas_zone": saas, "dnspod_zone": zone,
			"parent_records": parentRecords, "observed_nameservers": observed,
			"delegation_ready": delegated, "custom_hostname": host, "apex_records": apex,
		},
	}, nil
}

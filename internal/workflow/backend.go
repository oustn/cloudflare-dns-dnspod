package workflow

import (
	"context"
	"sort"

	"github.com/oustn/cloudflare-dns-dnspod/internal/config"
	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
)

type BackendOptions struct {
	Zone   string
	DryRun bool
}

func SetBackend(ctx context.Context, cfg config.Config, hostname, target string, services Services, options BackendOptions) (domain.OperationResult, error) {
	if err := validateServices(services); err != nil {
		return domain.OperationResult{}, err
	}
	hostname, err := domain.NormalizeHostname(hostname)
	if err != nil {
		return domain.OperationResult{}, err
	}
	recordType, value, err := domain.ClassifyTarget(target, "backend")
	if err != nil {
		return domain.OperationResult{}, err
	}
	infra, err := discoverInfrastructure(ctx, cfg, hostname, options.Zone, services.Cloudflare)
	if err != nil {
		return domain.OperationResult{}, &BlockedError{Operation: "set-backend", Blockers: []string{err.Error()}}
	}
	blockers := []string{}
	checks := []domain.Check{
		{Name: "cloudflare_parent_zone", OK: true},
		{Name: "cloudflare_saas_zone", OK: true, Message: infra.Source},
		{Name: "fallback_origin", OK: true},
	}
	if infra.Hostname == nil {
		blockers = append(blockers, "Cloudflare Custom Hostname does not exist")
	} else {
		checks = append(checks, domain.Check{Name: "custom_hostname_exists", OK: true})
	}
	if domain.EqualTarget(value, hostname) || domain.EqualTarget(value, infra.OriginName) {
		blockers = append(blockers, "backend target would create a routing loop")
	}
	var chain []string
	if recordType == "CNAME" {
		chain, err = services.Resolver.CheckHostTarget(ctx, hostname, value, 16)
		if err != nil {
			blockers = append(blockers, err.Error())
		} else {
			for _, item := range chain {
				if domain.EqualTarget(item, infra.OriginName) {
					blockers = append(blockers, "backend CNAME chain reaches the derived Custom Origin name")
					break
				}
			}
			checks = append(checks, domain.Check{Name: "backend_target_resolves", OK: true})
		}
	} else {
		checks = append(checks, domain.Check{Name: "backend_ip_globally_routable", OK: true})
	}
	records, err := services.Cloudflare.ListDNSRecords(ctx, infra.SaaS.ID, infra.OriginName)
	if err != nil {
		blockers = append(blockers, "cannot inspect Custom Origin DNS: "+err.Error())
	}
	action := ""
	if err == nil {
		action, err = planOriginRecord(records, recordType, value, true)
		if err != nil {
			blockers = append(blockers, err.Error())
		} else {
			checks = append(checks, domain.Check{Name: "custom_origin_dns_unambiguous", OK: true})
		}
	}
	if len(blockers) > 0 {
		sort.Strings(blockers)
		return domain.OperationResult{}, &BlockedError{Operation: "set-backend", Blockers: blockers}
	}

	result := domain.OperationResult{
		Operation: "set-backend", Hostname: hostname, Ready: true, DryRun: options.DryRun,
		State: "unchanged", Checks: checks,
		Status: map[string]any{
			"backend": map[string]any{
				"mode": "custom", "fallback_origin": infra.Fallback.Origin,
				"custom_origin_server": infra.OriginName, "type": recordType,
				"target": value, "proxied": true, "cname_chain": chain,
			},
		},
	}
	dnsAction := action
	switch action {
	case "create":
		dnsAction = "would-create"
		if !options.DryRun {
			if _, err := services.Cloudflare.CreateProxiedDNSRecord(ctx, infra.SaaS.ID, recordType, infra.OriginName, value); err != nil {
				return result, err
			}
			dnsAction = "created"
			result.Wrote = true
		}
	case "update":
		dnsAction = "would-update"
		if !options.DryRun {
			if _, err := services.Cloudflare.UpdateProxiedDNSRecord(ctx, infra.SaaS.ID, records[0].ID, recordType, infra.OriginName, value); err != nil {
				return result, err
			}
			dnsAction = "updated"
			result.Wrote = true
		}
	}
	result.Changes = append(result.Changes, domain.Change{Resource: "origin_dns", Action: dnsAction, Detail: recordType + " " + value})

	hostAction := "unchanged"
	if !domain.EqualTarget(infra.Hostname.CustomOriginServer, infra.OriginName) {
		hostAction = "would-update"
		if !options.DryRun {
			if _, err := services.Cloudflare.UpdateCustomHostnameOrigin(ctx, infra.SaaS.ID, infra.Hostname.ID, infra.OriginName); err != nil {
				return result, err
			}
			hostAction = "updated"
			result.Wrote = true
		}
	}
	result.Changes = append(result.Changes, domain.Change{Resource: "custom_hostname_backend", Action: hostAction, Detail: infra.OriginName})
	if result.Wrote {
		result.State = "updated"
	} else if options.DryRun && (dnsAction != "unchanged" || hostAction != "unchanged") {
		result.State = "planned"
	}
	return result, nil
}

package workflow

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/oustn/cloudflare-dns-dnspod/internal/config"
	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
)

type Infrastructure struct {
	Parent     domain.Zone
	SaaS       domain.Zone
	Fallback   domain.FallbackOrigin
	Hostname   *domain.CustomHostname
	OriginName string
	Source     string
}

func discoverInfrastructure(ctx context.Context, cfg config.Config, hostname, zoneOverride string, cf Cloudflare) (Infrastructure, error) {
	zones, err := cf.ListZones(ctx)
	if err != nil {
		return Infrastructure{}, fmt.Errorf("list Cloudflare Zones: %w", err)
	}
	parent, err := domain.FindParentZone(hostname, zones)
	if err != nil {
		return Infrastructure{}, err
	}
	candidates := make([]domain.Zone, 0, len(zones))
	for _, zone := range zones {
		if zone.ID != parent.ID {
			candidates = append(candidates, zone)
		}
	}

	selector := strings.TrimSpace(zoneOverride)
	source := "command"
	if selector == "" {
		selector = cfg.CFZone
		source = "configuration"
	}
	if selector != "" {
		name, err := domain.NormalizeHostname(selector)
		if err != nil {
			return Infrastructure{}, fmt.Errorf("Cloudflare SaaS Zone selector: %w", err)
		}
		if domain.EqualTarget(name, parent.Name) {
			return Infrastructure{}, fmt.Errorf("Cloudflare SaaS Zone cannot be the parent Zone %s", parent.Name)
		}
		selected, ok := zoneByName(candidates, name)
		if !ok {
			return Infrastructure{}, fmt.Errorf("Cloudflare SaaS Zone %s is not an active Zone available to the token", name)
		}
		return validateInfrastructure(ctx, hostname, parent, selected, source, cf)
	}

	owners := make([]domain.Zone, 0, 1)
	for _, zone := range candidates {
		host, findErr := cf.FindCustomHostname(ctx, zone.ID, hostname)
		if findErr == nil && host != nil && domain.EqualTarget(host.Hostname, hostname) {
			owners = append(owners, zone)
		}
	}
	if len(owners) == 1 {
		return validateInfrastructure(ctx, hostname, parent, owners[0], "custom_hostname", cf)
	}
	if len(owners) > 1 {
		return Infrastructure{}, ambiguousZones("Custom Hostname exists in multiple Cloudflare Zones", owners)
	}

	fallbackCandidates := make([]domain.Zone, 0, len(candidates))
	for _, zone := range candidates {
		fallback, fallbackErr := cf.GetFallbackOrigin(ctx, zone.ID)
		if fallbackErr == nil && validFallbackForZone(fallback, zone) {
			fallbackCandidates = append(fallbackCandidates, zone)
		}
	}
	if len(fallbackCandidates) == 0 {
		return Infrastructure{}, fmt.Errorf("no non-parent Cloudflare Zone has an active Fallback Origin; use --zone after configuring one")
	}
	if len(fallbackCandidates) > 1 {
		return Infrastructure{}, ambiguousZones("multiple Cloudflare Zones have an active Fallback Origin", fallbackCandidates)
	}
	return validateInfrastructure(ctx, hostname, parent, fallbackCandidates[0], "fallback", cf)
}

func validateInfrastructure(ctx context.Context, hostname string, parent, selected domain.Zone, source string, cf Cloudflare) (Infrastructure, error) {
	zone, err := cf.GetZone(ctx, selected.ID)
	if err != nil {
		return Infrastructure{}, fmt.Errorf("verify Cloudflare SaaS Zone %s: %w", selected.Name, err)
	}
	if zone.ID != selected.ID || !domain.EqualTarget(zone.Name, selected.Name) || !strings.EqualFold(zone.Status, "active") {
		return Infrastructure{}, fmt.Errorf("Cloudflare SaaS Zone identity/status changed during discovery")
	}
	fallback, err := cf.GetFallbackOrigin(ctx, zone.ID)
	if err != nil {
		return Infrastructure{}, fmt.Errorf("verify Cloudflare Fallback Origin for %s: %w", zone.Name, err)
	}
	if !validFallbackForZone(fallback, zone) {
		return Infrastructure{}, fmt.Errorf("Cloudflare Zone %s does not have an active in-zone Fallback Origin", zone.Name)
	}
	host, err := cf.FindCustomHostname(ctx, zone.ID, hostname)
	if err != nil {
		return Infrastructure{}, fmt.Errorf("inspect Cloudflare Custom Hostname in %s: %w", zone.Name, err)
	}
	originName, err := domain.RebaseHostname(hostname, parent.Name, zone.Name)
	if err != nil {
		return Infrastructure{}, err
	}
	return Infrastructure{
		Parent: parent, SaaS: zone, Fallback: fallback, Hostname: host,
		OriginName: originName, Source: source,
	}, nil
}

func validFallbackForZone(fallback domain.FallbackOrigin, zone domain.Zone) bool {
	if !strings.EqualFold(fallback.Status, "active") {
		return false
	}
	origin, err := domain.NormalizeHostname(fallback.Origin)
	if err != nil {
		return false
	}
	return domain.EqualTarget(origin, zone.Name) || strings.HasSuffix(origin, "."+strings.ToLower(zone.Name))
}

func zoneByName(zones []domain.Zone, name string) (domain.Zone, bool) {
	for _, zone := range zones {
		if domain.EqualTarget(zone.Name, name) {
			return zone, true
		}
	}
	return domain.Zone{}, false
}

func ambiguousZones(message string, zones []domain.Zone) error {
	names := make([]string, 0, len(zones))
	for _, zone := range zones {
		names = append(names, zone.Name)
	}
	sort.Strings(names)
	return fmt.Errorf("%s: %s; select one with --zone or CF_ZONE", message, strings.Join(names, ", "))
}

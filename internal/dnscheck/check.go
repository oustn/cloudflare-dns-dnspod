package dnscheck

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/oustn/cloudflare-dns-dnspod/internal/domain"
)

type Backend interface {
	LookupNS(context.Context, string) ([]string, error)
	LookupCNAME(context.Context, string) (target string, exists bool, err error)
	LookupHost(context.Context, string) ([]string, error)
}

type Checker struct {
	backend Backend
}

func New(backend Backend) *Checker {
	if backend == nil {
		backend = systemBackend{resolver: net.DefaultResolver}
	}
	return &Checker{backend: backend}
}

func NewPublic() *Checker {
	return New(&fallbackBackend{backends: []Backend{
		systemBackend{resolver: resolverAt("8.8.8.8:53")},
		systemBackend{resolver: resolverAt("1.1.1.1:53")},
		systemBackend{resolver: net.DefaultResolver},
	}})
}

func resolverAt(address string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: 3 * time.Second}
			return dialer.DialContext(ctx, network, address)
		},
	}
}

type fallbackBackend struct {
	backends []Backend
}

func (b *fallbackBackend) LookupNS(ctx context.Context, name string) ([]string, error) {
	var lastErr error
	for _, backend := range b.backends {
		values, err := backend.LookupNS(ctx, name)
		if err == nil && len(values) > 0 {
			return values, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no resolver returned NS records")
	}
	return nil, lastErr
}

func (b *fallbackBackend) LookupCNAME(ctx context.Context, name string) (string, bool, error) {
	var lastErr error
	for _, backend := range b.backends {
		target, exists, err := backend.LookupCNAME(ctx, name)
		if err == nil {
			return target, exists, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no resolver available")
	}
	return "", false, lastErr
}

func (b *fallbackBackend) LookupHost(ctx context.Context, name string) ([]string, error) {
	var lastErr error
	for _, backend := range b.backends {
		values, err := backend.LookupHost(ctx, name)
		if err == nil && len(values) > 0 {
			return values, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no resolver returned addresses")
	}
	return nil, lastErr
}

func (c *Checker) Delegation(ctx context.Context, hostname string, assigned []string) ([]string, bool, error) {
	host, err := domain.NormalizeHostname(hostname)
	if err != nil {
		return nil, false, err
	}
	raw, err := c.backend.LookupNS(ctx, host)
	if err != nil {
		return nil, false, fmt.Errorf("lookup NS for %s: %w", host, err)
	}
	observed, err := normalizeNS(raw, false)
	if err != nil {
		return nil, false, err
	}
	expected, err := normalizeNS(assigned, true)
	if err != nil {
		return observed, false, err
	}
	set := map[string]bool{}
	for _, ns := range observed {
		set[ns] = true
	}
	for _, ns := range expected {
		if !set[ns] {
			return observed, false, nil
		}
	}
	return observed, true, nil
}

func normalizeNS(values []string, requireTwo bool) ([]string, error) {
	seen := map[string]bool{}
	for _, value := range values {
		ns, err := domain.NormalizeHostname(value)
		if err != nil {
			return nil, err
		}
		seen[ns] = true
	}
	result := make([]string, 0, len(seen))
	for ns := range seen {
		result = append(result, ns)
	}
	sort.Strings(result)
	if requireTwo && len(result) < 2 {
		return nil, fmt.Errorf("DNSPod must assign at least two nameservers")
	}
	return result, nil
}

func (c *Checker) CheckHostTarget(ctx context.Context, managedHostname, target string, maxHops int) ([]string, error) {
	managed, err := domain.NormalizeHostname(managedHostname)
	if err != nil {
		return nil, err
	}
	current, err := domain.NormalizeHostname(target)
	if err != nil {
		return nil, fmt.Errorf("edge Host: %w", err)
	}
	if maxHops < 1 {
		return nil, fmt.Errorf("CNAME hop limit must be positive")
	}
	seen := map[string]bool{}
	chain := make([]string, 0, maxHops+1)
	for hop := 0; hop < maxHops; hop++ {
		if current == managed {
			return chain, fmt.Errorf("edge Host CNAME chain references managed hostname %s", managed)
		}
		if seen[current] {
			return chain, fmt.Errorf("edge Host CNAME loop at %s", current)
		}
		seen[current] = true
		chain = append(chain, current)
		next, exists, err := c.backend.LookupCNAME(ctx, current)
		if err != nil {
			return chain, fmt.Errorf("lookup CNAME for %s: %w", current, err)
		}
		if !exists {
			addresses, err := c.backend.LookupHost(ctx, current)
			if err != nil || len(addresses) == 0 {
				return chain, fmt.Errorf("edge Host %s does not resolve to an address", current)
			}
			return chain, nil
		}
		current, err = domain.NormalizeHostname(next)
		if err != nil {
			return chain, fmt.Errorf("invalid CNAME target: %w", err)
		}
	}
	return chain, fmt.Errorf("edge Host CNAME chain exceeds %d hops", maxHops)
}

type systemBackend struct {
	resolver *net.Resolver
}

func (b systemBackend) LookupNS(ctx context.Context, name string) ([]string, error) {
	values, err := b.resolver.LookupNS(ctx, name)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		result = append(result, item.Host)
	}
	return result, nil
}

func (b systemBackend) LookupCNAME(ctx context.Context, name string) (string, bool, error) {
	target, err := b.resolver.LookupCNAME(ctx, name)
	if err != nil {
		var dnsError *net.DNSError
		if errorsAs(err, &dnsError) && dnsError.IsNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	target = strings.TrimSuffix(strings.ToLower(target), ".")
	return target, target != "" && target != strings.TrimSuffix(strings.ToLower(name), "."), nil
}

func errorsAs(err error, target any) bool {
	// Local wrapper keeps the resolver implementation isolated for deterministic tests.
	return errors.As(err, target)
}

func (b systemBackend) LookupHost(ctx context.Context, name string) ([]string, error) {
	return b.resolver.LookupHost(ctx, name)
}

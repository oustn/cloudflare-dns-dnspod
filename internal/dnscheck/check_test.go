package dnscheck

import (
	"context"
	"fmt"
	"testing"
)

type fakeBackend struct {
	ns     map[string][]string
	cname  map[string]string
	addrs  map[string][]string
	errors map[string]error
}

func (f *fakeBackend) LookupNS(_ context.Context, name string) ([]string, error) {
	return f.ns[name], f.errors["ns:"+name]
}
func (f *fakeBackend) LookupCNAME(_ context.Context, name string) (string, bool, error) {
	value, ok := f.cname[name]
	return value, ok, f.errors["cname:"+name]
}
func (f *fakeBackend) LookupHost(_ context.Context, name string) ([]string, error) {
	return f.addrs[name], f.errors["host:"+name]
}

func TestDelegationReadyNormalizesNameservers(t *testing.T) {
	t.Parallel()
	checker := New(&fakeBackend{ns: map[string][]string{"custom.example.com": {"F1.NS.EXAMPLE.", "f2.ns.example."}}})
	observed, ready, err := checker.Delegation(context.Background(), "custom.example.com", []string{"f1.ns.example", "f2.ns.example"})
	if err != nil || !ready || len(observed) != 2 {
		t.Fatalf("observed=%v ready=%v err=%v", observed, ready, err)
	}
}

func TestCheckHostTargetFollowsChain(t *testing.T) {
	t.Parallel()
	checker := New(&fakeBackend{
		cname: map[string]string{"edge.example.com": "next.example.com"},
		addrs: map[string][]string{"next.example.com": {"1.1.1.1"}},
	})
	chain, err := checker.CheckHostTarget(context.Background(), "custom.example.com", "edge.example.com", 8)
	if err != nil || len(chain) != 2 || chain[1] != "next.example.com" {
		t.Fatalf("chain=%v err=%v", chain, err)
	}
}

func TestCheckHostTargetRejectsLoopAndSelfReference(t *testing.T) {
	t.Parallel()
	for name, backend := range map[string]*fakeBackend{
		"loop": {cname: map[string]string{"a.example.com": "b.example.com", "b.example.com": "a.example.com"}},
		"self": {cname: map[string]string{"a.example.com": "custom.example.com"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(backend).CheckHostTarget(context.Background(), "custom.example.com", "a.example.com", 8)
			if err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestCheckHostTargetRejectsUnresolved(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{errors: map[string]error{"host:edge.example.com": fmt.Errorf("not found")}}
	if _, err := New(backend).CheckHostTarget(context.Background(), "custom.example.com", "edge.example.com", 8); err == nil {
		t.Fatal("expected unresolved target rejection")
	}
}

func TestFallbackBackendUsesNextResolverAfterFailure(t *testing.T) {
	t.Parallel()
	first := &fakeBackend{errors: map[string]error{"ns:dns-test.example.com": fmt.Errorf("timeout")}}
	second := &fakeBackend{ns: map[string][]string{"dns-test.example.com": {"f1.dnspod.net", "f2.dnspod.net"}}}
	checker := New(&fallbackBackend{backends: []Backend{first, second}})
	observed, ready, err := checker.Delegation(
		context.Background(),
		"dns-test.example.com",
		[]string{"f1.dnspod.net", "f2.dnspod.net"},
	)
	if err != nil || !ready || len(observed) != 2 {
		t.Fatalf("observed=%v ready=%v err=%v", observed, ready, err)
	}
}

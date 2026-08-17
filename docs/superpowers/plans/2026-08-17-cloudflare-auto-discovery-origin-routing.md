# Cloudflare Auto-Discovery And Origin Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace static Cloudflare Zone/Fallback configuration with API discovery, add per-host Custom Origin onboarding and backend updates, and keep DNSPod preferred edge updates independent.

**Architecture:** Extend the existing explicit REST client instead of adding a Cloudflare SDK. Put provider-neutral hostname/target helpers in `internal/domain`, Zone selection in a focused `internal/workflow/discovery.go`, Custom Origin reconciliation in `internal/workflow/backend.go`, and retain the existing onboarding orchestration in `workflow.go` while replacing config-derived identities with discovered state.

**Tech Stack:** Go 1.25, standard `flag` and `net/http`, `httptest`, existing Tencent DNSPod SDK, existing provider interfaces, GitHub Actions, static cross-compilation.

---

## File Map

- Modify `internal/config/config.go`: minimal secrets plus optional `CF_ZONE`; add `set-backend` command.
- Modify `internal/config/config_test.go`: minimal requirements, override behavior, and redaction.
- Modify `internal/domain/domain.go`: proxied/custom-origin fields and hostname/target discovery helpers.
- Modify `internal/domain/domain_test.go`: parent selection, relative-name mapping, and target classification.
- Modify `internal/app/app.go`: positional FQDN parser, optional-value `--origin`, `--zone`, and `set-backend` dispatch.
- Modify `internal/app/app_test.go`: parser and early-validation coverage.
- Modify `internal/cloudflare/client.go`: Zone pagination, proxied DNS writes, Custom Origin create/edit, and response parsing.
- Modify `internal/cloudflare/client_test.go`: exact HTTP paths, methods, payloads, pagination, and parsed state.
- Create `internal/workflow/discovery.go`: Parent/SaaS/Fallback discovery and deterministic ambiguity handling.
- Create `internal/workflow/discovery_test.go`: selector precedence and candidate behavior.
- Create `internal/workflow/backend.go`: derived origin record planning and `set-backend` workflow.
- Create `internal/workflow/backend_test.go`: backend create/update/promotion/conflict/no-DNSPod-write tests.
- Modify `internal/workflow/workflow.go`: discovered identities, FQDN input, Custom Origin-aware `add`, adapted `set-edge`, and split status output.
- Modify `internal/workflow/workflow_test.go`: onboarding modes, edge isolation, recovery, and status tests.
- Modify `README.md`: minimal configuration and final command model.

### Task 1: Minimal Configuration And Domain Helpers

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/domain/domain.go`
- Modify: `internal/domain/domain_test.go`

- [ ] **Step 1: Write failing configuration and domain tests**

Add tests that require only the three secrets, normalize optional `CF_ZONE`, choose the longest proper parent Zone, derive the same relative name in another Zone, and classify Host/IPv4 targets:

```go
func TestFromValuesUsesMinimalConfiguration(t *testing.T) {
	values := map[string]string{
		"CF_API_TOKEN": "cf-secret", "CF_ZONE": "Platform.Example.Net.",
		"DNSPOD_SECRET_ID": "dns-id", "DNSPOD_SECRET_KEY": "dns-secret",
	}
	cfg, err := FromValues(values, CommandAdd)
	if err != nil || cfg.CFZone != "platform.example.net" || cfg.DNSPodRecordLine != "默认" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestDiscoverNames(t *testing.T) {
	zones := []Zone{
		{ID: "root", Name: "example.com", Status: "active"},
		{ID: "nested", Name: "eu.example.com", Status: "active"},
	}
	parent, err := FindParentZone("shop.eu.example.com", zones)
	if err != nil || parent.ID != "nested" {
		t.Fatalf("parent=%+v err=%v", parent, err)
	}
	origin, err := RebaseHostname("shop.eu.example.com", "eu.example.com", "platform.example.net")
	if err != nil || origin != "shop.platform.example.net" {
		t.Fatalf("origin=%q err=%v", origin, err)
	}
}
```

- [ ] **Step 2: Run focused tests and verify failure**

Run:

```bash
go test ./internal/config ./internal/domain
```

Expected: FAIL because `CFZone`, `FindParentZone`, `RebaseHostname`, and generic backend target classification do not exist and old Zone/Fallback variables are still required.

- [ ] **Step 3: Implement minimal configuration and helpers**

Replace the static Cloudflare identity fields with:

```go
type Config struct {
	CFAPIToken       string
	CFZone           string
	DNSPodSecretID   string
	DNSPodSecretKey  string
	DNSPodRecordLine string
}

const (
	CommandAdd        Command = "add"
	CommandSetEdge    Command = "set-edge"
	CommandSetBackend Command = "set-backend"
	CommandStatus     Command = "status"
)
```

Require only `CF_API_TOKEN`, `DNSPOD_SECRET_ID`, and `DNSPOD_SECRET_KEY`; normalize `CF_ZONE` only when present; default the DNSPod line to `默认`.

Add provider-neutral fields and helpers:

```go
type DNSRecord struct {
	ID, Name, Type, Content string
	Line                    string
	Managed                 bool
	Source                  string
	Proxied                 bool
}

type CustomHostname struct {
	ID, Hostname, Status, SSLStatus string
	CustomOriginServer              string
	ValidationRecords               []ValidationRecord
}

func FindParentZone(hostname string, zones []Zone) (Zone, error)
func RebaseHostname(hostname, parentZone, targetZone string) (string, error)
func ClassifyTarget(value, label string) (recordType, normalized string, err error)
```

`FindParentZone` accepts only active proper suffix Zones and chooses the longest label-boundary match. `ClassifyTarget` returns `A` for a globally routable IPv4 and `CNAME` for a normalized hostname.

- [ ] **Step 4: Run focused tests and verify pass**

Run `go test ./internal/config ./internal/domain`.

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config internal/domain
git commit -m "refactor: simplify provider configuration"
```

### Task 2: Positional CLI And Command Routing

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing parser tests**

Cover the final syntax and reject legacy or ambiguous forms:

```go
func TestParseAddPositionalHostnameAndOriginModes(t *testing.T) {
	for _, tc := range []struct {
		args         []string
		originSet    bool
		originValue  string
	}{
		{[]string{"add", "test.example.com"}, false, ""},
		{[]string{"add", "test.example.com", "--origin"}, true, ""},
		{[]string{"add", "test.example.com", "--origin=backend.example.org"}, true, "backend.example.org"},
	} {
		got, err := parse(tc.args, io.Discard)
		if err != nil || got.hostname != "test.example.com" || got.originSet != tc.originSet || got.origin != tc.originValue {
			t.Fatalf("args=%v got=%+v err=%v", tc.args, got, err)
		}
	}
}

func TestParseSetBackend(t *testing.T) {
	got, err := parse([]string{"set-backend", "test.example.com", "1.1.1.1", "--zone", "platform.example.net"}, io.Discard)
	if err != nil || got.backend != "1.1.1.1" || got.zone != "platform.example.net" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
```

Also test that `add --subdomain test`, `add test.example.com --origin backend.example.org`, a missing positional hostname, and a non-FQDN hostname return input errors before configuration is loaded.

- [ ] **Step 2: Run parser tests and verify failure**

Run `go test ./internal/app`.

Expected: FAIL because the parser still requires `--subdomain` and has no `set-backend` or optional-value origin flag.

- [ ] **Step 3: Implement parser and dispatch**

Use positional arguments before the command-specific `FlagSet`: `args[1]` for the managed hostname and `args[2]` for the `set-backend` target. Parse remaining options so documented options may follow positionals.

Implement a bool-compatible optional value:

```go
type originFlag struct {
	set   bool
	value string
}

func (o *originFlag) String() string { return o.value }
func (o *originFlag) IsBoolFlag() bool { return true }
func (o *originFlag) Set(value string) error {
	o.set = true
	if value == "true" {
		o.value = ""
		return nil
	}
	if value == "false" {
		return fmt.Errorf("--origin=false is not supported")
	}
	o.value = value
	return nil
}
```

Extend `parsed` with `hostname`, `zone`, `originSet`, `origin`, and `backend`. Normalize the FQDN, Zone, edge target, and backend target before loading configuration. Dispatch `set-backend` to `workflow.SetBackend`.

- [ ] **Step 4: Run parser tests and verify pass**

Run `go test ./internal/app`.

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app
git commit -m "feat: add positional hostname commands"
```

### Task 3: Cloudflare Discovery And Custom Origin API Surface

**Files:**
- Modify: `internal/cloudflare/client.go`
- Modify: `internal/cloudflare/client_test.go`

- [ ] **Step 1: Write failing REST client tests**

Add `httptest` cases for Zone pagination, proxied DNS create/update, parsed `proxied`, parsed `custom_origin_server`, Custom Hostname creation with/without a Custom Origin, and Custom Hostname patch:

```go
func TestCreateProxiedDNSRecord(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/zones/saas/dns_records" { t.Fatalf("%s %s", r.Method, r.URL.Path) }
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"r1","name":"test.platform.example.net","type":"CNAME","content":"backend.example.org","proxied":true}}`))
	}))
	defer server.Close()
	_, err := New("token", server.URL, server.Client()).CreateProxiedDNSRecord(context.Background(), "saas", "CNAME", "test.platform.example.net", "backend.example.org")
	if err != nil || body["proxied"] != true { t.Fatalf("body=%v err=%v", body, err) }
}

func TestCreateCustomHostnameIncludesOrigin(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/zones/saas/custom_hostnames" { t.Fatalf("%s %s", r.Method, r.URL.Path) }
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"h1","hostname":"test.example.com","status":"pending","custom_origin_server":"test.platform.example.net","ssl":{"status":"pending_validation"}}}`))
	}))
	defer server.Close()
	host, err := New("token", server.URL, server.Client()).CreateCustomHostname(context.Background(), "saas", "test.example.com", "test.platform.example.net")
	ssl, _ := body["ssl"].(map[string]any)
	if err != nil || host.CustomOriginServer != "test.platform.example.net" || body["custom_origin_server"] != "test.platform.example.net" || ssl["method"] != "txt" || ssl["type"] != "dv" {
		t.Fatalf("host=%+v body=%v err=%v", host, body, err)
	}
}
```

- [ ] **Step 2: Run client tests and verify failure**

Run `go test ./internal/cloudflare`.

Expected: FAIL because listing, proxied record writes, origin parsing, and patch methods do not exist.

- [ ] **Step 3: Implement the REST methods**

Extend the workflow-facing client with:

```go
func (c *Client) ListZones(ctx context.Context) ([]domain.Zone, error)
func (c *Client) CreateProxiedDNSRecord(ctx context.Context, zoneID, recordType, name, content string) (domain.DNSRecord, error)
func (c *Client) UpdateProxiedDNSRecord(ctx context.Context, zoneID, recordID, recordType, name, content string) (domain.DNSRecord, error)
func (c *Client) CreateCustomHostname(ctx context.Context, zoneID, hostname, customOrigin string) (*domain.CustomHostname, error)
func (c *Client) UpdateCustomHostnameOrigin(ctx context.Context, zoneID, hostnameID, customOrigin string) (*domain.CustomHostname, error)
```

`ListZones` requests active Zones in pages of 50 until a short page, normalizes every Zone, sorts by name, and rejects duplicate IDs/names. Proxied writes include `ttl: 1` and `proxied: true`. `parseCustomHostname` normalizes optional `custom_origin_server`.

- [ ] **Step 4: Run client tests and verify pass**

Run `go test ./internal/cloudflare`.

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cloudflare
git commit -m "feat: support Cloudflare origin routing APIs"
```

### Task 4: Parent And SaaS Zone Discovery

**Files:**
- Create: `internal/workflow/discovery.go`
- Create: `internal/workflow/discovery_test.go`
- Modify: `internal/workflow/workflow.go`

- [ ] **Step 1: Write failing discovery tests**

Use a focused fake Cloudflare service to cover selector precedence, existing Custom Hostname ownership, unique auto-discovery, exclusion of the parent, and multiple-candidate failure:

```go
func TestDiscoverUsesExplicitZoneBeforeConfiguredZone(t *testing.T) {
	cf := discoveryCF{
		zones: []domain.Zone{
			{ID: "parent", Name: "example.com", Status: "active"},
			{ID: "one", Name: "one.example.net", Status: "active"},
			{ID: "two", Name: "two.example.net", Status: "active"},
		},
		fallback: map[string]domain.FallbackOrigin{
			"one": {Origin: "fallback.one.example.net", Status: "active"},
			"two": {Origin: "fallback.two.example.net", Status: "active"},
		},
	}
	got, err := discoverInfrastructure(context.Background(), config.Config{CFZone: "one.example.net"}, "test.example.com", "two.example.net", &cf)
	if err != nil || got.SaaS.ID != "two" || got.Source != "command" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestDiscoverBlocksMultipleCandidates(t *testing.T) {
	cf := discoveryCF{
		zones: []domain.Zone{
			{ID: "parent", Name: "example.com", Status: "active"},
			{ID: "one", Name: "one.example.net", Status: "active"},
			{ID: "two", Name: "two.example.net", Status: "active"},
		},
		fallback: map[string]domain.FallbackOrigin{
			"one": {Origin: "fallback.one.example.net", Status: "active"},
			"two": {Origin: "fallback.two.example.net", Status: "active"},
		},
	}
	_, err := discoverInfrastructure(context.Background(), config.Config{}, "test.example.com", "", &cf)
	if err == nil || !strings.Contains(err.Error(), "one.example.net") || !strings.Contains(err.Error(), "two.example.net") {
		t.Fatalf("err=%v", err)
	}
	if cf.writeCalls != 0 { t.Fatalf("discovery performed %d writes", cf.writeCalls) }
}
```

- [ ] **Step 2: Run workflow discovery tests and verify failure**

Run `go test ./internal/workflow -run 'TestDiscover'`.

Expected: FAIL because discovery types and methods do not exist.

- [ ] **Step 3: Implement discovery**

Define:

```go
type Infrastructure struct {
	Parent     domain.Zone
	SaaS       domain.Zone
	Fallback   domain.FallbackOrigin
	Hostname   *domain.CustomHostname
	OriginName string
	Source     string
}

func discoverInfrastructure(ctx context.Context, cfg config.Config, hostname, zoneOverride string, cf Cloudflare) (Infrastructure, error)
```

Extend `Cloudflare` with `ListZones`. Discover the longest proper Parent Zone first. For SaaS selection, apply `--zone`, `CF_ZONE`, unique existing Custom Hostname ownership, then unique active Fallback candidate. Validate selected Zone identity/status/Fallback and derive `OriginName` with `domain.RebaseHostname`. Sort candidate names in ambiguity errors.

- [ ] **Step 4: Run discovery tests and verify pass**

Run `go test ./internal/workflow -run 'TestDiscover'`.

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/workflow/discovery.go internal/workflow/discovery_test.go internal/workflow/workflow.go
git commit -m "feat: discover Cloudflare routing zones"
```

### Task 5: Custom Origin-Aware Onboarding

**Files:**
- Modify: `internal/workflow/workflow.go`
- Modify: `internal/workflow/workflow_test.go`

- [ ] **Step 1: Write failing onboarding tests**

Adapt workflow fakes to the new Cloudflare interface and add mode coverage:

```go
func TestAddBareOriginCreatesFallbackBackedAlias(t *testing.T) {
	cf, dns, resolver := readyServices()
	result, err := Add(context.Background(), minimalConfig(), "custom.example.com", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, AddOptions{OriginSet: true})
	if err != nil { t.Fatal(err) }
	if !slices.Contains(cf.writes, "dns:CNAME:custom.platform.example.net:fallback.platform.example.net:proxied") {
		t.Fatalf("writes=%v result=%+v", cf.writes, result)
	}
	if !slices.Contains(cf.writes, "hostname:custom.example.com:custom.platform.example.net") {
		t.Fatalf("writes=%v", cf.writes)
	}
}

func TestAddFallbackModeOmitsCustomOrigin(t *testing.T) {
	cf, dns, resolver := readyServices()
	cf.host = nil
	_, err := Add(context.Background(), minimalConfig(), "custom.example.com", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, AddOptions{})
	if err != nil { t.Fatal(err) }
	for _, write := range cf.writes {
		if strings.HasPrefix(write, "origin-dns:") { t.Fatalf("unexpected origin write: %v", cf.writes) }
	}
	if !slices.Contains(cf.writes, "hostname:custom.example.com:") { t.Fatalf("writes=%v", cf.writes) }
}

func TestAddOriginMismatchBlocksBeforeWrites(t *testing.T) {
	cf, dns, resolver := readyServices()
	cf.host.CustomOriginServer = "other.platform.example.net"
	_, err := Add(context.Background(), minimalConfig(), "custom.example.com", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, AddOptions{OriginSet: true})
	if err == nil || !strings.Contains(err.Error(), "custom origin") { t.Fatalf("err=%v", err) }
	if len(cf.writes) != 0 || len(dns.writes) != 0 { t.Fatalf("cf=%v dns=%v", cf.writes, dns.writes) }
}
```

Retain tests for stale NS handling, wait/retry, validation TXT publication, and preserving an existing DNSPod edge record.

Replace the old static test configuration helper with:

```go
func minimalConfig() config.Config {
	return config.Config{
		CFAPIToken: "token", DNSPodSecretID: "id", DNSPodSecretKey: "key", DNSPodRecordLine: "默认",
	}
}
```

- [ ] **Step 2: Run onboarding tests and verify failure**

Run `go test ./internal/workflow -run 'TestAdd'`.

Expected: FAIL because `Add` still builds a hostname from static config and cannot reconcile Custom Origin records.

- [ ] **Step 3: Implement discovered onboarding**

Change `Add` to accept a normalized FQDN. Extend options:

```go
type AddOptions struct {
	Zone           string
	OriginSet      bool
	Origin         string
	Wait           bool
	DryRun         bool
	ReplaceStaleNS bool
	Timeout        time.Duration
	PollInterval   time.Duration
}
```

Replace `preflightIdentity` with `discoverInfrastructure`. Pass discovered Zone IDs into `ensureParentTXT` and `reconcileNS`. In origin mode, normalize an explicit target or use the literal placeholder `example.com`, classify exact origin records before any writes, create the proxied record before the Custom Hostname, and pass `Infrastructure.OriginName` to `CreateCustomHostname`. Validate an existing Custom Hostname's `CustomOriginServer` against the requested mode.

Use the discovered Fallback Host for `DNSPod.EnsureFallback`. Preserve the current interrupted-run behavior and report `origin_dns`, `custom_hostname`, `edge`, and `backend` state separately.

- [ ] **Step 4: Run onboarding and full workflow tests**

Run:

```bash
go test ./internal/workflow
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/workflow/workflow.go internal/workflow/workflow_test.go
git commit -m "feat: onboard fallback and custom origins"
```

### Task 6: Independent Edge And Backend Updates

**Files:**
- Create: `internal/workflow/backend.go`
- Create: `internal/workflow/backend_test.go`
- Modify: `internal/workflow/workflow.go`
- Modify: `internal/workflow/workflow_test.go`

- [ ] **Step 1: Write failing backend and edge-isolation tests**

Add tests for backend create/update/unchanged/promotion/conflict and prove provider write boundaries:

```go
func TestSetBackendPromotesFallbackHostname(t *testing.T) {
	cf, dns, resolver := readyServices()
	cf.host.CustomOriginServer = ""
	result, err := SetBackend(context.Background(), minimalConfig(), "custom.example.com", "backend.example.org", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, BackendOptions{})
	if err != nil { t.Fatal(err) }
	if !result.Wrote || len(dns.writes) != 0 {
		t.Fatalf("result=%+v DNSPod writes=%v", result, dns.writes)
	}
	if !slices.Contains(cf.writes, "hostname-origin:custom.platform.example.net") {
		t.Fatalf("Cloudflare writes=%v", cf.writes)
	}
}

func TestSetBackendConflictHasZeroWrites(t *testing.T) {
	cf, dns, resolver := readyServices()
	cf.records["custom.platform.example.net"] = []domain.DNSRecord{
		{ID: "a", Name: "custom.platform.example.net", Type: "A", Content: "1.1.1.1", Proxied: true},
		{ID: "c", Name: "custom.platform.example.net", Type: "CNAME", Content: "old.example.org", Proxied: true},
	}
	_, err := SetBackend(context.Background(), minimalConfig(), "custom.example.com", "backend.example.org", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, BackendOptions{})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") { t.Fatalf("err=%v", err) }
	if len(cf.writes) != 0 || len(dns.writes) != 0 { t.Fatalf("cf=%v dns=%v", cf.writes, dns.writes) }
}

func TestSetEdgeNeverWritesBackend(t *testing.T) {
	cf, dns, resolver := readyServices()
	_, err := SetEdge(context.Background(), minimalConfig(), "custom.example.com", EdgeTarget{Host: "preferred.example.net"}, Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver}, EdgeOptions{})
	if err != nil { t.Fatal(err) }
	if len(cf.writes) != 0 { t.Fatalf("Cloudflare writes=%v", cf.writes) }
	if len(dns.writes) != 1 || !strings.HasPrefix(dns.writes[0], "set-edge:") { t.Fatalf("DNSPod writes=%v", dns.writes) }
}
```

- [ ] **Step 2: Run focused tests and verify failure**

Run `go test ./internal/workflow -run 'TestSet(Backend|Edge)'`.

Expected: FAIL because `SetBackend` does not exist and `SetEdge` still uses static Zone identity.

- [ ] **Step 3: Implement backend reconciliation and adapt edge discovery**

Create:

```go
type BackendOptions struct {
	Zone   string
	DryRun bool
}

func SetBackend(ctx context.Context, cfg config.Config, hostname, target string, services Services, options BackendOptions) (domain.OperationResult, error)
```

`SetBackend` discovers infrastructure and requires an existing Custom Hostname. It validates target resolution/loops, classifies the derived origin record set, performs zero or one proxied DNS record create/update, then patches `custom_origin_server` only when needed. It never calls DNSPod.

Adapt `SetEdge` to accept a normalized FQDN and `Zone` in `EdgeOptions`. Use discovery to locate the existing Custom Hostname, but keep DNSPod as its only write provider.

- [ ] **Step 4: Run focused and full workflow tests**

Run:

```bash
go test ./internal/workflow
go test ./internal/app
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/workflow internal/app
git commit -m "feat: separate edge and backend updates"
```

### Task 7: Status, Documentation, And Release Verification

**Files:**
- Modify: `internal/workflow/workflow.go`
- Modify: `internal/workflow/workflow_test.go`
- Modify: `README.md`

- [ ] **Step 1: Write failing status tests**

Require separate `edge` and `backend` state:

```go
func TestStatusSeparatesEdgeAndBackend(t *testing.T) {
	cf, dns, resolver := readyServices()
	result, err := Status(context.Background(), minimalConfig(), "custom.example.com", "", Services{Cloudflare: cf, DNSPod: dns, Resolver: resolver})
	if err != nil { t.Fatal(err) }
	if result.Status["edge"] == nil || result.Status["backend"] == nil {
		t.Fatalf("status=%+v", result.Status)
	}
}
```

- [ ] **Step 2: Run status test and verify failure**

Run `go test ./internal/workflow -run TestStatus`.

Expected: FAIL because status still exposes a flat static-config snapshot.

- [ ] **Step 3: Implement status and update README**

Make status discover Parent/SaaS/Fallback state, inspect the derived origin record, and emit separate objects:

```go
status["edge"] = map[string]any{"provider": "dnspod", "records": apex}
status["backend"] = map[string]any{
	"mode": customOrFallback,
	"fallback_origin": infra.Fallback.Origin,
	"custom_origin_server": customOrigin,
	"records": originRecords,
}
```

Rewrite `README.md` with only public example domains, the three required secrets, optional `CF_ZONE`, positional hostname syntax, Zone ambiguity behavior, origin modes, `set-edge` versus `set-backend`, dry runs, and static builds.

- [ ] **Step 4: Run formatting and complete verification**

Run:

```bash
gofmt -w internal/app internal/cloudflare internal/config internal/domain internal/workflow
go test ./...
go test -race ./...
go vet ./...
make build
make smoke
git diff --check
```

Expected: every command exits `0`; smoke output lists static Linux amd64/arm64 ELF binaries and a macOS arm64 Mach-O binary.

- [ ] **Step 5: Scan public artifacts**

Run a repository scan excluding `.git`, `.env`, and build output. Confirm examples use only `example.com`, `example.net`, `example.org`, or documented public IPs, and confirm `.env` remains ignored and untracked.

- [ ] **Step 6: Commit**

```bash
git add README.md internal/workflow
git commit -m "docs: explain discovered edge and backend routing"
```

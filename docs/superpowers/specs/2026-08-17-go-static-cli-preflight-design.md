# Go Static CLI And Preflight Design

## Goal

Build a Go implementation of the Cloudflare for SaaS and DNSPod onboarding tool as a separate project under `go/`. The Go CLI must produce standalone binaries for Linux amd64, Linux arm64, and macOS arm64, keep the existing Python implementation unchanged, separate initial onboarding from later edge-target changes, and run read-only preflight checks before every provider write.

## Scope

The Go version covers the existing DNSPod child-zone and Cloudflare Custom Hostname workflow, including:

- DNSPod child-zone ownership validation through a TXT record in the Cloudflare parent zone;
- DNSPod child-zone creation, NS discovery, and explicit Zone enablement;
- Cloudflare parent-zone NS delegation;
- Cloudflare for SaaS Custom Hostname creation and TXT certificate validation;
- initial DNSPod apex CNAME to the configured Fallback Origin;
- later DNSPod apex switching to a selected edge Host or IPv4 address;
- status reporting, dry runs, resumable execution, conflict handling, and secret redaction.

The existing Python files and their current deliverable remain unchanged while the Go version is implemented and validated.

## Project Layout

```text
go/
  cmd/cf-dnspod/
    main.go
  internal/config/
  internal/domain/
  internal/preflight/
  internal/cloudflare/
  internal/dnspod/
  internal/dnscheck/
  internal/workflow/
  internal/output/
  Makefile
  go.mod
  go.sum
```

Responsibilities are separated as follows:

- `config`: read-only `.env` loading, environment override handling, and command-specific required-variable validation.
- `domain`: provider-neutral hostname, IP, Zone, record, validation, and result types.
- `preflight`: read-only checks and operation-specific eligibility decisions.
- `cloudflare`: Cloudflare REST calls for Zone identity, fallback-origin status, DNS records, and Custom Hostnames.
- `dnspod`: Tencent Cloud DNSPod SDK calls for Zone validation, creation, enablement, and record operations.
- `dnscheck`: pure-Go public DNS resolution and delegation/target checks.
- `workflow`: orchestration for `add`, `set-edge`, and `status` without embedding provider protocol details.
- `output`: stable JSON results and redacted errors.

## Dependencies

Use a deliberately small dependency set:

- Tencent Cloud's official Go DNSPod SDK for TC3 authentication and DNSPod models;
- Go `net/http` for the small Cloudflare API surface;
- a maintained dotenv parser for `.env` syntax;
- a pure-Go DNS package only if `net.Resolver` cannot provide the authoritative/delegation checks required by tests.

Do not implement Tencent TC3 signing manually. Do not use a broad Cloudflare SDK when the required REST surface is limited and explicit.

## Configuration

The binary looks for `.env` in the current working directory by default. It only reads the file and never creates, rewrites, moves, compares, or deletes it. Existing process environment variables override values loaded from `.env`.

The existing variable names remain valid:

```dotenv
CF_API_TOKEN=
CF_PARENT_ZONE_ID=
CF_PARENT_ZONE_NAME=
CF_SAAS_ZONE_ID=
CF_FALLBACK_HOST=
DNSPOD_SECRET_ID=
DNSPOD_SECRET_KEY=
DNSPOD_RECORD_LINE=默认
```

Configuration requirements are command-specific. `add` requires all Cloudflare, DNSPod, and fallback-origin values. `set-edge` requires the Cloudflare read credentials used by preflight plus DNSPod credentials, but its only provider write is to DNSPod. `status` does not require `CF_FALLBACK_HOST`.

## Commands

### `add`

```bash
cf-dnspod add --subdomain custom --wait
```

`add` owns initial onboarding and interrupted-run recovery. It does not accept an edge Host or edge IP.

After preflight succeeds, it converges the system in this order:

1. Reuse or validate and create the DNSPod child Zone.
2. Reconcile NS delegation in the Cloudflare parent Zone.
3. Enable the DNSPod Zone when it is not already enabled.
4. Optionally wait for public NS delegation.
5. Reuse or create the Cloudflare Custom Hostname.
6. Publish all ownership and certificate TXT records in DNSPod.
7. Create the DNSPod apex Fallback CNAME only when no apex A/AAAA/CNAME already exists.
8. Optionally wait until Custom Hostname and SSL status are both active.

An `add` rerun must preserve an existing apex edge A/CNAME. It must never switch an established edge target back to the Fallback Origin.

### `set-edge`

```bash
cf-dnspod set-edge --subdomain custom --host preferred.example.com
cf-dnspod set-edge --subdomain custom --ip 203.0.113.10
```

Exactly one of `--host` and `--ip` is required. `set-edge` performs read-only preflight checks and then makes at most one DNSPod apex-record write:

- identical record type and value: return `unchanged` without a write;
- one unambiguous different A/AAAA/CNAME: modify it once;
- no traffic record: create it once;
- multiple conflicting traffic records: stop without writing.

It must not create or modify Cloudflare records, DNSPod TXT records, DNSPod NS records, Custom Hostnames, fallback configuration, or certificate state.

### `status`

```bash
cf-dnspod status --subdomain custom
```

`status` is read-only and reports configuration identity, DNSPod Zone status, assigned and observed NS, Cloudflare parent records, Custom Hostname/SSL state, validation records, and DNSPod apex traffic records. Missing or incomplete onboarding state is reported rather than treated as a write-preflight failure.

## Common Input Validation

Every command validates local input before provider calls:

- `--subdomain` is a relative DNS name, not an FQDN, URL, wildcard, parent apex, port, or path;
- the resulting hostname is normalized and remains below `CF_PARENT_ZONE_NAME`;
- Host targets are valid normalized hostnames;
- IPv4 targets are syntactically valid;
- mutually exclusive and required options are enforced by the command parser;
- timeouts and polling intervals are non-negative.

Input/configuration failures exit with code `2` and perform no provider calls.

## `add` Preflight

Before the first write, `add` collects a read-only snapshot and evaluates all blockers:

1. Fetch the Cloudflare parent Zone and verify its ID resolves to `CF_PARENT_ZONE_NAME` and its status is active.
2. Fetch the Cloudflare SaaS Zone and verify it is accessible and active.
3. Fetch the singular fallback-origin resource and verify it is active and matches `CF_FALLBACK_HOST`.
4. List all Cloudflare parent-zone DNS records whose name exactly equals the candidate hostname.
5. Find the exact DNSPod Zone, if any, and collect its status and assigned NS.
6. Find the exact Cloudflare Custom Hostname, if any, and collect hostname/SSL status.
7. Classify the operation as new onboarding, resumable onboarding, or already converged.

The preflight blocks all writes when it finds:

- any exact-name A, AAAA, or CNAME in the Cloudflare parent Zone;
- a Workers-managed or application-managed exact-name record;
- any other incompatible exact-name record at the future delegation point;
- an existing delegation that belongs to another DNS provider;
- a Cloudflare Zone ID/name mismatch or inactive Zone;
- an absent/inactive/mismatched Fallback Origin;
- malformed or contradictory provider data.

TXT records used for ownership validation are allowed. Exact NS records are allowed only when they match the existing DNSPod Zone. Unexpected NS records remain blocked unless an explicit stale-NS replacement option is supplied. The option never authorizes deletion of A, AAAA, CNAME, Workers-managed, or application-managed records.

All blockers are returned together where practical so the user does not have to fix them one at a time. No provider write occurs until the entire preflight passes.

Provider APIs must still enforce conflicts at write time because state can change after preflight.

## `set-edge` Preflight

Before its sole possible write, `set-edge` verifies:

1. The exact DNSPod Zone exists and is enabled.
2. DNSPod has assigned at least two valid nameservers.
3. Public DNS delegation contains the assigned DNSPod nameservers.
4. The exact Cloudflare Custom Hostname exists and both hostname and SSL states are active.
5. A Host target resolves to at least one address. Its CNAME chain is followed with a bounded hop count and must not contain the managed hostname, revisit a previous name, or exceed the hop limit.
6. An IP target is a globally routable IPv4 address.
7. The current DNSPod apex traffic-record set is unambiguous.

These checks may read Cloudflare, DNSPod, and public DNS, but the only permitted write is the final DNSPod apex A/CNAME upsert.

## Dry Run And Idempotency

`add --dry-run` and `set-edge --dry-run` execute the same local and read-only provider checks, then report planned writes without changing state.

Repeated commands converge safely:

- existing exact resources are reused;
- identical records are unchanged;
- interrupted `add` resumes from provider state;
- `add` never overwrites an existing edge traffic record with fallback;
- `set-edge` never runs onboarding writes;
- validation TXT values are preserved when Cloudflare still reports them;
- no unexpected record is deleted without explicit, narrowly scoped authorization.

## Output And Errors

Successful commands print stable JSON. Results include:

- normalized hostname and requested operation;
- preflight checks and readiness;
- provider state relevant to the command;
- planned or completed changes;
- whether a write occurred;
- final Custom Hostname, SSL, delegation, and edge state when applicable.

Configuration/input failures use exit code `2`. Provider failures, preflight blockers, timeouts, and conflicts use exit code `1`. Successful or unchanged operations use exit code `0`.

Secrets, validation TXT values, authorization headers, SecretId, and SecretKey are never printed. Provider request IDs may be printed for troubleshooting.

## Testing

Tests use provider interfaces with deterministic fakes and do not read the real `.env` or call live services.

Coverage includes:

- `.env` precedence and command-specific requirements;
- input normalization and rejection;
- parent/SaaS Zone identity and fallback-origin validation;
- exact-name Cloudflare record conflict classification, including Workers-managed records;
- no writes when any preflight check fails;
- new and resumable `add` flows;
- DNSPod child validation, creation, NS reconciliation, and Zone enablement;
- preservation of an existing edge record during `add` reruns;
- `set-edge` unchanged/create/modify/conflict paths;
- proof that `set-edge` has no Cloudflare or onboarding writes;
- Host resolution, IPv4 routability, and direct-loop rejection;
- wait timeouts, dry runs, JSON output, exit codes, and redaction.

After fake-provider tests pass, live validation uses a disposable subdomain and explicitly supplied credentials. It verifies provider state and DNS/HTTPS behavior without modifying the existing Python project.

## Build And Release

The Makefile produces:

```text
dist/cf-dnspod-linux-amd64
dist/cf-dnspod-linux-arm64
dist/cf-dnspod-darwin-arm64
```

Linux builds use `CGO_ENABLED=0`, `-trimpath`, and stripped release flags, and are checked with `file`/`ldd` to confirm static linkage. The macOS arm64 build uses `CGO_ENABLED=0` and is a single Mach-O executable with no separate Go runtime or third-party dynamic-library deployment requirement; macOS system libraries remain platform-provided.

Release verification runs unit/integration tests, `go test -race` on the host architecture, `go vet`, all three builds, binary-format checks, CLI help smoke tests, and fake-provider end-to-end commands.

## Non-Goals

The first Go version does not:

- delete Workers Custom Domains or application-managed Cloudflare records;
- discover, benchmark, rotate, or monitor preferred edge Hosts/IPs;
- configure multiple fallback origins;
- add per-host `custom_origin_server` support;
- implement SNI rewrites;
- remove or modify the existing Python implementation;
- automatically delete DNSPod Zones, validation records, or unrelated DNS records.

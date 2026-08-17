# Cloudflare Auto-Discovery And Origin Routing Design

## Goal

Simplify the Cloudflare for SaaS and DNSPod CLI so that normal configuration requires only one Cloudflare API token and DNSPod credentials. The CLI discovers the Cloudflare parent Zone, SaaS Zone, and active Fallback Origin through read-only API calls. It keeps preferred edge routing and actual backend routing as separate operations.

The design preserves the existing safety requirements: every command validates its complete resource state before writing, repeated operations are idempotent, interrupted onboarding can resume, and local `.env` contents are never created, modified, moved, or deleted by the CLI.

## Configuration

The minimal configuration is:

```dotenv
CF_API_TOKEN=
DNSPOD_SECRET_ID=
DNSPOD_SECRET_KEY=
```

An optional default SaaS Zone name resolves ambiguity when a token can access multiple Cloudflare for SaaS Zones:

```dotenv
CF_ZONE=platform.example.net
```

`CF_ZONE` stores a Zone name, not a Zone ID. The CLI resolves and validates the current ID through the Cloudflare API. A command-level `--zone` value overrides `CF_ZONE`.

`DNSPOD_RECORD_LINE` is no longer required. The CLI uses `默认` when no process environment override is present. Existing obsolete Cloudflare Zone IDs, Zone names, or Fallback Host variables are not required for any command and are not used as authoritative state.

The CLI reads `.env` from the current working directory and allows process environment variables to override file values. It never writes the file or prints secret values.

## Commands

All managed hostnames are complete hostnames supplied as the first positional argument. Options may follow the hostname.

```text
cf-dnspod add HOSTNAME [--zone ZONE] [--origin[=TARGET]] [--wait] [--dry-run] [--replace-stale-ns]
cf-dnspod set-edge HOSTNAME (--host HOST | --ip IPV4) [--zone ZONE] [--dry-run]
cf-dnspod set-backend HOSTNAME TARGET [--zone ZONE] [--dry-run]
cf-dnspod status HOSTNAME [--zone ZONE]
```

### `add`

```bash
cf-dnspod add test.example.com
cf-dnspod add test.example.com --origin
cf-dnspod add test.example.com --origin=backend.example.org
cf-dnspod add test.example.com --origin=1.1.1.1
cf-dnspod add test.example.com --zone platform.example.net --wait
```

`add` performs first-time onboarding or resumes interrupted onboarding.

Origin modes are selected as follows:

- no `--origin`: create or reuse a Custom Hostname that uses the selected SaaS Zone's default Fallback Origin;
- bare `--origin`: create a same-relative-name Custom Origin DNS record whose target is the discovered Fallback Host, then set that hostname as `custom_origin_server`;
- `--origin=HOST`: create a proxied same-relative-name CNAME to the explicit Host;
- `--origin=IPV4`: create a proxied same-relative-name A record to the explicit globally routable IPv4 address.

An explicit origin value uses only `--origin=VALUE`. The separated form `--origin VALUE` is rejected because it is ambiguous with the positional managed hostname.

For `test.example.com` with parent Zone `example.com` and SaaS Zone `platform.example.net`, the same-relative-name Custom Origin is `test.platform.example.net`. For `shop.eu.example.com`, it is `shop.eu.platform.example.net`.

The DNSPod apex traffic record initially points to the discovered Fallback Host. An existing DNSPod A, AAAA, or CNAME traffic record is preserved so that rerunning `add` never removes an established preferred edge target.

### `set-edge`

```bash
cf-dnspod set-edge test.example.com --host preferred.example.net
cf-dnspod set-edge test.example.com --ip 1.1.1.1
```

`set-edge` manages the customer-facing DNSPod apex traffic record. It accepts exactly one of `--host` and `--ip` and makes at most one DNSPod traffic-record change after preflight.

It never creates or changes a Cloudflare Custom Origin record, `custom_origin_server`, Fallback Origin, Custom Hostname, validation record, or NS delegation.

### `set-backend`

```bash
cf-dnspod set-backend test.example.com backend.example.org
cf-dnspod set-backend test.example.com 1.1.1.1
cf-dnspod set-backend test.example.com backend.example.org --zone platform.example.net
```

`set-backend` manages the actual Cloudflare backend for one Custom Hostname. Its second positional argument is auto-classified as a Host or globally routable IPv4 address.

It creates or updates the same-relative-name proxied CNAME/A record in the selected SaaS Zone. When the Custom Hostname currently uses the default Fallback Origin, it also updates that Custom Hostname to use the same-relative-name record as `custom_origin_server`.

It never changes DNSPod traffic, NS, TXT, or Zone state. Switching an existing Custom Hostname back from Custom Origin mode to native Fallback mode is outside this change because the Cloudflare edit API does not expose an unambiguous nullable `custom_origin_server` contract.

### `status`

```bash
cf-dnspod status test.example.com
cf-dnspod status test.example.com --zone platform.example.net
```

`status` is read-only. It reports edge ingress and backend routing as separate objects:

```json
{
  "edge": {
    "provider": "dnspod",
    "type": "CNAME",
    "target": "preferred.example.net"
  },
  "backend": {
    "mode": "custom",
    "custom_origin_server": "test.platform.example.net",
    "type": "CNAME",
    "target": "backend.example.org",
    "proxied": true
  }
}
```

Fallback backend mode reports the discovered Fallback Host and does not claim that a per-host Custom Origin DNS record exists.

## Cloudflare Discovery

The CLI does not use `/user/tokens/verify` as a gate. It validates the token by calling the exact Zone, DNS, Fallback Origin, and Custom Hostname endpoints required by the requested operation. This accommodates tokens for which capability calls succeed even when the generic verification endpoint does not.

### Parent Zone

The CLI lists all active Zones available to the token and finds the longest label-boundary suffix of the managed hostname. For `test.example.com`, `example.com` is a valid parent; `ample.com` is not.

The selected parent must be a proper parent of the managed hostname. If the complete managed hostname is itself an active Cloudflare Zone, or no proper parent is accessible, preflight stops without writing. The parent Zone supplies the location for DNSPod ownership TXT records and child-Zone NS delegation.

### SaaS Zone

The SaaS Zone selection precedence is:

1. command-level `--zone`;
2. optional `CF_ZONE`;
3. the unique active Zone that already owns the exact Custom Hostname;
4. the unique remaining active Zone with an active Fallback Origin after excluding the parent Zone.

Every selected Zone is revalidated by ID, name, active status, active Fallback Origin, and required API capabilities. A selected SaaS Zone cannot be the parent Zone because a same-relative-name Custom Origin record would conflict with the customer hostname's NS delegation.

If discovery produces no candidate or multiple candidates, the command stops before writing. The error lists candidate Zone names and asks for `--zone` or `CF_ZONE`; API response ordering is never used to choose a Zone.

All pages of the Cloudflare Zone list are processed. Discovery results live only for the current command and are not written back to `.env` or another local state file.

## Resource Model

The workflow has two independent routing layers:

```text
customer hostname
  -> DNSPod apex A/CNAME             (edge ingress, managed by set-edge)
  -> Cloudflare edge and Custom Hostname
  -> Fallback Origin or Custom Origin (backend, managed by set-backend)
```

The DNSPod target determines how traffic reaches Cloudflare. The Custom Hostname's backend mode determines where Cloudflare sends the request after recognizing the original hostname. Updating one layer must not modify the other.

Custom Origin DNS records are exact, non-wildcard records in the selected SaaS Zone. They must be A, AAAA, or CNAME records accepted by Cloudflare for proxying, and the CLI always requests `proxied: true`. The API `custom_origin_server` value is always a hostname; explicit IP input is represented by an A record at the derived Custom Origin hostname.

## `add` Data Flow

Before the first write, `add` obtains one read-only snapshot covering all Cloudflare, DNSPod, and public DNS resources used by the workflow. It rejects every known blocker together where practical.

After preflight succeeds, it converges resources in this order:

1. Request or reuse DNSPod child-Zone ownership validation data.
2. Publish the required ownership TXT in the discovered Cloudflare parent Zone.
3. Create or reuse the DNSPod child Zone.
4. Reconcile the DNSPod nameservers as NS delegation records in the parent Zone.
5. Enable the DNSPod Zone and optionally wait for public delegation.
6. In Custom Origin mode, create or reuse the derived proxied origin A/CNAME in the SaaS Zone.
7. Create or reuse the exact Custom Hostname with the selected backend mode.
8. Publish Cloudflare ownership and certificate TXT validation records in DNSPod.
9. Create the DNSPod apex Fallback CNAME only when no existing traffic record is present.
10. Optionally wait for both Custom Hostname and SSL activation.

Provider operations are not transactional across Cloudflare and DNSPod. If a later step fails, successful earlier resources remain in place and are included in the result. A rerun reads current provider state and resumes without duplicating or reverting matching resources.

## Preflight And Conflict Rules

Every command completes local parsing, configuration validation, provider discovery, and operation-specific read checks before its first write.

Common blockers include:

- malformed hostname, URL, wildcard, port, path, or non-global IPv4 input;
- missing parent Zone or ambiguous SaaS Zone selection;
- inactive Zone or inactive/missing Fallback Origin;
- missing permission for an API required by the command;
- provider data whose Zone ID, Zone name, hostname, record, or status is contradictory;
- Workers-managed, application-managed, wildcard, or otherwise incompatible records;
- CNAME loops, targets that point back to the managed hostname or derived origin alias, and excessive CNAME depth.

`add` is conservative. Matching resources are reused, missing resources are created, and an existing incompatible backend mode or origin record stops the command with a direction to use `set-backend`. Existing preferred edge traffic remains untouched.

`set-edge` is authoritative only for one unambiguous DNSPod apex traffic record. Identical type/value is unchanged; when no traffic record exists, one is created; one different record is updated; multiple records, mixed lines, or provider-managed records block the command.

`set-backend` is authoritative only for one unambiguous derived Cloudflare origin record. Identical type/content/proxy state is unchanged. A single compatible different record is updated to the requested type, target, and `proxied: true`. Multiple A/AAAA/CNAME records, conflicting record types, or provider-managed records block the command rather than being deleted.

When `set-backend` promotes a Fallback-mode Custom Hostname, it first converges the derived DNS record and then patches `custom_origin_server`. If the patch fails, the unused origin record may remain; the result reports the partial change and a rerun resumes safely.

## Dry Run, Output, And Exit Codes

`add`, `set-edge`, and `set-backend` support `--dry-run`. Dry runs perform the same discovery and read-only preflight, then report ordered planned changes without provider writes.

Successful commands print stable JSON containing the selected parent and SaaS Zones, discovery source, edge state, backend state, checks, completed or planned changes, and whether any write occurred. Sensitive tokens, DNSPod secrets, authorization headers, and validation TXT values are redacted.

Exit codes remain:

- `0`: successful or already unchanged;
- `1`: discovery ambiguity, preflight blocker, provider error, conflict, or timeout;
- `2`: local command or configuration error.

## Testing

Tests use provider interfaces and local HTTP servers. They never read the real `.env`, call live providers, or depend on the user's Zone names.

Required coverage includes:

- complete-hostname positional parsing and options after the hostname;
- bare `--origin`, `--origin=HOST`, and `--origin=IPV4`, including rejection of the ambiguous separated-value form;
- longest proper parent-Zone discovery with pagination and label-boundary matching;
- SaaS selection precedence for `--zone`, `CF_ZONE`, existing Custom Hostname ownership, unique discovery, no candidates, and multiple candidates;
- capability checks that do not depend on `/user/tokens/verify`;
- fallback, default-target Custom Origin, Host Custom Origin, and IP Custom Origin onboarding;
- exact origin-alias derivation for one-label and multi-label relative names;
- Cloudflare DNS payloads with `proxied: true` and Custom Hostname payloads with the correct `custom_origin_server`;
- `set-edge` unchanged/create/update/conflict paths and proof that it never performs backend writes;
- `set-backend` unchanged/create/update/promotion/conflict paths and proof that it never performs DNSPod writes;
- interrupted `add` and `set-backend` recovery without duplicate resources;
- preservation of an existing DNSPod preferred edge record during `add`;
- malformed targets, non-global IPs, direct and indirect loops, managed records, and ambiguous state;
- status separation between `edge` and `backend`;
- dry runs, waits, redaction, JSON output, and exit codes.

Verification runs:

```bash
go test ./...
go test -race ./...
go vet ./...
make build
make smoke
```

The release workflow continues producing static Linux amd64, Linux arm64, and macOS arm64 binaries through the existing semantic-release pipeline.

## Non-Goals

This change does not:

- benchmark, discover, or rotate preferred Cloudflare edge IPs/Hosts;
- silently select the first SaaS Zone returned by Cloudflare;
- create or modify a Fallback Origin;
- implement wildcard Custom Origins;
- implement SNI rewrites or Origin Rules;
- automatically revert a Custom Hostname from Custom Origin mode to native Fallback mode;
- delete ambiguous, application-managed, Workers-managed, or unrelated DNS records;
- write discovered provider state back to `.env`.

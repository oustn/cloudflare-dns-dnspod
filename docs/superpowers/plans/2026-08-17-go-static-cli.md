# Go Static CLI Implementation Plan

> Execute directly in the current workspace. Do not read from tests or modify any real `.env`; do not modify the Python deliverable.

## Goal

Create a production-oriented Go CLI under `go/` that implements the approved Cloudflare for SaaS and DNSPod workflow with mandatory read-only preflight, separate onboarding and edge-update commands, deterministic JSON output, and standalone Linux amd64/Linux arm64/macOS arm64 binaries.

## 1. Module, domain types, and configuration

- Create `go/go.mod`, command entrypoint, package layout, and Makefile.
- Add table-driven tests for hostname/subdomain normalization, globally routable IPv4 validation, command-specific config requirements, dotenv precedence, and redaction.
- Implement provider-neutral Zone, DNS record, fallback, Custom Hostname, validation, preflight, action, and result types.
- Load `.env` read-only from the current directory when present; overlay process environment without mutating either source.

## 2. Cloudflare REST client

- Test with `httptest.Server`: API envelopes/errors, Zone identity, exact-name DNS record listing, fallback-origin parsing, Custom Hostname lookup/create/detail, TXT creation, and NS reconciliation.
- Implement a narrow `net/http` client with pagination, strict response validation, bearer authentication, timeouts, and no secret-bearing errors.
- Keep parent-Zone and SaaS-Zone responsibilities explicit even when their IDs are equal.

## 3. DNSPod SDK client

- Define the workflow-facing DNSPod interface before provider code.
- Test record filtering/upsert decisions and provider-model parsing separately from live calls.
- Implement exact Zone discovery, ownership-validation token/status, Zone creation/detail/enablement, record listing, TXT creation, and unambiguous apex A/CNAME create-or-modify with Tencent's official DNSPod SDK.

## 4. Public DNS checks

- Test NS normalization, delegation matching, Host CNAME traversal, loop/self-reference detection, hop bounds, no-address failures, and IP routability.
- Implement resolver interfaces using Go's resolver and a small pure-Go DNS dependency for bounded CNAME-chain inspection and authoritative-style NS queries.

## 5. Read-only preflight

- Build fake Cloudflare, DNSPod, and resolver providers with call logs.
- Test `add` classification for new/resumable/converged states and every hard blocker, including parent/SaaS Zone mismatch, fallback mismatch/inactivity, exact A/AAAA/CNAME, Workers-managed records, incompatible records, stale delegation, and malformed state.
- Test `set-edge` blockers for missing/disabled Zone, delegation mismatch, inactive Custom Hostname/SSL, invalid/unresolved/looping target, and ambiguous apex records.
- Assert zero writes whenever preflight fails and aggregate blockers where practical.

## 6. Workflows

- Test new, resumable, dry-run, and idempotent `add` flows.
- Implement child-Zone ownership TXT publication, Zone creation, NS delegation, Zone enablement, optional public delegation wait, Custom Hostname creation/reuse, validation TXT publication, fallback creation only when traffic records are absent, and optional activation wait.
- Test that rerunning `add` preserves every existing apex A/CNAME and never resets it to fallback.
- Test `set-edge` unchanged/create/modify/conflict and prove that at most one DNSPod traffic write occurs with no Cloudflare/onboarding writes.
- Implement read-only `status`, including incomplete states.

## 7. CLI and output

- Test argument validation, exit codes 0/1/2, JSON schemas, dry-run output, and secret redaction.
- Implement `add`, `set-edge`, and `status` flags with command-specific config validation and dependency construction.
- Keep validation TXT values out of command output while retaining record names and state.

## 8. Documentation, builds, and verification

- Add `go/README.md` documenting configuration, preflight gates, command separation, examples, and `.env` read-only behavior.
- Build Linux amd64, Linux arm64, and macOS arm64 with `CGO_ENABLED=0`, `-trimpath`, and stripped flags.
- Run `go test ./...`, `go test -race ./...`, `go vet ./...`, CLI help/input smoke tests, and fake-provider workflow tests.
- Inspect artifacts with `file`; verify Linux executables have no dynamic interpreter/dependencies where host tooling permits.


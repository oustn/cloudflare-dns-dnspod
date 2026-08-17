# Public Example Domain Cleanup Design

## Goal

Remove every private-domain example from the public project and replace it with
RFC-reserved `example.com` names without reading or changing the ignored root
`.env` file.

## Scope

- Replace private-domain literals in tracked documentation and Go tests.
- Preserve program behavior and test intent.
- Replace the public repository with a new repository at the same URL so old
  commits, tags, releases, branches, and pull-request references are removed.
- Recreate CI and the semantic release from the sanitized history.
- Verify the repository, Git history, release metadata, and release binaries.

## Approach

The working tree is sanitized first and validated with a case-insensitive
tracked-file scan plus the complete Go verification suite. A new orphan root
commit then disconnects the repository from every old Git object. The existing
GitHub repository is deleted only after the sanitized commit is ready and
verified, then recreated as a public repository with the same owner and name.

The root `.env` remains ignored and outside every scan, edit, staging command,
and commit.

## Verification

- No tracked file contains the private domain.
- Example hostnames use `example.com`; vendor and repository domains remain.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, and `make build` pass.
- The rebuilt GitHub repository has only the sanitized history.
- The replacement CI run succeeds and its release assets pass SHA-256 and
  binary-format checks.


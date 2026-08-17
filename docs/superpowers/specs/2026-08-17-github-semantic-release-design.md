# GitHub CI And Semantic Release Design

## Goal

Initialize the root Go project as a Git repository, add one GitHub Actions workflow that verifies and builds the CLI, use the official `go-semantic-release/action` to publish semantic versions, attach release binaries, and create/push the public repository `oustn/cloudflare-dns-dnspod` with `gh`.

## Repository Initialization

Initialize Git with `main` as the initial branch. Configure no repository-specific identity unless the existing global Git identity is missing. Before staging, verify `.env` is ignored. After staging, inspect the complete staged path list and explicitly assert that `.env` is absent.

The initial commit uses this Conventional Commit message:

```text
feat: initial Go implementation
```

This gives semantic-release a release-producing initial commit.

## Workflow

Create `.github/workflows/release.yml`. It runs for all branch pushes and pull requests, with two jobs.

### Verify Job

The `verify` job runs on `ubuntu-latest` and:

1. checks out the repository;
2. installs the Go version declared by `go.mod`;
3. runs `go test ./...`;
4. runs `go test -race ./...`;
5. runs `go vet ./...`;
6. runs `make build` to create Linux amd64, Linux arm64, and macOS arm64 binaries;
7. creates `dist/SHA256SUMS` from those three files;
8. uploads `dist/*` as a short-lived workflow artifact.

This job has read-only repository contents permission.

### Release Job

The `release` job depends on `verify` and runs only for a push to `main`. It:

1. checks out full Git history with `fetch-depth: 0`;
2. downloads the verified build artifact;
3. calls `go-semantic-release/action@v1` with `github-token` and `allow-initial-development-versions: true`;
4. when the action returns a non-empty version, uploads the three binaries and `SHA256SUMS` to release tag `v<version>` using the authenticated GitHub CLI.

The release job alone receives `contents: write`. Pull requests and non-main branches cannot create tags or releases.

The initial `feat:` commit produces development release `v0.1.0`. Later releases follow Conventional Commits. A push with no release-producing change passes verification and skips asset upload when the semantic action returns an empty version.

## GitHub Repository

Use the active authenticated `gh` account `oustn`. The target `oustn/cloudflare-dns-dnspod` is currently absent.

Create it as a public repository from the local source, add `origin`, and push `main`:

```bash
gh repo create oustn/cloudflare-dns-dnspod --public --source=. --remote=origin --push
```

Git operations use the configured SSH protocol. Do not upload `.env` or any secret value.

## Documentation

Update `README.md` so it no longer says a Python implementation coexists. Add concise GitHub Actions and release sections explaining:

- CI verifies every push and pull request;
- `main` uses Conventional Commits to publish semantic versions;
- release assets contain the three supported binaries and `SHA256SUMS`;
- `.env` is local-only and never committed.

## Verification

Before the remote is created:

- run unit tests, race tests, vet, and `make build` locally;
- validate workflow YAML syntax structurally;
- verify `.env` is ignored and absent from Git's index;
- inspect the initial commit and branch name.

After pushing:

- verify the remote repository is public and `main` is its default branch;
- follow the workflow run to completion with `gh run watch`;
- verify a GitHub Release exists with the expected semantic version;
- verify all three binaries and `SHA256SUMS` are attached;
- report the public repository and release URLs.


# Contributing to finemcp

Thanks for your interest in contributing!

## Development workflow

- Go version: see `go.mod` (currently Go 1.25.x).
- After cloning, run `make setup` to install git hooks and tools.
- Use `make verify` to check your local dev environment.
- Common targets: `make test`, `make lint`, `make ci`.

Before opening a PR:

1. Ensure `make ci` succeeds locally.
2. Keep changes focused and include tests for new behavior or bug fixes.

## Git hooks

Git hooks are installed by `make setup` into `.git/hooks`:

- `pre-commit`: runs `gofmt` on staged Go files, then `go vet`, `go build`, and short tests for only the affected packages.
- `commit-msg`: enforces Conventional Commits (`<type>[optional scope]: <description>`) with a minimum description length.
- `pre-push`: runs `make lint` and `make sec` before allowing a push.

You can re-run `make setup` at any time to reinstall or refresh the hooks.

## Commit and PR guidelines

- Use clear, descriptive commit messages.
- For feature work, prefer small, reviewable PRs.
- Include a short description of what changed and why.
- Mention any breaking changes explicitly in the description.

## Coding style

- Follow idiomatic Go style (`gofmt`, `go vet`).
- Prefer clarity over cleverness.
- Match existing patterns in this repository when adding new code.

## Reporting issues

- Use the GitHub issue tracker.
- Include Go version, OS, reproduction steps, and expected vs actual behavior.

## Security issues

Please **do not** open public issues for security bugs. Instead, follow the process described in `SECURITY.md`.

## Releasing

Releases are tagged from the `main` branch. Only maintainers cut releases.

1. **Update `CHANGELOG.md`** — move items from `## [Unreleased]` to a new version heading (`## [vX.Y.Z] — YYYY-MM-DD`).
2. **Create a signed tag**:
   ```bash
   git tag -s vX.Y.Z -m "vX.Y.Z"
   ```
3. **Push**:
   ```bash
   git push origin vX.Y.Z
   ```
4. **Verify** — the Go module proxy picks up the new version automatically. Confirm with `go list -m github.com/finemcp/finemcp@vX.Y.Z`.

Tag format follows [Semantic Versioning](https://semver.org/): `vMAJOR.MINOR.PATCH`. Pre-release versions use `-rc.N` suffix (e.g. `v0.1.0-rc.1`).

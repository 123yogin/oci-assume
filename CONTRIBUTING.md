# Contributing

Thanks for helping. Bug reports, docs fixes and pull requests are all welcome.

## Prerequisites

- Go, at the version in `go.mod`.
- Docker, for the shell integration tests (`make shell-test`).
- [golangci-lint](https://golangci-lint.run/) v2 (optional locally; CI runs it).

## Make targets

| Target | What it does |
|---|---|
| `make build` | Build `./oci-assume`. |
| `make test` | Run `go test ./... -race`. |
| `make shell-test` | Run the zsh, bash, fish and pwsh integration cases in containers against a fake `oci`. |
| `make install-test` | Test `install.sh` against a local fake release. |

Run the linter with `golangci-lint run` from the repo root. The config is in `.golangci.yml`.

## Pull requests

- Write commit messages as [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, `chore:`). The release notes are generated from them.
- Add or update tests with your change. Keep comments short.
- Keep stdout clean: on the assume path only shell-evaluable lines go to stdout; everything for humans goes to stderr.
- The pull request must pass CI (tests on macOS, Linux and Windows, lint, `govulncheck`, shell and install tests, and dependency review) before it can be merged.

By contributing you agree that your work is licensed under Apache-2.0, and you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

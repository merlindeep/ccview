# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Codex (OpenAI/ChatGPT) usage support.** `ccview` now reads the ChatGPT
  backend usage endpoint (`GET /backend-api/wham/usage`) and renders Codex's
  rolling and weekly rate-limit windows alongside Claude's. When credentials for
  both providers are present, both are shown, each in its own titled block.
- `--provider claude|codex|all` (default `all`, `-p`) to restrict which
  providers are shown.
- Codex OAuth token resolution from `CODEX_ACCESS_TOKEN` and
  `~/.codex/auth.json`, read-only, mirroring the Claude chain. Token expiry is
  derived from the access token's JWT `exp` claim.
- `--auto-reload-expired-token` now also refreshes an expired Codex token by
  running `codex login status` (override with `CCVIEW_CODEX_RELOAD_CMD`).

### Changed

- **JSON output is now a `providers` array** (`{"generated_at", "providers":
  [{"provider", "plan", "meters"}]}`) so one or several providers share a
  uniform shape. This is a breaking change for consumers of the previous
  single-provider `{"plan", "meters"}` document.
- The `table` mode now heads each provider with a title line instead of the
  standalone `Plan:` line.

## [0.1.1] - 2026-06-14

### Added

- `--debug` now prints a per-source credential breakdown (environment, macOS
  Keychain, credentials file), making a failed token lookup self-diagnosable.

### Documentation

- New "Requirements" section in the README: `ccview` needs a Claude Code CLI
  token; the Claude desktop app on its own is not sufficient (and why).

## [0.1.0] - 2026-06-14

### Added

- Console monitor for Claude usage limits backed by the OAuth usage endpoint.
- Output modes: `compact` (default), `table`, `json`, and `oneline`.
- Interactive `tui` dashboard (Bubble Tea) with manual refresh and quit keys.
- Configurable poll interval (`--interval`, default `1m`) with a legacy
  positional `interval-seconds` form, plus `--once` for a single snapshot.
- OAuth token resolution from `CLAUDE_CODE_OAUTH_TOKEN`, the macOS Keychain, and
  `~/.claude/.credentials.json`.
- Plan classification (Free, Pro, Max 5x/20x, Team, Enterprise) with adaptive
  rendering of whichever windows the endpoint returns.
- Exponential backoff on `HTTP 429`, and a warning when the interval is below
  the recommended floor.
- `--debug` diagnostics, `version` subcommand, and shell completions via Cobra.
- Full Go project packaging: Makefile, golangci-lint config, GitHub Actions CI,
  and a GoReleaser configuration that publishes binaries, checksums, and a
  Homebrew tap formula (`brew install merlindeep/tap/ccview`).

[Unreleased]: https://github.com/merlindeep/claude-cost-viewer/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/merlindeep/claude-cost-viewer/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/merlindeep/claude-cost-viewer/releases/tag/v0.1.0

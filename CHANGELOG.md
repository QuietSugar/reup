# Changelog

All notable changes to this project are documented following
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
[Semantic Versioning](https://semver.org/).

On every release: turn `[Unreleased]` into a dated version section and
keep a fresh empty `[Unreleased]` on top.

## [Unreleased]

## [0.0.2] - 2026-10-24

### Added

- The interactive picker exits on its own after 30 minutes without input, so a forgotten session browser no longer sits in the background forever; it says why on exit

### Fixed

- OpenCode provider memory blow-up on large session databases: the scanner no longer reads `opencode.db` (and its `-wal`) whole into memory. Pages are fetched on demand through a small LRU cache, table scans stream one row at a time, and an oversized `-wal` (past 256MB) falls back to the checkpointed snapshot. A 2.6GB database that peaked at ~2.9GB RSS now scans in ~55MB; a scan interrupted by a live writer's checkpoint retries once with a cold cache instead of failing

## [0.0.1] - 2026-10-18

### Added

- Cross-provider AI-CLI session browser for CodeBuddy, Kimi Code and OpenCode (DeepSeek Harness list-only), in one static binary — no Python / fzf / setup
- Interactive picker: provider tabs, workspace sidebar, full session preview, fuzzy filter, mouse support, adaptive panels (detail ≥100 cols, sidebar ≥140 cols) and context-aware key hints
- `reup list` batch views grouped by workspace with selectors: `--days`, `--date`, `--all`, `--project`, `--source`, `--limit`
- Session lifecycle: hide/ignore sessions with config-backed filtering; `reup hidden` list + `restore`; kimi-code managed through its official archive API (restorable); legacy resumer config auto-imported on first run
- `install.sh` bootstrap: arch detection, latest/tag resolution (`RESUMER_VERSION`), sha256 verification via `checksums.txt`, `GH_PROXY` mirror fallback
- Release pipeline: GoReleaser artifacts for macOS & Linux (arm64/amd64) + `checksums.txt`; CI across OS matrix with golangci-lint and `goreleaser check` gate

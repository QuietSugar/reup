[English](README.md) | [简体中文](README.zh-CN.md)

# reup

[![CI](https://github.com/QuietSugar/reup/actions/workflows/ci.yml/badge.svg)](https://github.com/QuietSugar/reup/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey)](#install)

> Browse, resume, and manage sessions from CodeBuddy, Kimi Code, and OpenCode (DeepSeek Harness list-only) — one picker, zero dependencies.

A single static binary. No Python, no fzf, no setup. Open the picker, see every
recent AI-CLI session across providers with a full preview, hit Enter, and you're
back in the conversation.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/QuietSugar/reup/master/install.sh | sh
```

If GitHub is unreachable, prefix the URL with a mirror and the script will
reuse that prefix for the assets it downloads (or set `GH_PROXY` explicitly):

```bash
curl -fsSL https://gh-proxy.com/https://raw.githubusercontent.com/QuietSugar/reup/master/install.sh | sh
```

or with Go:

```bash
go install github.com/QuietSugar/reup@latest
```

macOS & Linux (arm64/amd64). Windows is on the roadmap.

## Usage

```bash
reup              # interactive picker
reup list         # grouped by workspace, every session
reup --help       # everything else
```

Picker keys: `↑↓` browse · `/` filter · `tab` provider tabs · `w` sidebar ·
`o`/`v` toggle panels · `g` group mode · `d` hide/archive · `P` providers · `?` help ·
`enter` resume · `esc` cancel. The mouse works too: click a provider tab to
switch source, a sidebar workspace to filter, a session row to select it
(double-click resumes), and wheel-scroll the list. Enabling mouse tracking
means text selection needs Shift+drag. The last line of the screen
always shows the keys available in the current context. The session list
carries a one-line column header (`age` `agent` `title`); on terminals ≥100
columns a detail panel opens on the right, and ≥140 columns a
workspace/agents sidebar joins on the left — `o`/`v` toggle either panel.
The leading marker column (`sz`) is a rough conversation-weight bar, colored gray →
yellow → red in the picker: blank <20 messages, `▁` 20–49, `▄` 50–149, `█` 150+.

**Session management**: `d` on a selected session hides it.

- **kimi-code**: official archive through kimi's own API — restorable, and
  hidden in kimi's surfaces too. If `kimi web` is not running, reup starts
  it detached (no terminal, no browser), archives, and shuts it down again
  through its graceful `/api/v1/shutdown`; a server you started yourself is
  left alone and the modal points you at its web UI instead.
- **opencode / codebuddy**: hidden in reup only (neither exposes an
  official per-session hide/delete for stored history — codebuddy's
  `DELETE /api/v1/sessions/:id` is a silent no-op outside a live instance,
  verified against its sources). Files stay on disk, the agent keeps showing
  the session, and token statistics are unaffected.

`reup hidden` lists hidden sessions; `reup hidden restore SOURCE
SESSION_ID` shows one again.

**Migrating from resumer**: on first run, `~/.config/resumer/config.json`
(hidden sessions and provider toggles) and `tips.md` are copied to
`~/.config/reup/` automatically; the old directory is kept untouched. This
bridge exists for the rename's deprecation window and will be removed once
the installed base has moved.

Useful flags (both picker and `list`): `--days N` (narrow to the last N days —
**the default is no time limit, so every session is listed**), `--date YYYY-MM-DD`,
`--all` (kept for compatibility; it is now the default), `--project foo`,
`--source codebuddy|kimi-code|opencode|dsh`, `--limit N`. List mode adds
`--no-group` (flat table instead of workspace groups), `--json`, and `--full [N]`.
The `asst_count` JSON field and detail-preview
assistant activity figure are provider-specific rough size estimates; counting
rules differ and the values are not exact or directly comparable across providers.

The help popup (`?`) embeds provider-specific built-in tips by default. To
replace these with your own plain-text tips, create `~/.config/reup/tips.md`
or set `REUP_TIPS_FILE` to another file path. The file is read when the
picker starts; a missing or blank file falls back to the built-in tips.

## Workspaces

`reup list` and the picker group sessions by **workspace**: sessions opened in
the same working directory are grouped together, across providers. A workspace
header looks like:

```
── /home/xu/git-repo/my-project  ·  3 sessions  ·  codebuddy, kimi-code
```

The grouping key is the session's working directory, so a directory used from
several agents lands in one group. When a provider records a native
workspace/project identifier (Kimi Code's `wd_<slug>_<hash>` session bucket and
`workspaces.json`; OpenCode's `workspace_id`/`project_id`), it is used as the
group's identity — and as the last-resort key when no directory is known.
Sessions with neither a directory nor a native id are shown under
`(no workspace)`, scoped per provider.

`--json` stays a flat array (add the `workspace_id`/`workspace_root` fields
yourself if you need to group downstream). Use `reup list --no-group` for the
old flat table, or `--source NAME` to restrict to a single provider (no
cross-provider merging then).

## Enabling / disabling providers

Each provider can be turned off; a disabled provider is **not scanned or
parsed at all** — no storage walk, no file/db reads — until turned back on.

```bash
reup provider             # state table — toggle with P inside the picker
reup provider list        # same table, explicit
reup provider off opencode
reup provider on opencode
```

The state persists in `~/.config/reup/config.json` (`$REUP_CONFIG`
overrides the path; `$XDG_CONFIG_HOME` overrides its directory). Disabled
providers disappear from ambient scans and the picker's `tab` source cycle.
An explicit `reup list --source opencode` still works on a disabled
provider — the flag is a deliberate per-invocation request.

## Providers

| Provider | Session source | Resume command |
|---|---|---|
| CodeBuddy CLI | `~/.codebuddy/projects/**/*.jsonl` | `codebuddy --resume <id>` |
| Kimi Code CLI | `~/.kimi-code/sessions/<workDirKey>/<id>/` | `kimi --session <id>` |
| OpenCode | `~/.local/share/opencode/opencode.db` (v1.1+; v2 recommended) | `opencode --session <id>` |
| DeepSeek Harness (`dsh`) | `~/.dsh/sessions/<workDirKey>/<id>/session.v4.jsonl[.zstd]` | list-only — the harness exposes no resume entry point, Enter reports "no resume command" |

The committed opencode test database is regenerated with
`go run ./tools/opencodefixture tests/fixtures/opencode-home/opencode.db`.

For multi-agent format research, see [TokenTracker](https://github.com/xiufengsun/TokenTracker):
the inspected snapshot's README lists 42 supported AI tools, including CodeBuddy, and its
CodeBuddy notes describe `~/.codebuddy/projects/**/*.jsonl` session storage and
its Claude Code-derived layout (inspected snapshot: `daa6c55099d34458ff773f6c1edd777f16fb7693`).
reup only reads the session metadata needed for browsing and resuming.

CodeBuddy's storage format was cross-checked against TokenTracker and its CLI
resume reference. Kimi Code and OpenCode behavior was verified against
kimi-code **2.1.1** and opencode **1.18.33**/**2.0.32** sources; older releases
are best-effort only. For real-machine validation steps,
see [docs/real-world-testing.md](docs/real-world-testing.md).

Kimi leaves an opened-but-unused session directory behind (a `state.json` with
no title or prompt, and a `wire.jsonl` carrying only lifecycle events). reup
skips these, matching Kimi's own session list, so a directory you merely opened
Kimi in does not show up as a session.

reup also fixes a real-world annoyance: when a session's stored cwd has gone
stale (iCloud/Obsidian path drift), it re-derives the correct project directory
from the session file location, so `codebuddy --resume` actually works.

When a session's working directory has been deleted — the project was renamed,
moved, or removed, or the session data was carried to another machine — that
session cannot be resumed. reup replaces the project column with
`(deleted)` in both `reup list` and the picker, and refuses to launch the
agent CLI on Enter, printing the missing path and the command that unblocks it
instead of the CLI's own opaque `created under a different directory` error:

```
error: cannot resume [kimi-code] <session id> — its working directory has been deleted:
       /home/xu/git-repo/my-project
       the agent CLI cannot start there, so reup did not run the resume command.
       to resume this session, recreate the directory first:
         mkdir -p "/home/xu/git-repo/my-project"
       then run reup again and select this session.
```

<details>
<summary>Development</summary>

```bash
git clone https://github.com/QuietSugar/reup.git
cd reup
go build -o reup .
./tests/run-qa.sh   # go vet + full test suite (no external deps)
```

The test suite includes PTY-driven integration tests that exercise the real
TUI end to end (picker → filter → select → exec) against fixtures in
`tests/fixtures/`.

Releases are automated: pushing a `v*` tag builds binaries via goreleaser.

Roadmap: Windows support.

</details>

---

MIT © 2026 Jintae Song · QuietSugar — see [LICENSE](LICENSE).

# reup usage tutorial

English | [简体中文](tutorial.zh-CN.md)

reup finds local AI coding-agent sessions in one place and resumes them through each agent's native CLI. It supports Antigravity CLI (`agy`), CodeBuddy, Kimi Code, OpenCode, and DeepSeek Harness (`dsh`, list-only).

This guide is for day-to-day use. For development and real-agent validation, see [real-world-testing.md](real-world-testing.md).

## 1. Install and prepare

### Requirements

- macOS or Linux, arm64 / amd64
- At least one supported agent CLI
- Existing sessions created by that agent on this machine

`agy` is currently Linux-only. The other providers support both macOS and Linux.

### Install reup

Use the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/QuietSugar/reup/master/install.sh | sh
```

Or install with Go:

```bash
go install github.com/QuietSugar/reup@latest
```

Verify the installation:

```bash
reup --version
```

Run the same installation command again to upgrade.

### Check provider status

```bash
reup provider list
```

The important columns are:

- `PROVIDER`: the source name used by reup.
- `STATE`: whether the provider is enabled in reup's configuration.
- `STORAGE`: whether the CLI and session storage were detected; `missing` means the provider is currently unavailable.

If an agent is `missing`, check that its CLI is on `PATH` and that it has created a session. Detection requirements vary by provider, so installing the agent alone is not always enough.

## 2. Five-minute quick start

### Step 1: Open the picker

```bash
reup
```

Common controls:

- `↑` / `↓` or `j` / `k`: move the selection.
- `/`: enter a filter, then press Enter to apply it.
- `tab`: cycle between all sources and individual providers.
- `g`: switch between workspace groups and a flat list.
- `o` / `v`: toggle the detail and sidebar panels.
- `d`: hide a session; Kimi Code uses its official archive flow.
- `P`: manage provider switches.
- `?`: show keys and tips for the current provider.
- `enter`: resume the selected session.
- `esc`: cancel and exit.

The detail panel opens on terminals at least 100 columns wide; the workspace/agent sidebar joins at 140 columns.

### Step 2: List sessions without the picker

```bash
reup list
```

Sessions are grouped by workspace by default. For a flat table:

```bash
reup list --no-group
```

### Step 3: Resume a session

Select a session and press `enter`. reup will:

1. Change to the session's recorded working directory.
2. Run the agent's native resume command.
3. Return to the shell when the agent exits, printing a `cd <dir>` hint if the directory changed.

A child process cannot change the parent shell's directory, so use that hint when you want to change it yourself.

## 3. Common tasks

### Show one provider

```bash
reup list --source agy
reup list --source codebuddy
reup list --source kimi-code
reup list --source opencode
reup list --source dsh
```

The same option works with the picker:

```bash
reup --source agy
```

An explicit `--source` also works for a provider disabled in the configuration; it is a deliberate one-invocation override.

### Filter by project or time

```bash
# Case-insensitive project-name substring
reup list --project foo

# Sessions active in the last 7 days
reup list --days 7

# A specific date
reup list --date 2026-11-23

# No time limit
reup list --all
```

There is no time limit by default. `--all` is useful for clarity and compatibility with older usage.

Current limitation: `dsh` does not apply `--project`, `--days`, or `--date`; the other providers support these filters.

### Limit the number of listed sessions

```bash
reup list --limit 20
```

`--limit` currently applies only to `list` mode; the picker does not use it.

### Show details or export JSON

```bash
# Detailed cards for the first 5 sessions
reup list --full

# Detailed cards for the first 10
reup list --full 10

# JSON output
reup list --json
```

JSON is a flat array containing fields such as `source`, `session_id`, `cwd`, `workspace_id`, `workspace_root`, timestamps, title, prompts, and `resume_argv`.

### Hide and restore a session

Select a session in the picker, press `d`, and follow the confirmation prompt.

List hidden sessions:

```bash
reup hidden
```

Restore one:

```bash
reup hidden restore SOURCE SESSION_ID
```

For example:

```bash
reup hidden restore agy 00000000-0000-0000-0000-000000000000
```

This hidden list belongs to reup's configuration; it does not delete the agent's session files.

### Disable or re-enable a provider

```bash
reup provider off opencode
reup provider on opencode
```

A disabled provider is not read during ambient scans or by the picker.

## 4. Provider-specific behavior

| Provider | Resume mechanism | What `d` does |
|---|---|---|
| Antigravity CLI (`agy`, Linux only) | `agy --conversation <id>` | Hides in reup only |
| CodeBuddy | `codebuddy --resume <id>` | Hides in reup only |
| Kimi Code | `kimi --session <id>` | Archives through Kimi's official API; restorable |
| OpenCode | `opencode --session <id>` | Hides in reup only; real deletion uses OpenCode's own command |
| DeepSeek Harness (`dsh`) | List-only; reup cannot resume these sessions | Hides in reup only |

Kimi Code has special server rules:

- If `kimi web` is not running, confirming an archive starts it with `kimi web --no-open`. After a successful archive, reup stops the spawned server through the official shutdown API.
- If a server is already running, reup does not operate on that instance. The modal shows the Kimi Web UI address so you can manage the session yourself.
- Automatic startup only happens after you confirm an archive; ordinary browsing and resume do not start the server.

To delete an OpenCode session for real, use its official command:

```bash
opencode session delete <sessionID>
```

Deletion changes OpenCode's session data. Hiding only changes what reup displays.

## 5. Workspaces and working directories

reup groups by working directory first, so sessions created by different agents in the same directory land together. A provider-native workspace/project ID is a fallback when no directory is known.

### The working directory was deleted

If a project was moved, renamed, or removed, its rows show `(deleted)`. On `enter`, reup refuses to launch the agent and prints the missing path.

If you intend to restore the original directory, follow the printed hint:

```bash
mkdir -p "/path/from/the/error/message"
```

If the project has moved, restore the original path recorded by the session or use the agent's official migration mechanism. Do not edit the agent's storage files directly.

### CodeBuddy path drift

When a recorded path has drifted in environments such as iCloud or Obsidian but the session file location remains valid, reup tries to derive the correct cwd from CodeBuddy's project-directory name.

## 6. Configuration, privacy, and customization

### Configuration file

Default location:

```text
~/.config/reup/config.json
```

It stores provider switches and reup's hidden list. You can change its location with:

- `REUP_CONFIG`: override the complete config-file path.
- `XDG_CONFIG_HOME`: override the configuration root.

On first run, reup copies an existing `~/.config/resumer/config.json` to the new location. Setting `REUP_CONFIG` explicitly disables this migration.

### Custom help tips

Create:

```text
~/.config/reup/tips.md
```

Or set:

```bash
export REUP_TIPS_FILE=/path/to/tips.md
```

A custom file replaces the built-in provider tips in the picker. A missing or blank file falls back to the built-in tips.

### Storage overrides

For tests and non-standard installations:

| Variable | Location overridden |
|---|---|
| `REUP_AGY_HOME` | `~/.gemini/antigravity-cli` |
| `REUP_CODEBUDDY_HOME` | `~/.codebuddy` |
| `REUP_KIMI_HOME` | `~/.kimi-code` |
| `REUP_OPENCODE_DATA` | OpenCode data directory |
| `REUP_DSH_HOME` | `~/.dsh` |

OpenCode also honors `XDG_DATA_HOME`.

### Data-access boundaries

- Ordinary scans only read existing agent session metadata. reup does not install hooks or upload session contents.
- Hiding in reup changes only reup's configuration; it never edits agent storage.
- Kimi archiving is the only agent mutation initiated by reup, and it requires confirmation in the modal.
- Once a session is resumed, subsequent behavior belongs to the corresponding agent CLI.

## 7. Troubleshooting

### “No session providers available”

Start with:

```bash
reup provider list
agy --help                 # Antigravity CLI
codebuddy --help           # CodeBuddy
kimi --help                # Kimi Code
opencode --help            # OpenCode
```

Check that the CLI is on `PATH`, its session storage exists, and it has produced at least one usable session. Re-enable a disabled provider with:

```bash
reup provider on NAME
```

### The list is empty or a session is missing

Remove source and time restrictions first:

```bash
reup list --all
reup list --source NAME --all
```

Then check:

- Whether `--project`, `--days`, or `--date` is too narrow.
- Whether the session is hidden in `reup hidden`.
- Whether the provider is disabled.
- Empty Kimi sessions that were opened but never used are skipped.
- Sessions archived or deleted by the agent do not reappear.

### Resume says the agent command was not found

Install the corresponding CLI and make sure it is on the current shell's `PATH`. After reopening the shell, check:

```bash
command -v agy
command -v codebuddy
command -v kimi
command -v opencode
```

### A `dsh` session says “no resume command”

This is expected: DeepSeek Harness is currently list-only in reup.

### Kimi says a server is already running

Open the Web UI address shown in the modal and manage the session in Kimi's own interface. reup does not operate on a server instance you already started.

### Further diagnosis

Compare the plain list, JSON, and a single-provider query:

```bash
reup list --all --no-group
reup list --all --json
reup list --source NAME --all
```

For fixtures, mock agents, and real-agent validation, see [real-world-testing.md](real-world-testing.md).

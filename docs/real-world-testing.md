# Real-world testing notes

Manual checks against the real agents — things the synthetic fixtures and
PTY tests cannot cover. Run after upgrading an agent CLI or changing a
provider.

## Environment overrides

| var | purpose |
|---|---|
| `REUP_AGY_HOME` / `REUP_AGY_BIN` | Linux only: redirect `~/.gemini/antigravity-cli` / binary |
| `REUP_CODEBUDDY_HOME` | redirect `~/.codebuddy` |
| `REUP_KIMI_HOME` / `REUP_KIMI_BIN` | redirect `~/.kimi-code` / binary |
| `REUP_OPENCODE_DATA` / `REUP_OPENCODE_BIN` | redirect opencode data / binary |
| `REUP_DSH_HOME` | redirect `~/.dsh` |
| `REUP_CONFIG` | redirect the config file (disables legacy migration) |

The default locations are always the real ones.

## Known limitations (by design)

- the shared SQLite reader replays committed `-wal` frames, but does not load
  a WAL past 256 MiB; in that case reads fall back to the last checkpointed
  main-database snapshot.
- codebuddy's REST API only manages sessions opened in a live instance;
  storage-history DELETE is a silent 204 no-op there,
  which is why reup hides codebuddy sessions config-side instead.
- kimi archive requires a running `kimi web`; reup starts it detached and
  stops it afterwards when the user confirms.

## Manual checks

- Hide flows (d → Enter): opencode/codebuddy/agy/dsh write a config key and the
  row disappears with no agent call; kimi archives through its API.
- `reup hidden` lists; `restore SOURCE SESSION_ID` unhides.
- Resume: agent starts in the session's directory (mock-bin logs assert
  `pwd=`); after exit a `cd <dir>` hint is printed.
- Config migration: with `~/.config/resumer/` present and no
  `~/.config/reup` config, the first run copies it and prints a note.

Regression pins live in the Go tests (`internal/provider/*/*_test.go`,
`internal/tui/mutate_test.go`, `tests/integration/`); this file only holds
what cannot run unattended.

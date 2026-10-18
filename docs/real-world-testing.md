# Real-world testing notes

Manual checks against the real agents — things the synthetic fixtures and
PTY tests cannot cover. Run after upgrading an agent CLI or changing a
provider.

## Environment overrides

| var | purpose |
|---|---|
| `REUP_CODEBUDDY_HOME` | redirect `~/.codebuddy` |
| `REUP_KIMI_HOME` / `REUP_KIMI_BIN` | redirect `~/.kimi-code` / binary |
| `REUP_OPENCODE_DATA` / `REUP_OPENCODE_BIN` | redirect opencode data / binary |
| `REUP_DSH_HOME` | redirect `~/.dsh` |
| `REUP_CONFIG` | redirect the config file (disables legacy migration) |

The default locations are always the real ones.

## Known limitations (by design)

- opencode's SQLite reader does not replay the `-wal` file: sessions the
  opencode service has not checkpointed yet are invisible until then.
- codebuddy's REST API only manages sessions opened in a live instance;
  storage-history DELETE is a silent 204 no-op there,
  which is why reup hides codebuddy sessions config-side instead.
- kimi archive requires a running `kimi web`; reup starts it detached and
  stops it afterwards when the user confirms.

## Manual checks

- Hide flows (d → Enter): opencode/codebuddy write a config key and the row
  disappears with no agent call; kimi archives through its API.
- `reup hidden` lists; `restore SOURCE SESSION_ID` unhides.
- Resume: agent starts in the session's directory (mock-bin logs assert
  `pwd=`); after exit a `cd <dir>` hint is printed.
- Config migration: with `~/.config/resumer/` present and no
  `~/.config/reup` config, the first run copies it and prints a note.

Regression pins live in the Go tests (`internal/provider/*/*_test.go`,
`internal/tui/mutate_test.go`, `tests/integration/`); this file only holds
what cannot run unattended.

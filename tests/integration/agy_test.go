package integration

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestAgySelectExec(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("agy is not supported on macOS")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE conversation_summaries (
		conversation_id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT "",
		preview TEXT NOT NULL DEFAULT "",
		last_modified_time TEXT NOT NULL,
		workspace_uris TEXT NOT NULL,
		project_id TEXT NOT NULL DEFAULT "",
		nesting_depth INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	workspaceJSON, _ := json.Marshal([]string{"file://" + workspace})
	if _, err := db.Exec(`INSERT INTO conversation_summaries
		(conversation_id, title, preview, last_modified_time, workspace_uris, project_id, nesting_depth)
		VALUES (?, ?, ?, ?, ?, ?, 0)`, "agy-integration-001", "AGY Integration",
		"agy integration prompt", "2026-04-15 05:05:00+00:00", string(workspaceJSON), "agy-integration-project"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	transcriptDir := filepath.Join(root, "brain", "agy-integration-001", ".system_generated", "logs")
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"USER_INPUT","created_at":"2026-04-15T05:00:00Z","content":"agy integration prompt"}` + "\n"
	if err := os.WriteFile(filepath.Join(transcriptDir, "transcript.jsonl"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(t.TempDir(), "agy-mock.log")
	env := append(fixtureEnv(t),
		"REUP_AGY_HOME="+root,
		"REUP_AGY_BIN=agy",
		"AGY_MOCK_LOG="+logPath,
	)
	r := startPicker(t, env, "--source=agy", "--all")
	r.waitFor(t, "AGY Integration", 5*time.Second)
	r.send("\r")
	r.waitExit(t, 5*time.Second)

	log := waitForFile(t, logPath, 5*time.Second)
	if !strings.Contains(log, "args=--conversation agy-integration-001") {
		t.Errorf("expected agy --conversation with session ID, log: %q", log)
	}
	if !strings.Contains(log, "pwd="+workspace) {
		t.Errorf("expected exec from recorded workspace %q, log: %q", workspace, log)
	}
}

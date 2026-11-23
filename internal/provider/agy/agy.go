// Package agy reads Antigravity CLI conversation summaries and transcripts
// from ~/.gemini/antigravity-cli. Resume: agy --conversation <id>.
package agy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuietSugar/reup/internal/brand"
	"github.com/QuietSugar/reup/internal/session"
	"github.com/QuietSugar/reup/internal/sqliteread"
	"github.com/QuietSugar/reup/internal/textutil"
)

const maxLineBytes = 10 << 20

var (
	envHome = brand.Env("AGY_HOME")
	envBin  = brand.Env("AGY_BIN")
)

func home() string {
	if root := os.Getenv(envHome); root != "" {
		return root
	}
	base, _ := os.UserHomeDir()
	return filepath.Join(base, ".gemini", "antigravity-cli")
}

func dbPath() string {
	path := filepath.Join(home(), "conversation_summaries.db")
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	return path
}

func transcriptPath(id string) string {
	if id == "" || filepath.Base(id) != id {
		return ""
	}
	return filepath.Join(home(), "brain", id, ".system_generated", "logs", "transcript.jsonl")
}

func agyBin() string {
	if bin := os.Getenv(envBin); bin != "" {
		return bin
	}
	return "agy"
}

// Provider lists Antigravity CLI sessions.
type Provider struct{}

func New() *Provider                  { return &Provider{} }
func (p *Provider) Name() string      { return "agy" }
func (p *Provider) Badge() string     { return "agy" }
func (p *Provider) BadgeANSI() string { return "\x1b[33m" }
func (p *Provider) IsAvailable() bool {
	_, err := exec.LookPath(agyBin())
	return err == nil && dbPath() != ""
}

func parseTime(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.Replace(raw, " ", "T", 1)
	t, ok := textutil.ParseISO(raw)
	if !ok || t.Year() <= 1 {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func workspaceDir(raw string) string {
	var uris []string
	if json.Unmarshal([]byte(raw), &uris) != nil {
		return ""
	}
	for _, value := range uris {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.Path == "" {
			continue
		}
		return u.Path
	}
	return ""
}

func projectLabel(dir string) string {
	if dir == "" {
		return "(unknown)"
	}
	if base := filepath.Base(strings.TrimRight(dir, "/")); base != "" && base != "/" && base != "." {
		return base
	}
	return "(unknown)"
}

func internalConversations() map[string]bool {
	data, err := os.ReadFile(filepath.Join(home(), "cache", "conversation_metadata.json"))
	if err != nil {
		return nil
	}
	var metadata struct {
		Conversations map[string]struct {
			IsInternal bool `json:"is_internal"`
		} `json:"conversations"`
	}
	if json.Unmarshal(data, &metadata) != nil {
		return nil
	}
	out := map[string]bool{}
	for id, item := range metadata.Conversations {
		if item.IsInternal {
			out[id] = true
		}
	}
	return out
}

type transcriptMeta struct {
	firstTS string
	lastTS  string
	first   string
	last    string
	prompts []session.Prompt
	asst    int
}

func cleanUserInput(content string) string {
	const settingsOpen = "<USER_SETTINGS_CHANGE>"
	const settingsClose = "</USER_SETTINGS_CHANGE>"
	for {
		start := strings.Index(content, settingsOpen)
		end := strings.Index(content, settingsClose)
		if start < 0 || end < start {
			break
		}
		content = strings.TrimSpace(content[:start] + "\n" + content[end+len(settingsClose):])
	}
	if start := strings.Index(content, "<USER_REQUEST>"); start >= 0 {
		rest := content[start+len("<USER_REQUEST>"):]
		if end := strings.Index(rest, "</USER_REQUEST>"); end >= 0 {
			content = rest[:end]
		}
	}
	lines := strings.Split(content, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(line, "changed setting `") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func parseTranscript(path string, detail bool) transcriptMeta {
	var meta transcriptMeta
	if path == "" {
		return meta
	}
	f, err := os.Open(path)
	if err != nil {
		return meta
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for sc.Scan() {
		var event struct {
			Type      string `json:"type"`
			CreatedAt string `json:"created_at"`
			Content   string `json:"content"`
		}
		if json.Unmarshal(sc.Bytes(), &event) != nil {
			continue
		}
		ts := parseTime(event.CreatedAt)
		if ts != "" {
			if meta.firstTS == "" || ts < meta.firstTS {
				meta.firstTS = ts
			}
			if ts > meta.lastTS {
				meta.lastTS = ts
			}
		}
		switch event.Type {
		case "USER_INPUT":
			text := cleanUserInput(event.Content)
			if text == "" {
				continue
			}
			if meta.first == "" {
				meta.first = text
			}
			meta.last = text
			if detail {
				meta.prompts = append(meta.prompts, session.Prompt{TS: ts, Text: text})
			}
		case "PLANNER_RESPONSE":
			meta.asst++
		}
	}
	return meta
}

func (p *Provider) listRaw(targetID string, detail bool) ([]session.Session, error) {
	path := dbPath()
	if path == "" {
		return nil, nil
	}
	db, err := sqliteread.Open(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tbl, err := db.Table("conversation_summaries")
	if err != nil {
		return nil, err
	}
	internal := internalConversations()
	var out []session.Session
	err = tbl.Scan(func(r sqliteread.Row) error {
		id, _ := r.Str("conversation_id")
		depth, _ := r.Int("nesting_depth")
		if id == "" || (targetID != "" && id != targetID) || depth != 0 || internal[id] {
			return nil
		}
		workspaceRaw, _ := r.Str("workspace_uris")
		cwd := workspaceDir(workspaceRaw)
		workspaceID, _ := r.Str("project_id")
		title, _ := r.Str("title")
		preview, _ := r.Str("preview")
		lastModified, _ := r.Str("last_modified_time")
		transcript := transcriptPath(id)
		meta := parseTranscript(transcript, detail)

		firstPrompt := meta.first
		if firstPrompt == "" {
			firstPrompt = strings.TrimSpace(preview)
		}
		lastPrompt := meta.last
		if lastPrompt == "" {
			lastPrompt = firstPrompt
		}
		title = strings.TrimSpace(title)
		if title == "" {
			title = firstPrompt
		}
		lastTS := parseTime(lastModified)
		if lastTS == "" {
			lastTS = meta.lastTS
		}
		sessionPath := transcript
		if sessionPath == "" {
			sessionPath = fmt.Sprintf("%s#conversation/%s", path, id)
		}
		out = append(out, session.Session{
			Source:        "agy",
			SessionID:     id,
			Path:          sessionPath,
			ProjectLabel:  projectLabel(cwd),
			Cwd:           cwd,
			WorkspaceID:   workspaceID,
			WorkspaceRoot: cwd,
			FirstTS:       meta.firstTS,
			LastTS:        lastTS,
			Title:         title,
			FirstPrompt:   firstPrompt,
			LastPrompt:    lastPrompt,
			Prompts:       meta.prompts,
			AsstCount:     meta.asst,
			ResumeArgv:    []string{"agy", "--conversation", id},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func touchesDate(s *session.Session, day time.Time) bool {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	end := start.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	first, okFirst := textutil.ParseISO(s.FirstTS)
	last, okLast := textutil.ParseISO(s.LastTS)
	if !okFirst && !okLast {
		return false
	}
	if !okFirst {
		first = last
	}
	if !okLast {
		last = first
	}
	return !(last.Before(start) || first.After(end))
}

func cutoffForFilters(f session.Filters) *time.Time {
	if f.AllTime || f.Date != "" || f.Days == 0 {
		return nil
	}
	days := f.Days
	if days < 0 {
		days = 3
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	cutoff := midnight.AddDate(0, 0, -days)
	return &cutoff
}

func (p *Provider) ListSessions(f session.Filters) ([]session.Session, error) {
	cutoff := cutoffForFilters(f)
	var day *time.Time
	if f.Date != "" {
		if parsed, err := time.ParseInLocation("2006-01-02", f.Date, time.UTC); err == nil {
			day = &parsed
		}
	}
	raw, err := p.listRaw("", false)
	if err != nil {
		return nil, fmt.Errorf("agy db: %w", err)
	}
	var out []session.Session
	for i := range raw {
		s := &raw[i]
		if f.Project != "" && !strings.Contains(strings.ToLower(s.ProjectLabel), strings.ToLower(f.Project)) {
			continue
		}
		if day != nil {
			if !touchesDate(s, *day) {
				continue
			}
		} else if cutoff != nil {
			last, ok := textutil.ParseISO(s.LastTS)
			if !ok || last.Before(*cutoff) {
				continue
			}
		}
		out = append(out, *s)
	}
	return out, nil
}

func (p *Provider) LoadDetail(id string) (*session.Session, error) {
	raw, err := p.listRaw(id, true)
	if err != nil {
		return nil, fmt.Errorf("agy db: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return &raw[0], nil
}

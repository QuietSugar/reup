// Package codebuddy reads CodeBuddy CLI session JSONL files under
// ~/.codebuddy/projects. CodeBuddy is a Claude Code-derived CLI; this provider
// only extracts session metadata needed for browsing and resuming.
package codebuddy

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuietSugar/reup/internal/brand"
	"github.com/QuietSugar/reup/internal/session"
	"github.com/QuietSugar/reup/internal/textutil"
)

const maxLineBytes = 10 << 20

var envCodebuddyHome = brand.Env("CODEBUDDY_HOME")

func home() string {
	if root := os.Getenv(envCodebuddyHome); root != "" {
		return root
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codebuddy")
}

func projectsRoot() string { return filepath.Join(home(), "projects") }

type Provider struct{}

func New() *Provider                  { return &Provider{} }
func (p *Provider) Name() string      { return "codebuddy" }
func (p *Provider) Badge() string     { return "cb" }
func (p *Provider) BadgeANSI() string { return "\x1b[36m" }
func (p *Provider) IsAvailable() bool {
	st, err := os.Stat(projectsRoot())
	return err == nil && st.IsDir()
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type record struct {
	Type        string          `json:"type"`
	Role        string          `json:"role"`
	Timestamp   json.RawMessage `json:"timestamp"`
	SessionID   string          `json:"sessionId"`
	Cwd         string          `json:"cwd"`
	Topic       string          `json:"topic"`
	CustomTitle string          `json:"customTitle"`
	AITitle     string          `json:"aiTitle"`
	// IsSidechain marks records a sub-agent wrote into this session's file.
	// They are not part of the main conversation.
	IsSidechain bool            `json:"isSidechain"`
	Content     json.RawMessage `json:"content"`
	Message     json.RawMessage `json:"message"`
}

func parseTimestamp(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if t, ok := textutil.ParseISO(text); ok {
			return t.UTC().Format(time.RFC3339Nano)
		}
		return ""
	}
	var millis int64
	if json.Unmarshal(raw, &millis) == nil && millis > 0 {
		return time.UnixMilli(millis).UTC().Format(time.RFC3339)
	}
	return ""
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, block := range blocks {
			switch block.Type {
			case "text", "input_text", "output_text":
				if text := strings.TrimSpace(block.Text); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func messageFields(r record) (role string, content json.RawMessage) {
	role, content = r.Role, r.Content
	if len(r.Message) == 0 {
		return role, content
	}
	var msg message
	if json.Unmarshal(r.Message, &msg) == nil {
		if msg.Role != "" {
			role = msg.Role
		}
		if len(msg.Content) > 0 {
			content = msg.Content
		}
	}
	return role, content
}

func parseJSONL(path string) *session.Session {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	encodedProject := filepath.Base(filepath.Dir(path))
	var cwd, firstTS, lastTS, topic, customTitle, aiTitle string
	var prompts []session.Prompt
	assistantCount := 0

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	for sc.Scan() {
		var r record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if r.IsSidechain {
			// Sub-agent traffic shares the file but is not this session's
			// conversation: counting it would inflate the prompt list, the
			// assistant-turn estimate, and the activity window.
			continue
		}
		ts := parseTimestamp(r.Timestamp)
		if ts != "" {
			if firstTS == "" || ts < firstTS {
				firstTS = ts
			}
			if lastTS == "" || ts > lastTS {
				lastTS = ts
			}
		}
		if r.SessionID != "" {
			id = r.SessionID
		}
		if cwd == "" && r.Cwd != "" {
			cwd = r.Cwd
		}
		if r.Topic != "" {
			topic = strings.TrimSpace(r.Topic)
		}
		if r.CustomTitle != "" {
			customTitle = strings.TrimSpace(r.CustomTitle)
		}
		if r.AITitle != "" {
			aiTitle = strings.TrimSpace(r.AITitle)
		}

		role, content := messageFields(r)
		if r.Type == "user" && role == "" {
			role = "user"
		}
		if r.Type == "assistant" && role == "" {
			role = "assistant"
		}
		if r.Type != "message" && r.Type != "user" && r.Type != "assistant" {
			continue
		}
		switch role {
		case "user":
			text := extractText(content)
			if isRealPrompt(text) {
				prompts = append(prompts, session.Prompt{TS: ts, Text: text})
			}
		case "assistant":
			assistantCount++
		}
	}

	project := "(unknown)"
	if cwd != "" {
		if base := filepath.Base(strings.TrimRight(cwd, "/")); base != "" && base != "/" && base != "." {
			project = base
		}
	} else {
		trimmed := strings.TrimLeft(encodedProject, "-")
		if idx := strings.LastIndex(trimmed, "-"); idx >= 0 && idx+1 < len(trimmed) {
			project = trimmed[idx+1:]
		} else if trimmed != "" {
			project = trimmed
		}
	}

	firstPrompt, lastPrompt := "", ""
	if len(prompts) > 0 {
		firstPrompt = prompts[0].Text
		lastPrompt = prompts[len(prompts)-1].Text
	}
	title := customTitle
	if title == "" {
		title = aiTitle
	}
	if title == "" {
		title = topic
	}
	if title == "" {
		title = firstPrompt
	}

	return &session.Session{
		Source:       "codebuddy",
		SessionID:    id,
		Path:         path,
		ProjectLabel: project,
		Cwd:          cwd,
		// The encoded project bucket is CodeBuddy's native project key; there
		// is no registry recording a root path, so WorkspaceRoot stays empty
		// and grouping falls back to the session's cwd.
		WorkspaceID: encodedProject,
		FirstTS:     firstTS,
		LastTS:      lastTS,
		Title:       title,
		FirstPrompt: firstPrompt,
		LastPrompt:  lastPrompt,
		Prompts:     prompts,
		AsstCount:   assistantCount,
		ResumeArgv:  []string{"codebuddy", "--resume", id},
	}
}

func isRealPrompt(text string) bool {
	if text == "" {
		return false
	}
	for _, prefix := range []string{"<local-command", "<command-", "<system-reminder", "<ide_opened_file", "[Request interrupted"} {
		if strings.HasPrefix(strings.TrimSpace(text), prefix) {
			return false
		}
	}
	return true
}

func findSessionFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == "subagents" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

func cutoff(f session.Filters, now time.Time) *time.Time {
	if f.AllTime || f.Date != "" || f.Days == 0 {
		return nil
	}
	days := f.Days
	if days < 0 {
		days = 7
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	value := midnight.AddDate(0, 0, -days)
	return &value
}

func (p *Provider) ListSessions(f session.Filters) ([]session.Session, error) {
	root := projectsRoot()
	maxAge := cutoff(f, time.Now())
	var day *time.Time
	if f.Date != "" {
		if parsed, err := time.ParseInLocation("2006-01-02", f.Date, time.UTC); err == nil {
			day = &parsed
		}
	}

	var out []session.Session
	for _, path := range findSessionFiles(root) {
		s := parseJSONL(path)
		if s == nil || s.SessionID == "" {
			continue
		}
		if f.Project != "" && !strings.Contains(strings.ToLower(s.ProjectLabel), strings.ToLower(f.Project)) {
			continue
		}
		if day != nil {
			if !touchesDate(s, *day) {
				continue
			}
		} else if maxAge != nil {
			last, ok := textutil.ParseISO(s.LastTS)
			if !ok || last.Before(*maxAge) {
				continue
			}
		}
		out = append(out, *s)
	}
	return out, nil
}

func touchesDate(s *session.Session, day time.Time) bool {
	for _, value := range []string{s.FirstTS, s.LastTS} {
		parsed, ok := textutil.ParseISO(value)
		if !ok {
			continue
		}
		y, m, d := parsed.In(day.Location()).Date()
		if y == day.Year() && m == day.Month() && d == day.Day() {
			return true
		}
	}
	return false
}

func (p *Provider) LoadDetail(id string) (*session.Session, error) {
	for _, file := range findSessionFiles(projectsRoot()) {
		if strings.TrimSuffix(filepath.Base(file), ".jsonl") == id {
			return parseJSONL(file), nil
		}
	}
	return nil, nil
}

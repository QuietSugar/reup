// Package dsh reads DeepSeek Harness (dsh) session logs so its sessions show
// up in reup's list. List-only by design: the harness has no documented
// resume entry point reup could hand off to, so ResumeArgv stays empty and
// the picker's Enter reports "no resume command".
//
// Storage (verified against the harness's own files and TokenTracker's
// reader): each session is an append-only JSONL log under
// ~/.dsh/sessions/<project-key>/<session-id>/session.v4.jsonl[.zstd]. The
// first line is a type:"session" header (id, createdAt epoch-ms, cwd,
// delegationDepth); every following line is an event with type/seq/time.
// Title arrives as session/title events, user prompts as user/message events
// with data.content[].text. The .zstd files are concatenated zstd frames —
// klauspost/compress decodes them all transparently.
package dsh

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/QuietSugar/reup/internal/brand"
	"github.com/QuietSugar/reup/internal/session"
)

var envHome = brand.Env("DSH_HOME")

// envDshHome is the harness's own home override, honored like the harness
// itself does (after reup's explicit override).
const envDshHome = "DSH_HOME"

// Provider lists DeepSeek Harness sessions.
type Provider struct{}

// New builds the dsh provider.
func New() *Provider { return &Provider{} }

// Name implements provider.Provider.
func (p *Provider) Name() string { return "dsh" }

// Badge implements provider.Provider.
func (p *Provider) Badge() string { return "dsh" }

// BadgeANSI implements provider.Provider (no color: the badge style map owns
// the color).
func (p *Provider) BadgeANSI() string { return "" }

// IsAvailable reports whether a dsh home with a sessions directory exists.
func (p *Provider) IsAvailable() bool {
	_, err := os.Stat(filepath.Join(home(), "sessions"))
	return err == nil
}

// home resolves the harness home: REUP_DSH_HOME > DSH_HOME > ~/.dsh.
func home() string {
	if v := os.Getenv(envHome); v != "" {
		return v
	}
	if v := os.Getenv(envDshHome); v != "" {
		return v
	}
	base, _ := os.UserHomeDir()
	return filepath.Join(base, ".dsh")
}

// headerEvent is the type:"session" first line of a session log.
type headerEvent struct {
	ID              string `json:"id"`
	CreatedAt       int64  `json:"createdAt"` // epoch ms
	Cwd             string `json:"cwd"`
	DelegationDepth int    `json:"delegationDepth"`
}

// sessionMeta is what the list needs from one session log.
type sessionMeta struct {
	id      string
	cwd     string
	created int64 // epoch ms
	last    int64 // epoch ms
	title   string
	prompts int
	depth   int    // delegation depth: >0 marks a spawned sub-agent
	first   string // first user prompt text
}

// parseLog streams one session log (already decompressed) into meta.
func parseLog(r io.Reader, meta *sessionMeta) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	firstPromptSeen := false
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Type string          `json:"type"`
			Time int64           `json:"time"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		switch probe.Type {
		case "session":
			var h headerEvent
			if json.Unmarshal(line, &h) == nil {
				meta.id, meta.cwd, meta.created = h.ID, h.Cwd, h.CreatedAt
				meta.depth = h.DelegationDepth
			}
		case "session/title":
			var ev struct {
				Data struct {
					Title string `json:"title"`
				} `json:"data"`
			}
			if json.Unmarshal(line, &ev) == nil && ev.Data.Title != "" {
				meta.title = ev.Data.Title
			}
		case "user/message":
			meta.prompts++
			if !firstPromptSeen {
				if text := firstText(probe.Data); text != "" {
					meta.first = text
					firstPromptSeen = true
				}
			}
		}
		if probe.Time > meta.last {
			meta.last = probe.Time
		}
	}
}

// firstText extracts the first text part of a user/message content array.
func firstText(data json.RawMessage) string {
	var ev struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(data, &ev) != nil {
		return ""
	}
	for _, c := range ev.Content {
		if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
			return strings.TrimSpace(c.Text)
		}
	}
	return ""
}

// decode returns a reader for a session log, transparently decompressing
// .zstd (the harness appends concatenated frames; klauspost reads them all).
func decode(path string) (io.Reader, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	if !strings.HasSuffix(path, ".zstd") {
		return f, func() { f.Close() }, nil
	}
	zr, err := zstd.NewReader(f)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return zr.IOReadCloser(), func() { zr.Close(); f.Close() }, nil
}

// ListSessions implements provider.Provider. Sub-agent logs
// (delegationDepth > 0) and header-only logs without prompts are skipped, so
// the list shows the sessions a user actually started.
func (p *Provider) ListSessions(f session.Filters) ([]session.Session, error) {
	root := filepath.Join(home(), "sessions")
	paths, err := filepath.Glob(filepath.Join(root, "*", "session-*", "session*.jsonl*"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []session.Session
	for _, path := range paths {
		s, err := parseSessionFile(path)
		if err != nil || s == nil {
			continue
		}
		out = append(out, *s)
	}
	return out, nil
}

// parseSessionFile reads one log; nil, nil means "skip this file".
func parseSessionFile(path string) (*session.Session, error) {
	r, closeFn, err := decode(path)
	if err != nil {
		return nil, nil // unreadable (e.g. mid-write): skip, never fail the scan
	}
	defer closeFn()
	var meta sessionMeta
	parseLog(r, &meta)
	if meta.prompts == 0 || meta.depth > 0 {
		return nil, nil // header-only sessions and spawned sub-agents
	}
	s := &session.Session{
		Source:      "dsh",
		SessionID:   meta.id,
		Path:        path,
		Cwd:         meta.cwd,
		FirstTS:     epochToISO(meta.created),
		LastTS:      epochToISO(meta.last),
		Title:       meta.title,
		FirstPrompt: firstLine(meta.first),
		AsstCount:   0, // assistant counts are not needed for listing
	}
	return s, nil
}

// LoadDetail implements provider.Provider: the same parse, plus every user
// prompt for the preview panel.
func (p *Provider) LoadDetail(id string) (*session.Session, error) {
	paths, err := filepath.Glob(filepath.Join(home(), "sessions", "*", "session-*", "session*.jsonl*"))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		r, closeFn, err := decode(path)
		if err != nil {
			continue
		}
		meta, prompts := parseDetail(r)
		closeFn()
		if meta.id == id {
			s := &session.Session{
				Source:      "dsh",
				SessionID:   meta.id,
				Path:        path,
				Cwd:         meta.cwd,
				FirstTS:     epochToISO(meta.created),
				LastTS:      epochToISO(meta.last),
				Title:       meta.title,
				FirstPrompt: firstLine(meta.first),
				AsstCount:   0,
				Prompts:     prompts,
			}
			return s, nil
		}
	}
	return nil, fmt.Errorf("dsh session %s not found", id)
}

// parseDetail streams a log, additionally collecting user prompts.
func parseDetail(r io.Reader) (sessionMeta, []session.Prompt) {
	var meta sessionMeta
	var prompts []session.Prompt
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var probe struct {
			Type string          `json:"type"`
			Time int64           `json:"time"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		switch probe.Type {
		case "session":
			var h headerEvent
			if json.Unmarshal(line, &h) == nil {
				meta.id, meta.cwd, meta.created = h.ID, h.Cwd, h.CreatedAt
			}
		case "session/title":
			var ev struct {
				Data struct {
					Title string `json:"title"`
				} `json:"data"`
			}
			if json.Unmarshal(line, &ev) == nil && ev.Data.Title != "" {
				meta.title = ev.Data.Title
			}
		case "user/message":
			meta.prompts++
			if text := firstText(probe.Data); text != "" {
				ts := epochToISO(probe.Time)
				prompts = append(prompts, session.Prompt{TS: ts, Text: text})
			}
		}
		if probe.Time > meta.last {
			meta.last = probe.Time
		}
	}
	return meta, prompts
}

// epochToISO renders epoch milliseconds as the UTC ISO stamps the other
// providers use; 0 stays empty.
func epochToISO(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// firstLine cuts a prompt to its first line for list display.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

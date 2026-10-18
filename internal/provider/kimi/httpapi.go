package kimi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// kimiSessionMutator implements provider.SessionMutator against the agent's
// own local server (`kimi web` / the kap-server every kimi surface talks to).
// Per the kap-server sources: the action is the URL tail after the session id
// ("POST /api/v1/sessions/{id}:delete"), the body is an empty JSON object,
// and errors arrive as HTTP 200 with an envelope whose code is non-zero —
// only auth failures use HTTP 401. Storage files are never touched.
const (
	kimiActionDelete  = "delete"
	kimiActionArchive = "archive"

	// kimi's instanceRegistry only treats a process as live while
	// kill(pid, 0) succeeds; EPERM (owned by someone else) also counts.
	kimiHTTPTimeout = 5 * time.Second
)

// kimi's official archive is the only mutation reup offers: restorable,
// and it hides the session in kimi's own surfaces too.
// serverInstance mirrors ~/.kimi-code/server/instances/*.json.
type serverInstance struct {
	ServerID string `json:"server_id"`
	PID      int    `json:"pid"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	StartAt  int64  `json:"started_at"`
}

// pidAlive mirrors kimi's pidAlive: signal 0 succeeds for a live process,
// and EPERM means it exists but is owned by someone else — still alive.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}

// liveInstances scans the instance registry, keeping only entries whose
// process is still running, newest started_at first. Heartbeat age is
// deliberately ignored — kimi's own liveness semantics are PID-based.
func liveInstances() []serverInstance {
	entries, err := filepath.Glob(filepath.Join(kimiHome(), "server", "instances", "*.json"))
	if err != nil {
		return nil
	}
	var live []serverInstance
	for _, e := range entries {
		data, err := os.ReadFile(e)
		if err != nil {
			continue
		}
		var inst serverInstance
		if json.Unmarshal(data, &inst) != nil || inst.Port <= 0 || !pidAlive(inst.PID) {
			continue
		}
		live = append(live, inst)
	}
	sort.Slice(live, func(i, j int) bool { return live[i].StartAt > live[j].StartAt })
	return live
}

// serverHost normalizes a bind address to a connectable host: kimi may bind
// 0.0.0.0 (all interfaces), which is never dialable as-is.
func serverHost(host string) string {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return "127.0.0.1"
	}
	return host
}

// serverToken reads and trims the bearer token the server generated at boot.
func serverToken() string {
	data, err := os.ReadFile(filepath.Join(kimiHome(), "server.token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// ServerAvailable reports whether a live kimi server instance is registered.
func (p *Provider) ServerAvailable() bool {
	return len(liveInstances()) > 0
}

// CheckSession gates per policy: when a kimi server is already running (the
// user's own, or one whose auto-stop failed), reup does nothing and points
// the user at its web UI instead. When nothing is running, the auto-start
// path (start → archive → graceful stop) is used.
func (p *Provider) CheckSession(id string) error {
	if insts := liveInstances(); len(insts) > 0 {
		return fmt.Errorf(
			"kimi web is already running — archive or delete this session in its web UI: http://%s:%d",
			serverHost(insts[0].Host), insts[0].Port)
	}
	return nil
}

// ArchiveSession hides the session; kimi can restore it later.
func (p *Provider) ArchiveSession(id string) error {
	return p.mutateSession(id, kimiActionArchive)
}

func (p *Provider) mutateSession(id, action string) error {
	if strings.Contains(id, "/") {
		return fmt.Errorf("kimi-code %s: invalid session id %q", action, id)
	}
	insts := liveInstances()
	if len(insts) == 0 {
		return fmt.Errorf("kimi-code server is not running — start kimi first")
	}
	url := fmt.Sprintf("http://%s:%d/api/v1/sessions/%s:%s",
		serverHost(insts[0].Host), insts[0].Port, id, action)

	body := bytes.NewReader([]byte("{}"))
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("kimi-code %s: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := serverToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: kimiHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("kimi-code server is not reachable: %v", err)
	}
	defer resp.Body.Close()

	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	decodeErr := json.NewDecoder(resp.Body).Decode(&env)

	switch {
	case resp.StatusCode == http.StatusUnauthorized || (decodeErr == nil && env.Code == 40101):
		return fmt.Errorf("kimi-code %s: unauthorized (server token mismatch)", action)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return fmt.Errorf("kimi-code %s failed: HTTP %d", action, resp.StatusCode)
	case decodeErr != nil:
		return fmt.Errorf("kimi-code %s failed: unreadable response (%v)", action, decodeErr)
	case env.Code != 0:
		return fmt.Errorf("kimi-code %s failed: %s (code %d)", action, env.Msg, env.Code)
	}
	return nil
}

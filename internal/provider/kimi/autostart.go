package kimi

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// startWaitTimeout bounds how long StartServer waits for the spawned server
// to register itself and become reachable.
const startWaitTimeout = 12 * time.Second

// StartServer implements provider.ServerStarter: when no live kimi server is
// registered, spawn `kimi web --no-open` detached — its own session (Setsid),
// stdio redirected to a log file, no terminal and no browser — and wait until
// the instance registry reports it live. The server is left running: it is
// the user's kimi server, and killing it from reup could interrupt other
// sessions using it.
func (p *Provider) StartServer() error {
	if p.ServerAvailable() {
		return nil
	}
	logPath, err := kimiWebLogPath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("kimi web log: %v", err)
	}
	defer logFile.Close()

	cmd := exec.Command(kimiBin(), "web", "--no-open")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("kimi web: %v", err)
	}
	go func() { _ = cmd.Wait() }() // reaped in the background; we never signal it

	if err := p.waitReady(startWaitTimeout); err != nil {
		return fmt.Errorf("kimi web did not become ready — see %s: %v", logPath, err)
	}
	// Remember which instance we spawned: StopServer shuts down only this one,
	// never a server the user started themselves.
	if insts := liveInstances(); len(insts) > 0 {
		p.startedPID = insts[0].PID
	}
	return nil
}

// StopServer gracefully shuts down the instance reup spawned, via the
// server's own POST /api/v1/shutdown (full cleanup — no signals). When
// reup did not spawn a server this is a no-op. It waits briefly for the
// process to exit; it never kills anything.
func (p *Provider) StopServer() error {
	pid := p.startedPID
	if pid == 0 {
		return nil
	}
	var target *serverInstance
	for i := range liveInstances() {
		if inst := liveInstances()[i]; inst.PID == pid {
			target = &inst
			break
		}
	}
	if target == nil {
		p.startedPID = 0
		return nil // already gone
	}
	url := fmt.Sprintf("http://%s:%d/api/v1/shutdown", serverHost(target.Host), target.Port)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := serverToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: kimiHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("shutdown request failed: %v", err)
	}
	resp.Body.Close()

	// Give the server a moment to finish its graceful teardown.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			p.startedPID = 0
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("kimi web (pid %d) did not exit after the shutdown request", pid)
}

// waitReady polls the instance registry until a live server shows up.
func (p *Provider) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.ServerAvailable() {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", timeout)
}

func kimiWebLogPath() (string, error) {
	dir := filepath.Join(kimiHome(), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("kimi logs dir: %v", err)
	}
	return filepath.Join(dir, "reup-kimi-web.log"), nil
}

// StartCommand is the display form of the auto-start command, shown in the
// picker's confirm modal.
func (p *Provider) StartCommand() string { return "kimi web --no-open" }

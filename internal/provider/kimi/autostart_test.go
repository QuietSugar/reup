package kimi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeWebBin writes a shell script that mimics `kimi web --no-open`: it
// registers an instance file (its own pid — alive for as long as it sleeps)
// and then sleeps, exactly like the real server registers and serves.
func fakeWebBin(t *testing.T, home string, port int) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "kimi-fake")
	script := fmt.Sprintf(`#!/bin/sh
echo "$$" > %[1]s/pid.txt
mkdir -p %[2]s/server/instances
cat > %[2]s/server/instances/fake.json <<EOF
{"server_id":"fake","pid":$$,"host":"127.0.0.1","port":%[3]d,"started_at":1,"heartbeat_at":1,"host_version":"test"}
EOF
sleep 60
`, home, home, port)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// cleanupSpawned kills the fake server via its recorded pid, so detached
// test processes never outlive the test.
func cleanupSpawned(t *testing.T, home string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "pid.txt"))
	if err != nil {
		return
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return
	}
	if pid > 0 {
		_ = exec.Command("kill", fmt.Sprint(pid)).Run()
	}
}

func TestKimiStartServerSpawnsDetachedAndWaits(t *testing.T) {
	home := t.TempDir()
	t.Setenv(envKimiHome, home)
	t.Setenv(envKimiBin, fakeWebBin(t, home, 58627))
	p := New()

	if p.ServerAvailable() {
		t.Fatal("registry must start empty")
	}
	if err := p.StartServer(); err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	defer cleanupSpawned(t, home)
	if !p.ServerAvailable() {
		t.Error("server should be available after StartServer")
	}
	// Idempotent: a second call with the server up must not spawn again.
	if err := p.StartServer(); err != nil {
		t.Fatalf("second StartServer: %v", err)
	}
	entries, _ := filepath.Glob(filepath.Join(home, "server", "instances", "*.json"))
	if len(entries) != 1 {
		t.Errorf("instance files = %d, want 1 (no duplicate spawn)", len(entries))
	}
}

func TestKimiStartServerAlreadyRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	fakeHome(t, portOf(t, srv), spawnLivePid(t), "127.0.0.1")
	_ = home
	// A live instance exists → StartServer must return without spawning.
	if err := New().StartServer(); err != nil {
		t.Fatalf("StartServer with live server: %v", err)
	}
}

// When a kimi server is already running, reup refuses to touch anything
// and points the user at the web UI instead.
func TestKimiCheckSessionRunningShowsWebUI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	fakeHome(t, portOf(t, srv), spawnLivePid(t), "127.0.0.1")

	err := New().CheckSession("ses_1")
	if err == nil || !strings.Contains(err.Error(), "web UI") {
		t.Fatalf("CheckSession = %v, want the web-UI pointer", err)
	}
	if !strings.Contains(err.Error(), srv.URL[7:]) { // host:port
		t.Errorf("error should carry the running server's URL: %v", err)
	}
}

// StopServer must use the official shutdown endpoint and only target the
// instance reup spawned; without one it is a no-op.
func TestKimiStopServer(t *testing.T) {
	t.Run("no spawned server: no-op", func(t *testing.T) {
		srv, hits := shutdownStub(t, nil)
		fakeHome(t, portOf(t, srv), spawnLivePid(t), "127.0.0.1")
		if err := New().StopServer(); err != nil {
			t.Fatalf("StopServer: %v", err)
		}
		if len(*hits) != 0 {
			t.Errorf("shutdown posted %d times, want 0", len(*hits))
		}
	})

	t.Run("spawned server: official shutdown, waits for exit", func(t *testing.T) {
		home := t.TempDir()
		srv, hits := shutdownStub(t, killFromPidFile(home))
		t.Setenv(envKimiHome, home)
		t.Setenv(envKimiBin, fakeWebBin(t, home, portOf(t, srv)))
		p := New()
		if err := p.StartServer(); err != nil {
			t.Fatalf("StartServer: %v", err)
		}
		defer cleanupSpawned(t, home)

		if err := p.StopServer(); err != nil {
			t.Fatalf("StopServer: %v", err)
		}
		if len(*hits) != 1 {
			t.Errorf("shutdown posts = %d, want 1", len(*hits))
		}
		if p.ServerAvailable() {
			t.Error("spawned instance should be gone after StopServer")
		}
	})
}

// shutdownStub answers the shutdown POST; die (optional) simulates the real
// server exiting itself after a graceful teardown, so the pid-exit wait in
// StopServer terminates naturally.
func shutdownStub(t *testing.T, die func()) (*httptest.Server, *[]string) {
	t.Helper()
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/shutdown" {
			hits = append(hits, r.URL.Path)
			w.Write([]byte(`{"code":0,"data":{"ok":true}}`))
			if die != nil {
				go die()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// killFromPidFile terminates the fake server recorded in home/pid.txt — the
// test's stand-in for the real server exiting on shutdown.
func killFromPidFile(home string) func() {
	return func() {
		if data, err := os.ReadFile(filepath.Join(home, "pid.txt")); err == nil {
			var pid int
			if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err == nil && pid > 0 {
				_ = exec.Command("kill", fmt.Sprint(pid)).Run()
			}
		}
	}
}

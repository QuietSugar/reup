package kimi

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// spawnLivePid returns the pid of a real, running process (sleep) so the
// PID-liveness check passes.
func spawnLivePid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn a live process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process.Pid
}

func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot spawn a process: %v", err)
	}
	return cmd.Process.Pid
}

// fakeHome writes a kimi home with an instance registry pointing at the
// httptest server plus a token file, activated via REUP_KIMI_HOME.
func fakeHome(t *testing.T, port int, pid int, host string) {
	t.Helper()
	home := t.TempDir()
	instDir := filepath.Join(home, "server", "instances")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inst, _ := json.Marshal(serverInstance{
		ServerID: "srv-test", PID: pid, Host: host, Port: port, StartAt: 12345,
	})
	if err := os.WriteFile(filepath.Join(instDir, "srv-test.json"), inst, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "server.token"), []byte("tok-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envKimiHome, home)
}

// kimiStubServer asserts the request the mutator must send and replies with
// the given envelope. The recorded request is read back after the call.
func kimiStubServer(t *testing.T, status int, envelope string) (*httptest.Server, *[]string) {
	t.Helper()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization"))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(envelope))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func portOf(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	var p int
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	return p
}

func TestKimiMutateSendsActionSuffixAndToken(t *testing.T) {
	srv, got := kimiStubServer(t, http.StatusOK,
		`{"code":0,"msg":"success","data":{"archived":true}}`)
	fakeHome(t, portOf(t, srv), spawnLivePid(t), "0.0.0.0")

	if err := New().ArchiveSession("ses_1"); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if len(*got) != 1 {
		t.Fatalf("%d requests, want 1", len(*got))
	}
	req := (*got)[0]
	if !strings.HasPrefix(req, "POST /api/v1/sessions/ses_1:archive") {
		t.Errorf("request = %q, want POST /api/v1/sessions/ses_1:archive", req)
	}
	if !strings.Contains(req, "auth=Bearer tok-123") {
		t.Errorf("bearer token missing: %q", req)
	}
}

func TestKimiServerAvailablePIDLiveness(t *testing.T) {
	p := New()
	srv, _ := kimiStubServer(t, http.StatusOK, `{"code":0}`)

	fakeHome(t, portOf(t, srv), spawnLivePid(t), "127.0.0.1")
	if !p.ServerAvailable() {
		t.Error("live pid must count as available")
	}

	fakeHome(t, portOf(t, srv), deadPid(t), "127.0.0.1")
	if p.ServerAvailable() {
		t.Error("dead pid must not count as available")
	}

	// No instance file at all.
	t.Setenv(envKimiHome, t.TempDir())
	if p.ServerAvailable() {
		t.Error("empty registry must not count as available")
	}
}

func TestKimiMutateErrorMapping(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		envelop string
		want    string
	}{
		{"envelope error in 200", 200, `{"code":5001,"msg":"session busy"}`, "session busy (code 5001)"},
		{"unauthorized", 401, `{"code":40101,"msg":"unauthorized"}`, "unauthorized (server token mismatch)"},
	}
	for _, tc := range cases {
		srv, _ := kimiStubServer(t, tc.status, tc.envelop)
		fakeHome(t, portOf(t, srv), spawnLivePid(t), "127.0.0.1")
		err := New().ArchiveSession("ses_1")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want containing %q", tc.name, err, tc.want)
		}
	}
}

func TestKimiMutateRejectsPathInID(t *testing.T) {
	srv, got := kimiStubServer(t, http.StatusOK, `{"code":0}`)
	fakeHome(t, portOf(t, srv), spawnLivePid(t), "127.0.0.1")
	if err := New().ArchiveSession("a/b"); err == nil {
		t.Error("id containing / must be rejected client-side")
	}
	if len(*got) != 0 {
		t.Error("rejected id must not reach the server")
	}
}

func TestKimiMutateWithoutLiveServer(t *testing.T) {
	t.Setenv(envKimiHome, t.TempDir())
	err := New().ArchiveSession("ses_1")
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("err = %v, want the start-kimi-first hint", err)
	}
}

//go:build !windows

package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunLiveClientKillsStalledProcessGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "descendant-survived")
	_, err := runLiveClient(t, 100*time.Millisecond, "/bin/sh", []string{"-c", "(sleep 0.4; touch \"$1\") & wait", "stalled-client", marker}, nil)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("runLiveClient error = %v, want deadline exceeded", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("stalled client descendant survived timeout, marker stat error = %v", err)
	}
}

func TestOriginalCodexDriverTimesOutAndReapsStalledAppServer(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("original Codex harness requires macOS sandbox-exec")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "late-marker")
	fakeBinary := filepath.Join(root, "fake-codex")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'codex-cli 0.0.1'; exit 0; fi\n(sleep 0.5; touch '" + marker + "') &\nsleep 30\n"
	if err := os.WriteFile(fakeBinary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(root, "catalog.json")
	if err := os.WriteFile(catalog, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "sandbox.sb")
	if err := os.WriteFile(profile, []byte("(version 1)\n(deny network*)\n(allow default)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate integration test source")
	}
	driver := filepath.Join(filepath.Dir(source), "..", "scripts", "original_codex_image.py")
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	_, runErr := runLiveClient(t, 10*time.Second, python, []string{driver, "--base-url", "http://127.0.0.1:12345/v1", "--output-root", root, "--image-url", "https://images.invalid/test.png", "--rpc-timeout", "0.1"}, func(command *exec.Cmd) {
		command.Env = append(os.Environ(), "CPA_LIVE_CODEX_BINARY="+fakeBinary, "CPA_LIVE_CODEX_EXPECTED_VERSION=0.0.1", "CPA_LIVE_CODEX_CATALOG="+catalog, "CPA_LIVE_CODEX_SANDBOX="+profile, "TASK_PROXY_API_KEY=fake-test-key")
		command.Stdout, command.Stderr = &stdout, &stderr
	})
	if runErr == nil {
		t.Fatalf("stalled app-server driver succeeded, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"error": "rpc_timeout"`) {
		t.Fatalf("driver did not report its RPC deadline, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("stalled app-server descendant survived driver cleanup, marker stat error = %v", err)
	}
}

func TestOriginalCodexDriverTimesOutDespiteUnrelatedNotifications(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("original Codex harness requires macOS sandbox-exec")
	}
	root := t.TempDir()
	marker := filepath.Join(root, "late-turn-marker")
	fakeBinary := filepath.Join(root, "fake-codex")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'codex-cli 0.0.1'; exit 0; fi\nwhile IFS= read -r line; do\ncase \"$line\" in\n*'\"method\": \"initialize\"'*) printf '{\"id\":1,\"result\":{}}\\n' ;;\n*'\"method\": \"thread/start\"'*) printf '{\"id\":2,\"result\":{\"model\":\"gpt-6-luna\",\"thread\":{\"id\":\"thread-1\"}}}\\n' ;;\n*'\"method\": \"turn/start\"'*) printf '{\"id\":3,\"result\":{\"turn\":{\"id\":\"turn-1\"}}}\\n'; (sleep 0.5; touch '" + marker + "') & while :; do printf '{\"method\":\"unrelated/progress\",\"params\":{}}\\n'; sleep 0.02; done ;;\nesac\ndone\n"
	if err := os.WriteFile(fakeBinary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(root, "catalog.json")
	if err := os.WriteFile(catalog, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "sandbox.sb")
	if err := os.WriteFile(profile, []byte("(version 1)\n(deny network*)\n(allow default)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate integration test source")
	}
	driver := filepath.Join(filepath.Dir(source), "..", "scripts", "original_codex_image.py")
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	_, runErr := runLiveClient(t, 10*time.Second, python, []string{driver, "--base-url", "http://127.0.0.1:12345/v1", "--output-root", root, "--image-url", "https://images.invalid/test.png", "--rpc-timeout", "1", "--turn-timeout", "0.1"}, func(command *exec.Cmd) {
		command.Env = append(os.Environ(), "CPA_LIVE_CODEX_BINARY="+fakeBinary, "CPA_LIVE_CODEX_EXPECTED_VERSION=0.0.1", "CPA_LIVE_CODEX_CATALOG="+catalog, "CPA_LIVE_CODEX_SANDBOX="+profile, "TASK_PROXY_API_KEY=fake-test-key")
		command.Stdout, command.Stderr = &stdout, &stderr
	})
	if runErr == nil {
		t.Fatalf("stalled turn driver succeeded, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"error": "turn_timeout"`) {
		t.Fatalf("driver did not report its turn deadline, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("stalled turn descendant survived driver cleanup, marker stat error = %v", err)
	}
}

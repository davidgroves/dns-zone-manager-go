//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	dnsCLIOnce sync.Once
	dnsCLIBin  string
	dnsCLIErr  error
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func dnsCLIPath(t *testing.T) string {
	t.Helper()
	dnsCLIOnce.Do(func() {
		root := moduleRoot(t)
		out := filepath.Join(os.TempDir(), fmt.Sprintf("dns-cli-integration-%d", os.Getpid()))
		cmd := exec.Command("go", "build", "-o", out, "./cmd/dns-cli")
		cmd.Dir = root
		var stderr strings.Builder
		cmd.Stderr = &stderr
		dnsCLIErr = cmd.Run()
		if dnsCLIErr != nil {
			dnsCLIErr = fmt.Errorf("build dns-cli: %w: %s", dnsCLIErr, stderr.String())
			return
		}
		dnsCLIBin = out
	})
	require.NoError(t, dnsCLIErr)
	return dnsCLIBin
}

func runDNSCLI(t *testing.T, baseURL string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	bin := dnsCLIPath(t)
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(),
		"DNS_API_URL="+baseURL,
		"DNS_API_KEY=dev",
	)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

func writeTempJSON(t *testing.T, v any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload.json")
	raw, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

func TestDNSCLI(t *testing.T) {
	stack := startBINDAPI(t, stackOptions{withStore: true, serveHTTP: true})
	require.NotEmpty(t, stack.BaseURL)
	zone := stack.Zone
	base := stack.BaseURL

	t.Run("list_zones", func(t *testing.T) {
		stdout, stderr, err := runDNSCLI(t, base, "list", "zones")
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, zone)
	})

	unique := fmt.Sprintf("cli-e2e-%d", time.Now().UnixNano())

	t.Run("add_and_get", func(t *testing.T) {
		stdout, stderr, err := runDNSCLI(t, base,
			"add", zone, unique, "300", "A", "192.0.2.55")
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)

		stdout, stderr, err = runDNSCLI(t, base, "get", zone, unique, "A")
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, "192.0.2.55")

		stdout, stderr, err = runDNSCLI(t, base, "list", "records", zone, "--name", unique)
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, "192.0.2.55")
	})

	var changeID string

	t.Run("schedule_lifecycle", func(t *testing.T) {
		schedName := "sched-" + unique[len(unique)-8:]
		schedHost := unique + "-sched." + zone
		payload := map[string]any{
			"name": schedName,
			"zone": zone,
			"operations": []map[string]any{
				{
					"action":  "add",
					"name":    schedHost,
					"type":    "A",
					"ttl":     300,
					"records": []string{"192.0.2.66"},
				},
			},
		}
		path := writeTempJSON(t, payload)

		stdout, stderr, err := runDNSCLI(t, base, "schedule", "create", "--file", path)
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, "Created scheduled change")

		// Extract id from human output line "  ID:     <uuid>"
		for _, line := range strings.Split(stdout, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "ID:") {
				changeID = strings.TrimSpace(strings.TrimPrefix(line, "ID:"))
				break
			}
		}
		require.NotEmpty(t, changeID, "stdout=%s", stdout)

		stdout, stderr, err = runDNSCLI(t, base, "schedule", "list", "--status", "draft")
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, changeID)
		require.Contains(t, stdout, schedName)

		stdout, stderr, err = runDNSCLI(t, base, "schedule", "preview", changeID)
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, "Preview")

		stdout, stderr, err = runDNSCLI(t, base, "schedule", "apply", changeID)
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, "Applied")

		stdout, stderr, err = runDNSCLI(t, base, "schedule", "get", changeID)
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, "applied")

		stdout, stderr, err = runDNSCLI(t, base, "schedule", "revert-preview", changeID)
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, "Revert preview")
	})

	t.Run("nsupdate_drafts", func(t *testing.T) {
		host := unique + "-ns"
		script := fmt.Sprintf("zone %s\nupdate add %s 300 A 192.0.2.77\nsend\n", zone, host)
		path := filepath.Join(t.TempDir(), "upd.txt")
		require.NoError(t, os.WriteFile(path, []byte(script), 0o600))

		stdout, stderr, err := runDNSCLI(t, base, "nsupdate", "--drafts", "--file", path)
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, "draft")

		stdout, stderr, err = runDNSCLI(t, base, "schedule", "list", "--status", "draft")
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, "draft")
		// nsupdate drafts name includes zone; look for the host in list output or just non-empty drafts
		require.NotContains(t, stdout, "No scheduled changes found")
	})

	t.Run("search", func(t *testing.T) {
		stdout, stderr, err := runDNSCLI(t, base,
			"search", "--zone", zone, "--name", unique)
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		require.Contains(t, stdout, unique)
	})

	t.Run("json_flag", func(t *testing.T) {
		stdout, stderr, err := runDNSCLI(t, base, "--json", "schedule", "list")
		require.NoError(t, err, "stderr=%s stdout=%s", stderr, stdout)
		var body map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &body), "stdout=%s", stdout)
		_, ok := body["changes"]
		require.True(t, ok, "missing changes key: %v", body)
	})
}

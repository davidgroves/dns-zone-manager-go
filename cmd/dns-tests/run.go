package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var errTestsFailed = errors.New("tests failed")

type options struct {
	unit        bool
	integration bool
	e2e         bool
	reportPath  string
}

func execute(opts options) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	rep := Report{Started: time.Now(), Git: gitDescribe(root)}

	if opts.unit {
		fmt.Println("=== Go Unit Tests ===")
		goSuite := runGoTest(root, "Go unit", false, "./...")
		rep.Suites = append(rep.Suites, goSuite)

		fmt.Println()
		fmt.Println("=== Frontend Unit Tests ===")
		rep.Suites = append(rep.Suites, runVitest(root))
	}

	if opts.integration {
		fmt.Println()
		fmt.Println("=== Go Integration Tests (requires Docker) ===")
		rep.Suites = append(rep.Suites, runGoTest(root, "Go integration", true,
			"-tags=integration", "./tests/integration/..."))
	}

	if opts.e2e {
		fmt.Println()
		fmt.Println("=== Frontend E2E Tests (requires running stack) ===")
		rep.Suites = append(rep.Suites, runPlaywright(root))
	}

	rep.Finished = time.Now()
	if opts.reportPath != "" {
		if err := writePDF(opts.reportPath, rep); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		fmt.Printf("\nWrote %s\n", opts.reportPath)
	}
	if rep.failed() {
		return errTestsFailed
	}
	fmt.Println()
	fmt.Println("All tests passed!")
	return nil
}

func runGoTest(root, name string, verbose bool, args ...string) SuiteResult {
	argv := append([]string{"test", "-json"}, args...)
	cmd := exec.Command("go", argv...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return SuiteResult{Name: name, Status: statusFailed, Detail: err.Error()}
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return SuiteResult{Name: name, Status: statusFailed, Detail: err.Error()}
	}
	suite := consumeGoJSON(stdout, os.Stdout, verbose)
	waitErr := cmd.Wait()
	suite.Name = name
	if waitErr != nil && suite.Status != statusFailed {
		suite.Status = statusFailed
		if suite.Detail == "" {
			suite.Detail = waitErr.Error()
		}
	}
	return suite
}

func runVitest(root string) SuiteResult {
	dir, err := os.MkdirTemp("", "dns-tests-vitest-*")
	if err != nil {
		return SuiteResult{Name: "Frontend unit", Status: statusFailed, Detail: err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	outPath := filepath.Join(dir, "vitest.json")
	started := time.Now()
	cmd := exec.Command("npm", "run", "test", "--",
		"--reporter=default",
		"--reporter=json",
		"--outputFile.json="+outPath,
	)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	data, readErr := os.ReadFile(outPath)
	suite := SuiteResult{Name: "Frontend unit", Status: statusFailed, Duration: time.Since(started)}
	if readErr != nil {
		suite.Detail = "vitest did not write a JSON report"
		if runErr != nil {
			suite.Detail = runErr.Error()
		}
		return suite
	}
	parsed := parseVitest(data)
	parsed.Duration = suite.Duration
	if runErr != nil && parsed.Status != statusFailed {
		parsed.Status = statusFailed
		if parsed.Detail == "" {
			parsed.Detail = runErr.Error()
		}
	}
	return parsed
}

func runPlaywright(root string) SuiteResult {
	if !urlUp("http://localhost:8000/health") {
		fmt.Println("SKIP: API not running on http://localhost:8000 — skipping Playwright")
		return SuiteResult{
			Name:   "Frontend E2E",
			Status: statusSkipped,
			Detail: "API not running on http://localhost:8000",
		}
	}
	if !urlUp("http://localhost:5173") {
		fmt.Println("SKIP: Vite not running on http://localhost:5173 — skipping Playwright")
		return SuiteResult{
			Name:   "Frontend E2E",
			Status: statusSkipped,
			Detail: "Vite not running on http://localhost:5173",
		}
	}
	fmt.Println("Dev servers detected, running E2E tests...")
	dir, err := os.MkdirTemp("", "dns-tests-pw-*")
	if err != nil {
		return SuiteResult{Name: "Frontend E2E", Status: statusFailed, Detail: err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	jsonPath := filepath.Join(dir, "playwright.json")

	check := exec.Command("npm", "run", "test:e2e:check")
	check.Dir = root
	check.Stdout = os.Stdout
	check.Stderr = os.Stderr
	if err := check.Run(); err != nil {
		return SuiteResult{Name: "Frontend E2E", Status: statusFailed, Detail: "e2e precheck failed"}
	}

	cmd := exec.Command("npx", "playwright", "test", "--reporter=list", "--reporter=json")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PLAYWRIGHT_JSON_OUTPUT_NAME="+jsonPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	data, readErr := os.ReadFile(jsonPath)
	if readErr != nil {
		detail := "Playwright did not write a JSON report"
		if runErr != nil {
			detail = runErr.Error()
		}
		return SuiteResult{Name: "Frontend E2E", Status: statusFailed, Detail: detail}
	}
	suite := parsePlaywright(data)
	if runErr != nil && suite.Status != statusFailed {
		suite.Status = statusFailed
		if suite.Detail == "" {
			suite.Detail = runErr.Error()
		}
	}
	return suite
}

func urlUp(raw string) bool {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(raw)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode < 500
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found from the current directory")
		}
		dir = parent
	}
}

func gitDescribe(root string) string {
	cmd := exec.Command("git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(bytes.TrimSpace(out)))
}

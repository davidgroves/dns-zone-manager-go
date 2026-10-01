package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type goEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

// consumeGoJSON reads `go test -json` output. When verbose is set, each test
// result is printed as it completes; otherwise only package lines are printed.
func consumeGoJSON(r io.Reader, w io.Writer, verbose bool) SuiteResult {
	suite := SuiteResult{Name: "Go", Status: statusPassed}
	var (
		pkgElapsed float64
		outputs    = map[string]*strings.Builder{}
		sawEvent   bool
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var ev goEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			_, _ = fmt.Fprintln(w, string(line))
			continue
		}
		sawEvent = true
		key := ev.Package + "\x00" + ev.Test
		switch ev.Action {
		case "output":
			b := outputs[key]
			if b == nil {
				b = &strings.Builder{}
				outputs[key] = b
			}
			b.WriteString(ev.Output)
		case "pass", "fail", "skip":
			if ev.Test == "" {
				pkgElapsed += ev.Elapsed
				if ev.Action == "fail" {
					suite.Status = statusFailed
					if buf := outputs[key]; buf != nil && strings.TrimSpace(buf.String()) != "" {
						suite.Failures = append(suite.Failures, Failure{
							Name:   ev.Package,
							Output: strings.TrimSpace(buf.String()),
						})
					}
				}
				mark := "ok"
				switch ev.Action {
				case "fail":
					mark = "FAIL"
				case "skip":
					mark = "?"
				}
				_, _ = fmt.Fprintf(w, "%-4s\t%s\t%.3fs\n", mark, ev.Package, ev.Elapsed)
				continue
			}
			switch ev.Action {
			case "pass":
				suite.Passed++
			case "fail":
				suite.Failed++
				suite.Status = statusFailed
				out := ""
				if buf := outputs[key]; buf != nil {
					out = strings.TrimSpace(buf.String())
				}
				suite.Failures = append(suite.Failures, Failure{Name: ev.Package + " " + ev.Test, Output: out})
			case "skip":
				suite.Skipped++
			}
			if verbose || ev.Action == "fail" {
				label := strings.ToUpper(ev.Action)
				_, _ = fmt.Fprintf(w, "--- %s: %s (%s)\n", label, ev.Test, ev.Package)
				if ev.Action == "fail" {
					if buf := outputs[key]; buf != nil {
						_, _ = fmt.Fprint(w, buf.String())
					}
				}
			}
		}
	}
	suite.Duration = time.Duration(pkgElapsed * float64(time.Second))
	if !sawEvent && suite.Passed == 0 && suite.Failed == 0 {
		suite.Status = statusFailed
		suite.Detail = "go test produced no JSON events"
	}
	return suite
}

type vitestReport struct {
	NumPassedTests  int  `json:"numPassedTests"`
	NumFailedTests  int  `json:"numFailedTests"`
	NumPendingTests int  `json:"numPendingTests"`
	Success         bool `json:"success"`
	TestResults     []struct {
		Name             string `json:"name"`
		AssertionResults []struct {
			FullName        string   `json:"fullName"`
			Status          string   `json:"status"`
			FailureMessages []string `json:"failureMessages"`
		} `json:"assertionResults"`
	} `json:"testResults"`
}

func parseVitest(data []byte) SuiteResult {
	suite := SuiteResult{Name: "Frontend unit", Status: statusPassed}
	var rep vitestReport
	if err := json.Unmarshal(data, &rep); err != nil {
		suite.Status = statusFailed
		suite.Detail = "could not parse vitest JSON: " + err.Error()
		return suite
	}
	suite.Passed = rep.NumPassedTests
	suite.Failed = rep.NumFailedTests
	suite.Skipped = rep.NumPendingTests
	if !rep.Success || rep.NumFailedTests > 0 {
		suite.Status = statusFailed
	}
	for _, file := range rep.TestResults {
		for _, a := range file.AssertionResults {
			if a.Status != "failed" {
				continue
			}
			name := a.FullName
			if name == "" {
				name = file.Name
			}
			suite.Failures = append(suite.Failures, Failure{
				Name:   name,
				Output: strings.TrimSpace(strings.Join(a.FailureMessages, "\n")),
			})
		}
	}
	return suite
}

type pwReport struct {
	Stats struct {
		Expected   int     `json:"expected"`
		Unexpected int     `json:"unexpected"`
		Skipped    int     `json:"skipped"`
		Flaky      int     `json:"flaky"`
		Duration   float64 `json:"duration"`
	} `json:"stats"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Suites []pwSuite `json:"suites"`
}

type pwSuite struct {
	Title  string    `json:"title"`
	Suites []pwSuite `json:"suites"`
	Specs  []struct {
		Title string `json:"title"`
		OK    bool   `json:"ok"`
		Tests []struct {
			Results []struct {
				Status string `json:"status"`
				Error  struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"results"`
		} `json:"tests"`
	} `json:"specs"`
}

func parsePlaywright(data []byte) SuiteResult {
	suite := SuiteResult{Name: "Frontend E2E", Status: statusPassed}
	var rep pwReport
	if err := json.Unmarshal(data, &rep); err != nil {
		suite.Status = statusFailed
		suite.Detail = "could not parse Playwright JSON: " + err.Error()
		return suite
	}
	suite.Passed = rep.Stats.Expected
	suite.Failed = rep.Stats.Unexpected
	suite.Skipped = rep.Stats.Skipped
	suite.Duration = time.Duration(rep.Stats.Duration * float64(time.Millisecond))
	if rep.Stats.Flaky > 0 {
		suite.Detail = fmt.Sprintf("%d flaky", rep.Stats.Flaky)
	}
	if rep.Stats.Unexpected > 0 || len(rep.Errors) > 0 {
		suite.Status = statusFailed
	}
	for _, err := range rep.Errors {
		if msg := strings.TrimSpace(err.Message); msg != "" {
			suite.Failures = append(suite.Failures, Failure{Name: "Playwright", Output: msg})
		}
	}
	var walk func(pwSuite, string)
	walk = func(s pwSuite, parent string) {
		title := s.Title
		if parent != "" && title != "" {
			title = parent + " " + title
		} else if title == "" {
			title = parent
		}
		for _, spec := range s.Specs {
			if spec.OK {
				continue
			}
			msg := ""
			for _, t := range spec.Tests {
				if len(t.Results) == 0 {
					continue
				}
				last := t.Results[len(t.Results)-1]
				if last.Status == "passed" || last.Status == "skipped" {
					continue
				}
				msg = strings.TrimSpace(last.Error.Message)
			}
			name := strings.TrimSpace(title + " " + spec.Title)
			suite.Failures = append(suite.Failures, Failure{Name: name, Output: msg})
		}
		for _, child := range s.Suites {
			walk(child, title)
		}
	}
	for _, s := range rep.Suites {
		walk(s, "")
	}
	return suite
}

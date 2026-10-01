package main

import "time"

const (
	statusPassed  = "passed"
	statusFailed  = "failed"
	statusSkipped = "skipped"
)

// Failure is one test that did not pass, with a short output excerpt.
type Failure struct {
	Name   string
	Output string
}

// SuiteResult is one invocation of go test, vitest, or Playwright.
type SuiteResult struct {
	Name     string
	Status   string
	Passed   int
	Failed   int
	Skipped  int
	Duration time.Duration
	Failures []Failure
	Detail   string
}

// Report is everything the PDF and the final summary are built from.
type Report struct {
	Started  time.Time
	Finished time.Time
	Git      string
	Suites   []SuiteResult
}

func (r Report) failed() bool {
	for _, s := range r.Suites {
		if s.Status == statusFailed {
			return true
		}
	}
	return false
}

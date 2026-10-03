package version

import (
	"os/exec"
	"strings"
	"sync"
)

const unknown = "v0.0.0+unknown"

// Version is set at link time via -ldflags.
var Version string

var (
	resolved     string
	resolvedOnce sync.Once
)

// Format builds the application version string.
// exactTag is used when HEAD is that tag; otherwise nearestTag+shortSHA.
func Format(exactTag, nearestTag, shortSHA string) string {
	exactTag = strings.TrimSpace(exactTag)
	nearestTag = strings.TrimSpace(nearestTag)
	shortSHA = strings.TrimSpace(shortSHA)
	if exactTag != "" {
		return exactTag
	}
	if nearestTag != "" && shortSHA != "" {
		return nearestTag + "+" + shortSHA
	}
	return unknown
}

// Current returns the application version string.
func Current() string {
	if Version != "" {
		return Version
	}
	resolvedOnce.Do(func() {
		resolved = Format(gitOutput("describe", "--tags", "--exact-match", "HEAD"), gitOutput("describe", "--tags", "--abbrev=0"), gitOutput("rev-parse", "--short", "HEAD"))
	})
	if resolved != "" {
		return resolved
	}
	return unknown
}

func gitOutput(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

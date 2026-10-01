package notifications

import (
	"path"
	"strings"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"
)

// MatchesEvent reports whether target should receive the given event type.
func MatchesEvent(target config.WebhookTarget, event string, defaultEvents []string) bool {
	allowed := target.Events
	if allowed == nil {
		allowed = defaultEvents
	}
	for _, e := range allowed {
		if e == event {
			return true
		}
	}
	return false
}

// MatchesZone reports whether target should receive changes for zone.
func MatchesZone(target config.WebhookTarget, zone string) bool {
	if len(target.Zones) == 0 {
		return true
	}
	if !strings.HasSuffix(zone, ".") {
		zone += "."
	}
	zone = strings.ToLower(zone)
	for _, pattern := range target.Zones {
		normalized := pattern
		if !strings.HasSuffix(normalized, ".") {
			normalized += "."
		}
		normalized = strings.ToLower(normalized)
		if matchZonePattern(zone, normalized) {
			return true
		}
	}
	return false
}

func matchZonePattern(zone, pattern string) bool {
	// path.Match supports * and ? like fnmatch for simple zone globs.
	ok, err := path.Match(pattern, zone)
	return err == nil && ok
}

// TargetByName finds a webhook target by name.
func TargetByName(settings config.WebhookSettings, name string) (config.WebhookTarget, bool) {
	for _, t := range settings.Targets {
		if t.Name == name {
			return t, true
		}
	}
	return config.WebhookTarget{}, false
}

// MatchingTargets returns targets that should receive event for zone.
func MatchingTargets(settings config.WebhookSettings, event, zone string) []config.WebhookTarget {
	var out []config.WebhookTarget
	for _, t := range settings.Targets {
		if MatchesEvent(t, event, settings.Events) && MatchesZone(t, zone) {
			out = append(out, t)
		}
	}
	return out
}

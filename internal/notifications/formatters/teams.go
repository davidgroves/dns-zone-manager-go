package formatters

import (
	"fmt"
	"strings"
)

const maxTeamsOperations = 15

const adaptiveCardContentType = "application/vnd.microsoft.card.adaptive"

func operationText(event Event) string {
	ops := event.GetOperations()
	trimmed := ops
	if len(trimmed) > maxTeamsOperations {
		trimmed = trimmed[:maxTeamsOperations]
	}
	lines := make([]string, 0, len(trimmed)+1)
	for _, op := range trimmed {
		lines = append(lines, op.Describe())
	}
	remaining := len(ops) - len(trimmed)
	if remaining > 0 {
		lines = append(lines, fmt.Sprintf("... and %d more", remaining))
	}
	if len(lines) == 0 {
		return "_(no record operations)_"
	}
	parts := make([]string, len(lines))
	for i, line := range lines {
		parts[i] = "- " + line
	}
	return strings.Join(parts, "\n\n")
}

// FormatTeams renders the event as a Teams Adaptive Card.
func FormatTeams(event Event, baseURL string) map[string]any {
	resultText := "OK"
	if r := event.GetRcode(); r != nil && *r != "" {
		resultText = *r
	} else if !event.Succeeded() {
		resultText = "FAILED"
	}

	facts := []map[string]any{
		{"title": "Zone", "value": event.GetZone()},
		{"title": "Changed by", "value": event.ActorDisplay()},
		{"title": "Change type", "value": event.TriggerDisplay()},
		{"title": "Result", "value": resultText},
	}
	if a := event.GetAuthType(); a != nil && *a != "" {
		facts = append(facts, map[string]any{"title": "Auth", "value": *a})
	}
	if n := event.GetChangeName(); n != nil && *n != "" {
		facts = append(facts, map[string]any{"title": "Change", "value": *n})
	}
	facts = append(facts, map[string]any{"title": "Time", "value": event.GetTimestampISO()})

	color := "Good"
	if !event.Succeeded() {
		color = "Attention"
	}

	body := []map[string]any{
		{
			"type":   "TextBlock",
			"text":   event.Summary(),
			"weight": "Bolder",
			"size":   "Medium",
			"wrap":   true,
			"color":  color,
		},
		{"type": "FactSet", "facts": facts},
		{
			"type":    "TextBlock",
			"text":    "**Records**",
			"wrap":    true,
			"spacing": "Medium",
		},
		{"type": "TextBlock", "text": operationText(event), "wrap": true},
	}

	if e := event.GetError(); e != nil && *e != "" {
		body = append(body, map[string]any{
			"type":  "TextBlock",
			"text":  "**Error:** " + *e,
			"wrap":  true,
			"color": "Attention",
		})
	}

	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body":    body,
	}

	if link := event.Link(baseURL); link != nil {
		card["actions"] = []map[string]any{
			{"type": "Action.OpenUrl", "title": "View change", "url": *link},
		}
	}

	return map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": adaptiveCardContentType,
				"contentUrl":  nil,
				"content":     card,
			},
		},
	}
}

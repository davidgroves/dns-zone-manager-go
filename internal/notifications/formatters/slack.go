package formatters

import (
	"fmt"
	"strings"
)

const maxSlackOperations = 10

func operationLines(event Event, max int) string {
	ops := event.GetOperations()
	trimmed := ops
	if len(trimmed) > max {
		trimmed = trimmed[:max]
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
		return "(no record operations)"
	}
	return strings.Join(lines, "\n")
}

// FormatSlack renders the event as a Slack Block Kit message.
func FormatSlack(event Event, baseURL string) map[string]any {
	icon := ":white_check_mark:"
	if !event.Succeeded() {
		icon = ":x:"
	}
	resultText := "OK"
	if r := event.GetRcode(); r != nil && *r != "" {
		resultText = *r
	} else if !event.Succeeded() {
		resultText = "FAILED"
	}

	fields := []map[string]any{
		{"type": "mrkdwn", "text": "*Zone*\n" + event.GetZone()},
		{"type": "mrkdwn", "text": "*Changed by*\n" + event.ActorDisplay()},
		{"type": "mrkdwn", "text": "*Change type*\n" + event.TriggerDisplay()},
		{"type": "mrkdwn", "text": "*Result*\n" + resultText},
	}
	if a := event.GetAuthType(); a != nil && *a != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": "*Auth*\n" + *a})
	}
	if n := event.GetChangeName(); n != nil && *n != "" {
		fields = append(fields, map[string]any{"type": "mrkdwn", "text": "*Change*\n" + *n})
	}

	blocks := []map[string]any{
		{
			"type": "header",
			"text": map[string]any{"type": "plain_text", "text": icon + " " + event.Summary()},
		},
		{"type": "section", "fields": fields},
		{
			"type": "section",
			"text": map[string]any{
				"type": "mrkdwn",
				"text": "*Records*\n```" + operationLines(event, maxSlackOperations) + "```",
			},
		},
	}

	if e := event.GetError(); e != nil && *e != "" {
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": "*Error*\n" + *e},
		})
	}

	if link := event.Link(baseURL); link != nil {
		blocks = append(blocks, map[string]any{
			"type": "actions",
			"elements": []map[string]any{
				{
					"type": "button",
					"text": map[string]any{"type": "plain_text", "text": "View change"},
					"url":  *link,
				},
			},
		})
	}

	contextParts := []string{event.GetTimestampISO()}
	if rid := event.GetRequestID(); rid != nil && *rid != "" {
		contextParts = append(contextParts, "request "+*rid)
	}
	blocks = append(blocks, map[string]any{
		"type": "context",
		"elements": []map[string]any{
			{"type": "mrkdwn", "text": strings.Join(contextParts, " | ")},
		},
	})

	return map[string]any{
		"text":   event.Summary(),
		"blocks": blocks,
	}
}

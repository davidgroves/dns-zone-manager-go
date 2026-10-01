package formatters

import "fmt"

// Event is the subset of a DNS change event needed by webhook formatters.
type Event interface {
	Succeeded() bool
	Summary() string
	DefaultName() string
	ActorDisplay() string
	TriggerDisplay() string
	Link(baseURL string) *string
	ToDict(baseURL string) map[string]any

	GetZone() string
	GetRcode() *string
	GetError() *string
	GetAuthType() *string
	GetChangeName() *string
	GetRequestID() *string
	GetTimestampISO() string
	GetOperations() []Operation
}

// Operation is a single record op for formatter display.
type Operation struct {
	Action  string
	Name    string
	RDType  string
	RDClass string
	TTL     *int
	Records []string
}

// Describe returns a human-readable one-line summary.
func (op Operation) Describe() string {
	head := fmt.Sprintf("%s %s %s", op.Action, op.RDType, op.Name)
	if len(op.Records) > 0 {
		return head + " -> " + join(op.Records, ", ")
	}
	return head
}

func join(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += sep + parts[i]
	}
	return out
}

// FormatGeneric renders the event as plain structured JSON.
func FormatGeneric(event Event, baseURL string) map[string]any {
	return event.ToDict(baseURL)
}

// FormatPayload selects a formatter by target type name.
func FormatPayload(targetType string, event Event, baseURL string) (map[string]any, error) {
	switch targetType {
	case "", "generic":
		return FormatGeneric(event, baseURL), nil
	case "slack":
		return FormatSlack(event, baseURL), nil
	case "teams":
		return FormatTeams(event, baseURL), nil
	default:
		return nil, fmt.Errorf("unknown webhook target type: %s", targetType)
	}
}

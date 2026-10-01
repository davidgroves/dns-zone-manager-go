package dnsx

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const descriptionMax = 2000

// NSUpdateConversionError is raised when an nsupdate transaction cannot map to a scheduled change.
type NSUpdateConversionError struct {
	Message string
}

func (e *NSUpdateConversionError) Error() string { return e.Message }

// ScheduledChangeDraft is the create payload produced from an nsupdate transaction.
type ScheduledChangeDraft struct {
	Name              string
	Description       string
	Zone              string
	Operations        []Operation
	Prerequisites     []Prerequisite
	AutoPrerequisites bool
}

func formatPrereqLine(p ParsedPrerequisite) string {
	parts := []string{"prereq", p.PrereqType, p.Name}
	if p.PrereqType == "nxrrset" || p.PrereqType == "yxrrset" {
		if p.Class != "" && p.Class != "IN" {
			parts = append(parts, p.Class)
		}
		if p.RdType != "" {
			parts = append(parts, p.RdType)
		}
		if p.Data != "" {
			parts = append(parts, p.Data)
		}
	}
	return strings.Join(parts, " ")
}

func formatUpdateLine(op ParsedOperation) string {
	if op.Action == "add" {
		ttl := uint32(0)
		if op.TTL != nil {
			ttl = *op.TTL
		}
		parts := []string{"update", "add", op.Name, fmt.Sprintf("%d", ttl)}
		if op.Class != "" && op.Class != "IN" {
			parts = append(parts, op.Class)
		}
		if op.RdType != "" {
			parts = append(parts, op.RdType)
		}
		if op.Data != "" {
			parts = append(parts, op.Data)
		}
		return strings.Join(parts, " ")
	}
	parts := []string{"update", "delete", op.Name}
	if op.Class != "" && op.Class != "IN" {
		parts = append(parts, op.Class)
	}
	if op.RdType != "" {
		parts = append(parts, op.RdType)
	}
	if op.Data != "" {
		parts = append(parts, op.Data)
	}
	return strings.Join(parts, " ")
}

func transactionDescription(parsed ParsedUpdate) string {
	lines := []string{"zone " + parsed.Zone}
	for _, p := range parsed.Prerequisites {
		lines = append(lines, formatPrereqLine(p))
	}
	for _, op := range parsed.Operations {
		lines = append(lines, formatUpdateLine(op))
	}
	lines = append(lines, "send")
	text := strings.Join(lines, "\n")
	if utf8.RuneCountInString(text) > descriptionMax {
		runes := []rune(text)
		return string(runes[:descriptionMax-1]) + "…"
	}
	return text
}

func prereqsToChange(prereqs []ParsedPrerequisite) []Prerequisite {
	out := make([]Prerequisite, 0, len(prereqs))
	for _, p := range prereqs {
		class := p.Class
		if class == "" {
			class = "IN"
		}
		out = append(out, Prerequisite{
			Type:   p.PrereqType,
			Name:   p.Name,
			RdType: p.RdType,
			Class:  class,
			Data:   p.Data,
		})
	}
	return out
}

func opsToAtomic(operations []ParsedOperation) ([]Operation, error) {
	var atomic []Operation
	for _, op := range operations {
		if op.Action == "delete" {
			if op.RdType == "" {
				return nil, &NSUpdateConversionError{
					Message: "update delete without a record type cannot be saved as a " +
						"scheduled change; specify the type " +
						"(e.g. 'update delete name A')",
				}
			}
			ttl := uint32(3600)
			if op.TTL != nil {
				ttl = *op.TTL
			}
			class := op.Class
			if class == "" {
				class = "IN"
			}
			var records []string
			if op.Data != "" {
				records = []string{op.Data}
			}
			atomic = append(atomic, Operation{
				Action:  "delete",
				Name:    op.Name,
				Type:    op.RdType,
				Class:   class,
				TTL:     ttl,
				Records: records,
			})
			continue
		}

		if op.RdType == "" {
			return nil, &NSUpdateConversionError{
				Message: fmt.Sprintf("update add requires a record type for %s", op.Name),
			}
		}
		if op.Data == "" {
			return nil, &NSUpdateConversionError{
				Message: fmt.Sprintf("update add requires data for %s %s", op.Name, op.RdType),
			}
		}
		ttl := uint32(3600)
		if op.TTL != nil {
			ttl = *op.TTL
		}
		class := op.Class
		if class == "" {
			class = "IN"
		}

		if len(atomic) > 0 {
			last := &atomic[len(atomic)-1]
			if last.Action == "add" &&
				last.Name == op.Name &&
				last.Type == op.RdType &&
				last.Class == class &&
				last.TTL == ttl &&
				last.Records != nil {
				last.Records = append(last.Records, op.Data)
				continue
			}
		}
		atomic = append(atomic, Operation{
			Action:  "add",
			Name:    op.Name,
			Type:    op.RdType,
			Class:   class,
			TTL:     ttl,
			Records: []string{op.Data},
		})
	}
	return atomic, nil
}

// ParsedUpdateToCreate converts one ParsedUpdate into a draft scheduled change.
func ParsedUpdateToCreate(parsed ParsedUpdate, name string, index, total int, now time.Time) (ScheduledChangeDraft, error) {
	if len(parsed.Operations) == 0 {
		return ScheduledChangeDraft{}, &NSUpdateConversionError{
			Message: fmt.Sprintf(
				"Transaction for zone %s has no update operations (prerequisites alone cannot form a scheduled change)",
				parsed.Zone,
			),
		}
	}
	operations, err := opsToAtomic(parsed.Operations)
	if err != nil {
		return ScheduledChangeDraft{}, err
	}
	prerequisites := prereqsToChange(parsed.Prerequisites)

	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	if name == "" {
		stamp := now.Format("20060102T150405Z")
		zoneLabel := strings.TrimSuffix(parsed.Zone, ".")
		name = fmt.Sprintf("NSUPDATE · %s · %s", zoneLabel, stamp)
		if total > 1 && index > 0 {
			name = fmt.Sprintf("%s · %d", name, index)
		}
	}
	if len(name) > 200 {
		name = name[:200]
	}

	return ScheduledChangeDraft{
		Name:              name,
		Description:       transactionDescription(parsed),
		Zone:              parsed.Zone,
		Operations:        operations,
		Prerequisites:     prerequisites,
		AutoPrerequisites: len(prerequisites) == 0,
	}, nil
}

// NSUpdateTextToCreates parses nsupdate text and converts each send transaction to a draft.
func NSUpdateTextToCreates(text string, defaultZone string, now time.Time) ([]ScheduledChangeDraft, error) {
	parsed, err := ParseNSUpdate(text, defaultZone)
	if err != nil {
		return nil, err
	}
	if len(parsed) == 0 {
		return nil, &NSUpdateConversionError{Message: "No update transactions found in input"}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	total := len(parsed)
	out := make([]ScheduledChangeDraft, 0, total)
	for i, p := range parsed {
		draft, err := ParsedUpdateToCreate(p, "", i+1, total, now)
		if err != nil {
			return nil, err
		}
		out = append(out, draft)
	}
	return out, nil
}

package notifications

import (
	"fmt"
	"strings"
	"time"

	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/notifications/formatters"
)

// Ensure DnsChangeEvent satisfies formatters.Event.
var _ formatters.Event = DnsChangeEvent{}

// Event type constants matching Python.
const (
	EventChangeApplied = "change_applied"
	EventChangeFailed  = "change_failed"
)

// ChangeOperation is a single record operation recovered from a DNS UPDATE.
type ChangeOperation struct {
	Action  string   `json:"action"`
	Name    string   `json:"name"`
	RDType  string   `json:"type"`
	RDClass string   `json:"rdclass"`
	TTL     *int     `json:"ttl"`
	Records []string `json:"records"`
}

// Describe returns a human-readable one-line summary.
func (op ChangeOperation) Describe() string {
	head := fmt.Sprintf("%s %s %s", op.Action, op.RDType, op.Name)
	if len(op.Records) > 0 {
		return head + " -> " + strings.Join(op.Records, ", ")
	}
	return head
}

// DnsChangeEvent is a committed or failed DNS change ready for notification.
type DnsChangeEvent struct {
	Event      string            `json:"event"`
	Zone       string            `json:"zone"`
	Operations []ChangeOperation `json:"operations"`
	Timestamp  time.Time         `json:"timestamp"`
	Trigger    string            `json:"trigger"`
	Actor      *string           `json:"actor"`
	ActorName  *string           `json:"actor_name"`
	ActorEmail *string           `json:"actor_email"`
	AuthType   *string           `json:"auth_type"`
	ChangeID   *string           `json:"change_id"`
	ChangeName *string           `json:"change_name"`
	RequestID  *string           `json:"request_id"`
	Server     *string           `json:"server"`
	Rcode      *string           `json:"rcode"`
	Error      *string           `json:"error"`
	// Autorecord is true when ChangeID was minted and still needs persisting.
	Autorecord bool `json:"autorecord"`
}

// Succeeded reports whether this event represents a successful change.
func (e DnsChangeEvent) Succeeded() bool {
	return e.Event == EventChangeApplied
}

// ActorDisplay returns the best available human-readable actor label.
func (e DnsChangeEvent) ActorDisplay() string {
	if e.ActorName != nil && *e.ActorName != "" {
		return *e.ActorName
	}
	if e.ActorEmail != nil && *e.ActorEmail != "" {
		return *e.ActorEmail
	}
	if e.Actor != nil && *e.Actor != "" {
		return *e.Actor
	}
	return "unknown"
}

// TriggerDisplay returns a human-readable trigger label.
func (e DnsChangeEvent) TriggerDisplay() string {
	switch e.Trigger {
	case TriggerManual:
		return "Manual"
	case TriggerScheduler:
		return "Scheduled"
	case TriggerApplyNow:
		return "Scheduled (applied now)"
	case TriggerRevert:
		return "Revert of a scheduled change"
	default:
		return e.Trigger
	}
}

// Summary is the short title used in notifications.
func (e DnsChangeEvent) Summary() string {
	verb := "DNS change applied"
	if !e.Succeeded() {
		verb = "DNS change failed"
	}
	return verb + ": " + e.Zone
}

// DefaultName is used when auto-recording this change in the scheduler.
func (e DnsChangeEvent) DefaultName() string {
	if e.ChangeName != nil && *e.ChangeName != "" {
		return *e.ChangeName
	}
	if len(e.Operations) == 0 {
		return e.TriggerDisplay() + " change on " + e.Zone
	}
	first := e.Operations[0]
	label := fmt.Sprintf("%s %s %s", first.Action, first.RDType, first.Name)
	if len(e.Operations) > 1 {
		label += fmt.Sprintf(" (+%d more)", len(e.Operations)-1)
	}
	if len(label) > 200 {
		label = label[:200]
	}
	return label
}

// Link returns a deep link to this change, preferring the scheduler view.
func (e DnsChangeEvent) Link(baseURL string) *string {
	if baseURL == "" {
		return nil
	}
	baseURL = strings.TrimRight(baseURL, "/")
	var link string
	if e.ChangeID != nil && *e.ChangeID != "" {
		link = baseURL + "/?view=scheduled&change=" + *e.ChangeID
	} else {
		link = baseURL + "/?zone=" + e.Zone
	}
	return &link
}

// ActorMap serialises the actor block for the generic webhook payload.
func (e DnsChangeEvent) ActorMap() map[string]any {
	return map[string]any{
		"id":        e.Actor,
		"name":      e.ActorName,
		"email":     e.ActorEmail,
		"auth_type": e.AuthType,
	}
}

// GetZone implements formatters.Event.
func (e DnsChangeEvent) GetZone() string { return e.Zone }

// GetRcode implements formatters.Event.
func (e DnsChangeEvent) GetRcode() *string { return e.Rcode }

// GetError implements formatters.Event.
func (e DnsChangeEvent) GetError() *string { return e.Error }

// GetAuthType implements formatters.Event.
func (e DnsChangeEvent) GetAuthType() *string { return e.AuthType }

// GetChangeName implements formatters.Event.
func (e DnsChangeEvent) GetChangeName() *string { return e.ChangeName }

// GetRequestID implements formatters.Event.
func (e DnsChangeEvent) GetRequestID() *string { return e.RequestID }

// GetTimestampISO implements formatters.Event.
func (e DnsChangeEvent) GetTimestampISO() string {
	return formatTimestamp(e.Timestamp)
}

// GetOperations implements formatters.Event.
func (e DnsChangeEvent) GetOperations() []formatters.Operation {
	out := make([]formatters.Operation, len(e.Operations))
	for i, op := range e.Operations {
		out[i] = formatters.Operation{
			Action:  op.Action,
			Name:    op.Name,
			RDType:  op.RDType,
			RDClass: op.RDClass,
			TTL:     op.TTL,
			Records: op.Records,
		}
	}
	return out
}

// ToDict serialises for the generic webhook payload.
func (e DnsChangeEvent) ToDict(baseURL string) map[string]any {
	ops := make([]map[string]any, len(e.Operations))
	for i, op := range e.Operations {
		ops[i] = map[string]any{
			"action":  op.Action,
			"name":    op.Name,
			"type":    op.RDType,
			"rdclass": op.RDClass,
			"ttl":     op.TTL,
			"records": append([]string(nil), op.Records...),
		}
	}
	name := e.DefaultName()
	return map[string]any{
		"event":     e.Event,
		"timestamp": formatTimestamp(e.Timestamp),
		"zone":      e.Zone,
		"trigger":   e.Trigger,
		"actor":     e.ActorMap(),
		"change": map[string]any{
			"id":   e.ChangeID,
			"name": name,
			"link": e.Link(baseURL),
		},
		"operations": ops,
		"result": map[string]any{
			"success": e.Succeeded(),
			"rcode":   e.Rcode,
			"error":   e.Error,
		},
		"server":     e.Server,
		"request_id": e.RequestID,
	}
}

func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	t = t.UTC()
	// Match Python datetime.isoformat() for UTC (+00:00, no Z).
	return t.Format("2006-01-02T15:04:05") + "+00:00"
}

// OperationsFromUpdate recovers record operations from a DNS UPDATE message.
//
// REPLACE is encoded as delete-ANY followed by an add for the same name/type;
// those pairs are coalesced into a single "replace" operation.
func OperationsFromUpdate(msg *dns.Msg) []ChangeOperation {
	if msg == nil {
		return nil
	}
	operations := make([]ChangeOperation, 0, len(msg.Ns))
	lastIndex := map[string]int{}

	for _, rr := range msg.Ns {
		if rr == nil {
			continue
		}
		h := rr.Header()
		name := dns.Fqdn(h.Name)
		rdtype := dns.TypeToString[h.Rrtype]
		if rdtype == "" {
			rdtype = fmt.Sprintf("TYPE%d", h.Rrtype)
		}
		rdclass := dns.ClassToString[h.Class]
		if rdclass == "" {
			rdclass = fmt.Sprintf("CLASS%d", h.Class)
		}

		var records []string
		var ttl *int
		var action string
		// Wire Class ANY/NONE encodes delete intent; presentation class stays IN.
		presentClass := "IN"
		if h.Class != dns.ClassANY && h.Class != dns.ClassNONE {
			presentClass = rdclass
			if presentClass == "" || presentClass == "NONE" || presentClass == "ANY" {
				presentClass = "IN"
			}
		}

		switch h.Class {
		case dns.ClassNONE:
			action = "delete"
			if txt := rdataWithoutHeader(rr); txt != "" {
				records = []string{txt}
			} else {
				records = []string{}
			}
		case dns.ClassANY:
			action = "delete"
			records = []string{}
		default:
			action = "add"
			t := int(h.Ttl)
			ttl = &t
			if txt := rdataWithoutHeader(rr); txt != "" {
				records = []string{txt}
			} else {
				records = []string{}
			}
		}

		key := name + "|" + presentClass + "|" + rdtype

		if action == "add" {
			if prev, ok := lastIndex[key]; ok {
				prevOp := &operations[prev]
				if prevOp.Action == "delete" && len(prevOp.Records) == 0 {
					*prevOp = ChangeOperation{
						Action:  "replace",
						Name:    name,
						RDType:  rdtype,
						RDClass: presentClass,
						TTL:     ttl,
						Records: records,
					}
					continue
				}
				if prevOp.Action == "replace" || prevOp.Action == "add" {
					prevOp.Records = append(prevOp.Records, records...)
					if ttl != nil {
						prevOp.TTL = ttl
					}
					continue
				}
			}
		}

		op := ChangeOperation{
			Action:  action,
			Name:    name,
			RDType:  rdtype,
			RDClass: presentClass,
			TTL:     ttl,
			Records: records,
		}
		lastIndex[key] = len(operations)
		operations = append(operations, op)
	}
	return operations
}

func rdataWithoutHeader(rr dns.RR) string {
	s := strings.TrimSpace(rr.String())
	fields := strings.Fields(s)
	if len(fields) <= 4 {
		return ""
	}
	return strings.Join(fields[4:], " ")
}

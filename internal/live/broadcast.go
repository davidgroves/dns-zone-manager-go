package live

import (
	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
)

// BroadcastApplied fans out a zone_change event with the applied operations.
// No-op when hub is nil or ops is empty — callers should prefer this over
// relying solely on NOTIFY→IXFR after optimistic cache updates.
func BroadcastApplied(h *Hub, zone, trigger string, ops []dnsx.Operation, serial *uint32) {
	if h == nil || len(ops) == 0 {
		return
	}
	payload := map[string]any{
		"type":       "zone_change",
		"event":      "change_applied",
		"trigger":    trigger,
		"change_id":  nil,
		"operations": ops,
	}
	if serial != nil {
		payload["serial"] = *serial
	}
	h.Broadcast(zone, payload)
}

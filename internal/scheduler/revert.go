package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
	"github.com/davidgroves/dns-zone-manager-go/internal/store"
)

// RevertWarning explains the scope of a single-change revert.
const RevertWarning = "This reverts this scheduled change only. Other DNS changes may have been " +
	"made since it was applied; the zone may not return to its earlier overall state."

// RevertOperation is an inverse ADD/DELETE for preview or apply.
type RevertOperation struct {
	Action  string
	Name    string
	Type    string
	RDClass string
	TTL     uint32
	Records []string
}

// CapturePriorState snapshots current RRset state for delete/replace ops before apply.
func CapturePriorState(zone string, operations []store.ScheduledOperation, cache *dnsx.ZoneCache, now time.Time) []store.OpSnapshot {
	if !strings.HasSuffix(zone, ".") {
		zone += "."
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	snapshots := make([]store.OpSnapshot, 0, len(operations))
	for seq, op := range operations {
		action := strings.ToLower(op.Action)
		if action != "delete" && action != "replace" {
			snapshots = append(snapshots, store.OpSnapshot{
				Seq:        seq,
				SnapshotAt: now,
			})
			continue
		}
		info := lookupRRset(cache, zone, op.Name, op.Type)
		if info == nil {
			snapshots = append(snapshots, store.OpSnapshot{
				Seq:        seq,
				SnapshotAt: now,
			})
			continue
		}
		ttl := info.TTL
		recs := append([]string(nil), info.Records...)
		snapshots = append(snapshots, store.OpSnapshot{
			Seq:          seq,
			PriorTTL:     &ttl,
			PriorRecords: recs,
			SnapshotAt:   now,
		})
	}
	return snapshots
}

func lookupRRset(cache *dnsx.ZoneCache, zone, name, typ string) *dnsx.RRsetInfo {
	if cache == nil {
		return nil
	}
	cz := cache.PeekZone(zone)
	if cz == nil || cz.Zone == nil {
		return nil
	}
	typeCode, err := dnsx.TypeFromString(typ)
	if err != nil {
		return nil
	}
	info, ok := cz.Zone.GetRRset(name, typeCode)
	if !ok {
		return nil
	}
	return info
}

// CanRevert reports whether an applied change has enough snapshot data to revert.
func CanRevert(change *store.ScheduledChange) bool {
	if change == nil || change.Status != store.StatusApplied {
		return false
	}
	if len(change.Operations) == 0 {
		return false
	}
	for _, op := range change.Operations {
		if op.SnapshotAt == nil {
			return false
		}
	}
	return true
}

// BuildRevertOps builds inverse ADD/DELETE operations for an applied change.
func BuildRevertOps(change *store.ScheduledChange) ([]RevertOperation, error) {
	if !CanRevert(change) {
		return nil, fmt.Errorf("change cannot be reverted: it is not applied or is missing pre-apply snapshots (applied before revert support)")
	}
	var revertOps []RevertOperation
	for _, op := range change.Operations {
		rdclass := op.RDClass
		if rdclass == "" {
			rdclass = "IN"
		}
		switch strings.ToLower(op.Action) {
		case "add":
			if len(op.Records) == 0 {
				continue
			}
			revertOps = append(revertOps, RevertOperation{
				Action:  "delete",
				Name:    op.Name,
				Type:    op.Type,
				RDClass: rdclass,
				TTL:     op.TTL,
				Records: append([]string(nil), op.Records...),
			})
		case "delete":
			if op.PriorRecords == nil {
				continue
			}
			ttl := op.TTL
			if op.PriorTTL != nil {
				ttl = *op.PriorTTL
			}
			revertOps = append(revertOps, RevertOperation{
				Action:  "add",
				Name:    op.Name,
				Type:    op.Type,
				RDClass: rdclass,
				TTL:     ttl,
				Records: append([]string(nil), op.PriorRecords...),
			})
		case "replace":
			if len(op.Records) > 0 {
				revertOps = append(revertOps, RevertOperation{
					Action:  "delete",
					Name:    op.Name,
					Type:    op.Type,
					RDClass: rdclass,
					TTL:     op.TTL,
					Records: append([]string(nil), op.Records...),
				})
			}
			if op.PriorRecords != nil {
				ttl := op.TTL
				if op.PriorTTL != nil {
					ttl = *op.PriorTTL
				}
				revertOps = append(revertOps, RevertOperation{
					Action:  "add",
					Name:    op.Name,
					Type:    op.Type,
					RDClass: rdclass,
					TTL:     ttl,
					Records: append([]string(nil), op.PriorRecords...),
				})
			}
		}
	}
	return revertOps, nil
}

// RevertOpsToAtomic converts revert preview ops into AtomicOperation for BuildUpdate.
func RevertOpsToAtomic(operations []RevertOperation) []dnsx.Operation {
	out := make([]dnsx.Operation, len(operations))
	for i, op := range operations {
		out[i] = dnsx.Operation{
			Action:  op.Action,
			Name:    op.Name,
			Type:    op.Type,
			Class:   op.RDClass,
			TTL:     op.TTL,
			Records: op.Records,
		}
	}
	return out
}

// ForwardOpsToAtomic strips snapshot fields for DDNS construction.
func ForwardOpsToAtomic(operations []store.ScheduledOperation) []dnsx.Operation {
	out := make([]dnsx.Operation, len(operations))
	for i, op := range operations {
		class := op.RDClass
		if class == "" {
			class = "IN"
		}
		out[i] = dnsx.Operation{
			Action:  op.Action,
			Name:    op.Name,
			Type:    op.Type,
			Class:   class,
			TTL:     op.TTL,
			Records: op.Records,
		}
	}
	return out
}

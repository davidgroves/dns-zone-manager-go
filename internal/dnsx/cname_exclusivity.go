package dnsx

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// DNSSEC companion types may coexist with CNAME at an owner name.
var dnssecCompanionTypes = map[string]struct{}{
	"RRSIG": {}, "NSEC": {}, "NSEC3": {},
}

// CNAMEConflictError is returned when an add would violate CNAME exclusivity.
type CNAMEConflictError struct {
	Message          string
	Name             string
	Type             string
	ConflictingTypes []string
	Index            *int
}

func (e *CNAMEConflictError) Error() string {
	return e.Message
}

// OwnerTypeOp is a delete/add/replace affecting types at an owner (for simulation).
type OwnerTypeOp struct {
	Action string // add | delete | replace
	Name   string
	RdType string // empty on delete means delete all types at the name
	Index  *int
}

func materialTypes(existing []string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, t := range existing {
		u := strings.ToUpper(strings.TrimSpace(t))
		if u == "" {
			continue
		}
		if _, companion := dnssecCompanionTypes[u]; companion {
			continue
		}
		out[u] = struct{}{}
	}
	return out
}

// CheckCNAMEExclusivity returns an error if newType cannot coexist with existingTypes.
func CheckCNAMEExclusivity(existingTypes []string, newType string) error {
	adding := strings.ToUpper(strings.TrimSpace(newType))
	present := materialTypes(existingTypes)
	conflicts := conflictingTypesForAdd(present, adding)
	if len(conflicts) == 0 {
		return nil
	}
	name := "" // caller may wrap; message matches Python without name when unknown
	msg := formatCNAMEConflictMessage(name, adding, conflicts, nil)
	return &CNAMEConflictError{
		Message:          msg,
		Type:             adding,
		ConflictingTypes: conflicts,
	}
}

func conflictingTypesForAdd(present map[string]struct{}, adding string) []string {
	if _, ok := dnssecCompanionTypes[adding]; ok {
		return nil
	}
	if adding == "CNAME" {
		var out []string
		for t := range present {
			if t != "CNAME" {
				out = append(out, t)
			}
		}
		sort.Strings(out)
		return out
	}
	if _, hasCNAME := present["CNAME"]; hasCNAME {
		return []string{"CNAME"}
	}
	return nil
}

func formatCNAMEConflictMessage(name, adding string, conflicting []string, index *int) string {
	types := strings.Join(conflicting, ", ")
	prefix := ""
	if index != nil {
		prefix = fmt.Sprintf("Operation %d: ", *index)
	}
	if adding == "CNAME" {
		if name == "" {
			return fmt.Sprintf("%sCannot add CNAME: name already has %s. Delete those records first (or include deletes in the same atomic update).", prefix, types)
		}
		return fmt.Sprintf("%sCannot add CNAME at %s: name already has %s. Delete those records first (or include deletes in the same atomic update).", prefix, name, types)
	}
	if name == "" {
		return fmt.Sprintf("%sCannot add %s: name already has a CNAME. Delete the CNAME first (or include the delete in the same atomic update).", prefix, adding)
	}
	return fmt.Sprintf("%sCannot add %s at %s: name already has a CNAME. Delete the CNAME first (or include the delete in the same atomic update).", prefix, adding, name)
}

// CheckCNAMEExclusivityAt is like CheckCNAMEExclusivity but includes the owner name in errors.
func CheckCNAMEExclusivityAt(name string, existingTypes []string, newType string) error {
	if err := CheckCNAMEExclusivity(existingTypes, newType); err != nil {
		var ce *CNAMEConflictError
		if errors.As(err, &ce) {
			adding := strings.ToUpper(strings.TrimSpace(newType))
			conflicts := ce.ConflictingTypes
			ce.Name = name
			ce.Message = formatCNAMEConflictMessage(name, adding, conflicts, nil)
		}
		return err
	}
	return nil
}

// SimulateCNAMEExclusivity walks delete/add/replace ops in order; returns the first conflict.
func SimulateCNAMEExclusivity(initialTypes map[string]map[string]struct{}, ops []OwnerTypeOp) *CNAMEConflictError {
	state := make(map[string]map[string]struct{}, len(initialTypes))
	for name, types := range initialTypes {
		cp := make(map[string]struct{}, len(types))
		for t := range types {
			cp[t] = struct{}{}
		}
		state[name] = cp
	}

	for _, op := range ops {
		types := state[op.Name]
		if types == nil {
			types = make(map[string]struct{})
			state[op.Name] = types
		}

		action := strings.ToLower(op.Action)
		if action == "delete" {
			if op.RdType == "" {
				clear(types)
			} else {
				delete(types, strings.ToUpper(op.RdType))
			}
			continue
		}
		if op.RdType == "" {
			continue
		}
		rdtype := strings.ToUpper(op.RdType)
		if action == "replace" {
			delete(types, rdtype)
		}

		existing := make([]string, 0, len(types))
		for t := range types {
			existing = append(existing, t)
		}
		conflicts := conflictingTypesForAdd(materialTypes(existing), rdtype)
		if len(conflicts) > 0 {
			return &CNAMEConflictError{
				Message:          formatCNAMEConflictMessage(op.Name, rdtype, conflicts, op.Index),
				Name:             op.Name,
				Type:             rdtype,
				ConflictingTypes: conflicts,
				Index:            op.Index,
			}
		}
		types[rdtype] = struct{}{}
	}
	return nil
}

// OpsDeleteCNAMEOrAll reports whether ops include a delete of CNAME or all types at name.
func OpsDeleteCNAMEOrAll(ops []OwnerTypeOp, name string) bool {
	for _, op := range ops {
		if strings.ToLower(op.Action) != "delete" || op.Name != name {
			continue
		}
		if op.RdType == "" || strings.EqualFold(op.RdType, "CNAME") {
			return true
		}
	}
	return false
}

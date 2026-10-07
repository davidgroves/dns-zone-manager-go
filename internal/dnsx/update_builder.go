package dnsx

import (
	"fmt"
	"strings"

	"github.com/miekg/dns"
)

// Operation is a single add/delete/replace within a DNS UPDATE.
// JSON names match the live WebSocket payload the UI applies.
type Operation struct {
	Action  string   `json:"action"` // add | delete | replace
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Class   string   `json:"rdclass"`
	TTL     uint32   `json:"ttl"`
	Records []string `json:"records"`
}

// Prerequisite is an explicit RFC 2136 prerequisite.
type Prerequisite struct {
	Type   string // nxdomain | yxdomain | nxrrset | yxrrset
	Name   string
	RdType string
	Class  string
	Data   string
}

// CacheUpdate describes an optimistic cache mutation after a successful DDNS update.
type CacheUpdate struct {
	Action  string // add | delete | replace
	Name    string
	RdType  string
	Class   string
	TTL     uint32
	Records []string // nil means whole RRset for delete
}

// PrerequisitePreview is a dry-run evaluation of a prerequisite.
type PrerequisitePreview struct {
	PrereqType string
	Name       string
	RdType     string
	Class      string
	Data       string
	Passed     bool
	Message    string
	Source     string // auto | explicit
}

// BuiltUpdate is the result of building a DNS UPDATE message.
type BuiltUpdate struct {
	Msg               *dns.Msg
	CacheUpdates      []CacheUpdate
	AutoPrerequisites []PrerequisitePreview
}

// UpdateBuildError is raised when an operation cannot be built into an UPDATE.
type UpdateBuildError struct {
	Message string
	Index   *int
	Code    string
}

func (e *UpdateBuildError) Error() string { return e.Message }

func updateBuildErr(index int, code, format string, args ...any) *UpdateBuildError {
	idx := index
	return &UpdateBuildError{
		Message: fmt.Sprintf(format, args...),
		Index:   &idx,
		Code:    code,
	}
}

// LookupFunc returns cached RRset presentation records for owner name and type.
type LookupFunc func(name, typ string) (ttl uint32, records []string, ok bool)

// TypesAtNameFunc returns type names present at an owner (for CNAME simulation).
type TypesAtNameFunc func(name string) []string

// BuildUpdate builds a single DNS UPDATE message from operations and prerequisites.
//
// Auto-derived prerequisites match Python:
//   - Atomic ADD: typed NXRRSET; optional CNAME-absent unless same UPDATE deletes CNAME/all at name
//   - NEVER NXDOMAIN in multi-op builds
//   - Delete/replace: YXRRSET with each cached rdata, or type-only when cache miss
func BuildUpdate(
	zone string,
	ops []Operation,
	prereqs []Prerequisite,
	autoPrereqs bool,
	validateCacheState bool,
	lookup LookupFunc,
	typesAtName TypesAtNameFunc,
) (*BuiltUpdate, error) {
	zone = NormalizeZoneName(zone)
	msg := new(dns.Msg)
	msg.SetUpdate(zone)

	if lookup == nil {
		lookup = func(string, string) (uint32, []string, bool) { return 0, nil, false }
	}

	if err := applyExplicitPrerequisites(msg, zone, prereqs); err != nil {
		return nil, err
	}

	simOps := make([]OwnerTypeOp, 0, len(ops))
	fqdns := make([]string, len(ops))
	for i, op := range ops {
		fqdn, err := RequireNameInZone(op.Name, zone)
		if err != nil {
			return nil, updateBuildErr(i, "INVALID_NAME", "Operation %d: %v", i, err)
		}
		fqdns[i] = fqdn
		idx := i
		rdtype := strings.ToUpper(op.Type)
		simOps = append(simOps, OwnerTypeOp{
			Action: strings.ToLower(op.Action),
			Name:   fqdn,
			RdType: rdtype,
			Index:  &idx,
		})
	}

	initialTypes := make(map[string]map[string]struct{})
	for _, sim := range simOps {
		if _, ok := initialTypes[sim.Name]; ok {
			continue
		}
		set := make(map[string]struct{})
		if typesAtName != nil {
			for _, t := range typesAtName(sim.Name) {
				if t = strings.ToUpper(strings.TrimSpace(t)); t != "" {
					set[t] = struct{}{}
				}
			}
		}
		initialTypes[sim.Name] = set
	}

	if conflict := SimulateCNAMEExclusivity(initialTypes, simOps); conflict != nil {
		return nil, &UpdateBuildError{
			Message: conflict.Message,
			Index:   conflict.Index,
			Code:    "CNAME_CONFLICT",
		}
	}

	var cacheUpdates []CacheUpdate
	var autoPreview []PrerequisitePreview

	for i, op := range ops {
		action := strings.ToLower(op.Action)
		rdtype := strings.ToUpper(op.Type)
		rdclass := op.Class
		if rdclass == "" {
			rdclass = "IN"
		}
		normClass, err := NormalizeClass(rdclass)
		if err != nil {
			return nil, updateBuildErr(i, "INVALID_CLASS", "Operation %d: %v", i, err)
		}
		rdclass = normClass
		classCode, err := ClassFromString(rdclass)
		if err != nil {
			return nil, updateBuildErr(i, "INVALID_CLASS", "Operation %d: %v", i, err)
		}
		typeCode, err := TypeFromString(rdtype)
		if err != nil {
			return nil, updateBuildErr(i, "INVALID_TYPE", "Operation %d: %v", i, err)
		}
		fqdn := fqdns[i]

		switch action {
		case "add":
			if len(op.Records) == 0 {
				return nil, updateBuildErr(i, "MISSING_RECORDS", "Operation %d: 'add' requires records", i)
			}
			_, _, exists := lookup(op.Name, rdtype)
			if validateCacheState && exists {
				return nil, updateBuildErr(i, "RRSET_EXISTS",
					"Operation %d: RRset %s %s %s already exists", i, op.Name, rdclass, rdtype)
			}
			if autoPrereqs {
				stub := &dns.ANY{Hdr: dns.RR_Header{Name: fqdn, Rrtype: typeCode}}
				msg.RRsetNotUsed([]dns.RR{stub})
				autoPreview = append(autoPreview, PrerequisitePreview{
					PrereqType: "nxrrset",
					Name:       op.Name,
					RdType:     rdtype,
					Class:      rdclass,
					Passed:     !exists,
					Message:    "auto nxrrset for add",
					Source:     "auto",
				})
				if rdtype != "CNAME" && !OpsDeleteCNAMEOrAll(simOps, fqdn) {
					cnameStub := &dns.ANY{Hdr: dns.RR_Header{Name: fqdn, Rrtype: dns.TypeCNAME}}
					msg.RRsetNotUsed([]dns.RR{cnameStub})
					_, _, cnameExists := lookup(op.Name, "CNAME")
					autoPreview = append(autoPreview, PrerequisitePreview{
						PrereqType: "nxrrset",
						Name:       op.Name,
						RdType:     "CNAME",
						Class:      rdclass,
						Passed:     !cnameExists,
						Message:    "auto nxrrset CNAME for non-CNAME add",
						Source:     "auto",
					})
				}
			}
			rrs, err := buildRRs(fqdn, typeCode, classCode, op.TTL, op.Records)
			if err != nil {
				return nil, updateBuildErr(i, "INVALID_RDATA", "Operation %d: %v", i, err)
			}
			msg.Insert(rrs)
			cacheUpdates = append(cacheUpdates, CacheUpdate{
				Action:  "add",
				Name:    op.Name,
				RdType:  rdtype,
				Class:   rdclass,
				TTL:     op.TTL,
				Records: append([]string(nil), op.Records...),
			})

		case "delete":
			_, cachedRecs, exists := lookup(op.Name, rdtype)
			if validateCacheState && !exists {
				return nil, updateBuildErr(i, "RRSET_NOT_FOUND",
					"Operation %d: RRset %s %s %s not found", i, op.Name, rdclass, rdtype)
			}
			if autoPrereqs {
				if exists && len(cachedRecs) > 0 {
					prereqRRs, err := buildRRs(fqdn, typeCode, classCode, 0, cachedRecs)
					if err != nil {
						return nil, updateBuildErr(i, "INVALID_RDATA", "Operation %d: %v", i, err)
					}
					msg.Used(prereqRRs)
				} else {
					stub := &dns.ANY{Hdr: dns.RR_Header{Name: fqdn, Rrtype: typeCode}}
					msg.RRsetUsed([]dns.RR{stub})
				}
				autoPreview = append(autoPreview, PrerequisitePreview{
					PrereqType: "yxrrset",
					Name:       op.Name,
					RdType:     rdtype,
					Class:      rdclass,
					Passed:     exists,
					Message:    "auto yxrrset for delete",
					Source:     "auto",
				})
			}
			if len(op.Records) > 0 {
				rrs, err := buildRRs(fqdn, typeCode, classCode, 0, op.Records)
				if err != nil {
					return nil, updateBuildErr(i, "INVALID_RDATA", "Operation %d: %v", i, err)
				}
				msg.Remove(rrs)
			} else {
				stub := &dns.ANY{Hdr: dns.RR_Header{Name: fqdn, Rrtype: typeCode}}
				msg.RemoveRRset([]dns.RR{stub})
			}
			var delRecs []string
			if op.Records != nil {
				delRecs = append([]string(nil), op.Records...)
			}
			cacheUpdates = append(cacheUpdates, CacheUpdate{
				Action:  "delete",
				Name:    op.Name,
				RdType:  rdtype,
				Class:   rdclass,
				Records: delRecs,
			})

		case "replace":
			if len(op.Records) == 0 {
				return nil, updateBuildErr(i, "MISSING_RECORDS", "Operation %d: 'replace' requires records", i)
			}
			_, cachedRecs, exists := lookup(op.Name, rdtype)
			if validateCacheState && !exists {
				return nil, updateBuildErr(i, "RRSET_NOT_FOUND",
					"Operation %d: RRset %s %s %s not found (use add)", i, op.Name, rdclass, rdtype)
			}
			if autoPrereqs {
				if exists && len(cachedRecs) > 0 {
					prereqRRs, err := buildRRs(fqdn, typeCode, classCode, 0, cachedRecs)
					if err != nil {
						return nil, updateBuildErr(i, "INVALID_RDATA", "Operation %d: %v", i, err)
					}
					msg.Used(prereqRRs)
				} else {
					stub := &dns.ANY{Hdr: dns.RR_Header{Name: fqdn, Rrtype: typeCode}}
					msg.RRsetUsed([]dns.RR{stub})
				}
				autoPreview = append(autoPreview, PrerequisitePreview{
					PrereqType: "yxrrset",
					Name:       op.Name,
					RdType:     rdtype,
					Class:      rdclass,
					Passed:     exists,
					Message:    "auto yxrrset for replace",
					Source:     "auto",
				})
			}
			stub := &dns.ANY{Hdr: dns.RR_Header{Name: fqdn, Rrtype: typeCode}}
			msg.RemoveRRset([]dns.RR{stub})
			rrs, err := buildRRs(fqdn, typeCode, classCode, op.TTL, op.Records)
			if err != nil {
				return nil, updateBuildErr(i, "INVALID_RDATA", "Operation %d: %v", i, err)
			}
			msg.Insert(rrs)
			cacheUpdates = append(cacheUpdates, CacheUpdate{
				Action:  "replace",
				Name:    op.Name,
				RdType:  rdtype,
				Class:   rdclass,
				TTL:     op.TTL,
				Records: append([]string(nil), op.Records...),
			})

		default:
			return nil, updateBuildErr(i, "UNKNOWN_ACTION", "Operation %d: unknown action '%s'", i, op.Action)
		}
	}

	return &BuiltUpdate{
		Msg:               msg,
		CacheUpdates:      cacheUpdates,
		AutoPrerequisites: autoPreview,
	}, nil
}

func applyExplicitPrerequisites(msg *dns.Msg, zone string, prereqs []Prerequisite) error {
	for i, p := range prereqs {
		fqdn, err := RequireNameInZone(p.Name, zone)
		if err != nil {
			return fmt.Errorf("prerequisite %d: %w", i, err)
		}
		switch strings.ToLower(p.Type) {
		case "nxdomain":
			msg.NameNotUsed([]dns.RR{&dns.ANY{Hdr: dns.RR_Header{Name: fqdn}}})
		case "yxdomain":
			msg.NameUsed([]dns.RR{&dns.ANY{Hdr: dns.RR_Header{Name: fqdn}}})
		case "nxrrset":
			tc, err := TypeFromString(p.RdType)
			if err != nil {
				return fmt.Errorf("prerequisite %d: %w", i, err)
			}
			msg.RRsetNotUsed([]dns.RR{&dns.ANY{Hdr: dns.RR_Header{Name: fqdn, Rrtype: tc}}})
		case "yxrrset":
			tc, err := TypeFromString(p.RdType)
			if err != nil {
				return fmt.Errorf("prerequisite %d: %w", i, err)
			}
			if p.Data != "" {
				className := p.Class
				if className == "" {
					className = "IN"
				}
				cc, err := ClassFromString(className)
				if err != nil {
					return fmt.Errorf("prerequisite %d: %w", i, err)
				}
				rrs, err := buildRRs(fqdn, tc, cc, 0, []string{p.Data})
				if err != nil {
					return fmt.Errorf("prerequisite %d: %w", i, err)
				}
				msg.Used(rrs)
			} else {
				msg.RRsetUsed([]dns.RR{&dns.ANY{Hdr: dns.RR_Header{Name: fqdn, Rrtype: tc}}})
			}
		default:
			return fmt.Errorf("prerequisite %d: unknown type %q", i, p.Type)
		}
	}
	return nil
}

func buildRRs(fqdn string, typ, class uint16, ttl uint32, records []string) ([]dns.RR, error) {
	out := make([]dns.RR, 0, len(records))
	for _, text := range records {
		rr, err := ParseRdata(typ, class, text)
		if err != nil {
			return nil, err
		}
		rr.Header().Name = fqdn
		rr.Header().Ttl = ttl
		rr.Header().Class = class
		rr.Header().Rrtype = typ
		out = append(out, rr)
	}
	return out, nil
}

// ApplyCacheUpdates applies optimistic cache mutations after a successful DDNS update.
func ApplyCacheUpdates(z *Zone, updates []CacheUpdate) error {
	if z == nil {
		return fmt.Errorf("nil zone")
	}
	for _, cu := range updates {
		className := cu.Class
		if className == "" {
			className = "IN"
		}
		classCode, err := ClassFromString(className)
		if err != nil {
			return err
		}
		typeCode, err := TypeFromString(cu.RdType)
		if err != nil {
			return err
		}
		switch strings.ToLower(cu.Action) {
		case "add":
			if err := z.AddRecords(cu.Name, typeCode, classCode, cu.TTL, cu.Records); err != nil {
				return err
			}
		case "delete":
			if err := z.DeleteRecords(cu.Name, typeCode, classCode, cu.Records); err != nil {
				return err
			}
		case "replace":
			if err := z.ReplaceRRset(cu.Name, typeCode, classCode, cu.TTL, cu.Records); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown cache update action %q", cu.Action)
		}
	}
	return nil
}

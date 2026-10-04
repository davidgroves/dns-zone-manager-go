package dnsx

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/miekg/dns"
)

// RRset holds records sharing owner, class, and type.
type RRset struct {
	TTL     uint32
	Class   uint16
	Records []dns.RR
}

// RRsetInfo is API-facing RRset metadata with presentation record strings.
type RRsetInfo struct {
	Name    string
	Type    string
	Class   string
	TTL     uint32
	Records []string
}

// RRsetCursor is an opaque pagination cursor for RRset listing.
type RRsetCursor struct {
	Name string `json:"n"`
	Type string `json:"t"`
}

// NameCursor is an opaque pagination cursor for name-ordered search.
type NameCursor struct {
	Name string `json:"n"`
}

type indexKey struct {
	owner string
	typ   uint16
}

// Zone is an in-memory DNS zone keyed by relative owner name.
type Zone struct {
	mu          sync.RWMutex
	Origin      string
	rrsets      map[string]map[uint16]*RRset
	index       []indexKey
	indexDirty  bool
	rrsetCount  int
	recordCount int
}

// NewZone creates an empty zone with a normalized origin.
func NewZone(origin string) *Zone {
	return &Zone{
		Origin: NormalizeZoneName(origin),
		rrsets: make(map[string]map[uint16]*RRset),
	}
}

// SetFromAXFR replaces zone contents from AXFR/IXFR answer RRs (TSIG excluded).
func (z *Zone) SetFromAXFR(rrs []dns.RR) error {
	z.mu.Lock()
	defer z.mu.Unlock()

	z.rrsets = make(map[string]map[uint16]*RRset)
	z.rrsetCount = 0
	z.recordCount = 0

	for _, rr := range dropAXFRTrailerSOA(rrs) {
		if rr == nil {
			continue
		}
		h := rr.Header()
		if h.Rrtype == dns.TypeTSIG {
			continue
		}
		key, err := ownerKey(h.Name, z.Origin)
		if err != nil {
			return err
		}
		z.insertRR(key, rr)
	}
	z.rebuildIndex()
	return nil
}

// dropAXFRTrailerSOA removes the RFC 5936 end-of-stream SOA. AXFR answers are
// SOA … SOA; the final SOA is a delimiter, not a second copy of the RRset.
func dropAXFRTrailerSOA(rrs []dns.RR) []dns.RR {
	first, last := -1, -1
	for i, rr := range rrs {
		if rr == nil || rr.Header().Rrtype == dns.TypeTSIG {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first < 0 || first == last {
		return rrs
	}
	if rrs[first].Header().Rrtype != dns.TypeSOA || rrs[last].Header().Rrtype != dns.TypeSOA {
		return rrs
	}
	out := make([]dns.RR, 0, len(rrs)-1)
	for i, rr := range rrs {
		if i == last {
			continue
		}
		out = append(out, rr)
	}
	return out
}

func (z *Zone) insertRR(ownerKey string, rr dns.RR) {
	h := rr.Header()
	class := h.Class
	typ := h.Rrtype
	types, ok := z.rrsets[ownerKey]
	if !ok {
		types = make(map[uint16]*RRset)
		z.rrsets[ownerKey] = types
	}
	rs, ok := types[typ]
	if !ok {
		rs = &RRset{Class: class, TTL: h.Ttl}
		types[typ] = rs
		z.rrsetCount++
	} else if h.Ttl != 0 {
		rs.TTL = h.Ttl
	}
	rdata := RdataText(rr)
	for _, existing := range rs.Records {
		if RdataText(existing) == rdata {
			return
		}
	}
	rs.Records = append(rs.Records, dns.Copy(rr))
	z.recordCount++
}

func (z *Zone) rebuildIndex() {
	z.index = z.index[:0]
	for owner, types := range z.rrsets {
		for typ := range types {
			z.index = append(z.index, indexKey{owner: owner, typ: typ})
		}
	}
	sort.Slice(z.index, func(i, j int) bool {
		ni := strings.ToLower(fqdnFromKey(z.index[i].owner, z.Origin))
		nj := strings.ToLower(fqdnFromKey(z.index[j].owner, z.Origin))
		if ni != nj {
			return ni < nj
		}
		return z.index[i].typ < z.index[j].typ
	})
	z.indexDirty = false
}

// ensureIndexLocked rebuilds the sorted index if writes left it dirty.
// Caller must hold z.mu (write lock).
func (z *Zone) ensureIndexLocked() {
	if z.indexDirty {
		z.rebuildIndex()
	}
}

// SOASerial returns the apex SOA serial if present.
func (z *Zone) SOASerial() (uint32, bool) {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.soaSerialLocked()
}

func (z *Zone) soaSerialLocked() (uint32, bool) {
	types := z.rrsets[""]
	if types == nil {
		return 0, false
	}
	rs := types[dns.TypeSOA]
	if rs == nil || len(rs.Records) == 0 {
		return 0, false
	}
	soa, ok := rs.Records[0].(*dns.SOA)
	if !ok {
		return 0, false
	}
	return soa.Serial, true
}

// SOARefresh returns the apex SOA refresh interval if present.
func (z *Zone) SOARefresh() (uint32, bool) {
	z.mu.RLock()
	defer z.mu.RUnlock()
	types := z.rrsets[""]
	if types == nil {
		return 0, false
	}
	rs := types[dns.TypeSOA]
	if rs == nil || len(rs.Records) == 0 {
		return 0, false
	}
	soa, ok := rs.Records[0].(*dns.SOA)
	if !ok {
		return 0, false
	}
	return soa.Refresh, true
}

// SetSOASerial rewrites the apex SOA while preserving other fields.
func (z *Zone) SetSOASerial(serial uint32) error {
	z.mu.Lock()
	defer z.mu.Unlock()

	types := z.rrsets[""]
	if types == nil {
		return fmt.Errorf("no SOA record in zone")
	}
	rs := types[dns.TypeSOA]
	if rs == nil || len(rs.Records) == 0 {
		return fmt.Errorf("no SOA record in zone")
	}
	old, ok := rs.Records[0].(*dns.SOA)
	if !ok {
		return fmt.Errorf("invalid SOA record")
	}
	newSOA := &dns.SOA{
		Hdr:     old.Hdr,
		Ns:      old.Ns,
		Mbox:    old.Mbox,
		Serial:  serial,
		Refresh: old.Refresh,
		Retry:   old.Retry,
		Expire:  old.Expire,
		Minttl:  old.Minttl,
	}
	newSOA.Hdr.Name = fqdnFromKey("", z.Origin)
	rs.Records[0] = newSOA
	return nil
}

// GetRRset returns RRset metadata for name and type.
func (z *Zone) GetRRset(name string, typ uint16) (*RRsetInfo, bool) {
	z.mu.RLock()
	defer z.mu.RUnlock()

	key, err := z.resolveOwnerKey(name)
	if err != nil {
		return nil, false
	}
	types := z.rrsets[key]
	if types == nil {
		return nil, false
	}
	rs := types[typ]
	if rs == nil {
		return nil, false
	}
	return z.rrsetInfoLocked(key, typ, rs), true
}

func (z *Zone) rrsetInfoLocked(owner string, typ uint16, rs *RRset) *RRsetInfo {
	recs := make([]string, len(rs.Records))
	for i, rr := range rs.Records {
		recs[i] = RdataText(rr)
	}
	return &RRsetInfo{
		Name:    fqdnFromKey(owner, z.Origin),
		Type:    TypeName(typ),
		Class:   ClassName(rs.Class),
		TTL:     rs.TTL,
		Records: recs,
	}
}

// ListRRsets returns paginated RRsets. offset takes priority over afterCursor.
func (z *Zone) ListRRsets(offset, limit int, afterCursor *RRsetCursor) (items []RRsetInfo, total int, next *RRsetCursor, hasMore bool) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.ensureIndexLocked()

	all := make([]RRsetInfo, 0, z.rrsetCount)
	for _, k := range z.index {
		types := z.rrsets[k.owner]
		rs := types[k.typ]
		if rs == nil {
			continue
		}
		all = append(all, *z.rrsetInfoLocked(k.owner, k.typ, rs))
	}
	total = len(all)

	start := 0
	if offset > 0 {
		if offset > total {
			start = total
		} else {
			start = offset
		}
	} else if afterCursor != nil {
		cursorName := strings.ToLower(afterCursor.Name)
		cursorType := strings.ToUpper(afterCursor.Type)
		found := false
		for i, item := range all {
			key := strings.ToLower(item.Name)
			if key > cursorName || (key == cursorName && item.Type >= cursorType) {
				start = i
				found = true
				break
			}
		}
		if !found {
			start = total
		}
	}

	end := total
	if limit > 0 && start+limit < end {
		end = start + limit
		hasMore = end < total
	}
	items = all[start:end]
	if hasMore && end < total {
		nxt := all[end]
		next = &RRsetCursor{Name: nxt.Name, Type: nxt.Type}
	}
	return items, total, next, hasMore
}

// Search finds RRsets matching optional patterns and type filter.
func (z *Zone) Search(namePattern, valuePattern *regexp.Regexp, typ *uint16, offset, limit int, after *NameCursor) (items []RRsetInfo, total int, next *NameCursor, hasMore bool) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.ensureIndexLocked()

	var matches []RRsetInfo
	for _, k := range z.index {
		types := z.rrsets[k.owner]
		rs := types[k.typ]
		if rs == nil {
			continue
		}
		if typ != nil && k.typ != *typ {
			continue
		}
		info := z.rrsetInfoLocked(k.owner, k.typ, rs)
		if namePattern != nil && !namePattern.MatchString(info.Name) {
			continue
		}
		recs := info.Records
		if valuePattern != nil {
			filtered := recs[:0]
			for _, r := range recs {
				if valuePattern.MatchString(r) {
					filtered = append(filtered, r)
				}
			}
			if len(filtered) == 0 {
				continue
			}
			info.Records = append([]string(nil), filtered...)
		}
		matches = append(matches, *info)
	}

	sort.Slice(matches, func(i, j int) bool {
		ni := strings.ToLower(matches[i].Name)
		nj := strings.ToLower(matches[j].Name)
		if ni != nj {
			return ni < nj
		}
		return matches[i].Type < matches[j].Type
	})
	total = len(matches)

	start := 0
	if offset > 0 {
		if offset > total {
			start = total
		} else {
			start = offset
		}
	} else if after != nil {
		cursor := strings.ToLower(after.Name)
		for i, item := range matches {
			if strings.ToLower(item.Name) >= cursor {
				start = i
				break
			}
		}
	}

	end := total
	if limit > 0 && start+limit < end {
		end = start + limit
		hasMore = end < total
	}
	items = matches[start:end]
	if hasMore && end < total {
		next = &NameCursor{Name: matches[end].Name}
	}
	return items, total, next, hasMore
}

// AddRecords appends rdata strings to an RRset (optimistic cache update).
func (z *Zone) AddRecords(name string, typ, class uint16, ttl uint32, rdata []string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.addRecordsLocked(name, typ, class, ttl, rdata)
}

func (z *Zone) addRecordsLocked(name string, typ, class uint16, ttl uint32, rdata []string) error {
	key, err := z.resolveOwnerKey(name)
	if err != nil {
		return err
	}
	types := z.rrsets[key]
	if types == nil {
		types = make(map[uint16]*RRset)
		z.rrsets[key] = types
	}
	rs := types[typ]
	if rs == nil {
		rs = &RRset{Class: class, TTL: ttl}
		types[typ] = rs
		z.rrsetCount++
		z.insertIndexKey(key, typ)
	} else if ttl != 0 {
		rs.TTL = ttl
	}
	fqdn := fqdnFromKey(key, z.Origin)
	for _, text := range rdata {
		parsed, err := ParseRdata(typ, class, text)
		if err != nil {
			return err
		}
		parsed.Header().Name = fqdn
		parsed.Header().Rdlength = 0
		parsed.Header().Ttl = rs.TTL
		parsed.Header().Class = class
		rs.Records = append(rs.Records, parsed)
		z.recordCount++
	}
	return nil
}

// DeleteRecords removes specific rdata values, or the entire RRset when rdata is nil.
func (z *Zone) DeleteRecords(name string, typ, class uint16, rdata []string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.deleteRecordsLocked(name, typ, class, rdata)
}

func (z *Zone) deleteRecordsLocked(name string, typ, class uint16, rdata []string) error {
	key, err := z.resolveOwnerKey(name)
	if err != nil {
		return err
	}
	types := z.rrsets[key]
	if types == nil {
		return nil
	}
	rs := types[typ]
	if rs == nil {
		return nil
	}
	if rdata == nil {
		z.recordCount -= len(rs.Records)
		delete(types, typ)
		z.rrsetCount--
		if len(types) == 0 {
			delete(z.rrsets, key)
		}
		z.removeIndexKey(key, typ)
		return nil
	}
	for _, text := range rdata {
		target, err := ParseRdata(typ, class, text)
		if err != nil {
			return err
		}
		targetText := RdataText(target)
		filtered := rs.Records[:0]
		for _, rr := range rs.Records {
			if RdataText(rr) == targetText {
				z.recordCount--
				continue
			}
			filtered = append(filtered, rr)
		}
		rs.Records = filtered
	}
	if len(rs.Records) == 0 {
		delete(types, typ)
		z.rrsetCount--
		if len(types) == 0 {
			delete(z.rrsets, key)
		}
		z.removeIndexKey(key, typ)
	}
	return nil
}

// ReplaceRRset replaces all records at name/type with new rdata.
func (z *Zone) ReplaceRRset(name string, typ, class uint16, ttl uint32, rdata []string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if err := z.deleteRecordsLocked(name, typ, class, nil); err != nil {
		return err
	}
	return z.addRecordsLocked(name, typ, class, ttl, rdata)
}

// ApplyIXFRChanges applies deletion and addition RRs then sets SOA serial.
func (z *Zone) ApplyIXFRChanges(deletes, adds []dns.RR, newSerial uint32) error {
	z.mu.Lock()
	defer z.mu.Unlock()

	for _, rr := range deletes {
		if rr == nil {
			continue
		}
		h := rr.Header()
		key, err := ownerKey(h.Name, z.Origin)
		if err != nil {
			return err
		}
		types := z.rrsets[key]
		if types == nil {
			continue
		}
		rs := types[h.Rrtype]
		if rs == nil {
			continue
		}
		target := RdataText(rr)
		filtered := rs.Records[:0]
		for _, existing := range rs.Records {
			if RdataText(existing) == target {
				z.recordCount--
				continue
			}
			filtered = append(filtered, existing)
		}
		rs.Records = filtered
		if len(rs.Records) == 0 {
			delete(types, h.Rrtype)
			z.rrsetCount--
			if len(types) == 0 {
				delete(z.rrsets, key)
			}
			z.removeIndexKey(key, h.Rrtype)
		}
	}

	for _, rr := range adds {
		if rr == nil {
			continue
		}
		if rr.Header().Rrtype == dns.TypeTSIG {
			continue
		}
		key, err := ownerKey(rr.Header().Name, z.Origin)
		if err != nil {
			return err
		}
		z.insertRR(key, rr)
		if !z.hasIndexKey(key, rr.Header().Rrtype) {
			z.insertIndexKey(key, rr.Header().Rrtype)
		}
	}
	z.rebuildIndex()
	return z.setSOASerialLocked(newSerial)
}

func (z *Zone) setSOASerialLocked(serial uint32) error {
	types := z.rrsets[""]
	if types == nil {
		return fmt.Errorf("no SOA record in zone")
	}
	rs := types[dns.TypeSOA]
	if rs == nil || len(rs.Records) == 0 {
		return fmt.Errorf("no SOA record in zone")
	}
	old, ok := rs.Records[0].(*dns.SOA)
	if !ok {
		return fmt.Errorf("invalid SOA record")
	}
	old.Serial = serial
	return nil
}

// hasIndexKey reports whether owner/typ is present in the rrsets map.
func (z *Zone) hasIndexKey(owner string, typ uint16) bool {
	types := z.rrsets[owner]
	return types != nil && types[typ] != nil
}

// insertIndexKey appends to the index without sorting. Listing/search rebuild
// when indexDirty is set — O(n) mid-slice inserts on multi-million-RRset zones
// previously dominated write latency under the zone mutex.
func (z *Zone) insertIndexKey(owner string, typ uint16) {
	z.index = append(z.index, indexKey{owner: owner, typ: typ})
	z.indexDirty = true
}

// removeIndexKey marks the index dirty; stale entries are dropped on rebuild.
func (z *Zone) removeIndexKey(owner string, typ uint16) {
	z.indexDirty = true
}

// ToText renders a BIND master file with $ORIGIN.
func (z *Zone) ToText() string {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.ensureIndexLocked()

	var b strings.Builder
	b.WriteString("$ORIGIN ")
	b.WriteString(z.Origin)
	b.WriteByte('\n')
	for _, k := range z.index {
		types := z.rrsets[k.owner]
		rs := types[k.typ]
		if rs == nil {
			continue
		}
		owner := "@"
		if k.owner != "" {
			owner = k.owner
		}
		for _, rr := range rs.Records {
			fmt.Fprintf(&b, "%s\t%d\t%s\t%s\t%s\n",
				owner,
				rs.TTL,
				ClassName(rs.Class),
				TypeName(k.typ),
				RdataText(rr),
			)
		}
	}
	return b.String()
}

// SizeBytes returns a rough memory footprint estimate.
func (z *Zone) SizeBytes() int64 {
	z.mu.RLock()
	defer z.mu.RUnlock()

	var size int64 = 500
	for owner, types := range z.rrsets {
		size += int64(100 + len(owner)*2)
		for typ, rs := range types {
			_ = typ
			size += 50
			for _, rr := range rs.Records {
				size += int64(len(rr.String()) + 50)
			}
		}
	}
	return size
}

// RRsetCount returns the number of RRsets in the zone.
func (z *Zone) RRsetCount() int {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.rrsetCount
}

// RecordCount returns the number of individual RRs in the zone.
func (z *Zone) RecordCount() int {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return z.recordCount
}

// TypesAtName returns the type names present at owner name (relative or FQDN).
func (z *Zone) TypesAtName(name string) []string {
	z.mu.RLock()
	defer z.mu.RUnlock()
	key, err := z.resolveOwnerKey(name)
	if err != nil {
		return nil
	}
	types := z.rrsets[key]
	if len(types) == 0 {
		return nil
	}
	out := make([]string, 0, len(types))
	for typ := range types {
		out = append(out, TypeName(typ))
	}
	sort.Strings(out)
	return out
}

// AllRRs returns a copy of all resource records with proper owner names.
func (z *Zone) AllRRs() []dns.RR {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.ensureIndexLocked()

	out := make([]dns.RR, 0, z.recordCount)
	for _, k := range z.index {
		types := z.rrsets[k.owner]
		rs := types[k.typ]
		if rs == nil {
			continue
		}
		fqdn := fqdnFromKey(k.owner, z.Origin)
		for _, rr := range rs.Records {
			copy := dns.Copy(rr)
			copy.Header().Name = fqdn
			out = append(out, copy)
		}
	}
	return out
}

func (z *Zone) resolveOwnerKey(name string) (string, error) {
	fqdn, err := RequireNameInZone(name, z.Origin)
	if err != nil {
		return "", err
	}
	return ownerKey(fqdn, z.Origin)
}

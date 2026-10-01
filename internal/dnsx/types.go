package dnsx

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/miekg/dns"
)

// PROTECTED types must not be modified via the API (meta / query-only).
var PROTECTED = map[string]struct{}{
	"OPT": {}, "TKEY": {}, "TSIG": {}, "IXFR": {}, "AXFR": {},
	"ANY": {}, "MAILB": {}, "MAILA": {},
}

// UPDATABLE_TYPES is every known IANA type name except PROTECTED.
var UPDATABLE_TYPES map[string]struct{}

// typeNameToCode maps canonical uppercase type names to wire types.
var typeNameToCode map[string]uint16

// typeCodeToName maps wire types to canonical names (one name per code).
var typeCodeToName map[uint16]string

func init() {
	typeNameToCode = map[string]uint16{
		"A":          dns.TypeA,
		"NS":         dns.TypeNS,
		"MD":         dns.TypeMD,
		"MF":         dns.TypeMF,
		"CNAME":      dns.TypeCNAME,
		"SOA":        dns.TypeSOA,
		"MB":         dns.TypeMB,
		"MG":         dns.TypeMG,
		"MR":         dns.TypeMR,
		"NULL":       dns.TypeNULL,
		"WKS":        11,
		"PTR":        dns.TypePTR,
		"HINFO":      dns.TypeHINFO,
		"MINFO":      dns.TypeMINFO,
		"MX":         dns.TypeMX,
		"TXT":        dns.TypeTXT,
		"RP":         dns.TypeRP,
		"AFSDB":      dns.TypeAFSDB,
		"X25":        dns.TypeX25,
		"ISDN":       dns.TypeISDN,
		"RT":         dns.TypeRT,
		"NSAP":       22,
		"NSAP-PTR":   dns.TypeNSAPPTR,
		"NSAP_PTR":   dns.TypeNSAPPTR,
		"SIG":        dns.TypeSIG,
		"KEY":        dns.TypeKEY,
		"PX":         dns.TypePX,
		"GPOS":       dns.TypeGPOS,
		"AAAA":       dns.TypeAAAA,
		"LOC":        dns.TypeLOC,
		"NXT":        dns.TypeNXT,
		"EID":        31,
		"NIMLOC":     32,
		"SRV":        dns.TypeSRV,
		"ATMA":       34,
		"NAPTR":      dns.TypeNAPTR,
		"KX":         dns.TypeKX,
		"CERT":       dns.TypeCERT,
		"A6":         38,
		"DNAME":      dns.TypeDNAME,
		"SINK":       40,
		"OPT":        dns.TypeOPT,
		"APL":        dns.TypeAPL,
		"DS":         dns.TypeDS,
		"SSHFP":      dns.TypeSSHFP,
		"IPSECKEY":   dns.TypeIPSECKEY,
		"RRSIG":      dns.TypeRRSIG,
		"NSEC":       dns.TypeNSEC,
		"DNSKEY":     dns.TypeDNSKEY,
		"DHCID":      dns.TypeDHCID,
		"NSEC3":      dns.TypeNSEC3,
		"NSEC3PARAM": dns.TypeNSEC3PARAM,
		"TLSA":       dns.TypeTLSA,
		"SMIMEA":     dns.TypeSMIMEA,
		"HIP":        dns.TypeHIP,
		"NINFO":      56,
		"RKEY":       57,
		"TALINK":     58,
		"CDS":        dns.TypeCDS,
		"CDNSKEY":    dns.TypeCDNSKEY,
		"OPENPGPKEY": dns.TypeOPENPGPKEY,
		"CSYNC":      dns.TypeCSYNC,
		"ZONEMD":     dns.TypeZONEMD,
		"SVCB":       dns.TypeSVCB,
		"HTTPS":      dns.TypeHTTPS,
		"SPF":        dns.TypeSPF,
		"UINFO":      100,
		"UID":        101,
		"GID":        102,
		"UNSPEC":     103,
		"NID":        104,
		"L32":        105,
		"L64":        106,
		"LP":         107,
		"EUI48":      108,
		"EUI64":      109,
		"NXNAME":     128,
		"TKEY":       dns.TypeTKEY,
		"TSIG":       dns.TypeTSIG,
		"IXFR":       dns.TypeIXFR,
		"AXFR":       dns.TypeAXFR,
		"MAILB":      dns.TypeMAILB,
		"MAILA":      dns.TypeMAILA,
		"ANY":        dns.TypeANY,
		"URI":        dns.TypeURI,
		"CAA":        dns.TypeCAA,
		"AVC":        258,
		"DOA":        259,
		"AMTRELAY":   dns.TypeAMTRELAY,
		"RESINFO":    261,
		"WALLET":     262,
		"CLA":        263,
		"IPN":        264,
		"TA":         32768,
		"DLV":        dns.TypeDLV,
	}

	typeCodeToName = make(map[uint16]string, len(typeNameToCode))
	for name, code := range typeNameToCode {
		if name == "NSAP_PTR" {
			continue
		}
		if existing, ok := typeCodeToName[code]; ok && existing == "NSAP-PTR" && name != "NSAP-PTR" {
			continue
		}
		typeCodeToName[code] = name
	}

	// Enrich from miekg's registry for any missing codes.
	for code, name := range dns.TypeToString {
		if _, ok := typeCodeToName[code]; !ok && name != "" {
			typeCodeToName[code] = strings.ToUpper(name)
			if _, known := typeNameToCode[strings.ToUpper(name)]; !known {
				typeNameToCode[strings.ToUpper(name)] = code
			}
		}
	}

	UPDATABLE_TYPES = make(map[string]struct{})
	for name := range typeNameToCode {
		if name == "NSAP_PTR" {
			continue
		}
		if _, prot := PROTECTED[name]; prot {
			continue
		}
		UPDATABLE_TYPES[name] = struct{}{}
	}
}

var classNameToCode = map[string]uint16{
	"IN":   dns.ClassINET,
	"CH":   dns.ClassCHAOS,
	"HS":   dns.ClassHESIOD,
	"NONE": dns.ClassNONE,
	"ANY":  dns.ClassANY,
}

var classCodeToName = map[uint16]string{
	dns.ClassINET:   "IN",
	dns.ClassCHAOS:  "CH",
	dns.ClassHESIOD: "HS",
	dns.ClassNONE:   "NONE",
	dns.ClassANY:    "ANY",
}

// IsValidType reports whether name is a known record type (name or TYPE### / number).
func IsValidType(name string) bool {
	_, err := parseTypeInput(name)
	return err == nil
}

// IsUpdatableType reports whether the type may be changed via DDNS/API.
func IsUpdatableType(name string) bool {
	norm, err := NormalizeType(name)
	if err != nil {
		return false
	}
	_, ok := UPDATABLE_TYPES[norm]
	return ok
}

// NormalizeType uppercases known names or returns TYPE### for unknown numeric types.
func NormalizeType(name string) (string, error) {
	code, err := parseTypeInput(name)
	if err != nil {
		return "", err
	}
	if n, ok := typeCodeToName[code]; ok {
		return n, nil
	}
	return fmt.Sprintf("TYPE%d", code), nil
}

func parseTypeInput(name string) (uint16, error) {
	s := strings.TrimSpace(strings.ToUpper(name))
	if s == "" {
		return 0, fmt.Errorf("empty record type")
	}
	if code, ok := typeNameToCode[s]; ok {
		return code, nil
	}
	if strings.HasPrefix(s, "TYPE") {
		n, err := strconv.ParseUint(s[4:], 10, 16)
		if err != nil || n > 65535 {
			return 0, fmt.Errorf("invalid record type %q", name)
		}
		return uint16(n), nil
	}
	if strings.HasPrefix(s, "0X") {
		n, err := strconv.ParseUint(s, 0, 16)
		if err != nil || n > 65535 {
			return 0, fmt.Errorf("invalid record type %q", name)
		}
		return uint16(n), nil
	}
	if n, err := strconv.ParseUint(s, 10, 16); err == nil && n <= 65535 {
		return uint16(n), nil
	}
	return 0, fmt.Errorf("unknown record type %q", name)
}

// TypeFromString resolves a type name or number to its wire value.
func TypeFromString(name string) (uint16, error) {
	return parseTypeInput(name)
}

// NormalizeClass returns IN/CH/HS/NONE/ANY or CLASS### for unknown codes.
func NormalizeClass(name string) (string, error) {
	code, err := parseClassInput(name)
	if err != nil {
		return "", err
	}
	if n, ok := classCodeToName[code]; ok {
		return n, nil
	}
	return fmt.Sprintf("CLASS%d", code), nil
}

func parseClassInput(name string) (uint16, error) {
	s := strings.TrimSpace(strings.ToUpper(name))
	if s == "" {
		return 0, fmt.Errorf("empty class")
	}
	if code, ok := classNameToCode[s]; ok {
		return code, nil
	}
	if strings.HasPrefix(s, "CLASS") {
		n, err := strconv.ParseUint(s[5:], 10, 16)
		if err != nil || n > 65535 {
			return 0, fmt.Errorf("invalid class %q", name)
		}
		return uint16(n), nil
	}
	if strings.HasPrefix(s, "0X") {
		n, err := strconv.ParseUint(s, 0, 16)
		if err != nil || n > 65535 {
			return 0, fmt.Errorf("invalid class %q", name)
		}
		return uint16(n), nil
	}
	if n, err := strconv.ParseUint(s, 10, 16); err == nil && n <= 65535 {
		return uint16(n), nil
	}
	return 0, fmt.Errorf("unknown class %q", name)
}

// ClassFromString resolves a class name or number to its wire value.
func ClassFromString(name string) (uint16, error) {
	return parseClassInput(name)
}

// TypeName returns the canonical type name for a wire type.
func TypeName(code uint16) string {
	if n, ok := typeCodeToName[code]; ok {
		return n
	}
	if s, ok := dns.TypeToString[code]; ok && s != "" {
		return strings.ToUpper(s)
	}
	return fmt.Sprintf("TYPE%d", code)
}

// ClassName returns the canonical class name for a wire class.
func ClassName(code uint16) string {
	if n, ok := classCodeToName[code]; ok {
		return n
	}
	return fmt.Sprintf("CLASS%d", code)
}

package dnsx

import (
	"fmt"
	"strings"

	"github.com/miekg/dns"
)

// RdataText returns presentation-format rdata without the owner/TTL/class/type header.
func RdataText(rr dns.RR) string {
	if rr == nil {
		return ""
	}
	s := strings.TrimSpace(rr.String())
	fields := strings.Fields(s)
	if len(fields) < 5 {
		return strings.Join(fields[4:], " ")
	}
	return strings.Join(fields[4:], " ")
}

// ParseRdata parses presentation rdata into an RR with a synthetic owner (.).
func ParseRdata(rrtype, class uint16, text string) (dns.RR, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty rdata")
	}
	typeName := TypeName(rrtype)
	className := ClassName(class)
	line := fmt.Sprintf(". 0 %s %s %s", className, typeName, text)
	rr, err := dns.NewRR(line)
	if err != nil {
		return nil, err
	}
	return rr, nil
}

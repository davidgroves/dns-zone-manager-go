package dnsx

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

var decimalEscapeRE = regexp.MustCompile(`\\\d{3}`)

var idnaLookup = idna.New()

func isPunycodeLabel(label string) bool {
	return strings.HasPrefix(strings.ToLower(label), "xn--")
}

func hasPunycode(value string) bool {
	if value == "" {
		return false
	}
	labels := strings.Split(strings.TrimSuffix(value, "."), ".")
	for _, label := range labels {
		if isPunycodeLabel(label) {
			return true
		}
	}
	return false
}

// GetIDNInfo returns original, unicode (decoded punycode labels), and has_idn.
func GetIDNInfo(name string) map[string]any {
	info := map[string]any{
		"original": name,
		"unicode":  nil,
		"has_idn":  false,
	}
	if !hasPunycode(name) {
		return info
	}
	info["has_idn"] = true
	decoded := decodePunycodeName(name)
	if decoded != "" {
		info["unicode"] = decoded
	}
	return info
}

func decodePunycodeName(value string) string {
	trailingDot := strings.HasSuffix(value, ".")
	clean := strings.TrimSuffix(value, ".")
	labels := strings.Split(clean, ".")
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if isPunycodeLabel(label) {
			u, err := idnaLookup.ToUnicode(label)
			if err != nil {
				out = append(out, label)
				continue
			}
			out = append(out, u)
		} else {
			out = append(out, label)
		}
	}
	result := strings.Join(out, ".")
	if trailingDot {
		result += "."
	}
	return result
}

// RecordsUTF8Info holds decoded presentation strings for record data.
type RecordsUTF8Info struct {
	HasUTF8 bool
	Records []string
}

// GetRecordsUTF8Info decodes RFC 1035 \DDD decimal escapes to UTF-8 for display.
func GetRecordsUTF8Info(records []string) RecordsUTF8Info {
	info := RecordsUTF8Info{Records: make([]string, len(records))}
	for i, rec := range records {
		if dec, ok := decodeEscapedUTF8(rec); ok {
			info.HasUTF8 = true
			info.Records[i] = dec
		} else {
			info.Records[i] = rec
		}
	}
	if !info.HasUTF8 {
		info.Records = nil
	}
	return info
}

func decodeEscapedUTF8(value string) (string, bool) {
	if value == "" || !decimalEscapeRE.MatchString(value) {
		return "", false
	}
	var buf []byte
	for i := 0; i < len(value); {
		if i+3 < len(value) && value[i] == '\\' && isDigit(value[i+1]) && isDigit(value[i+2]) && isDigit(value[i+3]) {
			n := int(value[i+1]-'0')*100 + int(value[i+2]-'0')*10 + int(value[i+3]-'0')
			if n <= 255 {
				buf = append(buf, byte(n))
				i += 4
				continue
			}
		}
		buf = append(buf, value[i])
		i++
	}
	if !utf8.Valid(buf) {
		return "", false
	}
	decoded := string(buf)
	if decoded == value {
		return "", false
	}
	return decoded, true
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

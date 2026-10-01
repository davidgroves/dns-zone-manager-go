package dnsx

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// NSUpdateParseError is raised when parsing nsupdate text fails.
type NSUpdateParseError struct {
	Message    string
	LineNumber int // 0 if unknown
}

func (e *NSUpdateParseError) Error() string {
	if e.LineNumber > 0 {
		return fmt.Sprintf("Line %d: %s", e.LineNumber, e.Message)
	}
	return e.Message
}

func parseErr(line int, format string, args ...any) *NSUpdateParseError {
	return &NSUpdateParseError{Message: fmt.Sprintf(format, args...), LineNumber: line}
}

// ParsedPrerequisite is a prerequisite from nsupdate text.
type ParsedPrerequisite struct {
	PrereqType string // nxdomain | yxdomain | nxrrset | yxrrset
	Name       string
	Class      string
	RdType     string
	Data       string // optional; for yxrrset with specific data
}

// ParsedOperation is a single update add/delete from nsupdate text.
type ParsedOperation struct {
	Action string // add | delete
	Name   string
	TTL    *uint32
	Class  string
	RdType string // empty means delete-all-at-name
	Data   string
}

// ParsedUpdate is one transaction (zone … send).
type ParsedUpdate struct {
	Zone          string
	Prerequisites []ParsedPrerequisite
	Operations    []ParsedOperation
}

// ParsedTransaction is an alias for ParsedUpdate (one send boundary).
type ParsedTransaction = ParsedUpdate

var dnsClasses = map[string]struct{}{
	"IN": {}, "CH": {}, "HS": {}, "NONE": {}, "ANY": {},
}

func isDNSClass(token string) bool {
	_, ok := dnsClasses[strings.ToUpper(token)]
	return ok
}

func isTTL(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// tokenizeLine splits a line like shlex, falling back to Fields on quote errors.
func tokenizeLine(line string) []string {
	tokens, err := shlexSplit(line)
	if err != nil {
		return strings.Fields(line)
	}
	return tokens
}

func shlexSplit(s string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	escaped := false

	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}

	for _, r := range s {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if unicode.IsSpace(r) && !inSingle && !inDouble {
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	if inSingle || inDouble || escaped {
		return nil, fmt.Errorf("unbalanced quotes")
	}
	flush()
	return tokens, nil
}

func parsePrereq(tokens []string, lineNumber int) (ParsedPrerequisite, error) {
	if len(tokens) < 2 {
		return ParsedPrerequisite{}, parseErr(lineNumber, "prereq requires at least type and name")
	}
	prereqType := strings.ToLower(tokens[0])
	switch prereqType {
	case "nxdomain", "yxdomain", "nxrrset", "yxrrset":
	default:
		return ParsedPrerequisite{}, parseErr(lineNumber,
			"Unknown prereq type: %s. Expected: nxdomain, yxdomain, nxrrset, yxrrset", prereqType)
	}
	name := tokens[1]
	if prereqType == "nxdomain" || prereqType == "yxdomain" {
		return ParsedPrerequisite{PrereqType: prereqType, Name: name, Class: "IN"}, nil
	}

	remaining := tokens[2:]
	if len(remaining) == 0 {
		return ParsedPrerequisite{}, parseErr(lineNumber, "prereq %s requires a record type", prereqType)
	}
	rdclass := "IN"
	if isDNSClass(remaining[0]) {
		rdclass = strings.ToUpper(remaining[0])
		remaining = remaining[1:]
	}
	if len(remaining) == 0 {
		return ParsedPrerequisite{}, parseErr(lineNumber, "prereq %s requires a record type", prereqType)
	}
	rdtype := strings.ToUpper(remaining[0])
	data := ""
	if prereqType == "yxrrset" && len(remaining) > 1 {
		data = strings.Join(remaining[1:], " ")
	}
	return ParsedPrerequisite{
		PrereqType: prereqType,
		Name:       name,
		Class:      rdclass,
		RdType:     rdtype,
		Data:       data,
	}, nil
}

func parseUpdateAdd(tokens []string, lineNumber int) (ParsedOperation, error) {
	if len(tokens) < 4 {
		return ParsedOperation{}, parseErr(lineNumber, "update add requires: name ttl [class] type data")
	}
	name := tokens[0]
	if !isTTL(tokens[1]) {
		return ParsedOperation{}, parseErr(lineNumber, "Expected TTL, got: %s", tokens[1])
	}
	ttl64, _ := strconv.ParseUint(tokens[1], 10, 32)
	ttl := uint32(ttl64)
	remaining := tokens[2:]

	rdclass := "IN"
	if isDNSClass(remaining[0]) {
		rdclass = strings.ToUpper(remaining[0])
		remaining = remaining[1:]
	}
	if len(remaining) < 2 {
		return ParsedOperation{}, parseErr(lineNumber, "update add requires type and data after TTL/class")
	}
	rdtype := strings.ToUpper(remaining[0])
	data := strings.Join(remaining[1:], " ")
	return ParsedOperation{
		Action: "add",
		Name:   name,
		TTL:    &ttl,
		Class:  rdclass,
		RdType: rdtype,
		Data:   data,
	}, nil
}

func parseUpdateDelete(tokens []string, lineNumber int) (ParsedOperation, error) {
	if len(tokens) < 1 {
		return ParsedOperation{}, parseErr(lineNumber, "update delete requires at least a name")
	}
	name := tokens[0]
	remaining := tokens[1:]
	if len(remaining) > 0 && isTTL(remaining[0]) {
		remaining = remaining[1:]
	}
	rdclass := "IN"
	rdtype := ""
	data := ""
	if len(remaining) > 0 {
		if isDNSClass(remaining[0]) {
			rdclass = strings.ToUpper(remaining[0])
			remaining = remaining[1:]
		}
		if len(remaining) > 0 {
			rdtype = strings.ToUpper(remaining[0])
			remaining = remaining[1:]
			if len(remaining) > 0 {
				data = strings.Join(remaining, " ")
			}
		}
	}
	return ParsedOperation{
		Action: "delete",
		Name:   name,
		Class:  rdclass,
		RdType: rdtype,
		Data:   data,
	}, nil
}

// ParseNSUpdate parses nsupdate(1)-formatted text into transactions (one per send).
func ParseNSUpdate(text string, defaultZone string) ([]ParsedUpdate, error) {
	var updates []ParsedUpdate

	currentZone := strings.TrimSpace(defaultZone)
	if currentZone != "" && !strings.HasSuffix(currentZone, ".") {
		currentZone += "."
	}
	var currentPrereqs []ParsedPrerequisite
	var currentOps []ParsedOperation

	lines := strings.Split(text, "\n")
	for lineNum, line := range lines {
		lineNum++ // 1-based
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		tokens := tokenizeLine(line)
		if len(tokens) == 0 {
			continue
		}
		cmd := strings.ToLower(tokens[0])

		switch cmd {
		case "zone":
			if len(tokens) < 2 {
				return nil, parseErr(lineNum, "zone requires a zone name")
			}
			currentZone = tokens[1]
			if !strings.HasSuffix(currentZone, ".") {
				currentZone += "."
			}

		case "server", "key", "local", "show", "answer", "debug":
			// Ignored — API uses its own configuration

		case "prereq":
			prereq, err := parsePrereq(tokens[1:], lineNum)
			if err != nil {
				return nil, err
			}
			currentPrereqs = append(currentPrereqs, prereq)

		case "update":
			if len(tokens) < 2 {
				return nil, parseErr(lineNum, "update requires add or delete")
			}
			sub := strings.ToLower(tokens[1])
			switch sub {
			case "add":
				op, err := parseUpdateAdd(tokens[2:], lineNum)
				if err != nil {
					return nil, err
				}
				currentOps = append(currentOps, op)
			case "delete":
				op, err := parseUpdateDelete(tokens[2:], lineNum)
				if err != nil {
					return nil, err
				}
				currentOps = append(currentOps, op)
			default:
				return nil, parseErr(lineNum, "Unknown update subcommand: %s. Expected: add, delete", sub)
			}

		case "send":
			if currentZone == "" {
				return nil, parseErr(lineNum, "No zone specified. Use 'zone' command or provide default_zone")
			}
			if len(currentPrereqs) > 0 || len(currentOps) > 0 {
				updates = append(updates, ParsedUpdate{
					Zone:          currentZone,
					Prerequisites: currentPrereqs,
					Operations:    currentOps,
				})
			}
			currentPrereqs = nil
			currentOps = nil

		case "quit":
			goto done

		default:
			return nil, parseErr(lineNum, "Unknown command: %s", cmd)
		}
	}

done:
	if len(currentPrereqs) > 0 || len(currentOps) > 0 {
		if currentZone == "" {
			return nil, parseErr(0, "No zone specified for trailing operations. Use 'zone' command or provide default_zone")
		}
		updates = append(updates, ParsedUpdate{
			Zone:          currentZone,
			Prerequisites: currentPrereqs,
			Operations:    currentOps,
		})
	}
	return updates, nil
}

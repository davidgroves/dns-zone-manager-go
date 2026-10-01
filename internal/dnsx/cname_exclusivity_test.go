package dnsx

import (
	"errors"
	"testing"
)

func TestCheckCNAMEExclusivity(t *testing.T) {
	if err := CheckCNAMEExclusivity([]string{"A"}, "MX"); err != nil {
		t.Fatalf("A and MX may coexist: %v", err)
	}
	err := CheckCNAMEExclusivity([]string{"A", "MX"}, "CNAME")
	var ce *CNAMEConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CNAMEConflictError, got %v", err)
	}
	if len(ce.ConflictingTypes) != 2 {
		t.Fatalf("conflicts: %v", ce.ConflictingTypes)
	}

	err = CheckCNAMEExclusivity([]string{"CNAME"}, "A")
	if !errors.As(err, &ce) {
		t.Fatalf("expected conflict adding A with CNAME")
	}

	if err := CheckCNAMEExclusivity([]string{"CNAME", "RRSIG"}, "NSEC"); err != nil {
		t.Fatalf("DNSSEC companions allowed: %v", err)
	}
}

func TestCheckCNAMEExclusivityAt(t *testing.T) {
	err := CheckCNAMEExclusivityAt("www.example.com.", []string{"A"}, "CNAME")
	var ce *CNAMEConflictError
	if !errors.As(err, &ce) {
		t.Fatal("expected error")
	}
	if ce.Name != "www.example.com." {
		t.Fatalf("name %q", ce.Name)
	}
}

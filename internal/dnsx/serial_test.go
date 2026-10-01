package dnsx

import "testing"

func TestSerialCompare(t *testing.T) {
	if Compare(1, 1) != 0 {
		t.Fatal("equal serials")
	}
	if Compare(1, 2) >= 0 {
		t.Fatal("1 should be less than 2")
	}
	if Compare(4294967295, 1) >= 0 {
		t.Fatal("wrap-around: max should be less than 1")
	}
	if !Less(100, 200) {
		t.Fatal("Less failed")
	}
}

func TestSerialInRange(t *testing.T) {
	if !InRange(5, 1, 10) {
		t.Fatal("5 in 1..10")
	}
	if InRange(11, 1, 10) {
		t.Fatal("11 not in 1..10")
	}
}

func TestOldestRequestableSerial(t *testing.T) {
	current := uint32(4_000_000_000)
	got := OldestRequestableSerial(current)
	if !Less(got, current) {
		t.Fatalf("oldest %d should be less than current %d in serial space", got, current)
	}
}

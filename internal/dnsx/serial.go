package dnsx

const (
	serialBits    = 32
	serialMax     = uint64(1 << serialBits)
	serialHalf    = uint64(1 << (serialBits - 1))
	serialMaxDiff = serialHalf - 1
)

// Compare compares two SOA serial numbers using RFC 1982 arithmetic.
// Returns -1 if a < b, 0 if equal, 1 if a > b.
func Compare(a, b uint32) int {
	if a == b {
		return 0
	}
	diff := (uint64(b) - uint64(a)) % serialMax
	if diff > 0 && diff < serialHalf {
		return -1
	}
	return 1
}

// Less reports whether a is less than b in serial number space.
func Less(a, b uint32) bool {
	return Compare(a, b) < 0
}

// InRange reports whether serial lies in the inclusive RFC 1982 range [start, end]
// when traversing forward from start to end.
func InRange(serial, start, end uint32) bool {
	if start == end {
		return serial == start
	}
	if Less(start, end) {
		return !Less(serial, start) && !Less(end, serial)
	}
	// Range wraps through zero.
	return !Less(serial, start) || !Less(end, serial)
}

// OldestRequestableSerial returns the oldest serial IXFR can meaningfully request.
func OldestRequestableSerial(current uint32) uint32 {
	return uint32((uint64(current) - serialMaxDiff) % serialMax)
}

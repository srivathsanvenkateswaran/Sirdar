package httpx

// Limit applies the adapter contract's List bounds to a caller's requested
// limit: nothing (zero or negative) takes def, and nothing exceeds max. It
// reports whether an explicit request was cut down, which is what an adapter
// warns about — a caller that asked for 500 and silently got 200 has no way
// to tell a capped result from a short one.
func Limit(requested, def, max int) (n int, capped bool) {
	if requested <= 0 {
		return def, false
	}
	if requested > max {
		return max, true
	}
	return requested, false
}

// PageSize is how many records to ask for in the next page: never more than
// the API's own ceiling, never more than the caller still wants. A
// non-positive n means "as many as the API allows".
func PageSize(n, apiMax int) int {
	if n <= 0 || n > apiMax {
		return apiMax
	}
	return n
}

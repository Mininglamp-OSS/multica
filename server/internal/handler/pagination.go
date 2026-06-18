package handler

import (
	"net/http"
	"strconv"
)

// parsePagination reads ?limit and ?offset from the request, clamping both to
// safe bounds. limit defaults to defaultLimit, is capped at maxLimit, and is
// never below 1; offset defaults to 0 and is bounded so it can never overflow
// int32. Non-numeric or out-of-range values fall back to the defaults rather
// than reaching SQL — a clamp BEFORE the int32 cast is what prevents a huge
// "?limit=2147483648" from wrapping negative and producing a negative SQL
// LIMIT/OFFSET (which Postgres rejects with a 500).
func parsePagination(r *http.Request, defaultLimit, maxLimit int32) (limit, offset int32) {
	// maxOffset bounds paging depth far beyond any real UI while staying well
	// under math.MaxInt32, so int32(v) below can never overflow.
	const maxOffset = 1 << 30

	limit = defaultLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			if v > int(maxLimit) {
				v = int(maxLimit)
			}
			limit = int32(v)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			if v > maxOffset {
				v = maxOffset
			}
			offset = int32(v)
		}
	}
	return limit, offset
}

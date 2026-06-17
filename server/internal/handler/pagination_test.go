package handler

import (
	"net/http/httptest"
	"testing"
)

func TestParsePagination(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		wantLimit  int32
		wantOffset int32
	}{
		{"defaults when absent", "", 20, 0},
		{"valid values", "limit=5&offset=10", 5, 10},
		{"limit capped at max", "limit=9999", 100, 0},
		{"limit overflow clamps, never negative", "limit=2147483648", 100, 0},
		{"offset overflow clamps, never negative", "offset=2147483648", 20, 1 << 30},
		{"non-numeric falls back", "limit=abc&offset=xyz", 20, 0},
		{"zero/negative limit ignored", "limit=0", 20, 0},
		{"negative offset ignored", "offset=-5", 20, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/x?"+tc.query, nil)
			limit, offset := parsePagination(r, 20, 100)
			if limit != tc.wantLimit {
				t.Errorf("limit = %d, want %d", limit, tc.wantLimit)
			}
			if offset != tc.wantOffset {
				t.Errorf("offset = %d, want %d", offset, tc.wantOffset)
			}
			if limit < 0 || offset < 0 {
				t.Errorf("limit/offset must never be negative: limit=%d offset=%d", limit, offset)
			}
		})
	}
}

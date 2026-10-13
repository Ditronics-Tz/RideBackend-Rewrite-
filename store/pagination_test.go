package store_test

import (
	"ride-backend/store"
	"testing"
)

func TestPaginationBounds(t *testing.T) {
	for _, tc := range []struct {
		limit, offset string
		valid         bool
		want          int
	}{{"", "", true, 50}, {"200", "5", true, 200}, {"1", "0", true, 1}, {"201", "", false, 0}, {"0", "", false, 0}, {"x", "", false, 0}, {"50", "-1", false, 0}, {"50", "x", false, 0}} {
		p, err := store.ParsePage(tc.limit, tc.offset)
		if (err == nil) != tc.valid {
			t.Fatalf("%q/%q got %v", tc.limit, tc.offset, err)
		}
		if tc.valid && p.Limit != tc.want {
			t.Fatal("incorrect limit")
		}
	}
}

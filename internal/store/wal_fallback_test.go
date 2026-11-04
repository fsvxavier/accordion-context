package store

import (
	"errors"
	"testing"
)

func TestWALUnavailable(t *testing.T) {
	for _, tc := range []struct {
		err  string
		want bool
	}{
		{"unable to open database file (14)", true},
		{"SQL logic error: unable to open database file", true},
		{"disk I/O error (4618)", true},
		{"disk I/O error (4874)", true},
		{"disk I/O error (522)", false},
		{"database is locked (5)", false},
	} {
		if got := walUnavailable(errors.New(tc.err)); got != tc.want {
			t.Errorf("walUnavailable(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

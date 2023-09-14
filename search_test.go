package main

import (
	"strings"
	"testing"
)

func TestMatchSlices(t *testing.T) {
	type S = []string
	tests := []struct {
		needle, haystack S
		want             bool
	}{
		{S{"a"}, S{"a"}, true},
		{S{"a"}, S{"b"}, false},
		{S{"a", "b"}, S{"a", "b"}, true},
		{S{"a", "b"}, S{"b", "a"}, false},
		{S{"a", "b"}, S{"a", "b", "c"}, true},
		{S{"a", "b"}, S{"a", "c", "b"}, true},
		{S{"a", "b"}, S{"a", "c", "d"}, false},
		{S{"a", "b"}, S{"aa", "bb", "c", "d"}, true},
	}

	for _, tt := range tests {
		got := matchSlices(tt.haystack, tt.needle, strings.HasPrefix)
		if got != tt.want {
			t.Errorf("matchSlices(%v, %v, strings.HasPrefix) = %v; want %v", tt.haystack, tt.needle, got, tt.want)
		}
	}
}

package rag

import "testing"

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"빈 문자열", "", 5, ""},
		{"n 이하면 그대로", "광복절", 5, "광복절"},
		{"정확히 n이면 그대로", "광복절", 3, "광복절"},
		{"n 초과면 잘리고 말줄임표", "광복절 기념 세일", 3, "광복절…"},
		{"rune 기준(byte 아님)으로 절단", "abcde한글", 5, "abcde…"},
		{"n=0이면 전부 잘림", "abc", 0, "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateRunes(tt.s, tt.n); got != tt.want {
				t.Errorf("truncateRunes(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

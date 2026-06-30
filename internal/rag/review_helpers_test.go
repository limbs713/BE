package rag

import (
	"strings"
	"testing"
)

func TestClampScore(t *testing.T) {
	cases := []struct{ in, want int }{
		{-5, 0}, {0, 0}, {50, 50}, {100, 100}, {150, 100},
	}
	for _, c := range cases {
		if got := clampScore(c.in); got != c.want {
			t.Errorf("clampScore(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestScoreToLevel(t *testing.T) {
	cases := []struct {
		score int
		want  string
	}{
		{0, "none"},
		{1, "low"},
		{33, "low"},
		{34, "medium"},
		{66, "medium"},
		{67, "high"},
		{100, "high"},
	}
	for _, c := range cases {
		if got := scoreToLevel(c.score); got != c.want {
			t.Errorf("scoreToLevel(%d) = %q, want %q", c.score, got, c.want)
		}
	}
}

func TestSafetyLabel(t *testing.T) {
	cases := []struct {
		score int
		want  string
	}{
		{0, "안전"}, {33, "안전"}, {34, "주의"}, {66, "주의"}, {67, "위험"}, {100, "위험"},
	}
	for _, c := range cases {
		if got := safetyLabel(c.score); got != c.want {
			t.Errorf("safetyLabel(%d) = %q, want %q", c.score, got, c.want)
		}
	}
}

func TestStatusLabel(t *testing.T) {
	cases := []struct {
		score int
		want  string
	}{
		{0, "reviewed"}, {33, "reviewed"}, {34, "needs_review"}, {67, "needs_review"}, {100, "needs_review"},
	}
	for _, c := range cases {
		if got := statusLabel(c.score); got != c.want {
			t.Errorf("statusLabel(%d) = %q, want %q", c.score, got, c.want)
		}
	}
}

func TestNormalizeSeverity(t *testing.T) {
	cases := []struct{ in, want string }{
		{"high", "high"},
		{"HIGH", "high"},
		{"위험", "high"},
		{"low", "low"},
		{"낮음", "low"},
		{"medium", "needs_review"},
		{"needs_review", "needs_review"},
		{"주의", "needs_review"},
		{"", "needs_review"},
	}
	for _, c := range cases {
		if got := normalizeSeverity(c.in); got != c.want {
			t.Errorf("normalizeSeverity(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSeverityScoreFloor(t *testing.T) {
	cases := []struct {
		name string
		sev  []string
		want int
	}{
		{"하이라이트 없음 → 0", nil, 0},
		{"낮음만 → 0", []string{"low"}, 0},
		{"주의 1건 → 34", []string{"needs_review"}, 34},
		{"주의+낮음 → 34", []string{"low", "needs_review"}, 34},
		{"위험 포함 → 67", []string{"needs_review", "high", "low"}, 67},
	}
	for _, c := range cases {
		hs := make([]Highlight, len(c.sev))
		for i, s := range c.sev {
			hs[i] = Highlight{Severity: s}
		}
		if got := severityScoreFloor(hs); got != c.want {
			t.Errorf("%s: severityScoreFloor = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestPhraseOffsets(t *testing.T) {
	input := "이 여름, 책상을 탁 치고 떠나는 특가"

	t.Run("한글 구절 rune 오프셋", func(t *testing.T) {
		start, end, _, ok := phraseOffsets(input, "책상을 탁 치고", 0)
		if !ok {
			t.Fatal("phrase를 찾지 못함")
		}
		// rune 오프셋으로 다시 잘라 원래 구절과 일치해야 함
		runes := []rune(input)
		if got := string(runes[start:end]); got != "책상을 탁 치고" {
			t.Errorf("offsets[%d:%d] = %q, want %q", start, end, got, "책상을 탁 치고")
		}
	})

	t.Run("없는 구절은 ok=false", func(t *testing.T) {
		if _, _, _, ok := phraseOffsets(input, "존재하지않는구절", 0); ok {
			t.Error("없는 구절인데 ok=true")
		}
	})

	t.Run("빈 구절은 ok=false", func(t *testing.T) {
		if _, _, _, ok := phraseOffsets(input, "", 0); ok {
			t.Error("빈 구절인데 ok=true")
		}
	})

	t.Run("중복 구절은 커서로 순차 매칭", func(t *testing.T) {
		in := "행사 그리고 또 행사"
		s1, _, end1, ok1 := phraseOffsets(in, "행사", 0)
		s2, _, _, ok2 := phraseOffsets(in, "행사", end1)
		if !ok1 || !ok2 {
			t.Fatalf("두 번 다 찾아야 함: ok1=%v ok2=%v", ok1, ok2)
		}
		if s1 != 0 {
			t.Errorf("첫 매칭 start = %d, want 0", s1)
		}
		if s2 <= s1 {
			t.Errorf("두 번째 매칭 start(%d)가 첫 매칭(%d) 이후여야 함", s2, s1)
		}
	})
}

func TestNewReviewID(t *testing.T) {
	id := newReviewID()
	if !strings.HasPrefix(id, "rev_") {
		t.Errorf("newReviewID() = %q, want rev_ 접두사", id)
	}
	if id == newReviewID() {
		t.Error("연속 호출이 동일 ID를 반환함(유니크하지 않음)")
	}
}

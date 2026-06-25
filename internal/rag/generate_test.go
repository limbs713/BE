package rag

import "testing"

func TestSafetyNote(t *testing.T) {
	tests := []struct {
		name string
		v    Verdict
		want string
	}{
		{
			name: "안전(score<34)은 advice가 있어도 숨김",
			v:    Verdict{Score: 10, Advice: "이 표현은 위험합니다"},
			want: "민감 표현 없음",
		},
		{
			name: "주의 이상이고 advice 있으면 advice",
			v:    Verdict{Score: 50, Advice: "표현을 완화하세요"},
			want: "표현을 완화하세요",
		},
		{
			name: "advice 비고 reasons 있으면 첫 사유",
			v:    Verdict{Score: 80, Reasons: []string{"역사적 비극 연상", "추가 사유"}},
			want: "역사적 비극 연상",
		},
		{
			name: "advice 공백이면 reasons로 폴백",
			v:    Verdict{Score: 70, Advice: "   ", Reasons: []string{"사유1"}},
			want: "사유1",
		},
		{
			name: "주의 이상인데 advice·reasons 모두 없으면 기본 문구",
			v:    Verdict{Score: 70},
			want: "민감 표현 없음",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safetyNote(tt.v); got != tt.want {
				t.Errorf("safetyNote(%+v) = %q, want %q", tt.v, got, tt.want)
			}
		})
	}
}

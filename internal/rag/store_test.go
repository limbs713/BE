package rag

import "testing"

func TestVectorLiteral(t *testing.T) {
	tests := []struct {
		name string
		vec  []float32
		want string
	}{
		{
			name: "기본 벡터",
			vec:  []float32{0.1, 0.2, 0.3},
			want: "[0.1,0.2,0.3]",
		},
		{
			name: "단일 원소",
			vec:  []float32{1.5},
			want: "[1.5]",
		},
		{
			name: "빈 벡터",
			vec:  []float32{},
			want: "[]",
		},
		{
			name: "음수 포함",
			vec:  []float32{-0.5, 0, 0.5},
			want: "[-0.5,0,0.5]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vectorLiteral(tt.vec)
			if got != tt.want {
				t.Errorf("vectorLiteral(%v) = %q, want %q", tt.vec, got, tt.want)
			}
		})
	}
}

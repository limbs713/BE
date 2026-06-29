package image

import (
	"testing"
	"time"
)

func TestNullableTime(t *testing.T) {
	t.Run("zero time returns nil", func(t *testing.T) {
		got := nullableTime(time.Time{})
		if got != nil {
			t.Errorf("nullableTime(zero) = %v, want nil", got)
		}
	})

	t.Run("non-zero time returns the value", func(t *testing.T) {
		now := time.Now()
		got := nullableTime(now)
		if got == nil {
			t.Fatal("nullableTime(non-zero) = nil, want non-nil")
		}
		gotTime, ok := got.(time.Time)
		if !ok {
			t.Fatalf("nullableTime returned %T, want time.Time", got)
		}
		if !gotTime.Equal(now) {
			t.Errorf("nullableTime returned %v, want %v", gotTime, now)
		}
	})
}

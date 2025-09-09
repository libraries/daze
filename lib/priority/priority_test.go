package priority

import (
	"testing"
)

func BenchmarkPriority(b *testing.B) {
	pri := NewPriority(3)
	for b.Loop() {
		pri.Pri(2, func() error {
			return nil
		})
	}
}

func TestPriority(t *testing.T) {
	pri := NewPriority(3)
	pri.Pri(0, func() error {
		return nil
	})
	pri.Pri(1, func() error {
		return nil
	})
	pri.Pri(2, func() error {
		return nil
	})
}

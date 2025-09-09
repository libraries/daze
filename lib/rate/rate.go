// Package rate provides a rate limiter. It implements a classic token bucket algorithm, which can achieve functions
// such as http api speed limit and network bandwidth speed limit.
package rate

import (
	"log"
	"math"
	"sync"
	"time"
)

// Limits represents a rate limiter that controls resource allocation over time.
type Limits struct {
	addition uint64
	capacity uint64
	last     time.Time
	mu       sync.Mutex
	size     uint64
	step     time.Duration
}

// Peek glances there are enough resources (n) available.
func (l *Limits) Peek(n uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cycles := uint64(time.Since(l.last) / l.step)
	if cycles > 0 {
		l.last = l.last.Add(l.step * time.Duration(cycles))
		if cycles > math.MaxUint64/l.addition {
			log.Panicln("rate: overflow")
		}
		if l.size > math.MaxUint64-l.addition*cycles {
			log.Panicln("rate: overflow")
		}
		l.size = l.size + l.addition*cycles
		l.size = min(l.size, l.capacity)
	}
	return l.size >= n
}

// Wait ensures there are enough resources (n) available, blocking if necessary.
func (l *Limits) Wait(n uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cycles := uint64(time.Since(l.last) / l.step)
	if cycles > 0 {
		l.last = l.last.Add(l.step * time.Duration(cycles))
		if cycles > math.MaxUint64/l.addition {
			log.Panicln("rate: overflow")
		}
		if l.size > math.MaxUint64-l.addition*cycles {
			log.Panicln("rate: overflow")
		}
		l.size = l.size + l.addition*cycles
		l.size = min(l.size, l.capacity)
	}
	if l.size < n {
		cycles = (n - l.size + l.addition - 1) / l.addition
		time.Sleep(l.step * time.Duration(cycles))
		l.last = l.last.Add(l.step * time.Duration(cycles))
		l.size = l.size + l.addition*cycles
	}
	l.size -= n
}

// NewLimits creates a new rate limiter with rate r over period p.
//
// Overflow warning:
// If the rate r is set to a very large value (e.g., 1G = 1024 * 1024 * 1024) and the period (p) is one second, the
// internal counters may overflow after approximately 544 years. However, this overflow only occurs if the limiter
// remains completely idle (i.e., neither peek nor wait is called) for the entire duration. Consider this when designing
// long-running systems with very high rates.
func NewLimits(r uint64, p time.Duration) *Limits {
	gcd := func(a, b uint64) uint64 {
		t := uint64(0)
		for b != 0 {
			t = b
			b = a % b
			a = t
		}
		return a
	}(r, uint64(p))
	return &Limits{
		addition: r / gcd,
		capacity: r,
		last:     time.Now(),
		mu:       sync.Mutex{},
		size:     r,
		step:     p / time.Duration(gcd),
	}
}

package czar

import (
	"math"
	"math/big"
	"math/bits"
	"sync"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/doa"
)

// Sip tracks which stream IDs are in use. Get reserves the smallest available
// ID, Del reserves a specific ID, and Put releases an ID for reuse.
// All methods are safe for concurrent use. Create a Sip with NewSip;
// the zero value is not ready for use.
type Sip struct {
	// Bit x is 1 while ID x is in use, and 0 while it is available.
	i *big.Int
	// Protects the bitmap during reads and writes.
	m *sync.Mutex
}

// Del marks ID x as in use, preventing Get from returning it.
// It panics if x is already in use.
func (s *Sip) Del(x uint16) {
	s.m.Lock()
	defer s.m.Unlock()
	doa.Doa(s.i.Bit(int(x)) == 0)
	s.i = s.i.SetBit(s.i, int(x), 1)
}

// Get marks the smallest available stream ID as in use and returns it.
// IDs start at 0 and cover the full uint16 range.
// If all IDs are in use, Get returns daze.ErrTooManyRequests without
// changing the bitmap; the returned ID must be ignored on error.
func (s *Sip) Get() (uint16, error) {
	s.m.Lock()
	defer s.m.Unlock()
	n := big.NewInt(0).Not(s.i)
	m := int(n.TrailingZeroBits())
	if m > math.MaxUint16 {
		return 0, daze.ErrTooManyRequests
	}
	s.i.SetBit(s.i, m, 1)
	return uint16(m), nil
}

// Pop returns the number of IDs currently in use, including IDs reserved
// by either Get or Del. It only counts IDs; it does not release them.
func (s *Sip) Pop() int {
	s.m.Lock()
	defer s.m.Unlock()
	c := 0
	for _, b := range s.i.Bits() {
		c += bits.OnesCount(uint(b))
	}
	return c
}

// Put releases ID x so that Get can return it again.
// It panics if x is not currently in use.
func (s *Sip) Put(x uint16) {
	s.m.Lock()
	defer s.m.Unlock()
	doa.Doa(s.i.Bit(int(x)) == 1)
	s.i = s.i.SetBit(s.i, int(x), 0)
}

// NewSip returns a ready-to-use Sip with no IDs in use.
func NewSip() *Sip {
	return &Sip{
		i: big.NewInt(0),
		m: &sync.Mutex{},
	}
}

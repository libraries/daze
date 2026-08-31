package czar

import (
	"errors"
	"math/big"
	"sync"

	"github.com/libraries/daze/lib/doa"
)

// A stream id generator. Stream id can be reused, and the smallest available stream id is guaranteed to be generated
// each time.
type Sip struct {
	i *big.Int
	m *sync.Mutex
}

// Get selects an stream id from the pool, removes it from the pool, and returns it to the caller.
func (s *Sip) Get() (uint16, error) {
	s.m.Lock()
	defer s.m.Unlock()
	n := big.NewInt(0).Not(s.i)
	m := int(n.TrailingZeroBits())
	if m == Conf.StreamPool {
		return 0, errors.New("daze: out of stream")
	}
	s.i.SetBit(s.i, m, 1)
	return uint16(m), nil
}

// Put adds x to the pool.
func (s *Sip) Put(x uint16) {
	s.m.Lock()
	defer s.m.Unlock()
	doa.Doa(s.i.Bit(int(x)) == 1)
	s.i = s.i.SetBit(s.i, int(x), 0)
}

// Set removes x from the pool.
func (s *Sip) Set(x uint16) {
	s.m.Lock()
	defer s.m.Unlock()
	doa.Doa(s.i.Bit(int(x)) == 0)
	s.i = s.i.SetBit(s.i, int(x), 1)
}

// NewSip returns a new sip.
func NewSip() *Sip {
	return &Sip{
		i: big.NewInt(0),
		m: &sync.Mutex{},
	}
}

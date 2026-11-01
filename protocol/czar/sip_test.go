package czar

import (
	"math"
	"testing"

	"github.com/libraries/daze/lib/doa"
)

func TestProtocolCzarSip(t *testing.T) {
	sid := NewSip()
	for i := range math.MaxUint16 + 1 {
		doa.Doa(doa.Try(sid.Get()) == uint16(i))
	}
	doa.Doa(doa.Err(sid.Get()) != nil)
	doa.Doa(sid.Pop() == math.MaxUint16+1)
	sid.Put(65)
	doa.Doa(sid.Pop() == math.MaxUint16)
	sid.Put(15)
	doa.Doa(sid.Pop() == math.MaxUint16-1)
	doa.Doa(doa.Try(sid.Get()) == 15)
	doa.Doa(sid.Pop() == math.MaxUint16)
	doa.Doa(doa.Try(sid.Get()) == 65)
	doa.Doa(sid.Pop() == math.MaxUint16+1)
}

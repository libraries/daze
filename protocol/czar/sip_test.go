package czar

import (
	"testing"

	"github.com/libraries/daze/lib/doa"
)

func TestProtocolCzarSip(t *testing.T) {
	sid := NewSip()
	for i := range 256 {
		doa.Doa(doa.Try(sid.Get()) == uint8(i))
	}
	doa.Doa(doa.Err(sid.Get()) != nil)
	sid.Put(65)
	sid.Put(15)
	doa.Doa(doa.Try(sid.Get()) == 15)
	doa.Doa(doa.Try(sid.Get()) == 65)
}

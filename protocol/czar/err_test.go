package czar

import (
	"errors"
	"testing"

	"github.com/libraries/daze/lib/doa"
)

func TestProtocolCzarErr(t *testing.T) {
	er0 := errors.New("0")
	er1 := errors.New("1")
	e := NewErr()
	e.Put(er0)
	e.Put(er1)
	doa.Doa(e.Get() == er0)
}

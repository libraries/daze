package czar

import (
	"cmp"
	"encoding/binary"
	"io"
	"sync"
	"time"

	"github.com/libraries/daze/lib/doa"
	"github.com/libraries/daze/lib/priority"
)

// A Stream managed by the multiplexer.
type Stream struct {
	cat time.Time
	idx uint8
	mux *Mux
	rbf []byte
	rch chan []byte
	rer *Err
	wer *Err
	zo0 sync.Once
	zo1 sync.Once
}

// Close implements io.Closer.
func (s *Stream) Close() error {
	s.rer.Put(io.ErrClosedPipe)
	s.wer.Put(io.ErrClosedPipe)
	s.zo0.Do(func() {
		s.mux.pri.Pri(0, func() error {
			s.mux.con.Write([]byte{s.idx, 0x02, 0x00, 0x00})
			return nil
		})
	})
	return nil
}

// Esolc closing a stream passively.
func (s *Stream) Esolc() error {
	s.rer.Put(io.EOF)
	s.wer.Put(io.ErrClosedPipe)
	s.zo0.Do(func() {
		s.mux.pri.Pri(0, func() error {
			s.mux.con.Write([]byte{s.idx, 0x02, 0x01, 0x00})
			return nil
		})
	})
	s.zo1.Do(func() {
		s.mux.idp.Put(s.idx)
	})
	return nil
}

// Read implements io.Reader.
func (s *Stream) Read(p []byte) (int, error) {
	if len(s.rbf) != 0 {
		n := copy(p, s.rbf)
		s.rbf = s.rbf[n:]
		return n, nil
	}
	if len(s.rch) != 0 {
		s.rbf = <-s.rch
		n := copy(p, s.rbf)
		s.rbf = s.rbf[n:]
		return n, nil
	}
	if err := s.rer.Get(); err != nil {
		return 0, err
	}
	select {
	case s.rbf = <-s.rch:
		n := copy(p, s.rbf)
		s.rbf = s.rbf[n:]
		return n, nil
	case <-s.rer.Sig():
		return 0, s.rer.Get()
	case <-s.mux.rer.Sig():
		s.rer.Put(s.mux.rer.Get())
		return 0, s.mux.rer.Get()
	}
}

// Write implements io.Writer.
func (s *Stream) Write(p []byte) (int, error) {
	var (
		buf []byte
		l   = 0
		n   = 0
		z   = 0
	)
	for {
		switch {
		case len(p) >= Conf.PacketSize-4:
			buf = make([]byte, Conf.PacketSize)
			l = Conf.PacketSize - 4
		case len(p) >= 1:
			buf = make([]byte, 4+len(p))
			l = len(p)
		case len(p) >= 0:
			return n, nil
		}
		buf[0] = s.idx
		buf[1] = 0x01
		binary.BigEndian.PutUint16(buf[2:4], uint16(l))
		copy(buf[4:], p[:l])
		p = p[l:]
		z = 2 - max(cmp.Compare(Conf.FastWriteDuration, time.Since(s.cat)), 0)
		doa.Doa(z >= 1)
		doa.Doa(z <= 2)
		err := s.mux.pri.Pri(z, func() error {
			if err := s.wer.Get(); err != nil {
				return err
			}
			_, err := s.mux.con.Write(buf)
			if err != nil {
				s.wer.Put(err)
				return err
			}
			return nil
		})
		if err != nil {
			return n, err
		}
		n += l
	}
}

// NewStream returns a new Stream.
func NewStream(idx uint8, mux *Mux) *Stream {
	return &Stream{
		cat: time.Now(),
		idx: idx,
		mux: mux,
		rbf: make([]byte, 0),
		rch: make(chan []byte, 32),
		rer: NewErr(),
		wer: NewErr(),
		zo0: sync.Once{},
		zo1: sync.Once{},
	}
}

// NewWither returns a new Stream. Stream has been automatically closed, used as a placeholder.
func NewWither(idx uint8, mux *Mux) *Stream {
	stm := NewStream(idx, mux)
	stm.zo0.Do(func() {})
	stm.zo1.Do(func() {})
	stm.Close()
	return stm
}

// Mux is used to wrap a reliable ordered connection and to multiplex it into multiple streams.
type Mux struct {
	ach chan *Stream
	con io.ReadWriteCloser
	idp *Sip
	pri *priority.Priority
	rer *Err
	usb []*Stream
}

// Accept is used to block until the next available stream is ready to be accepted.
func (m *Mux) Accept() chan *Stream {
	return m.ach
}

// Close closes the connection.
// Any blocked Read or Write operations will be unblocked and return errors.
func (m *Mux) Close() error {
	return m.con.Close()
}

// Open is used to create a new stream as a io.ReadWriteCloser.
func (m *Mux) Open() (*Stream, error) {
	var (
		err error
		idx uint8
		stm *Stream
	)
	idx, err = m.idp.Get()
	if err != nil {
		return nil, err
	}
	err = m.pri.Pri(0, func() error {
		return doa.Err(m.con.Write([]byte{idx, 0x00, 0x00, 0x00}))
	})
	if err != nil {
		m.idp.Put(idx)
		return nil, err
	}
	stm = NewStream(idx, m)
	m.usb[idx] = stm
	return stm, nil
}

// Recv continues to receive data until a fatal error is encountered.
func (m *Mux) Recv() {
	var (
		bsz uint16
		buf = make([]byte, 4)
		cmd uint8
		err error
		idx uint8
		msg []byte
		old *Stream
		prb = time.AfterFunc(Conf.IdleProbeDuration, func() {
			if m.pri.Pri(0, func() error {
				return doa.Err(m.con.Write([]byte{0x00, 0x03, 0x00, 0x00}))
			}) != nil {
				m.Close()
			}
		})
		rst = time.AfterFunc(Conf.IdleReplyDuration, func() {
			m.Close()
		})
		stm *Stream
	)
	for {
		prb.Reset(Conf.IdleProbeDuration)
		rst.Reset(Conf.IdleReplyDuration)
		_, err = io.ReadFull(m.con, buf[:4])
		if err != nil {
			m.rer.Put(err)
			break
		}
		idx = buf[0]
		cmd = buf[1]
		switch {
		case cmd == 0x00:
			// Make sure the stream has been closed properly.
			old = m.usb[idx]
			if old.rer.Get() == nil || old.wer.Get() == nil {
				m.con.Close()
				break
			}
			stm = NewStream(idx, m)
			m.idp.Set(idx)
			m.usb[idx] = stm
			m.ach <- stm
		case cmd == 0x01:
			bsz = binary.BigEndian.Uint16(buf[2:4])
			msg = make([]byte, bsz)
			_, err = io.ReadFull(m.con, msg)
			if err != nil {
				m.con.Close()
				break
			}
			stm = m.usb[idx]
			if stm.rer.Get() != nil {
				break
			}
			select {
			case stm.rch <- msg:
			case <-stm.rer.Sig():
			}
		case cmd == 0x02:
			stm = m.usb[idx]
			stm.Esolc()
			old = NewWither(idx, m)
			m.usb[idx] = old
		case cmd == 0x03:
			switch buf[2] {
			case 0x00:
				m.pri.Pri(0, func() error {
					return doa.Err(m.con.Write([]byte{0x00, 0x03, 0x01, 0x00}))
				})
			case 0x01:
			}
		case cmd >= 0x04:
			// Packet format error, connection closed.
			m.con.Close()
		}
	}
	close(m.ach)
}

// NewMux returns a new Mux.
func NewMux(conn io.ReadWriteCloser) *Mux {
	mux := &Mux{
		ach: make(chan *Stream),
		con: conn,
		idp: NewSip(),
		pri: priority.NewPriority(3),
		rer: NewErr(),
		usb: make([]*Stream, 256),
	}
	return mux
}

// NewMuxServer returns a new MuxServer.
func NewMuxServer(conn io.ReadWriteCloser) *Mux {
	mux := NewMux(conn)
	for i := range 256 {
		mux.usb[i] = NewWither(uint8(i), mux)
	}
	go mux.Recv()
	return mux
}

// NewMuxClient returns a new MuxClient.
func NewMuxClient(conn io.ReadWriteCloser) *Mux {
	mux := NewMux(conn)
	go mux.Recv()
	return mux
}

package etch

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/libraries/daze/lib/doa"
	"github.com/libraries/daze/lib/once"
)

// Protocol etch is a reliable, ordered, stream-oriented protocol implemented on top of udp. We call it rudp. It is
// Shaped after tcp: data is sent as a stream of bytes, segments may arrive out of order or be lost, and the protocol
// Is responsible for reassembly, retransmission, flow control and congestion control.
//
// The packet format is fixed 16-byte header followed by an optional payload:
//
// +-----+-----+-----+-----+
// | Cmd | Rsv |    Len    |
// +-----+-----+-----+-----+
// |          Seq          |
// +-----+-----+-----+-----+
// |          Ack          |
// +-----+-----+-----+-----+
// |          Win          |
// +-----+-----+-----+-----+
// |          Msg          |
// +-----+-----+-----+-----+
//
// - Cmd : 0x00 SYN    : Handshake initiation, consumes one byte of sequence space
//         0x01 SYNACK : Handshake response, consumes one byte of sequence space
//         0x02 ACK    : Pure ack or data carrier, payload may be empty
//         0x03 FIN    : Half-close, consumes one byte of sequence space
//         0x04 RST    : Reset, abort the connection
// - Len : Msg length, 0 to 65535
// - Seq : Sequence number of the first byte of payload, modulo 2^32
// - Ack : The next byte expected from the peer, cumulative
// - Win : Free space in the receive buffer of the sender of this packet, used for flow control
// - Msg : Data, may be empty. Only carries when cmd is ACK; SYN and FIN do not carry payload.
//
// Reliability is provided by sequence numbers, cumulative acknowledgements, a sliding window of inflight segments,
// Timeout based retransmission with an rfc6298 style rto estimator, and a fast retransmit triggered by three
// Duplicate acknowledgements. Congestion control follows the classic tcp-tahoe scheme of slow start and congestion
// Avoidance with aimd on loss. Both the congestion window and the peer advertised window are measured in bytes; the
// Sender is allowed to inject up to min(cwnd, rwnd) bytes into the network at any time.

// Conf is acting as package level configuration.
var Conf = struct {
	// Upper clamp for the retransmission timeout.
	RTOMax time.Duration
	// Lower clamp for the retransmission timeout.
	RTOMin time.Duration
	// The initial retransmission timeout, used until the first rtt sample.
	RTONew time.Duration

	// The number of times the handshake may be retransmitted before the dial gives up.
	HandshakeRetries int
	// The total time budget for completing a handshake.
	HandshakeTimeout time.Duration
	// If no data is read from the peer for more than this time, the connection is closed.
	IdleResetInterval time.Duration
	// The maximum size of the payload carried by a single segment.
	MaxSegmentSize int
	// The capacity of the receive buffer. This is the maximum window the connection will advertise.
	RecvBufSize int
	// The capacity of the send buffer. Write will block when the buffer is full.
	SendBufSize int
	// The interval at which the engine wakes up to scan for retransmission timeouts.
	TickInterval time.Duration
}{
	RTOMax: time.Second * 120,
	RTOMin: time.Second / 5,
	RTONew: time.Second * 1,

	HandshakeRetries:  6,
	HandshakeTimeout:  time.Second * 8,
	IdleResetInterval: time.Second * 64,
	MaxSegmentSize:    1200,
	RecvBufSize:       256 * 1024,
	SendBufSize:       256 * 1024,
	TickInterval:      time.Millisecond * 10,
}

const (
	cmdSyn    uint8 = 0x00
	cmdSynack uint8 = 0x01
	cmdAck    uint8 = 0x02
	cmdFin    uint8 = 0x03
	cmdRst    uint8 = 0x04
)

const (
	stateClosed      int = iota // Initial state. No handshake has happened.
	stateSynSent                // Client: sent syn, waiting for synack
	stateSynRcvd                // Server: sent synack, waiting for ack
	stateEstablished            // Bidirectional data transfer
	stateFinWait                // App called close, fin sent, waiting for it to be acked
	stateCloseWait              // Peer sent fin, app may still write
	stateLastAck                // App closed after peer closed, fin sent, waiting for ack
	stateClosing                // Simultaneous close
	stateDead                   // Terminal
)

// Packet is the decoded form of a single wire packet.
type Packet struct {
	cmd uint8
	seq uint32
	ack uint32
	win uint32
	msg []byte
}

// PacketEncode encodes a packet into wire format.
func PacketEncode(p Packet) []byte {
	doa.Doa(len(p.msg) <= math.MaxUint16)
	buf := make([]byte, 0x10+len(p.msg))
	buf[0] = p.cmd
	binary.BigEndian.PutUint16(buf[0x02:0x04], uint16(len(p.msg)))
	binary.BigEndian.PutUint32(buf[0x04:0x08], p.seq)
	binary.BigEndian.PutUint32(buf[0x08:0x0C], p.ack)
	binary.BigEndian.PutUint32(buf[0x0C:0x10], p.win)
	copy(buf[0x10:], p.msg)
	return buf
}

// PacketDecode decodes a packet from wire format.
func PacketDecode(buf []byte) (Packet, error) {
	if len(buf) < 0x10 {
		return Packet{}, errors.New("daze: packet format error")
	}
	n := int(binary.BigEndian.Uint16(buf[2:4]))
	if len(buf) < 0x10+n {
		return Packet{}, errors.New("daze: packet format error")
	}
	return Packet{
		cmd: buf[0],
		seq: binary.BigEndian.Uint32(buf[0x04:0x08]),
		ack: binary.BigEndian.Uint32(buf[0x08:0x0C]),
		win: binary.BigEndian.Uint32(buf[0x0C:0x10]),
		msg: slices.Clone(buf[0x10 : 0x10+n]),
	}, nil
}

// SeqLt, SeqLe and SeqGe implement RFC 1982 serial number arithmetic for the 32-bit sequence space
// (https://www.rfc-editor.org/rfc/rfc1982). Sequence numbers are unsigned 32-bit integers that wrap around modulo
// 2^32, so a naive comparison (a < b) gives the wrong answer once the counter crosses the boundary.
//
// RFC 1982 defines the correct ordering by casting the unsigned difference to a signed integer: int32(a-b) is negative
// if a is "behind" b in sequence space. The trick exploits the fact that uint32 subtraction already wraps modulo 2^32,
// so when a is just before the wrap point and b has just crossed it, a-b underflows to a large positive uint32 whose
// int32 interpretation is negative, correctly placing a before b. The contract holds as long as a and b are within
// 2^31 of each other, which is always true for a sliding-window protocol with a window much smaller than the sequence
// space.
func SeqLt(a, b uint32) bool { return int32(a-b) < 0 }
func SeqGe(a, b uint32) bool { return int32(a-b) >= 0 }
func SeqLe(a, b uint32) bool { return int32(a-b) <= 0 }

// ConnCli wraps a dedicated udp socket connected to a single remote.
type ConnCli struct {
	udp *net.UDPConn
}

// Close closes the underlying socket.
func (l *ConnCli) Close() error {
	return l.udp.Close()
}

// Read reads a packet from the wire.
func (l *ConnCli) Read(buf []byte) (int, error) {
	return l.udp.Read(buf)
}

// Write writes a packet to the wire.
func (l *ConnCli) Write(p []byte) (int, error) {
	return l.udp.Write(p)
}

// ConnSrv is owned by a Listener and routes packets via a channel.
type ConnSrv struct {
	err *once.OnceErr
	inp chan []byte
	lst *Listener
	rem *net.UDPAddr
	udp *net.UDPConn
}

// Close marks the link as closed and asks the listener to forget this peer.
func (l *ConnSrv) Close() error {
	l.err.Put(net.ErrClosed)
	l.lst.detach(l.rem.String())
	return nil
}

// Read blocks until the listener delivers a packet or the link is closed.
func (l *ConnSrv) Read(buf []byte) (int, error) {
	select {
	case p := <-l.inp:
		return copy(buf, p), nil
	case <-l.err.Sig():
		return 0, l.err.Get()
	}
}

// Write writes a packet to the peer.
func (l *ConnSrv) Write(p []byte) (int, error) {
	return l.udp.WriteToUDP(p, l.rem)
}

// Segment is a unit of data tracked by the sliding window. Each segment carries either DATA or FIN, and consumes seql
// Bytes of sequence space (len(data) for DATA, 1 for FIN).
type segment struct {
	cmd   uint8
	seq   uint32
	seql  uint32
	data  []byte
	sent  time.Time
	rto   time.Duration
	tries int
}

// Stream is a reliable bidirectional byte stream over the udp link. It implements io.ReadWriteCloser. All internal
// State is guarded by mu, and progress is announced via cnd.
type Stream struct {
	addr net.Addr
	lnk  io.ReadWriteCloser

	mu  sync.Mutex
	cnd *sync.Cond
	sid int           // State machine identifier
	rer *once.OnceErr // Read side sticky error
	wer *once.OnceErr // Write side sticky error

	// Send side.
	sndBuf      []byte     // Bytes accepted by Write but not yet placed into a segment
	sndUna      uint32     // Oldest unacked sequence number
	sndNxt      uint32     // Next sequence number to assign
	sndFin      bool       // App has called Close, fin must be sent after sndBuf drains
	sndFinSeq   uint32     // Sequence number assigned to the fin segment
	sndFinDone  bool       // Fin segment has been pushed into the inflight queue
	sndFinAcked bool       // Fin segment has been cumulatively acknowledged
	sndAckPnd   bool       // A pure ack is pending and must be sent
	inflight    []*segment // Sent but not yet acked, sorted by seq
	rwnd        uint32     // Last window advertised by peer
	cwnd        uint32     // Congestion window
	ssthresh    uint32
	dupAck      int
	dupAckSeq   uint32

	// RTT estimator following rfc6298.
	srtt   time.Duration
	rttvar time.Duration
	rto    time.Duration

	// Recv side.
	rcvBuf    []byte            // Contiguous bytes ready to be delivered to Read
	rcvNxt    uint32            // Next byte expected from peer
	rcvOoo    map[uint32][]byte // Out-of-order segments keyed by their starting seq
	rcvOooSz  int               // Bytes held in rcvOoo
	rcvFin    bool              // Peer has sent fin
	rcvFinSeq uint32            // Sequence number consumed by peer's fin

	// Background bookkeeping.
	lastRecv time.Time
	cancel   chan struct{}
	once     sync.Once
}

// NewConn allocates a Stream with default state.
func newConn(lnk io.ReadWriteCloser, addr net.Addr) *Stream {
	c := &Stream{
		addr:     addr,
		lnk:      lnk,
		sid:      stateClosed,
		rer:      once.NewOnceErr(),
		wer:      once.NewOnceErr(),
		sndBuf:   make([]byte, 0, Conf.SendBufSize),
		rcvOoo:   make(map[uint32][]byte),
		rwnd:     uint32(Conf.MaxSegmentSize),
		cwnd:     uint32(Conf.MaxSegmentSize),
		ssthresh: math.MaxUint32,
		rto:      Conf.RTONew,
		lastRecv: time.Now(),
		cancel:   make(chan struct{}),
	}
	c.cnd = sync.NewCond(&c.mu)
	return c
}

// RcvWnd returns the receive window currently advertised to the peer.
func (c *Stream) rcvWnd() uint32 {
	n := max(Conf.RecvBufSize-len(c.rcvBuf)-c.rcvOooSz, 0)
	return uint32(n)
}

// InflightBytes returns the total bytes currently in the sliding window.
func (c *Stream) inflightBytes() uint32 {
	return c.sndNxt - c.sndUna
}

// Sendable returns the maximum new bytes that may be put on the wire right now.
func (c *Stream) sendable() uint32 {
	cwnd := c.cwnd
	rwnd := c.rwnd
	win := min(rwnd, cwnd)
	infl := c.inflightBytes()
	if infl >= win {
		return 0
	}
	return win - infl
}

// Emit pushes a packet to the link. It must be called with mu held; the link write itself happens under the lock to
// Preserve send ordering relative to state updates.
func (c *Stream) emit(p Packet) error {
	p.ack = c.rcvNxt
	p.win = c.rcvWnd()
	_, err := c.lnk.Write(PacketEncode(p))
	return err
}

// Fail marks the connection as dead and wakes everyone up.
func (c *Stream) fail(err error) {
	c.sid = stateDead
	c.rer.Put(err)
	c.wer.Put(err)
	c.cnd.Broadcast()
}

// ============================================================================
// Send path
// ============================================================================

// Flush moves bytes from sndBuf into the inflight queue as long as the window permits, then sends any pending pure
// Ack. Must be called with mu held.
func (c *Stream) flush() {
	// Data may be drained from sndBuf in any connected state until our own fin has been emitted. This matters during
	// Half-close: app code typically calls Write followed immediately by Close, which transitions us into fin_wait or
	// Last_ack while bytes are still queued. We must continue pushing those bytes before the fin segment.
	for !c.sndFinDone && (c.sid == stateEstablished || c.sid == stateFinWait || c.sid == stateCloseWait || c.sid == stateLastAck) {
		win := c.sendable()
		if win == 0 || len(c.sndBuf) == 0 {
			break
		}
		n := Conf.MaxSegmentSize
		if uint32(n) > win {
			n = int(win)
		}
		if n > len(c.sndBuf) {
			n = len(c.sndBuf)
		}
		data := make([]byte, n)
		copy(data, c.sndBuf[:n])
		c.sndBuf = c.sndBuf[n:]
		seg := &segment{
			cmd:  cmdAck,
			seq:  c.sndNxt,
			seql: uint32(n),
			data: data,
			sent: time.Now(),
			rto:  c.rto,
		}
		c.sndNxt += uint32(n)
		c.inflight = append(c.inflight, seg)
		if err := c.emit(Packet{cmd: seg.cmd, msg: seg.data, seq: seg.seq}); err != nil {
			c.fail(err)
			return
		}
		c.sndAckPnd = false
		c.cnd.Broadcast()
	}
	// Once sndBuf is drained and app has closed, push the fin segment.
	if c.sndFin && !c.sndFinDone && len(c.sndBuf) == 0 && c.sendable() >= 1 {
		seg := &segment{
			cmd:  cmdFin,
			seq:  c.sndNxt,
			seql: 1,
			sent: time.Now(),
			rto:  c.rto,
		}
		c.sndFinSeq = c.sndNxt
		c.sndFinDone = true
		c.sndNxt += 1
		c.inflight = append(c.inflight, seg)
		if err := c.emit(Packet{cmd: seg.cmd, seq: seg.seq}); err != nil {
			c.fail(err)
			return
		}
		c.sndAckPnd = false
		c.cnd.Broadcast()
	}
	// Emit a standalone ack if one was scheduled and we did not piggyback any data.
	if c.sndAckPnd {
		if err := c.emit(Packet{cmd: cmdAck, seq: c.sndNxt}); err != nil {
			c.fail(err)
			return
		}
		c.sndAckPnd = false
	}
}

// Retransmit walks the inflight queue and resends any segment whose rto has expired. Must be called with mu held.
func (c *Stream) retransmit(now time.Time) {
	for _, seg := range c.inflight {
		if now.Sub(seg.sent) < seg.rto {
			continue
		}
		// Loss event: collapse cwnd, double rto (karn's algorithm rules out an rtt sample on this segment).
		c.ssthresh = max(c.inflightBytes()/2, uint32(Conf.MaxSegmentSize)*2)
		c.cwnd = uint32(Conf.MaxSegmentSize)
		seg.tries++
		seg.rto *= 2
		if seg.rto > Conf.RTOMax {
			seg.rto = Conf.RTOMax
		}
		c.rto = seg.rto
		seg.sent = now
		var pl []byte
		if seg.cmd == cmdAck {
			pl = seg.data
		}
		if err := c.emit(Packet{cmd: seg.cmd, msg: pl, seq: seg.seq}); err != nil {
			c.fail(err)
			return
		}
	}
}

// Ack processes a cumulative acknowledgement. It removes acked segments from the inflight queue, updates the rtt
// Estimator and the congestion window. Must be called with mu held.
func (c *Stream) ack(ackNum uint32, win uint32) {
	c.rwnd = win
	if SeqGe(c.sndUna, ackNum) && ackNum != c.sndUna {
		return
	}
	if SeqLt(ackNum, c.sndUna) {
		// Stale ack.
		return
	}
	if ackNum == c.sndUna {
		// Duplicate ack. Drives fast retransmit.
		if c.dupAckSeq == ackNum {
			c.dupAck++
		} else {
			c.dupAckSeq = ackNum
			c.dupAck = 1
		}
		if c.dupAck == 3 && len(c.inflight) > 0 {
			c.ssthresh = max(c.inflightBytes()/2, uint32(Conf.MaxSegmentSize)*2)
			c.cwnd = c.ssthresh
			seg := c.inflight[0]
			seg.tries++
			seg.sent = time.Now()
			var pl []byte
			if seg.cmd == cmdAck {
				pl = seg.data
			}
			if err := c.emit(Packet{cmd: seg.cmd, seq: seg.seq, msg: pl}); err != nil {
				c.fail(err)
				return
			}
		}
		return
	}
	// New ack.
	c.dupAck = 0
	c.dupAckSeq = ackNum
	now := time.Now()
	keep := c.inflight[:0]
	for _, seg := range c.inflight {
		segEnd := seg.seq + seg.seql
		if SeqLe(segEnd, ackNum) {
			// Fully acked. Take an rtt sample only when this segment was not retransmitted.
			if seg.tries == 0 {
				c.observeRTT(now.Sub(seg.sent))
			}
			// Slow start vs congestion avoidance.
			mss := uint32(Conf.MaxSegmentSize)
			if c.cwnd < c.ssthresh {
				c.cwnd += seg.seql
			} else {
				inc := max(mss*mss/c.cwnd, 1)
				c.cwnd += inc
			}
			continue
		}
		keep = append(keep, seg)
	}
	c.inflight = keep
	c.sndUna = ackNum
	c.cnd.Broadcast()
}

// ObserveRTT folds a new rtt sample into the estimator. Must be called with mu held.
func (c *Stream) observeRTT(sample time.Duration) {
	switch {
	case c.srtt == 0:
		c.srtt = sample
		c.rttvar = sample / 2
	case c.srtt != 0:
		c.rttvar = (3*c.rttvar + (c.srtt - sample).Abs()) / 4
		c.srtt = (7*c.srtt + sample) / 8
	}
	c.rto = min(max(c.srtt+4*c.rttvar, Conf.RTOMin), Conf.RTOMax)
}

// ============================================================================
// Recv path
// ============================================================================

// Deliver appends an in-order data slice to the recv buffer and tries to drain rcvOoo. Must be called with mu held.
func (c *Stream) deliver(data []byte) {
	c.rcvBuf = append(c.rcvBuf, data...)
	c.rcvNxt += uint32(len(data))
	for {
		nxt, ok := c.rcvOoo[c.rcvNxt]
		if !ok {
			break
		}
		delete(c.rcvOoo, c.rcvNxt)
		c.rcvOooSz -= len(nxt)
		c.rcvBuf = append(c.rcvBuf, nxt...)
		c.rcvNxt += uint32(len(nxt))
	}
}

// Handle processes a single decoded packet. Must be called with mu held.
func (c *Stream) handle(p Packet) {
	c.lastRecv = time.Now()
	switch p.cmd {
	case cmdRst:
		c.fail(errors.New("daze: connection reset by peer"))
		return
	case cmdSyn:
		// Spurious or retransmitted syn. Reply with a syn-ack mirroring our state to push the handshake forward.
		if c.sid == stateSynRcvd {
			c.emit(Packet{cmd: cmdSynack, seq: 0})
		}
		return
	case cmdSynack:
		switch c.sid {
		case stateSynSent:
			c.sndUna = 1
			c.sndNxt = 1
			c.rcvNxt = 1
			c.sid = stateEstablished
			c.sndAckPnd = true
			c.cnd.Broadcast()
		case stateEstablished:
			// Peer retransmitted synack because our ack was lost. Re-ack.
			c.sndAckPnd = true
		}
		return
	}

	// At this point cmd is ACK or FIN.
	if c.sid == stateSynRcvd && SeqLe(1, p.ack) {
		c.sndUna = 1
		c.sndNxt = 1
		c.sid = stateEstablished
		c.cnd.Broadcast()
	}
	if c.sid == stateClosed || c.sid == stateSynSent || c.sid == stateSynRcvd {
		return
	}

	// Cumulative ack handling.
	c.ack(p.ack, p.win)

	// FIN acknowledgement may complete a half-close.
	if c.sndFinDone && !c.sndFinAcked && SeqLt(c.sndFinSeq, p.ack) {
		c.sndFinAcked = true
		c.cnd.Broadcast()
	}

	// Payload bookkeeping.
	if p.cmd == cmdAck && len(p.msg) > 0 {
		c.intake(p.seq, p.msg)
		c.sndAckPnd = true
	}
	if p.cmd == cmdFin {
		// Treat fin as a one byte segment at p.seq.
		c.intakeFin(p.seq)
		c.sndAckPnd = true
	}
}

// Intake places a data segment into the recv pipeline, handling reorder and dedup. Must be called with mu held.
func (c *Stream) intake(seq uint32, data []byte) {
	// Drop data that falls entirely before rcvNxt.
	if SeqLe(seq+uint32(len(data)), c.rcvNxt) {
		return
	}
	// Trim already received prefix.
	if SeqLt(seq, c.rcvNxt) {
		skip := c.rcvNxt - seq
		data = data[skip:]
		seq = c.rcvNxt
	}
	// Drop data that overflows the advertised window.
	free := c.rcvWnd()
	if uint32(len(data)) > free {
		data = data[:free]
	}
	if len(data) == 0 {
		return
	}
	if seq == c.rcvNxt {
		c.deliver(data)
		c.cnd.Broadcast()
	} else {
		if _, ok := c.rcvOoo[seq]; !ok {
			c.rcvOoo[seq] = append([]byte(nil), data...)
			c.rcvOooSz += len(data)
		}
	}
}

// IntakeFin records a fin segment, possibly via the out-of-order map until preceding data arrives. Must be called
// With mu held.
func (c *Stream) intakeFin(seq uint32) {
	if SeqLt(seq, c.rcvNxt) {
		return
	}
	if seq == c.rcvNxt {
		c.rcvFin = true
		c.rcvFinSeq = seq
		c.rcvNxt = seq + 1
		c.rer.Put(io.EOF)
		switch c.sid {
		case stateEstablished:
			c.sid = stateCloseWait
		case stateFinWait:
			c.sid = stateClosing
		}
		c.cnd.Broadcast()
		return
	}
	// We received fin before all data; remember and process later.
	if _, ok := c.rcvOoo[seq]; !ok {
		// Marker: empty slice means fin sits here.
		c.rcvOoo[seq] = []byte{}
	}
}

// ============================================================================
// Engine: background recv loop and periodic ticker
// ============================================================================

// RecvLoop reads packets from the link until it fails.
func (c *Stream) recvLoop() {
	buf := make([]byte, 65536)
	for {
		n, err := c.lnk.Read(buf)
		if err != nil {
			c.mu.Lock()
			c.fail(err)
			c.mu.Unlock()
			return
		}
		p, err := PacketDecode(buf[:n])
		if err != nil {
			continue
		}
		c.mu.Lock()
		c.handle(p)
		c.flush()
		// Promote any out-of-order fin marker that is now in order.
		if v, ok := c.rcvOoo[c.rcvNxt]; ok && len(v) == 0 {
			delete(c.rcvOoo, c.rcvNxt)
			c.intakeFin(c.rcvNxt)
		}
		c.mu.Unlock()
	}
}

// TickLoop wakes up periodically to retransmit expired segments and to enforce the idle deadline.
func (c *Stream) tickLoop() {
	t := time.NewTicker(Conf.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
		case <-c.cancel:
			return
		}
		c.mu.Lock()
		if c.sid == stateDead {
			c.mu.Unlock()
			return
		}
		if time.Since(c.lastRecv) > Conf.IdleResetInterval {
			c.fail(errors.New("daze: connection timed out"))
			c.mu.Unlock()
			return
		}
		c.retransmit(time.Now())
		c.flush()
		c.mu.Unlock()
	}
}

// ============================================================================
// Public API: io.ReadWriteCloser
// ============================================================================

// Read implements io.Reader.
func (c *Stream) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if len(c.rcvBuf) > 0 {
			n := copy(p, c.rcvBuf)
			c.rcvBuf = c.rcvBuf[n:]
			// Receive buffer just freed up, advertise the new window.
			c.sndAckPnd = true
			c.flush()
			return n, nil
		}
		if err := c.rer.Get(); err != nil {
			return 0, err
		}
		c.cnd.Wait()
	}
}

// Write implements io.Writer.
func (c *Stream) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for len(p) > 0 {
		if err := c.wer.Get(); err != nil {
			return total, err
		}
		free := Conf.SendBufSize - len(c.sndBuf)
		if free <= 0 {
			c.cnd.Wait()
			continue
		}
		n := min(free, len(p))
		c.sndBuf = append(c.sndBuf, p[:n]...)
		p = p[n:]
		total += n
		c.flush()
	}
	return total, nil
}

// Close implements io.Closer. It performs an orderly half-close: any bytes already accepted by Write are pushed to
// The peer first, then a fin is sent and acknowledged. The call blocks for at most HandshakeTimeout before forcibly
// Tearing down the link. After Close returns the connection lingers briefly so that any incoming peer fin can still
// Be acknowledged.
func (c *Stream) Close() error {
	c.mu.Lock()
	if c.sid == stateDead || c.wer.Get() != nil {
		c.mu.Unlock()
		c.shutdown()
		return nil
	}
	if !c.sndFin {
		c.sndFin = true
		switch c.sid {
		case stateEstablished:
			c.sid = stateFinWait
		case stateCloseWait:
			c.sid = stateLastAck
		}
		c.flush()
	}
	deadline := time.Now().Add(Conf.HandshakeTimeout)
	done := make(chan struct{})
	go func() {
		t := time.NewTimer(time.Until(deadline))
		defer t.Stop()
		select {
		case <-t.C:
			c.mu.Lock()
			c.cnd.Broadcast()
			c.mu.Unlock()
		case <-done:
		}
	}()
	for !c.sndFinAcked && c.sid != stateDead && time.Now().Before(deadline) {
		c.cnd.Wait()
	}
	close(done)
	c.wer.Put(io.ErrClosedPipe)
	c.mu.Unlock()
	// Linger so we can acknowledge a peer fin that arrives just after our own fin was acked. This avoids stranding the
	// Peer in fin_wait until its idle deadline expires.
	go func() {
		t := time.NewTimer(time.Second)
		defer t.Stop()
		select {
		case <-t.C:
		case <-c.rer.Sig():
			// Peer has finished too; wait a tick to drain the final ack and then tear down.
			time.Sleep(50 * time.Millisecond)
		}
		c.shutdown()
	}()
	return nil
}

// Shutdown tears down background goroutines and the link. Safe to call multiple times.
func (c *Stream) shutdown() {
	c.once.Do(func() {
		close(c.cancel)
		c.lnk.Close()
	})
}

// RemoteAddr returns the network address of the peer.
func (c *Stream) RemoteAddr() net.Addr { return c.addr }

// ============================================================================
// Handshake (client side)
// ============================================================================

// DialHandshake performs the active open. It must be called before the engine goroutines have read any packet.
func (c *Stream) dialHandshake() error {
	c.mu.Lock()
	c.sid = stateSynSent
	c.mu.Unlock()
	// Send syn synchronously and wait for syn-ack with exponential backoff.
	rto := Conf.RTONew
	deadline := time.Now().Add(Conf.HandshakeTimeout)
	for try := range Conf.HandshakeRetries {
		_ = try
		if _, err := c.lnk.Write(PacketEncode(Packet{cmd: cmdSyn, seq: 0, win: uint32(Conf.RecvBufSize)})); err != nil {
			return err
		}
		// Read with a timeout managed by the udp socket. dialLink uses a connected udp socket; SetReadDeadline is
		// Needed. We poke into the concrete type to set the deadline.
		dl, ok := c.lnk.(*ConnCli)
		if !ok {
			return errors.New("daze: invalid dial link")
		}
		until := time.Now().Add(rto)
		if until.After(deadline) {
			until = deadline
		}
		dl.udp.SetReadDeadline(until)
		buf := make([]byte, 65536)
		n, err := dl.udp.Read(buf)
		dl.udp.SetReadDeadline(time.Time{})
		if err != nil {
			if time.Now().After(deadline) {
				return errors.New("daze: handshake timeout")
			}
			rto *= 2
			if rto > Conf.RTOMax {
				rto = Conf.RTOMax
			}
			continue
		}
		p, err := PacketDecode(buf[:n])
		if err != nil {
			continue
		}
		if p.cmd != cmdSynack {
			continue
		}
		// Handshake completed. Ack the synack and enter established.
		c.mu.Lock()
		c.sndUna = 1
		c.sndNxt = 1
		c.rcvNxt = 1
		c.rwnd = p.win
		c.sid = stateEstablished
		c.lastRecv = time.Now()
		c.emit(Packet{cmd: cmdAck, seq: 1})
		c.mu.Unlock()
		return nil
	}
	return errors.New("daze: handshake exhausted")
}

// ============================================================================
// Dial
// ============================================================================

// Dial connects to the etch server at address. The returned Stream is full-duplex and stream oriented.
func Dial(address string) (*Stream, error) {
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	udp, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}
	c := newConn(&ConnCli{udp: udp}, udp.RemoteAddr())
	if err := c.dialHandshake(); err != nil {
		udp.Close()
		return nil, err
	}
	go c.recvLoop()
	go c.tickLoop()
	return c, nil
}

// ============================================================================
// Listener: server side demultiplexer
// ============================================================================

// Listener accepts incoming etch connections on a single udp socket.
type Listener struct {
	udp    *net.UDPConn
	mu     sync.Mutex
	conns  map[string]*ConnSrv
	accept chan *Stream
	closed *once.OnceErr
}

// Listen announces on the local network address and returns a Listener.
func Listen(address string) (*Listener, error) {
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	udp, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	l := &Listener{
		udp:    udp,
		conns:  make(map[string]*ConnSrv),
		accept: make(chan *Stream, 32),
		closed: once.NewOnceErr(),
	}
	go l.demux()
	return l, nil
}

// Detach removes a peer from the routing table.
func (l *Listener) detach(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.conns, key)
}

// Demux reads udp packets and routes them to the matching connection. New peers that send a syn cause a fresh Stream
// To be created and pushed into the accept queue.
func (l *Listener) demux() {
	buf := make([]byte, 65536)
	for {
		n, addr, err := l.udp.ReadFromUDP(buf)
		if err != nil {
			l.closed.Put(err)
			close(l.accept)
			return
		}
		key := addr.String()
		l.mu.Lock()
		sl, ok := l.conns[key]
		l.mu.Unlock()
		if ok {
			select {
			case sl.inp <- append([]byte(nil), buf[:n]...):
			default:
				// Receiver is overwhelmed; dropping a packet is a normal loss event for rudp.
			}
			continue
		}
		// Unknown peer. Only react to syn; everything else is silently dropped.
		if n < 16 || buf[0] != cmdSyn {
			continue
		}
		sl = &ConnSrv{
			err: once.NewOnceErr(),
			inp: make(chan []byte, 64),
			lst: l,
			rem: addr,
			udp: l.udp,
		}
		l.mu.Lock()
		l.conns[key] = sl
		l.mu.Unlock()
		c := newConn(sl, addr)
		// Seed the handshake: peer just sent us a syn, mirror back a syn-ack.
		c.mu.Lock()
		c.sid = stateSynRcvd
		c.rcvNxt = 1
		c.lastRecv = time.Now()
		c.lnk.Write(PacketEncode(Packet{cmd: cmdSynack, seq: 0, win: uint32(Conf.RecvBufSize)}))
		// Forward the original syn so the loop counters stay consistent.
		c.mu.Unlock()
		go c.recvLoop()
		go c.tickLoop()
		select {
		case l.accept <- c:
		default:
			// Backlog full; drop the connection.
			c.shutdown()
		}
	}
}

// Accept blocks until the next available connection. The channel is closed when the listener is closed.
func (l *Listener) Accept() chan *Stream { return l.accept }

// Close terminates the listener. Established connections are not closed.
func (l *Listener) Close() error {
	l.closed.Put(net.ErrClosed)
	return l.udp.Close()
}

// Addr returns the network address the listener is bound to.
func (l *Listener) Addr() net.Addr { return l.udp.LocalAddr() }

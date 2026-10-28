package fec

import (
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/wireguard-go/conn"
)

type sender struct {
	encoder  *encoder
	endpoint conn.Endpoint
	timer    *time.Timer
	used     time.Time
}

type Bind struct {
	inner      conn.Bind
	codec      *codec
	logger     logger.Logger
	lifecycle  sync.Mutex
	io         sync.RWMutex
	mu         sync.Mutex
	open       bool
	generation uint64
	senders    map[string]*sender
	decoder    *decoder
}

type fdBind struct {
	*Bind
	conn.PeekLookAtSocketFd
}
type interfaceBind struct {
	*Bind
	conn.BindSocketToInterface
}
type platformBind struct {
	*Bind
	conn.PeekLookAtSocketFd
	conn.BindSocketToInterface
}

// NewBind preserves the platform socket-protection interfaces of the original bind.
func NewBind(inner conn.Bind, config Config, logger logger.Logger) (conn.Bind, error) {
	c, err := newCodec(config)
	if err != nil {
		return nil, err
	}
	b := &Bind{inner: inner, codec: c, logger: logger}
	fd, hasFD := inner.(conn.PeekLookAtSocketFd)
	iface, hasInterface := inner.(conn.BindSocketToInterface)
	switch {
	case hasFD && hasInterface:
		return &platformBind{b, fd, iface}, nil
	case hasFD:
		return &fdBind{b, fd}, nil
	case hasInterface:
		return &interfaceBind{b, iface}, nil
	default:
		return b, nil
	}
}

func (b *Bind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.lifecycle.Lock()
	defer b.lifecycle.Unlock()
	b.io.Lock()
	defer b.io.Unlock()
	b.mu.Lock()
	if b.open {
		b.mu.Unlock()
		return nil, 0, conn.ErrBindAlreadyOpen
	}
	b.mu.Unlock()
	fns, actual, err := b.inner.Open(port)
	if err != nil {
		return nil, 0, err
	}
	b.mu.Lock()
	b.open = true
	b.generation++
	generation := b.generation
	b.senders = make(map[string]*sender)
	b.decoder = newDecoder(b.codec)
	decoder := b.decoder
	b.mu.Unlock()
	wrapped := make([]conn.ReceiveFunc, len(fns))
	for i, fn := range fns {
		wrapped[i] = b.receive(fn, decoder, generation)
	}
	return wrapped, actual, nil
}

func (b *Bind) current(generation uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open && b.generation == generation
}

type receivedPacket struct {
	payload  []byte
	endpoint conn.Endpoint
}

func (b *Bind) receive(read conn.ReceiveFunc, decoder *decoder, generation uint64) conn.ReceiveFunc {
	var pending []receivedPacket
	return func(packets [][]byte, sizes []int, endpoints []conn.Endpoint) (int, error) {
		if !b.current(generation) {
			return 0, net.ErrClosed
		}
		if len(packets) == 0 || len(sizes) < len(packets) || len(endpoints) < len(packets) {
			return 0, errors.New("fec: invalid receive buffers")
		}
		for len(pending) == 0 {
			n, err := read(packets, sizes, endpoints)
			if err != nil {
				return 0, err
			}
			if !b.current(generation) {
				return 0, net.ErrClosed
			}
			for i := 0; i < n; i++ {
				if endpoints[i] == nil || sizes[i] < 0 || sizes[i] > len(packets[i]) {
					continue
				}
				for _, payload := range decoder.receive(endpoints[i].DstToString(), packets[i][:sizes[i]], time.Now()) {
					pending = append(pending, receivedPacket{payload, endpoints[i]})
				}
			}
		}
		n := min(len(pending), len(packets))
		for i := 0; i < n; i++ {
			if len(pending[i].payload) > len(packets[i]) {
				return 0, errors.New("fec: receive buffer too short")
			}
			sizes[i] = copy(packets[i], pending[i].payload)
			endpoints[i] = pending[i].endpoint
			pending[i] = receivedPacket{}
		}
		pending = pending[n:]
		return n, nil
	}
}

func (b *Bind) Close() error {
	b.lifecycle.Lock()
	defer b.lifecycle.Unlock()
	b.mu.Lock()
	b.open = false
	b.generation++
	for _, s := range b.senders {
		if s.timer != nil {
			s.timer.Stop()
		}
	}
	b.senders = nil
	b.decoder = nil
	b.mu.Unlock()
	err := b.inner.Close()
	// Closing the socket first interrupts blocked writes before waiting for them.
	b.io.Lock()
	b.io.Unlock()
	return err
}

func (b *Bind) sendFrames(frames [][]byte, endpoint conn.Endpoint, generation uint64) error {
	if len(frames) == 0 {
		return nil
	}
	b.io.RLock()
	defer b.io.RUnlock()
	if !b.current(generation) {
		return net.ErrClosed
	}
	for len(frames) > 0 {
		n := min(len(frames), b.inner.BatchSize())
		if n <= 0 {
			return errors.New("fec: invalid underlying batch size")
		}
		if err := b.inner.Send(frames[:n], endpoint, 0); err != nil {
			return err
		}
		frames = frames[n:]
	}
	return nil
}

func (b *Bind) flush(s *sender, generation, block uint64) {
	b.mu.Lock()
	if !b.open || b.generation != generation || s.encoder.block != block {
		b.mu.Unlock()
		return
	}
	frames, err := s.encoder.flush()
	s.timer = nil
	endpoint := s.endpoint
	b.mu.Unlock()
	if err == nil {
		err = b.sendFrames(frames, endpoint, generation)
	}
	if err != nil && !errors.Is(err, net.ErrClosed) && b.logger != nil {
		b.logger.Error("send FEC parity: ", err)
	}
}

func (b *Bind) Send(packets [][]byte, endpoint conn.Endpoint, offset int) error {
	if endpoint == nil {
		return conn.ErrWrongEndpointType
	}
	for _, p := range packets {
		if offset < 0 || offset > len(p) || len(p)-offset == 0 || len(p)-offset > b.codec.config.MaxPayload() {
			return errors.New("fec: invalid or oversized WireGuard datagram")
		}
	}
	b.mu.Lock()
	if !b.open {
		b.mu.Unlock()
		return net.ErrClosed
	}
	generation := b.generation
	key := endpoint.DstToString()
	s := b.senders[key]
	if s == nil {
		if len(b.senders) >= maxSendPeers {
			for addr, item := range b.senders {
				if item.timer == nil && time.Since(item.used) > blockLifetime {
					delete(b.senders, addr)
				}
			}
			if len(b.senders) >= maxSendPeers {
				b.mu.Unlock()
				return errors.New("fec: too many active send peers")
			}
		}
		encoder, err := newEncoder(b.codec)
		if err != nil {
			b.mu.Unlock()
			return err
		}
		s = &sender{encoder: encoder}
		b.senders[key] = s
	}
	s.endpoint = endpoint
	s.used = time.Now()
	var frames [][]byte
	for _, p := range packets {
		packet, err := s.encoder.add(p[offset:])
		if err != nil {
			b.mu.Unlock()
			return err
		}
		frames = append(frames, packet)
		if s.timer == nil {
			block := s.encoder.block
			s.timer = time.AfterFunc(b.codec.config.Interval, func() { b.flush(s, generation, block) })
		}
		if len(s.encoder.data) == b.codec.config.DataShards {
			s.timer.Stop()
			s.timer = nil
			parity, err := s.encoder.flush()
			if err != nil {
				b.mu.Unlock()
				return err
			}
			frames = append(frames, parity...)
		}
	}
	b.mu.Unlock()
	return b.sendFrames(frames, endpoint, generation)
}

func (b *Bind) SetMark(mark uint32) error                     { return b.inner.SetMark(mark) }
func (b *Bind) ParseEndpoint(s string) (conn.Endpoint, error) { return b.inner.ParseEndpoint(s) }
func (b *Bind) BatchSize() int                                { return b.inner.BatchSize() }
func (b *Bind) SetReservedForEndpoint(destination netip.AddrPort, reserved [3]byte) {
	b.inner.SetReservedForEndpoint(destination, reserved)
}

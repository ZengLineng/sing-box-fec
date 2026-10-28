package fec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/klauspost/reedsolomon"
)

const frameType = 0xf1

type codec struct {
	config   Config
	key      []byte
	encoders []reedsolomon.Encoder
	rsMu     sync.Mutex
}

func newCodec(config Config) (*codec, error) {
	c, err := config.Normalize()
	if err != nil {
		return nil, err
	}
	key, _ := base64.StdEncoding.DecodeString(c.Key)
	result := &codec{config: c, key: key, encoders: make([]reedsolomon.Encoder, c.DataShards+1)}
	for n := 1; n <= c.DataShards; n++ {
		r := result.parity(n)
		result.encoders[n], err = reedsolomon.New(n, r, reedsolomon.WithMaxGoroutines(1), reedsolomon.WithInversionCache(false))
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (c *codec) parity(n int) int {
	return (n*c.config.ParityShards + c.config.DataShards - 1) / c.config.DataShards
}

type frame struct {
	parity     bool
	actual     int
	redundancy int
	index      int
	session    uint64
	block      uint64
	payload    []byte
}

func (c *codec) pack(f frame) []byte {
	b := make([]byte, HeaderSize+len(f.payload)+TagSize)
	b[0], b[4], b[6], b[7] = frameType, 1, byte(c.config.DataShards), byte(c.config.ParityShards)
	if f.parity {
		b[5] = 1
	}
	b[8], b[9] = byte(f.actual), byte(f.redundancy)
	binary.BigEndian.PutUint16(b[10:12], uint16(f.index))
	binary.BigEndian.PutUint64(b[12:20], f.session)
	binary.BigEndian.PutUint64(b[20:28], f.block)
	binary.BigEndian.PutUint16(b[28:30], uint16(len(f.payload)))
	b[30], b[31] = 'S', 'F'
	copy(b[HeaderSize:], f.payload)
	mac := hmac.New(sha256.New, c.key)
	mac.Write(b[:1])
	// WireGuard binds may rewrite the three transport-reserved bytes.
	mac.Write(b[4 : len(b)-TagSize])
	copy(b[len(b)-TagSize:], mac.Sum(nil)[:TagSize])
	return b
}

func (c *codec) unpack(b []byte) (frame, bool) {
	if len(b) < HeaderSize+LengthSize+TagSize || len(b) > c.config.MTU || b[0] != frameType || b[4] != 1 || b[5] > 1 || b[6] != byte(c.config.DataShards) || b[7] != byte(c.config.ParityShards) || b[30] != 'S' || b[31] != 'F' {
		return frame{}, false
	}
	length := int(binary.BigEndian.Uint16(b[28:30]))
	if length != len(b)-HeaderSize-TagSize {
		return frame{}, false
	}
	mac := hmac.New(sha256.New, c.key)
	mac.Write(b[:1])
	mac.Write(b[4 : len(b)-TagSize])
	if !hmac.Equal(mac.Sum(nil)[:TagSize], b[len(b)-TagSize:]) {
		return frame{}, false
	}
	f := frame{parity: b[5] == 1, actual: int(b[8]), redundancy: int(b[9]), index: int(binary.BigEndian.Uint16(b[10:12])), session: binary.BigEndian.Uint64(b[12:20]), block: binary.BigEndian.Uint64(b[20:28]), payload: b[HeaderSize : len(b)-TagSize]}
	if f.parity {
		if f.actual < 1 || f.actual > c.config.DataShards || f.redundancy != c.parity(f.actual) || f.index < c.config.DataShards || f.index >= c.config.DataShards+f.redundancy {
			return frame{}, false
		}
	} else {
		if f.actual != 0 || f.redundancy != 0 || f.index >= c.config.DataShards || len(f.payload) <= LengthSize || int(binary.BigEndian.Uint16(f.payload[:2])) != len(f.payload)-2 {
			return frame{}, false
		}
	}
	return f, true
}

type encoder struct {
	codec   *codec
	session uint64
	block   uint64
	data    [][]byte
}

func newEncoder(c *codec) (*encoder, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	return &encoder{codec: c, session: binary.BigEndian.Uint64(random[:])}, nil
}

func (e *encoder) add(payload []byte) ([]byte, error) {
	if len(payload) == 0 || len(payload) > e.codec.config.MaxPayload() {
		return nil, fmt.Errorf("fec: datagram size %d exceeds payload limit %d", len(payload), e.codec.config.MaxPayload())
	}
	if len(e.data) >= e.codec.config.DataShards {
		return nil, errors.New("fec: encoder group must be flushed")
	}
	vector := make([]byte, len(payload)+LengthSize)
	binary.BigEndian.PutUint16(vector, uint16(len(payload)))
	copy(vector[2:], payload)
	packet := e.codec.pack(frame{index: len(e.data), session: e.session, block: e.block, payload: vector})
	e.data = append(e.data, vector)
	return packet, nil
}

func (e *encoder) flush() ([][]byte, error) {
	n := len(e.data)
	if n == 0 {
		return nil, nil
	}
	r, length := e.codec.parity(n), 0
	for _, item := range e.data {
		if len(item) > length {
			length = len(item)
		}
	}
	shards := make([][]byte, n+r)
	for i := range shards {
		shards[i] = make([]byte, length)
		if i < n {
			copy(shards[i], e.data[i])
		}
	}
	e.codec.rsMu.Lock()
	err := e.codec.encoders[n].Encode(shards)
	e.codec.rsMu.Unlock()
	if err != nil {
		return nil, err
	}
	packets := make([][]byte, r)
	for i := range packets {
		packets[i] = e.codec.pack(frame{parity: true, actual: n, redundancy: r, index: e.codec.config.DataShards + i, session: e.session, block: e.block, payload: shards[n+i]})
	}
	e.block++
	e.data = nil
	return packets, nil
}

type blockKey struct {
	peer           string
	session, block uint64
}
type decodeBlock struct {
	created                    time.Time
	data                       [][]byte
	parity                     [][]byte
	delivered                  []bool
	actual, redundancy, length int
	bytes                      int
	deliveredCount             int
	complete                   bool
}

type decoder struct {
	mu     sync.Mutex
	codec  *codec
	blocks map[blockKey]*decodeBlock
	bytes  int
}

func newDecoder(c *codec) *decoder {
	return &decoder{codec: c, blocks: make(map[blockKey]*decodeBlock)}
}

func (d *decoder) remove(key blockKey) { d.bytes -= d.blocks[key].bytes; delete(d.blocks, key) }

func (d *decoder) complete(b *decodeBlock) {
	d.bytes -= b.bytes
	b.bytes = 0
	b.data, b.parity, b.delivered = nil, nil, nil
	b.complete = true
}

func (d *decoder) prune(now time.Time, needed int) {
	for key, b := range d.blocks {
		if now.Sub(b.created) >= blockLifetime {
			d.remove(key)
		}
	}
	for len(d.blocks) >= maxBlocks || d.bytes+needed > maxBufferedBytes {
		var oldest blockKey
		var created time.Time
		for key, b := range d.blocks {
			if created.IsZero() || b.created.Before(created) {
				oldest, created = key, b.created
			}
		}
		if created.IsZero() {
			break
		}
		d.remove(oldest)
	}
}

func (d *decoder) receive(peer string, packet []byte, now time.Time) [][]byte {
	f, ok := d.codec.unpack(packet)
	if !ok {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.prune(now, len(f.payload))
	key := blockKey{peer: peer, session: f.session, block: f.block}
	b := d.blocks[key]
	if b == nil {
		b = &decodeBlock{created: now, data: make([][]byte, d.codec.config.DataShards), parity: make([][]byte, d.codec.config.ParityShards), delivered: make([]bool, d.codec.config.DataShards)}
		d.blocks[key] = b
	}
	if b.complete {
		return nil
	}
	var output [][]byte
	if !f.parity {
		if b.delivered[f.index] {
			return nil
		}
		if b.actual > 0 && (f.index >= b.actual || len(f.payload) > b.length) {
			return nil
		}
		b.data[f.index] = append([]byte(nil), f.payload...)
		b.delivered[f.index] = true
		b.deliveredCount++
		b.bytes += len(f.payload)
		d.bytes += len(f.payload)
		output = append(output, append([]byte(nil), f.payload[LengthSize:]...))
	} else {
		i := f.index - d.codec.config.DataShards
		if b.actual != 0 && (b.actual != f.actual || b.redundancy != f.redundancy || b.length != len(f.payload)) {
			return nil
		}
		if b.parity[i] != nil {
			return nil
		}
		for j, data := range b.data {
			if data != nil && (j >= f.actual || len(data) > len(f.payload)) {
				return nil
			}
		}
		b.actual, b.redundancy, b.length = f.actual, f.redundancy, len(f.payload)
		b.parity[i] = append([]byte(nil), f.payload...)
		b.bytes += len(f.payload)
		d.bytes += len(f.payload)
	}
	if b.actual == 0 {
		if b.deliveredCount == d.codec.config.DataShards {
			d.complete(b)
		}
		return output
	}
	available, missing := 0, 0
	for i := 0; i < b.actual; i++ {
		if b.data[i] != nil {
			available++
		} else {
			missing++
		}
	}
	for i := 0; i < b.redundancy; i++ {
		if b.parity[i] != nil {
			available++
		}
	}
	if missing == 0 {
		d.complete(b)
		return output
	}
	if available < b.actual || d.bytes+missing*b.length > maxBufferedBytes {
		return output
	}
	shards := make([][]byte, b.actual+b.redundancy)
	for i := 0; i < b.actual; i++ {
		if b.data[i] != nil {
			shards[i] = make([]byte, b.length)
			copy(shards[i], b.data[i])
		}
	}
	for i := 0; i < b.redundancy; i++ {
		shards[b.actual+i] = b.parity[i]
	}
	d.codec.rsMu.Lock()
	err := d.codec.encoders[b.actual].ReconstructData(shards)
	d.codec.rsMu.Unlock()
	if err != nil {
		return output
	}
	for i := 0; i < b.actual; i++ {
		if b.delivered[i] {
			continue
		}
		length := int(binary.BigEndian.Uint16(shards[i][:2]))
		if length == 0 || length > len(shards[i])-2 {
			continue
		}
		b.delivered[i] = true
		b.deliveredCount++
		b.data[i] = shards[i]
		b.bytes += len(shards[i])
		d.bytes += len(shards[i])
		output = append(output, append([]byte(nil), shards[i][2:2+length]...))
	}
	if b.deliveredCount == b.actual {
		d.complete(b)
	}
	return output
}

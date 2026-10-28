package fec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{Key: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x71}, 32)), DataShards: 10, ParityShards: 2, Interval: 10 * time.Millisecond, MTU: 1400}
}

func testCodec(t *testing.T) *codec {
	t.Helper()
	c, err := newCodec(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestV1WireVector(t *testing.T) {
	c := testCodec(t)
	packet := c.pack(frame{session: 0x0102030405060708, block: 9, payload: []byte{0, 1, 'A'}})
	const expected = "f100000001000a020000000001020304050607080000000000000009000353460001417beace71a39c11c4baa6faf28c5b2c78"
	if hex.EncodeToString(packet) != expected {
		t.Fatal("v1 wire format changed")
	}
	if f, ok := c.unpack(packet); !ok || f.session != 0x0102030405060708 || f.block != 9 || !bytes.Equal(f.payload, []byte{0, 1, 'A'}) {
		t.Fatal("v1 wire vector did not decode")
	}
}

func TestConfigurationValidation(t *testing.T) {
	valid := testConfig()
	cases := []Config{{}, {Key: "invalid"}, {Key: base64.StdEncoding.EncodeToString(make([]byte, 32))}}
	for _, modify := range []func(*Config){func(c *Config) { c.DataShards = -1 }, func(c *Config) { c.DataShards = 33 }, func(c *Config) { c.ParityShards = 17 }, func(c *Config) { c.Interval = time.Nanosecond }, func(c *Config) { c.MTU = 65535 }} {
		c := valid
		modify(&c)
		cases = append(cases, c)
	}
	for i, c := range cases {
		if _, err := c.Normalize(); err == nil {
			t.Fatalf("invalid case %d accepted", i)
		}
	}
	normal, err := (Config{Key: valid.Key}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if normal.DataShards != 10 || normal.ParityShards != 2 || normal.Interval != 20*time.Millisecond || normal.MTU != 1400 {
		t.Fatal(normal)
	}
	if err := normal.CheckWireGuardMTU(1280); err != nil {
		t.Fatal(err)
	}
	for _, mtu := range []uint32{1408, math.MaxUint32} {
		if normal.CheckWireGuardMTU(mtu) == nil {
			t.Fatalf("oversized mtu %d accepted", mtu)
		}
	}
}

func TestRecoveryAndImmediateDelivery(t *testing.T) {
	for n := 1; n <= 10; n++ {
		t.Run(string(rune('A'+n)), func(t *testing.T) {
			c := testCodec(t)
			e, err := newEncoder(c)
			if err != nil {
				t.Fatal(err)
			}
			d := newDecoder(c)
			var packets, original [][]byte
			for i := 0; i < n; i++ {
				original = append(original, bytes.Repeat([]byte{byte(i + 1)}, 20+i*83))
				p, err := e.add(original[i])
				if err != nil {
					t.Fatal(err)
				}
				packets = append(packets, p)
			}
			parity, err := e.flush()
			if err != nil {
				t.Fatal(err)
			}
			if len(parity) != (n*2+9)/10 {
				t.Fatal("non-adaptive redundancy", n, len(parity))
			}
			var received [][]byte
			// Parity may arrive first; only deliver originals once even after repair.
			for _, p := range parity {
				received = append(received, d.receive("peer", p, time.Now())...)
			}
			for i, p := range packets {
				if i == 0 || (n >= 6 && i == 2) {
					continue
				}
				received = append(received, d.receive("peer", p, time.Now())...)
			}
			if len(received) != n {
				t.Fatalf("recovered %d of %d", len(received), n)
			}
			seen := make(map[string]int)
			for _, p := range received {
				seen[string(p)]++
			}
			for _, p := range original {
				if seen[string(p)] != 1 {
					t.Fatalf("payload mismatch or duplicate: %d", seen[string(p)])
				}
			}
			for _, p := range append(packets, parity...) {
				if len(d.receive("peer", p, time.Now())) != 0 {
					t.Fatal("duplicate delivered")
				}
			}
			if d.bytes != 0 {
				t.Fatal("completed group retained payload memory", d.bytes)
			}
		})
	}
	c := testCodec(t)
	e, _ := newEncoder(c)
	p, _ := e.add([]byte("do not wait for parity"))
	d := newDecoder(c)
	got := d.receive("peer", p, time.Now())
	if len(got) != 1 || string(got[0]) != "do not wait for parity" {
		t.Fatal("data waited for parity")
	}
}

func TestAuthenticationReservedBytesAndPeerIsolation(t *testing.T) {
	c := testCodec(t)
	e, _ := newEncoder(c)
	p, _ := e.add([]byte("secret ciphertext"))
	parity, _ := e.flush()
	for _, offset := range []int{0, 4, 5, 6, 7, 10, 12, 20, 28, 30, HeaderSize, len(p) - 1} {
		d := newDecoder(c)
		bad := bytes.Clone(p)
		bad[offset] ^= 0x80
		if len(d.receive("peer", bad, time.Now())) != 0 || len(d.blocks) != 0 {
			t.Fatal("unauthenticated packet allocated state", offset)
		}
	}
	d := newDecoder(c)
	modified := bytes.Clone(p)
	copy(modified[1:4], []byte{1, 2, 3})
	if len(d.receive("peer", modified, time.Now())) != 1 {
		t.Fatal("transport-reserved bytes broke authentication")
	}
	if len(d.receive("another-peer", parity[0], time.Now())) != 1 {
		t.Fatal("independent peer could not recover")
	}
	for _, bad := range [][]byte{nil, make([]byte, 33), append(bytes.Clone(p), 0)} {
		if _, ok := c.unpack(bad); ok {
			t.Fatal("malformed frame accepted")
		}
	}
	other := testConfig()
	other.Key = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x72}, 32))
	wrong, err := newCodec(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := wrong.unpack(p); ok {
		t.Fatal("wrong key authenticated")
	}
	if _, err := e.add(make([]byte, c.config.MaxPayload()+1)); err == nil {
		t.Fatal("oversized packet accepted")
	}
}

func TestDecoderBoundsAndExpiry(t *testing.T) {
	c := testCodec(t)
	d := newDecoder(c)
	now := time.Now()
	for group := 0; group < maxBlocks+40; group++ {
		e, _ := newEncoder(c)
		p, _ := e.add(bytes.Repeat([]byte{1}, c.config.MaxPayload()))
		d.receive("peer", p, now)
		if len(d.blocks) > maxBlocks || d.bytes > maxBufferedBytes {
			t.Fatal("decoder exceeded limits")
		}
	}
	e, _ := newEncoder(c)
	p, _ := e.add([]byte("after idle"))
	d.receive("peer", p, now.Add(blockLifetime+time.Second))
	if len(d.blocks) != 1 {
		t.Fatal("expired groups retained", len(d.blocks))
	}
}

func FuzzAuthenticatedFrameParser(f *testing.F) {
	c, _ := newCodec(testConfig())
	e, _ := newEncoder(c)
	p, _ := e.add([]byte("seed ciphertext"))
	parity, _ := e.flush()
	f.Add(p)
	f.Add(parity[0])
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, packet []byte) {
		d := newDecoder(c)
		_ = d.receive("peer", packet, time.Now())
		// Also exercise structural validation behind the authentication boundary.
		if len(packet) >= HeaderSize+LengthSize+TagSize && len(packet) <= c.config.MTU {
			packet = bytes.Clone(packet)
			mac := hmac.New(sha256.New, c.key)
			mac.Write(packet[:1])
			mac.Write(packet[4 : len(packet)-TagSize])
			copy(packet[len(packet)-TagSize:], mac.Sum(nil)[:TagSize])
			_ = d.receive("peer", packet, time.Now())
		}
		if d.bytes > maxBufferedBytes || len(d.blocks) > maxBlocks {
			t.Fatal("unbounded decoder")
		}
	})
}

func TestDecoderPayloadBudget(t *testing.T) {
	config := testConfig()
	config.DataShards, config.ParityShards = 32, 16
	c, err := newCodec(config)
	if err != nil {
		t.Fatal(err)
	}
	d := newDecoder(c)
	now := time.Now()
	for range 240 {
		e, err := newEncoder(c)
		if err != nil {
			t.Fatal(err)
		}
		// Leave each group incomplete so payloads must be retained or evicted.
		for range 31 {
			p, err := e.add(bytes.Repeat([]byte{1}, c.config.MaxPayload()))
			if err != nil {
				t.Fatal(err)
			}
			d.receive("peer", p, now)
			if d.bytes > maxBufferedBytes {
				t.Fatal("payload budget exceeded", d.bytes)
			}
		}
	}
	if len(d.blocks) >= 240 || d.bytes < maxBufferedBytes-(32*1500) {
		t.Fatal("payload-budget eviction was not exercised", d.bytes, len(d.blocks))
	}
}

package fec

import (
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

const (
	HeaderSize       = 32
	TagSize          = 16
	LengthSize       = 2
	maxBufferedBytes = 8 << 20
	maxBlocks        = 512
	maxSendPeers     = 64
	blockLifetime    = 3 * time.Second
)

type Config struct {
	Key          string
	DataShards   int
	ParityShards int
	Interval     time.Duration
	MTU          int
}

func (c Config) Normalize() (Config, error) {
	if c.DataShards == 0 {
		c.DataShards = 10
	}
	if c.ParityShards == 0 {
		c.ParityShards = 2
	}
	if c.Interval == 0 {
		c.Interval = 20 * time.Millisecond
	}
	if c.MTU == 0 {
		c.MTU = 1400
	}
	if c.DataShards < 1 || c.DataShards > 32 || c.ParityShards < 1 || c.ParityShards > 16 || c.DataShards+c.ParityShards > 48 {
		return c, errors.New("fec: data_shards must be 1..32 and parity_shards 1..16")
	}
	if c.Interval < time.Millisecond || c.Interval > time.Second {
		return c, errors.New("fec: interval must be 1ms..1s")
	}
	if c.MTU < 576 || c.MTU > 1500 {
		return c, errors.New("fec: mtu must be 576..1500 UDP payload bytes")
	}
	key, err := base64.StdEncoding.DecodeString(c.Key)
	if err != nil || len(key) != 32 {
		return c, errors.New("fec: key must be a base64-encoded 32-byte secret")
	}
	var nonzero byte
	for _, b := range key {
		nonzero |= b
	}
	if nonzero == 0 {
		return c, errors.New("fec: all-zero key is not allowed")
	}
	return c, nil
}

func (c Config) MaxPayload() int { return c.MTU - HeaderSize - TagSize - LengthSize }

func (c Config) CheckWireGuardMTU(mtu uint32) error {
	if (uint64(mtu)+15)/16*16+32 > uint64(c.MaxPayload()) {
		return fmt.Errorf("fec: wireguard mtu %d exceeds fec mtu %d; use wireguard mtu 1280 with fec mtu 1400", mtu, c.MTU)
	}
	return nil
}

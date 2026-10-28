# FECBox WireGuard FEC v1

This experimental format belongs to this fork. Both peers must use `wireguard-fec`. It is incompatible with raw WireGuard and UDPspeeder, without silent downgrade. WireGuard retains encryption, peer identity and replay protection. FEC operates on complete encrypted datagrams, including handshakes.

All integers are big endian. Each UDP payload consists of a 32-byte header, one shard and a 16-byte authentication tag.

| Offset | Length | Value |
| --- | --- | --- |
| 0 | 1 | `0xf1` |
| 1 | 3 | Underlying bind reserved bytes, may be rewritten |
| 4 | 1 | Version `1` |
| 5 | 1 | `0` data, `1` parity |
| 6 | 1 | Configured maximum data count K |
| 7 | 1 | Configured maximum parity count R |
| 8 | 1 | Actual N on parity frames, zero on data |
| 9 | 1 | Actual M on parity frames, zero on data |
| 10 | 2 | Data index `0..K-1`, parity index `K..K+M-1` |
| 12 | 8 | Random sender session per remote address |
| 20 | 8 | Incrementing block number |
| 28 | 2 | Shard length |
| 30 | 2 | ASCII `SF` |

The tag is truncated HMAC-SHA256 over byte 0 followed by bytes 4 through the end of the shard, using a separate 32-byte FEC secret. Reserved bytes 1–3 are excluded because existing WireGuard binds rewrite/clear them. Authentication and structural validation precede any decoder block allocation.

Data vectors contain a two-byte original length plus the ciphertext and are sent immediately. On K packets or timer expiry, choose `N = accumulated count`, `M = ceil(N*R/K)`, pad data vectors to the longest length, and encode with klauspost/reedsolomon v1.13.3 using its default systematic Vandermonde matrix. Only parity frames carry N/M. Their wire indices start at K; their matrix indices start at N.

Receivers forward original data immediately and reconstruct after N distinct shards arrive. Length prefixes trim reconstructed ciphertext. There is no in-order wait. The retained block state suppresses duplicates; WireGuard independently rejects replayed ciphertext.

The MTU check reserves `ceil(inner_mtu/16)*16 + 32` bytes for WireGuard, plus 50 bytes for FEC. Default inner MTU 1280 produces at most 1362 bytes of FEC UDP payload.

Resource limits:

- At most 512 decoder blocks, 8 MiB retained shard payload, three-second lifetime. Fixed metadata and one bounded reconstruction scratch group are additional. Authenticated input triggers expiry/eviction; completed blocks retain only deduplication state.
- At most 64 sender addresses per bind, with inactive entry eviction. Intended deployment is a dedicated trusted link, not an unlimited public relay.
- K in 1..32, R in 1..16, K+R at most 48. Payload MTU in 576..1500, timer in 1ms..1s.
- Close stops timers, invalidates receiver generations, closes the socket before waiting for writes. Reopen uses new state/sessions. Platform socket protection interfaces remain available.

The format has not had an external security audit. An authenticated peer can consume its allowed buffers and CPU. A 64-bit session is not an encryption nonce or a replacement for WireGuard anti-replay. FEC provides neither obfuscation nor congestion control. A full 10+2 block adds 20% shard-count redundancy plus framing/padding; single-packet blocks approach 100% redundancy.

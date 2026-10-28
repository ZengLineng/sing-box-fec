//go:build with_wireguard && with_gvisor

package fecintegration

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/socks"
)

func keyPair(t *testing.T) (string, string) {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k.Bytes()), base64.StdEncoding.EncodeToString(k.PublicKey().Bytes())
}

func freePort(t *testing.T, network string) int {
	t.Helper()
	if network == "udp" {
		c, err := net.ListenPacket(network, "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).Port
	}
	c, err := net.Listen(network, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.Addr().(*net.TCPAddr).Port
}

func startBox(t *testing.T, config string) {
	t.Helper()
	ctx, cancel := context.WithCancel(include.Context(context.Background()))
	options, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	b, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(); cancel() })
	if err = b.Start(); err != nil {
		t.Fatal(err)
	}
}

// Drop the first and eighth data shard of every group, including handshakes.
func lossRelay(t *testing.T, serverPort int) (int, *atomic.Int64) {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetReadBuffer(1 << 20); err != nil {
		c.Close()
		t.Fatal(err)
	}
	server := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: serverPort}
	var dropped atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		var client *net.UDPAddr
		p := make([]byte, 2048)
		for {
			n, from, err := c.ReadFromUDP(p)
			if err != nil {
				return
			}
			destination := server
			if from.Port == serverPort {
				destination = client
			} else {
				client = from
			}
			if n > 32 && p[0] == 0xf1 && p[5] == 0 && (p[11] == 0 || p[11] == 7) {
				dropped.Add(1)
				continue
			}
			if destination != nil {
				_, _ = c.WriteToUDP(p[:n], destination)
			}
		}
	}()
	t.Cleanup(func() { c.Close(); <-done })
	return c.LocalAddr().(*net.UDPAddr).Port, &dropped
}

func TestExampleConfigurations(t *testing.T) {
	serverKey, serverPublic := keyPair(t)
	clientKey, clientPublic := keyPair(t)
	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x73}, 32))
	replace := strings.NewReplacer("SERVER_PRIVATE_KEY", serverKey, "SERVER_PUBLIC_KEY", serverPublic, "CLIENT_PRIVATE_KEY", clientKey, "CLIENT_PUBLIC_KEY", clientPublic, "SHARED_FEC_KEY", secret, "SOCKS_PASSWORD", "test-only-password")
	for _, file := range []string{"client", "server-native"} {
		t.Run(file, func(t *testing.T) {
			content, err := os.ReadFile("../../examples/wireguard-fec/" + file + ".json")
			if err != nil {
				t.Fatal(err)
			}
			ctx := include.Context(context.Background())
			options, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(replace.Replace(string(content))))
			if err != nil {
				t.Fatal(err)
			}
			instance, err := box.New(box.Options{Context: ctx, Options: options})
			if err != nil {
				t.Fatal(err)
			}
			instance.Close()
		})
	}
}

func TestWireGuardFECProxy(t *testing.T) {
	for _, kind := range []string{"wireguard", "wireguard-fec"} {
		t.Run(kind, func(t *testing.T) {
			serverKey, serverPublic := keyPair(t)
			clientKey, clientPublic := keyPair(t)
			secret := make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				t.Fatal(err)
			}
			fec := ""
			if kind == "wireguard-fec" {
				fec = fmt.Sprintf(`,"fec":{"key":%q,"interval":"5ms"}`, base64.StdEncoding.EncodeToString(secret))
			}
			serverPort, socksPort := freePort(t, "udp"), freePort(t, "tcp")
			relayPort, dropped := lossRelay(t, serverPort)
			startBox(t, fmt.Sprintf(`{
			  "log":{"level":"error"},
			  "endpoints":[{"type":%q,"tag":"wg","address":["10.99.0.1/24"],"private_key":%q,"listen_port":%d,
			    "peers":[{"public_key":%q,"allowed_ips":["10.99.0.2/32"]}]%s}],
			  "outbounds":[{"type":"direct","tag":"direct"}],"route":{"rules":[{"ip_cidr":["198.18.0.1/32"],"action":"route","outbound":"direct","override_address":"127.0.0.1"}],"final":"direct"}
			}`, kind, serverKey, serverPort, clientPublic, fec))
			startBox(t, fmt.Sprintf(`{
			  "log":{"level":"error"},
			  "inbounds":[{"type":"socks","tag":"local","listen":"127.0.0.1","listen_port":%d}],
			  "endpoints":[{"type":%q,"tag":"wg","address":["10.99.0.2/24"],"private_key":%q,
			    "peers":[{"address":"127.0.0.1","port":%d,"public_key":%q,"allowed_ips":["0.0.0.0/0"]}]%s}],
			  "route":{"final":"wg"}
			}`, socksPort, kind, clientKey, relayPort, serverPublic, fec))
			dialer := socks.NewClient(N.SystemDialer, M.ParseSocksaddrHostPort("127.0.0.1", uint16(socksPort)), socks.Version5, "", "")
			payload := bytes.Repeat([]byte("FEC over authenticated WireGuard.\n"), 32768)
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
			defer h.Close()
			transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, M.ParseSocksaddr(address))
			}}
			defer transport.CloseIdleConnections()
			httpClient := &http.Client{Transport: transport, Timeout: 15 * time.Second}
			response, err := httpClient.Get(strings.Replace(h.URL, "127.0.0.1", "198.18.0.1", 1))
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, payload) {
				t.Fatal("TCP transfer did not preserve payload", err, len(body))
			}
			echo, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer echo.Close()
			go func() {
				p := make([]byte, 2048)
				for {
					n, addr, err := echo.ReadFrom(p)
					if err != nil {
						return
					}
					_, _ = echo.WriteTo(p[:n], addr)
				}
			}()
			destination := M.ParseSocksaddr(strings.Replace(echo.LocalAddr().String(), "127.0.0.1", "198.18.0.1", 1))
			udp, err := dialer.ListenPacket(context.Background(), destination)
			if err != nil {
				t.Fatal(err)
			}
			defer udp.Close()
			_ = udp.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err = udp.WriteTo([]byte("UDP through FEC"), destination); err != nil {
				t.Fatal(err)
			}
			p := make([]byte, 2048)
			n, _, err := udp.ReadFrom(p)
			if err != nil || string(p[:n]) != "UDP through FEC" {
				t.Fatal("UDP echo failed", err)
			}
			if kind == "wireguard-fec" && dropped.Load() == 0 {
				t.Fatal("loss relay did not exercise FEC")
			}
			t.Logf("verified %d TCP bytes, UDP echo; dropped %d datagrams", len(body), dropped.Load())
		})
	}
}

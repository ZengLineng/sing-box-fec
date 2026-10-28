//go:build darwin

package main

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"

	"github.com/miekg/dns"
)

func TestTUNDNSAddresses(t *testing.T) {
	ctx := box.Context(context.Background(), include.InboundRegistry(), include.OutboundRegistry(), include.EndpointRegistry(), include.DNSTransportRegistry(), include.ServiceRegistry(), include.CertificateProviderRegistry())
	for _, test := range []struct {
		name    string
		inbound string
		want    []string
		invalid bool
	}{
		{name: "proxy-only", inbound: `{"type":"mixed"}`},
		{name: "no-auto-route", inbound: `{"type":"tun","address":["172.19.0.1/30"]}`},
		{name: "disabled", inbound: `{"type":"tun","auto_route":true,"dns_mode":"disabled","address":["172.19.0.1/30"]}`},
		{name: "automatic", inbound: `{"type":"tun","auto_route":true,"address":["172.19.0.1/30","fdfe:dcba:9876::1/126"]}`, want: []string{"172.19.0.2", "fdfe:dcba:9876::2"}},
		{name: "explicit", inbound: `{"type":"tun","dns_mode":"native","address":["172.19.0.1/30"],"dns_address":["1.1.1.1"]}`, want: []string{"1.1.1.1"}},
		{name: "ipv6-only", inbound: `{"type":"tun","auto_route":true,"address":["fdfe:dcba:9876::1/126"]}`, want: []string{"fdfe:dcba:9876::2"}},
		{name: "unusable-prefix", inbound: `{"type":"tun","auto_route":true,"address":["172.19.0.1/32"]}`, invalid: true},
		{name: "invalid-mode", inbound: `{"type":"invalid"}`, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			addresses, err := tunDNSAddresses(ctx, `{"inbounds":[`+test.inbound+`]}`)
			if (err != nil) != test.invalid {
				t.Fatalf("unexpected error: %v", err)
			}
			var got []string
			for _, address := range addresses {
				got = append(got, address.String())
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("DNS servers = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSystemDNS(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("system DNS requires administrator privileges; tested on the isolated macOS CI runner")
	}
	before := readSystemDNS(t)
	connection, err := net.ListenPacket("udp", "127.0.0.1:53")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{PacketConn: connection, Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, request *dns.Msg) {
		reply := new(dns.Msg)
		reply.SetReply(request)
		for _, question := range request.Question {
			if question.Qtype == dns.TypeA {
				reply.Answer = append(reply.Answer, &dns.A{Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}, A: net.IPv4(192, 0, 2, 77)})
			}
		}
		_ = writer.WriteMsg(reply)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })
	for _, crash := range []bool{false, true} {
		t.Run(fmt.Sprintf("crash=%t", crash), func(t *testing.T) {
			session, err := startDNS(context.Background(), []netip.Addr{netip.MustParseAddr("127.0.0.1")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			deadline := time.Now().Add(5 * time.Second)
			for !strings.Contains(readSystemDNS(t), "nameserver[0] : 127.0.0.1") {
				if time.Now().After(deadline) {
					t.Fatal("temporary DNS server was not published")
				}
				time.Sleep(20 * time.Millisecond)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			name := fmt.Sprintf("probe-%d.fecbox.test", time.Now().UnixNano())
			output, err := exec.CommandContext(ctx, "/usr/bin/dscacheutil", "-q", "host", "-a", "name", name).CombinedOutput()
			if err != nil || !strings.Contains(string(output), "192.0.2.77") {
				t.Fatalf("macOS lookup did not reach the managed resolver: %v: %s", err, output)
			}
			if crash {
				if err = session.command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			}
			if err = session.Close(); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(5 * time.Second)
			for readSystemDNS(t) != before {
				if time.Now().After(deadline) {
					t.Fatal("system DNS was not restored after session exit")
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

func readSystemDNS(t *testing.T) string {
	t.Helper()
	output, err := exec.Command("/usr/sbin/scutil", "--dns").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

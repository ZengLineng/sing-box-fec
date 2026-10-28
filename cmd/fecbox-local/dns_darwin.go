//go:build darwin

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/json"
)

func tunDNSAddresses(ctx context.Context, config string) ([]netip.Addr, error) {
	options, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	if err != nil {
		return nil, err
	}
	var addresses []netip.Addr
	for _, inbound := range options.Inbounds {
		settings, ok := inbound.Options.(*option.TunInboundOptions)
		if !ok {
			continue
		}
		dns := tun.Options{AutoRoute: settings.AutoRoute, DNSMode: settings.DNSMode, DNSAddress: settings.DNSAddress}
		if dns.DNSModeOrDefault() == tun.DNSModeDisabled {
			continue
		}
		for _, prefix := range settings.Address {
			if prefix.Addr().Is4() {
				dns.Inet4Address = append(dns.Inet4Address, prefix)
			} else {
				dns.Inet6Address = append(dns.Inet6Address, prefix)
			}
		}
		usable := len(dns.DNSAddress) > 0
		for _, prefixes := range [][]netip.Prefix{dns.Inet4Address, dns.Inet6Address} {
			usable = usable || (len(prefixes) > 0 && tun.HasNextAddress(prefixes[0], 1))
		}
		if !usable {
			return nil, fmt.Errorf("TUN %q needs a usable DNS address", inbound.Tag)
		}
		servers, err := dns.DNSServerAddress()
		if err != nil || len(servers) == 0 {
			return nil, fmt.Errorf("TUN %q has no usable DNS servers: %v", inbound.Tag, err)
		}
		for _, address := range servers {
			if !slices.Contains(addresses, address) {
				addresses = append(addresses, address)
			}
		}
	}
	return addresses, nil
}

type dnsSession struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  bytes.Buffer
	key     string
	done    chan struct{}
}

func startDNS(ctx context.Context, addresses []netip.Addr) (*dnsSession, error) {
	if len(addresses) == 0 {
		return nil, nil
	}
	session := &dnsSession{
		command: exec.CommandContext(ctx, "/usr/sbin/scutil"),
		key:     "State:/Network/Service/FECBox-" + rand.Text() + "/DNS",
		done:    make(chan struct{}),
	}
	input, err := session.command.StdinPipe()
	if err != nil {
		return nil, err
	}
	session.input = input
	session.command.Stdout = &session.output
	session.command.Stderr = &session.output
	session.command.WaitDelay = 2 * time.Second
	if err = session.command.Start(); err != nil {
		input.Close()
		return nil, err
	}
	go func() {
		_ = session.command.Wait()
		close(session.done)
	}()
	var script strings.Builder
	script.WriteString("d.init\nd.add ServerAddresses *")
	for _, address := range addresses {
		fmt.Fprintf(&script, " %s", address)
	}
	// A session-owned key disappears even if the backend is killed; never rewrite Wi-Fi preferences.
	fmt.Fprintf(&script, "\nd.add SupplementalMatchDomains * \"\"\nd.add SupplementalMatchOrders * # 1\nadd %s temporary\n", session.key)
	_, err = io.WriteString(input, script.String())
	if err == nil {
		ready, cancel := context.WithTimeout(ctx, 4*time.Second)
		err = exec.CommandContext(ready, "/usr/sbin/scutil", "-w", session.key, "-t", "3").Run()
		cancel()
	}
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("configure temporary macOS DNS: %w: %s", err, strings.TrimSpace(session.output.String()))
	}
	return session, nil
}

func (s *dnsSession) Close() error {
	if s == nil {
		return nil
	}
	_ = s.input.Close()
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		if err := s.command.Process.Kill(); err != nil && !os.IsNotExist(err) && err != os.ErrProcessDone {
			return err
		}
		<-s.done
	}
	return nil
}

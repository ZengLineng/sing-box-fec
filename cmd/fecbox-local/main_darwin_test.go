//go:build darwin

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestPrivateConfiguration(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "local-config.json")
	for _, content := range []string{"", strings.Repeat("x", maxConfigSize+1)} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readConfig(base, os.Getuid()); err == nil {
			t.Fatal("accepted empty or oversized configuration")
		}
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(base, os.Getuid()+1); err == nil {
		t.Fatal("accepted another user's file")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(base, os.Getuid()); err == nil {
		t.Fatal("accepted public configuration")
	}
	if err := os.Rename(path, path+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+".real", path); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(base, os.Getuid()); err == nil {
		t.Fatal("followed a configuration symlink")
	}
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := privateDirectory(base, os.Getuid()); err == nil {
		t.Fatal("accepted a public application directory")
	}
}

func TestLocalLifecycle(t *testing.T) {
	testLifecycle(t, `{"outbounds":[{"type":"direct","tag":"direct"}]}`, "")
}

func TestNativeTUN(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("native utun requires administrator privileges; exercised on the macOS CI runner")
	}
	testLifecycle(t, `{"inbounds":[{"type":"tun","tag":"local-tun","address":["172.31.254.1/30"],"auto_route":false,"dns_mode":"native","stack":"system"}],"outbounds":[{"type":"direct","tag":"direct"}]}`, "172.31.254.2")
}

func TestWithdrawnConfigurationStopsBackend(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "fecbox-local-lease-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	config := filepath.Join(base, "local-config.json")
	if err = os.WriteFile(config, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- run(base, os.Getpid(), os.Getuid(), os.Getgid()) }()
	socket := filepath.Join(base, "command.sock")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backend did not open its socket")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = os.Remove(config); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("backend retained its lease after configuration withdrawal")
	}
	if _, err = os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatal("backend left its command socket behind")
	}
}

func testLifecycle(t *testing.T, config, wantDNS string) {
	t.Helper()
	beforeDNS := readSystemDNS(t)
	// Darwin's Unix socket path is shorter than the usual Go test directory.
	base, err := os.MkdirTemp("/tmp", "fecbox-local-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	path := filepath.Join(base, "local-config.json")
	if err = os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- run(base, os.Getpid(), os.Getuid(), os.Getgid()) }()
	socket := filepath.Join(base, "command.sock")
	connection, err := grpc.NewClient("passthrough:///local-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := daemon.NewStartedServiceClient(connection)
	started, err := client.GetStartedAt(ctx, &emptypb.Empty{}, grpc.WaitForReady(true))
	if err != nil {
		select {
		case runErr := <-done:
			t.Fatalf("startup: %v; probe: %v", runErr, err)
		default:
		}
		t.Fatal(err)
	}
	info, err := os.Stat(socket)
	if err != nil || info.Mode().Perm() != 0o600 || started.StartedAt <= 0 {
		t.Fatalf("invalid socket permissions or startup: %v %v", info, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for wantDNS != "" && !strings.Contains(readSystemDNS(t), "nameserver[0] : "+wantDNS) {
		if time.Now().After(deadline) {
			t.Fatal("backend did not publish its TUN DNS server")
		}
		time.Sleep(20 * time.Millisecond)
	}
	runningDNS := readSystemDNS(t)
	managed := daemon.NewManagedServiceClient(connection)
	if err = os.WriteFile(path, []byte(`{"invalid_option":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = managed.ReloadService(ctx, &emptypb.Empty{}); err == nil {
		t.Fatal("accepted invalid reload")
	}
	stillStarted, err := client.GetStartedAt(ctx, &emptypb.Empty{})
	if err != nil || stillStarted.StartedAt != started.StartedAt {
		t.Fatal("invalid reload interrupted the running engine")
	}
	if readSystemDNS(t) != runningDNS {
		t.Fatal("invalid reload changed the system DNS")
	}
	if err = os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = managed.ReloadService(ctx, &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	if _, err = managed.StopService(ctx, &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("backend did not stop")
	}
	if _, err = os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatal("backend left its socket behind")
	}
	deadline = time.Now().Add(5 * time.Second)
	for readSystemDNS(t) != beforeDNS {
		if time.Now().After(deadline) {
			t.Fatal("backend did not restore the system DNS")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

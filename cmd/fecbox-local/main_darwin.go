//go:build darwin

// fecbox-local serves the existing Apple GUI protocol without a Network Extension.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	box "github.com/sagernet/sing-box"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing/service/filemanager"

	"google.golang.org/grpc"
)

const maxConfigSize = 2 << 20

type localService struct {
	service *daemon.StartedService
	base    string
	uid     int
	done    chan struct{}
	once    sync.Once
}

func main() {
	base := flag.String("base", "", "Private application data directory")
	parent := flag.Int("parent", 0, "GUI process to follow")
	uid := flag.Int("uid", os.Getuid(), "Configuration and socket owner")
	gid := flag.Int("gid", os.Getgid(), "Socket group")
	version := flag.Bool("version", false, "Print version")
	flag.Parse()
	if *version {
		fmt.Println("FECBox local backend", C.Version)
		return
	}
	if err := run(*base, *parent, *uid, *gid); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func privateDirectory(path string, uid int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || int(info.Sys().(*syscall.Stat_t).Uid) != uid {
		return fmt.Errorf("application directory must be private and owned by uid %d", uid)
	}
	return nil
}

func readConfig(base string, uid int) (string, error) {
	fd, err := syscall.Open(filepath.Join(base, "local-config.json"), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), "local-config.json")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || int(info.Sys().(*syscall.Stat_t).Uid) != uid {
		return "", fmt.Errorf("configuration must be a private regular file owned by uid %d", uid)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxConfigSize+1))
	if err != nil {
		return "", err
	}
	if len(data) == 0 || len(data) > maxConfigSize {
		return "", fmt.Errorf("configuration must contain 1 to %d bytes", maxConfigSize)
	}
	return string(data), nil
}

func run(base string, parent, uid, gid int) error {
	if !filepath.IsAbs(base) || parent <= 1 || uid < 0 || gid < 0 {
		return fmt.Errorf("an absolute base directory, GUI parent pid, uid and gid are required")
	}
	if err := privateDirectory(base, uid); err != nil {
		return err
	}
	if err := syscall.Kill(parent, 0); err != nil {
		return fmt.Errorf("GUI parent is not running: %w", err)
	}
	config, err := readConfig(base, uid)
	if err != nil {
		return err
	}
	socketPath := filepath.Join(base, "command.sock")
	if info, statErr := os.Lstat(socketPath); statErr == nil {
		if info.Mode()&os.ModeSocket == 0 || int(info.Sys().(*syscall.Stat_t).Uid) != uid {
			return fmt.Errorf("refusing to replace an unexpected command socket")
		}
		connection, dialErr := net.DialTimeout("unix", socketPath, time.Second)
		if dialErr == nil {
			connection.Close()
			return fmt.Errorf("a local backend is already running")
		}
		if err = os.Remove(socketPath); err != nil {
			return err
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return err
	}
	defer listener.Close()
	if err = os.Chmod(socketPath, 0o600); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err = os.Chown(socketPath, uid, gid); err != nil {
			return err
		}
	}
	working := filepath.Join(base, "Library", "Caches", "Working")
	ctx := filemanager.WithDefault(context.Background(), working, filepath.Dir(working), uid, gid)
	if err = filemanager.MkdirAll(ctx, working, 0o700); err != nil {
		return err
	}
	ctx = box.Context(ctx, include.InboundRegistry(), include.OutboundRegistry(), include.EndpointRegistry(), include.DNSTransportRegistry(), include.ServiceRegistry(), include.CertificateProviderRegistry())
	handler := &localService{base: base, uid: uid, done: make(chan struct{})}
	handler.service = daemon.NewStartedService(daemon.ServiceOptions{Context: ctx, Handler: handler, LogMaxLines: 3000})
	defer handler.service.Close()
	defer handler.service.CloseService()
	if err = handler.service.StartOrReloadService(ctx, config, nil); err != nil {
		return err
	}
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(daemon.UnaryLocaleInterceptor), grpc.ChainStreamInterceptor(daemon.StreamLocaleInterceptor))
	daemon.RegisterStartedServiceServer(server, handler.service)
	daemon.RegisterManagedServiceServer(server, daemon.NewManagedService(daemon.ManagedServiceOptions{Handler: handler}))
	defer server.Stop()
	serveError := make(chan error, 1)
	go func() { serveError <- server.Serve(listener) }()
	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(osSignals)
	watchdog := time.NewTicker(time.Second)
	defer watchdog.Stop()
	for {
		select {
		case <-handler.done:
			return nil
		case <-osSignals:
			return nil
		case err = <-serveError:
			return err
		case <-watchdog.C:
			// shortcut: follow the GUI pid; use a launchd lease if persistent background service is added.
			if syscall.Kill(parent, 0) != nil {
				return nil
			}
			if _, err = os.Lstat(filepath.Join(base, "local-config.json")); err != nil {
				return nil
			}
		}
	}
}

func (s *localService) ServiceStop() error {
	err := s.service.CloseService()
	s.once.Do(func() { time.AfterFunc(200*time.Millisecond, func() { close(s.done) }) })
	return err
}

func (s *localService) ServiceReload(ctx context.Context) error {
	config, err := readConfig(s.base, s.uid)
	if err != nil {
		return err
	}
	if err = s.service.CheckConfig(ctx, config); err != nil {
		return err
	}
	return s.service.StartOrReloadService(ctx, config, nil)
}

func (*localService) SystemProxyStatus() (*daemon.SystemProxyStatus, error) {
	return &daemon.SystemProxyStatus{}, nil
}

func (*localService) SetSystemProxyEnabled(bool) error {
	return fmt.Errorf("set system proxy in the sing-box configuration")
}

func (*localService) TriggerNativeCrash() error        { return fmt.Errorf("unavailable") }
func (*localService) ConnectSSHAgent() (int32, error)  { return -1, fmt.Errorf("unavailable") }
func (*localService) WriteDebugMessage(message string) { fmt.Fprintln(os.Stderr, message) }

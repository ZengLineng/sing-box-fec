package fec

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/wireguard-go/conn"
)

type droppingBind struct {
	conn.Bind
	drop map[int]bool
}

func (b *droppingBind) Send(bufs [][]byte, ep conn.Endpoint, offset int) error {
	var selected [][]byte
	for _, p := range bufs {
		p = p[offset:]
		if len(p) > HeaderSize && p[5] == 0 && b.drop[int(p[11])] {
			continue
		}
		selected = append(selected, p)
	}
	if len(selected) == 0 {
		return nil
	}
	return b.Bind.Send(selected, ep, 0)
}

func TestUDPBindRecoveryAndTimer(t *testing.T) {
	for _, count := range []int{1, 20} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			left, err := NewBind(&droppingBind{conn.NewStdNetBind(nil), map[int]bool{0: true, 7: true}}, testConfig(), nil)
			if err != nil {
				t.Fatal(err)
			}
			right, err := NewBind(conn.NewStdNetBind(nil), testConfig(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = left.Open(0)
			if err != nil {
				t.Fatal(err)
			}
			defer left.Close()
			fns, port, err := right.Open(0)
			if err != nil {
				t.Fatal(err)
			}
			defer right.Close()
			ep, err := left.ParseEndpoint(fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				t.Fatal(err)
			}
			left.SetReservedForEndpoint(netip.MustParseAddrPort(fmt.Sprintf("127.0.0.1:%d", port)), [3]byte{2, 3, 4})
			out := make(chan []byte, count)
			done := make(chan struct{})
			go func() {
				defer close(done)
				packets := make([][]byte, right.BatchSize())
				sizes := make([]int, len(packets))
				eps := make([]conn.Endpoint, len(packets))
				for i := range packets {
					packets[i] = make([]byte, 2048)
				}
				for {
					n, err := fns[0](packets, sizes, eps)
					if err != nil {
						return
					}
					for i := 0; i < n; i++ {
						out <- bytes.Clone(packets[i][:sizes[i]])
					}
				}
			}()
			for i := 0; i < count; i++ {
				p := bytes.Repeat([]byte{byte(i + 1)}, 100+i)
				if err := left.Send([][]byte{append([]byte{9, 9}, p...)}, ep, 2); err != nil {
					t.Fatal(err)
				}
			}
			seen := map[byte]bool{}
			for range count {
				select {
				case p := <-out:
					if len(p) != 100+int(p[0])-1 || seen[p[0]] {
						t.Fatal("bad or duplicate payload")
					}
					seen[p[0]] = true
				case <-time.After(3 * time.Second):
					t.Fatal("FEC recovery timed out")
				}
			}
			right.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("close did not unblock receive")
			}
		})
	}
}

func TestCloseAndReopen(t *testing.T) {
	b, err := NewBind(conn.NewStdNetBind(nil), testConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fns, _, err := b.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	ep, _ := b.ParseEndpoint("127.0.0.1:9")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 50 {
				_ = b.Send([][]byte{[]byte("test")}, ep, 0)
			}
		})
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if _, err := fns[0]([][]byte{make([]byte, 2048)}, make([]int, 1), make([]conn.Endpoint, 1)); !errors.Is(err, net.ErrClosed) {
		t.Fatal("old receiver survived close", err)
	}
	if _, _, err := b.Open(0); err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Send([][]byte{[]byte("new bind")}, ep, 0); err != nil {
		t.Fatal(err)
	}
}

type protectedBind struct{ conn.Bind }

func (b *protectedBind) PeekLookAtSocketFd4() (int, error)         { return 44, nil }
func (b *protectedBind) PeekLookAtSocketFd6() (int, error)         { return 66, nil }
func (b *protectedBind) BindSocketToInterface4(uint32, bool) error { return nil }
func (b *protectedBind) BindSocketToInterface6(uint32, bool) error { return nil }

func TestPlatformSocketInterfacesPreserved(t *testing.T) {
	b, err := NewBind(&protectedBind{conn.NewStdNetBind(nil)}, testConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fd, ok := b.(conn.PeekLookAtSocketFd)
	if !ok {
		t.Fatal("Android socket protection interface lost")
	}
	if got, _ := fd.PeekLookAtSocketFd4(); got != 44 {
		t.Fatal(got)
	}
	if _, ok := b.(conn.BindSocketToInterface); !ok {
		t.Fatal("Windows interface binding lost")
	}
}

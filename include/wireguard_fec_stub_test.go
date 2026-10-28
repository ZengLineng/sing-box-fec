//go:build !with_wireguard

package include

import (
	"context"
	"strings"
	"testing"

	C "github.com/sagernet/sing-box/constant"
)

func TestWireGuardFECBuildTagError(t *testing.T) {
	r := EndpointRegistry()
	options, ok := r.CreateOptions(C.TypeWireGuardFEC)
	if !ok {
		t.Fatal("missing FEC options in stub registry")
	}
	_, err := r.Create(context.Background(), nil, nil, "test", C.TypeWireGuardFEC, options)
	if err == nil || !strings.Contains(err.Error(), "with_wireguard") {
		t.Fatal("missing actionable build-tag error", err)
	}
}

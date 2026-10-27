package api

import (
	"net"
	"testing"
)

func TestParseCIDRs(t *testing.T) {
	nets, err := parseCIDRs([]string{"10.0.0.0/8", "192.168.1.10", "::1"})
	if err != nil {
		t.Fatal(err)
	}
	contains := func(ip string) bool {
		for _, n := range nets {
			if n.Contains(net.ParseIP(ip)) {
				return true
			}
		}
		return false
	}
	for ip, want := range map[string]bool{
		"10.1.2.3":     true,
		"192.168.1.10": true,
		"192.168.1.11": false,
		"::1":          true,
		"8.8.8.8":      false,
	} {
		if got := contains(ip); got != want {
			t.Errorf("%s: got %v want %v", ip, got, want)
		}
	}
	if _, err := parseCIDRs([]string{"not-an-ip"}); err == nil {
		t.Fatal("invalid CIDR must fail")
	}
}

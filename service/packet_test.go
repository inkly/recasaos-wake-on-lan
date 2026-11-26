package service

import (
	"bytes"
	"errors"
	"net"
	"testing"
)

func TestMagicPacket(t *testing.T) {
	mac, err := ParseMAC("01-23-45-67-89-aB")
	if err != nil {
		t.Fatal(err)
	}
	p, err := MagicPacket(mac)
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 102 {
		t.Fatalf("%d bytes, want 102", len(p))
	}
	if !bytes.Equal(p[:6], bytes.Repeat([]byte{0xff}, 6)) {
		t.Fatalf("header %x, want six 0xff", p[:6])
	}
	want := []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab}
	for i := 0; i < 16; i++ {
		if got := p[6+6*i : 12+6*i]; !bytes.Equal(got, want) {
			t.Fatalf("repetition %d is %x, want %x", i, got, want)
		}
	}
}

func TestParseMAC(t *testing.T) {
	for _, ok := range []string{"01:23:45:67:89:ab", "01-23-45-67-89-AB", "01:23-45:67-89:Ab"} {
		mac, err := ParseMAC(ok)
		if err != nil {
			t.Errorf("%q refused: %v", ok, err)
		} else if mac != "01:23:45:67:89:ab" {
			t.Errorf("%q gives %q, want 01:23:45:67:89:ab", ok, mac)
		}
	}
	for _, bad := range []string{
		"", "01:23:45:67:89", "01:23:45:67:89:ab:cd", "0123.4567.89ab", "01:23:45:67:89:ag",
		" 01:23:45:67:89:ab", "01:23:45:67:89:ab\n", "<script>", "01:23:45:67:89:abc",
	} {
		if _, err := ParseMAC(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func ipnet(cidr string) *net.IPNet {
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	n.IP = ip
	return n
}

func TestBroadcasts(t *testing.T) {
	up := net.FlagUp | net.FlagBroadcast
	got := Broadcasts([]Interface{
		{Flags: up, Addrs: []net.Addr{ipnet("192.168.1.20/24"), ipnet("fe80::1/64")}},
		{Flags: up, Addrs: []net.Addr{ipnet("10.0.3.7/16"), ipnet("192.168.1.21/24")}}, // the second /24 is the first's
		{Flags: up | net.FlagLoopback, Addrs: []net.Addr{ipnet("127.0.0.1/8")}},
		{Flags: net.FlagBroadcast, Addrs: []net.Addr{ipnet("172.16.0.2/12")}},         // down
		{Flags: net.FlagUp, Addrs: []net.Addr{ipnet("100.64.0.1/10")}},                // no broadcast (a tunnel)
		{Flags: up, Addrs: []net.Addr{ipnet("192.0.2.9/32"), ipnet("192.0.2.10/31")}}, // no broadcast address
	})
	want := []string{"192.168.1.255", "10.0.255.255"}
	if len(got) != len(want) {
		t.Fatalf("%v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Fatalf("%v, want %v", got, want)
		}
	}
}

func TestWakeSendsToEachBroadcastOnPort9(t *testing.T) {
	var sent []string
	s := Sender{
		Interfaces: func() ([]Interface, error) {
			return []Interface{
				{Flags: net.FlagUp | net.FlagBroadcast, Addrs: []net.Addr{ipnet("192.168.1.20/24")}},
				{Flags: net.FlagUp | net.FlagBroadcast, Addrs: []net.Addr{ipnet("10.1.0.1/16")}},
			}, nil
		},
		Send: func(to *net.UDPAddr, payload []byte) error {
			if len(payload) != 102 {
				t.Errorf("payload of %d bytes", len(payload))
			}
			sent = append(sent, to.String())
			return nil
		},
	}
	if err := s.Wake("01:23:45:67:89:ab"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0] != "192.168.1.255:9" || sent[1] != "10.1.255.255:9" {
		t.Fatalf("sent to %v", sent)
	}
}

func TestWakeFails(t *testing.T) {
	none := Sender{Interfaces: func() ([]Interface, error) { return nil, nil }, Send: func(*net.UDPAddr, []byte) error { return nil }}
	if err := none.Wake("01:23:45:67:89:ab"); err == nil {
		t.Error("no interface: no error")
	}

	failed := 0
	some := Sender{
		Interfaces: func() ([]Interface, error) {
			return []Interface{
				{Flags: net.FlagUp | net.FlagBroadcast, Addrs: []net.Addr{ipnet("192.168.1.20/24")}},
				{Flags: net.FlagUp | net.FlagBroadcast, Addrs: []net.Addr{ipnet("10.1.0.1/16")}},
			}, nil
		},
		Send: func(to *net.UDPAddr, _ []byte) error {
			if to.IP.String() == "192.168.1.255" {
				failed++
				return errors.New("unreachable")
			}
			return nil
		},
	}
	if err := some.Wake("01:23:45:67:89:ab"); err != nil || failed != 1 {
		t.Errorf("one network of two failed: %v (%d failures), want sent", err, failed)
	}

	all := some
	all.Send = func(*net.UDPAddr, []byte) error { return errors.New("unreachable") }
	if err := all.Wake("01:23:45:67:89:ab"); err == nil {
		t.Error("every send failed: no error")
	}

	if err := some.Wake("not a mac"); err == nil {
		t.Error("a bad address was sent")
	}
}

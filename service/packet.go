// Package service holds what the module does: the magic packet and the saved devices.
package service

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// WakePort is the port the magic packet goes to (discard): any port works, since the
// network card reads the packet below the IP layer, and 9 is the usual one.
const WakePort = 9

var macRE = regexp.MustCompile(`^([0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2}$`)

// ParseMAC checks a hardware address of six bytes, written with ':' or '-', and gives it in
// the one form the module keeps: lower case, with ':'.
func ParseMAC(s string) (string, error) {
	if !macRE.MatchString(s) {
		return "", errors.New("a MAC address is six bytes in hexadecimal, such as 01:23:45:67:89:ab")
	}
	return strings.ToLower(strings.ReplaceAll(s, "-", ":")), nil
}

// MagicPacket is six bytes 0xff, then the MAC address sixteen times: 102 bytes.
func MagicPacket(mac string) ([]byte, error) {
	normal, err := ParseMAC(mac)
	if err != nil {
		return nil, err
	}
	hw, err := net.ParseMAC(normal)
	if err != nil {
		return nil, err
	}
	return append(bytes.Repeat([]byte{0xff}, 6), bytes.Repeat(hw, 16)...), nil
}

// Interface is what the sender needs of a network interface of the box.
type Interface struct {
	Flags net.Flags
	Addrs []net.Addr
}

// SystemInterfaces lists the box's network interfaces.
func SystemInterfaces() ([]Interface, error) {
	found, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var list []Interface
	for _, i := range found {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		list = append(list, Interface{Flags: i.Flags, Addrs: addrs})
	}
	return list, nil
}

// Broadcasts gives the broadcast address of each IPv4 network the box is on, through an
// interface that is up, is not the loopback and has broadcast, each once, in order. A /31 or
// a /32 has no broadcast address and is left out.
func Broadcasts(ifaces []Interface) []net.IP {
	var out []net.IP
	seen := map[string]bool{}
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagBroadcast == 0 {
			continue
		}
		for _, a := range i.Addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := n.IP.To4()
			mask := n.Mask
			if len(mask) == net.IPv6len {
				mask = mask[12:]
			}
			if ip == nil || len(mask) != net.IPv4len {
				continue
			}
			if ones, _ := mask.Size(); ones >= 31 {
				continue
			}
			b := make(net.IP, net.IPv4len)
			for k := range b {
				b[k] = ip[k] | ^mask[k]
			}
			if !seen[b.String()] {
				seen[b.String()] = true
				out = append(out, b)
			}
		}
	}
	return out
}

// SendUDP sends one datagram. Go sets SO_BROADCAST on its UDP sockets, so a broadcast
// address is allowed.
func SendUDP(to *net.UDPAddr, payload []byte) error {
	conn, err := net.DialUDP("udp4", nil, to)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write(payload)
	return err
}

// Sender sends magic packets. Its two functions are those of the system in the service, and
// stand-ins in tests.
type Sender struct {
	Interfaces func() ([]Interface, error)
	Send       func(to *net.UDPAddr, payload []byte) error
}

// SystemSender sends on the box's own networks.
var SystemSender = Sender{Interfaces: SystemInterfaces, Send: SendUDP}

// Wake sends the magic packet of mac to the broadcast address of every local network of the
// box, port 9. It is sent when one network at least took it. The packet never goes to an
// address given from outside: only to the box's own broadcasts.
func (s Sender) Wake(mac string) error {
	packet, err := MagicPacket(mac)
	if err != nil {
		return err
	}
	ifaces, err := s.Interfaces()
	if err != nil {
		return fmt.Errorf("the network interfaces cannot be read: %w", err)
	}
	targets := Broadcasts(ifaces)
	if len(targets) == 0 {
		return errors.New("the box has no IPv4 network with broadcast to send on")
	}
	var errs []error
	for _, ip := range targets {
		if err := s.Send(&net.UDPAddr{IP: ip, Port: WakePort}, packet); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ip, err))
		}
	}
	if len(errs) == len(targets) {
		return errors.Join(errs...)
	}
	return nil
}

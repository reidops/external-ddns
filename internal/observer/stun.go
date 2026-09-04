/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package observer

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
)

// RFC 5389 Binding, IPv4 only.
const (
	stunMagic          = 0x2112A442
	stunBindingRequest = 0x0001
	stunBindingSuccess = 0x0101
	attrMappedAddress  = 0x0001
	attrXORMapped      = 0x0020
	stunHeaderLen      = 20
)

// STUN asks each server in turn for the reflexive address.
type STUN struct {
	name    string
	servers []string
	timeout time.Duration
}

func NewSTUN(name string, servers []string, timeout time.Duration) *STUN {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &STUN{name: name, servers: servers, timeout: timeout}
}

func (s *STUN) Name() string { return s.name }
func (s *STUN) Kind() Kind   { return ddnsv1alpha1.OutsideIn }

func (s *STUN) Observe(ctx context.Context) (Observation, error) {
	var errs []error
	for _, server := range s.servers {
		a, err := s.bind(ctx, server)
		if err == nil {
			return observed(a)
		}
		errs = append(errs, fmt.Errorf("%s: %w", server, err))
		if ctx.Err() != nil {
			break
		}
	}
	return Observation{}, errors.Join(errs...)
}

func (s *STUN) bind(ctx context.Context, server string) (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp4", server)
	if err != nil {
		return netip.Addr{}, err
	}
	defer func() { _ = conn.Close() }()
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	}

	req := make([]byte, stunHeaderLen)
	binary.BigEndian.PutUint16(req[0:2], stunBindingRequest)
	binary.BigEndian.PutUint32(req[4:8], stunMagic)
	if _, err := rand.Read(req[8:20]); err != nil {
		return netip.Addr{}, err
	}
	if _, err := conn.Write(req); err != nil {
		return netip.Addr{}, err
	}

	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return netip.Addr{}, err
	}
	return parseBindingResponse(buf[:n], req[8:20])
}

func parseBindingResponse(msg, txID []byte) (netip.Addr, error) {
	if len(msg) < stunHeaderLen {
		return netip.Addr{}, errors.New("short STUN message")
	}
	if binary.BigEndian.Uint16(msg[0:2]) != stunBindingSuccess {
		return netip.Addr{}, fmt.Errorf("STUN message type %#04x", binary.BigEndian.Uint16(msg[0:2]))
	}
	if binary.BigEndian.Uint32(msg[4:8]) != stunMagic || string(msg[8:20]) != string(txID) {
		return netip.Addr{}, errors.New("STUN response does not match request")
	}
	body := msg[stunHeaderLen:]
	if l := int(binary.BigEndian.Uint16(msg[2:4])); l <= len(body) {
		body = body[:l]
	}
	var mapped netip.Addr
	for len(body) >= 4 {
		typ := binary.BigEndian.Uint16(body[0:2])
		l := int(binary.BigEndian.Uint16(body[2:4]))
		if 4+l > len(body) {
			break
		}
		val := body[4 : 4+l]
		body = body[4+l+(-l&3):]
		if l < 8 || val[1] != 0x01 {
			continue
		}
		var ip [4]byte
		copy(ip[:], val[4:8])
		switch typ {
		case attrXORMapped:
			magic := [4]byte{0x21, 0x12, 0xA4, 0x42}
			for i := range ip {
				ip[i] ^= magic[i]
			}
			return netip.AddrFrom4(ip), nil
		case attrMappedAddress:
			mapped = netip.AddrFrom4(ip)
		}
	}
	if mapped.IsValid() {
		return mapped, nil
	}
	return netip.Addr{}, errors.New("STUN response carries no IPv4 mapped address")
}

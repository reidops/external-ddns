/*
Copyright 2026 Christopher Reid.

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
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

const public1 = "203.0.113.7"

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		public1:          true,
		"10.0.0.1":       false,
		"192.168.1.1":    false,
		"100.64.0.1":     false,
		"127.0.0.1":      false,
		"169.254.1.1":    false,
		"0.0.0.0":        false,
		"224.0.0.1":      false,
		"2001:db8::1":    false,
		"::ffff:8.8.8.8": true,
	} {
		if got := Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Public(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestHTTP(t *testing.T) {
	serve := func(status int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	}
	t.Run("ok", func(t *testing.T) {
		s := serve(200, public1+"\n")
		defer s.Close()
		o, err := NewHTTP("echo", s.URL, s.Client()).Observe(context.Background())
		if err != nil || o.Address.String() != public1 {
			t.Fatalf("%v %v", o, err)
		}
	})
	t.Run("private is no address", func(t *testing.T) {
		s := serve(200, "192.168.1.1")
		defer s.Close()
		_, err := NewHTTP("echo", s.URL, s.Client()).Observe(context.Background())
		if !errors.Is(err, ErrNoAddress) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("garbage", func(t *testing.T) {
		s := serve(200, "<html>")
		defer s.Close()
		if _, err := NewHTTP("echo", s.URL, s.Client()).Observe(context.Background()); err == nil {
			t.Fatal("no error")
		}
	})
	t.Run("status", func(t *testing.T) {
		s := serve(503, "")
		defer s.Close()
		if _, err := NewHTTP("echo", s.URL, s.Client()).Observe(context.Background()); err == nil {
			t.Fatal("no error")
		}
	})
}

func TestUniFi(t *testing.T) {
	serve := func(wanIP string, hasWAN bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-KEY") != "k" || r.URL.Path != "/proxy/network/api/s/site1/stat/health" {
				w.WriteHeader(401)
				return
			}
			body := `{"data":[{"subsystem":"lan","status":"ok"}`
			if hasWAN {
				body += `,{"subsystem":"wan","status":"ok","wan_ip":"` + wanIP + `"}`
			}
			_, _ = w.Write([]byte(body + `]}`))
		}))
	}
	key := func(context.Context) (string, error) { return "k", nil }
	observe := func(s *httptest.Server) (Observation, error) {
		return NewUniFi("gw", UniFiConfig{URL: s.URL + "/", Site: "site1", APIKey: key, Client: s.Client()}).Observe(context.Background())
	}
	t.Run("ok", func(t *testing.T) {
		s := serve(public1, true)
		defer s.Close()
		o, err := observe(s)
		if err != nil || o.Address.String() != public1 {
			t.Fatalf("%v %v", o, err)
		}
	})
	t.Run("empty wan_ip", func(t *testing.T) {
		s := serve("", true)
		defer s.Close()
		if _, err := observe(s); !errors.Is(err, ErrNoAddress) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("no wan subsystem", func(t *testing.T) {
		s := serve("", false)
		defer s.Close()
		if _, err := observe(s); err == nil || errors.Is(err, ErrNoAddress) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("bad key", func(t *testing.T) {
		s := serve(public1, true)
		defer s.Close()
		bad := func(context.Context) (string, error) { return "x", nil }
		if _, err := NewUniFi("gw", UniFiConfig{URL: s.URL, Site: "site1", APIKey: bad, Client: s.Client()}).Observe(context.Background()); err == nil {
			t.Fatal("no error")
		}
	})
}

// stunServer answers Binding requests with a fixed address, XOR-mapped or plain.
func stunServer(t *testing.T, reply netip.Addr, xor bool) string {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n < stunHeaderLen || binary.BigEndian.Uint16(buf[0:2]) != stunBindingRequest {
				continue
			}
			ip := reply.As4()
			attr := make([]byte, 8)
			attr[1] = 0x01
			binary.BigEndian.PutUint16(attr[2:4], 3478)
			copy(attr[4:8], ip[:])
			typ := uint16(attrMappedAddress)
			if xor {
				typ = attrXORMapped
				magic := [4]byte{0x21, 0x12, 0xA4, 0x42}
				for i := range 4 {
					attr[4+i] ^= magic[i]
				}
			}
			resp := make([]byte, stunHeaderLen+4+len(attr))
			binary.BigEndian.PutUint16(resp[0:2], stunBindingSuccess)
			binary.BigEndian.PutUint16(resp[2:4], uint16(4+len(attr)))
			binary.BigEndian.PutUint32(resp[4:8], stunMagic)
			copy(resp[8:20], buf[8:20])
			binary.BigEndian.PutUint16(resp[20:22], typ)
			binary.BigEndian.PutUint16(resp[22:24], uint16(len(attr)))
			copy(resp[24:], attr)
			_, _ = conn.WriteToUDP(resp, addr)
		}
	}()
	return conn.LocalAddr().String()
}

func TestSTUN(t *testing.T) {
	want := netip.MustParseAddr(public1)
	t.Run("xor mapped", func(t *testing.T) {
		o, err := NewSTUN("stun", []string{stunServer(t, want, true)}, time.Second).Observe(context.Background())
		if err != nil || o.Address != want {
			t.Fatalf("%v %v", o, err)
		}
	})
	t.Run("mapped", func(t *testing.T) {
		o, err := NewSTUN("stun", []string{stunServer(t, want, false)}, time.Second).Observe(context.Background())
		if err != nil || o.Address != want {
			t.Fatalf("%v %v", o, err)
		}
	})
	t.Run("falls through a silent server", func(t *testing.T) {
		silent, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = silent.Close() }()
		s := NewSTUN("stun", []string{silent.LocalAddr().String(), stunServer(t, want, true)}, 200*time.Millisecond)
		o, err := s.Observe(context.Background())
		if err != nil || o.Address != want {
			t.Fatalf("%v %v", o, err)
		}
	})
	t.Run("private reflexive is no address", func(t *testing.T) {
		_, err := NewSTUN("stun", []string{stunServer(t, netip.MustParseAddr("10.1.2.3"), true)}, time.Second).Observe(context.Background())
		if !errors.Is(err, ErrNoAddress) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("all silent", func(t *testing.T) {
		silent, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = silent.Close() }()
		if _, err := NewSTUN("stun", []string{silent.LocalAddr().String()}, 100*time.Millisecond).Observe(context.Background()); err == nil {
			t.Fatal("no error")
		}
	})
}

func TestStatic(t *testing.T) {
	s, err := NewStatic("fixed", "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if o, err := s.Observe(context.Background()); err != nil || o.Address.String() != "10.0.0.1" {
		t.Fatalf("%v %v", o, err)
	}
	if _, err := NewStatic("bad", "nope"); err == nil {
		t.Fatal("no error")
	}
}

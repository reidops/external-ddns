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
	"net/netip"
	"time"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
)

// Static asserts a fixed address. It is not checked for being public so a
// test fixture can use any range.
type Static struct {
	name string
	addr netip.Addr
}

func NewStatic(name, address string) (*Static, error) {
	a, err := netip.ParseAddr(address)
	if err != nil {
		return nil, err
	}
	return &Static{name: name, addr: a.Unmap()}, nil
}

func (s *Static) Name() string { return s.name }
func (s *Static) Kind() Kind   { return ddnsv1alpha1.InsideOut }

func (s *Static) Observe(context.Context) (Observation, error) {
	return Observation{Address: s.addr, ObservedAt: time.Now()}, nil
}

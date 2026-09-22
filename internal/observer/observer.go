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

// Package observer discovers the site's public IPv4 address by several
// independent methods.
package observer

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
)

// Kind is the direction an observer looks from.
type Kind = ddnsv1alpha1.ObserverKind

// Observation is one successful answer.
type Observation struct {
	Address    netip.Addr
	ObservedAt time.Time
}

// Observer answers "what is the public address" one way.
type Observer interface {
	Name() string
	Kind() Kind
	Observe(ctx context.Context) (Observation, error)
}

// ErrNoAddress: the method answered, and there is no public address.
var ErrNoAddress = errors.New("no public address")

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Public reports whether a is a globally routable unicast IPv4 address.
func Public(a netip.Addr) bool {
	a = a.Unmap()
	return a.Is4() && a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a)
}

func observed(a netip.Addr) (Observation, error) {
	if !Public(a) {
		return Observation{}, fmt.Errorf("%w: %s", ErrNoAddress, a)
	}
	return Observation{Address: a.Unmap(), ObservedAt: time.Now()}, nil
}

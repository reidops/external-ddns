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
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
)

// HTTP fetches a URL whose body is the caller's address as plain text.
type HTTP struct {
	name   string
	url    string
	client *http.Client
}

func NewHTTP(name, url string, client *http.Client) *HTTP {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTP{name: name, url: url, client: client}
}

func (h *HTTP) Name() string { return h.name }
func (h *HTTP) Kind() Kind   { return ddnsv1alpha1.OutsideIn }

func (h *HTTP) Observe(ctx context.Context) (Observation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url, nil)
	if err != nil {
		return Observation{}, err
	}
	req.Header.Set("Accept", "text/plain")
	resp, err := h.client.Do(req)
	if err != nil {
		return Observation{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Observation{}, fmt.Errorf("%s: HTTP %d", h.url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return Observation{}, err
	}
	a, err := netip.ParseAddr(strings.TrimSpace(string(body)))
	if err != nil {
		return Observation{}, fmt.Errorf("%s: body is not an address: %w", h.url, err)
	}
	return observed(a)
}

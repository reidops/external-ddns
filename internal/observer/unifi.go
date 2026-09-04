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
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
)

// UniFiConfig configures a UniFi Network gateway observer.
type UniFiConfig struct {
	URL  string
	Site string
	// APIKey is read per observation so a rotated Secret is picked up.
	APIKey func(ctx context.Context) (string, error)
	Client *http.Client
}

// UniFi reads wan_ip from the site's health endpoint.
type UniFi struct {
	name string
	cfg  UniFiConfig
}

func NewUniFi(name string, cfg UniFiConfig) *UniFi {
	if cfg.Site == "" {
		cfg.Site = "default"
	}
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	return &UniFi{name: name, cfg: cfg}
}

func (u *UniFi) Name() string { return u.name }
func (u *UniFi) Kind() Kind   { return ddnsv1alpha1.InsideOut }

func (u *UniFi) Observe(ctx context.Context) (Observation, error) {
	key, err := u.cfg.APIKey(ctx)
	if err != nil {
		return Observation{}, fmt.Errorf("api key: %w", err)
	}
	url := fmt.Sprintf("%s/proxy/network/api/s/%s/stat/health", u.cfg.URL, u.cfg.Site)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Observation{}, err
	}
	req.Header.Set("X-API-KEY", key)
	req.Header.Set("Accept", "application/json")
	resp, err := u.cfg.Client.Do(req)
	if err != nil {
		return Observation{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Observation{}, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	var health struct {
		Data []struct {
			Subsystem string `json:"subsystem"`
			WANIP     string `json:"wan_ip"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return Observation{}, fmt.Errorf("%s: %w", url, err)
	}
	for _, d := range health.Data {
		if d.Subsystem != "wan" {
			continue
		}
		if d.WANIP == "" {
			return Observation{}, fmt.Errorf("%w: wan subsystem has no wan_ip", ErrNoAddress)
		}
		a, err := netip.ParseAddr(d.WANIP)
		if err != nil {
			return Observation{}, fmt.Errorf("wan_ip %q: %w", d.WANIP, err)
		}
		return observed(a)
	}
	return Observation{}, fmt.Errorf("%s: no wan subsystem in response", url)
}

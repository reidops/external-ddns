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
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
)

// Factory builds observers from spec.
type Factory struct {
	// Secrets resolves API keys. An API reader, so only get is needed.
	Secrets client.Reader
	Timeout time.Duration
}

// Build returns the observer for one ObserverSpec.
func (f Factory) Build(spec ddnsv1alpha1.ObserverSpec) (Observer, error) {
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	switch {
	case spec.Static != nil:
		return NewStatic(spec.Name, spec.Static.Address)
	case spec.STUN != nil:
		return NewSTUN(spec.Name, spec.STUN.Servers, timeout), nil
	case spec.HTTP != nil:
		return NewHTTP(spec.Name, spec.HTTP.URL, &http.Client{Timeout: timeout, Transport: transport(false)}), nil
	case spec.Unifi != nil:
		u := spec.Unifi
		ref := u.APIKeySecretRef
		return NewUniFi(spec.Name, UniFiConfig{
			URL:    u.URL,
			Site:   u.Site,
			Client: &http.Client{Timeout: timeout, Transport: transport(u.InsecureSkipVerify)},
			APIKey: func(ctx context.Context) (string, error) {
				return f.secretKey(ctx, ref)
			},
		}), nil
	}
	return nil, fmt.Errorf("observer %q: no method set", spec.Name)
}

// transport dials fresh each observation: a pooled connection would keep
// answering from wherever it was first routed.
func transport(insecure bool) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableKeepAlives = true
	t.TLSClientConfig = &tls.Config{InsecureSkipVerify: insecure} //nolint:gosec // operator's choice
	return t
}

func (f Factory) secretKey(ctx context.Context, ref ddnsv1alpha1.SecretKeyReference) (string, error) {
	if f.Secrets == nil {
		return "", errors.New("no secret reader")
	}
	key := ref.Key
	if key == "" {
		key = "api-key"
	}
	var s corev1.Secret
	if err := f.Secrets.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, &s); err != nil {
		return "", err
	}
	v, ok := s.Data[key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s has no key %q", ref.Namespace, ref.Name, key)
	}
	return string(v), nil
}

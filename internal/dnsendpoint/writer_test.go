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

package dnsendpoint

import (
	"reflect"
	"testing"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
)

const addr = "203.0.113.7"

func TestDesired(t *testing.T) {
	pa := &ddnsv1alpha1.PublicAddress{
		Spec: ddnsv1alpha1.PublicAddressSpec{Publish: ddnsv1alpha1.PublishSpec{
			DNSEndpoint: ddnsv1alpha1.ObjectReference{Namespace: "ns", Name: "site"},
			TTL:         120,
			Records:     []string{"vpn.example.com", "*.example.com"},
		}},
		Status: ddnsv1alpha1.PublicAddressStatus{Address: addr},
	}
	got := Desired(pa)
	if got.Namespace != "ns" || got.Name != "site" || got.Kind != "DNSEndpoint" || got.APIVersion != "externaldns.k8s.io/v1alpha1" {
		t.Fatalf("%+v", got.ObjectMeta)
	}
	want := []Endpoint{
		{DNSName: "*.example.com", Targets: []string{addr}, RecordType: "A", RecordTTL: 120},
		{DNSName: "vpn.example.com", Targets: []string{addr}, RecordType: "A", RecordTTL: 120},
	}
	if !reflect.DeepEqual(got.Spec.Endpoints, want) {
		t.Fatalf("got %+v", got.Spec.Endpoints)
	}
}

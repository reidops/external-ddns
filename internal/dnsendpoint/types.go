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

// Package dnsendpoint writes the address into external-dns's DNSEndpoint.
// The type below covers only the fields written; the CRD is external-dns's.
package dnsendpoint

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var GroupVersion = schema.GroupVersion{Group: "externaldns.k8s.io", Version: "v1alpha1"}

func AddToScheme(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &DNSEndpoint{}, &DNSEndpointList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}

type Endpoint struct {
	DNSName    string   `json:"dnsName,omitempty"`
	Targets    []string `json:"targets,omitempty"`
	RecordType string   `json:"recordType,omitempty"`
	RecordTTL  int64    `json:"recordTTL,omitempty"`
}

type DNSEndpointSpec struct {
	Endpoints []Endpoint `json:"endpoints,omitempty"`
}

type DNSEndpointStatus struct {
	// Set by external-dns once it has consumed this generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

type DNSEndpoint struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DNSEndpointSpec   `json:"spec,omitempty"`
	Status            DNSEndpointStatus `json:"status,omitempty"`
}

type DNSEndpointList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DNSEndpoint `json:"items"`
}

func (in *DNSEndpoint) DeepCopyObject() runtime.Object { return in.DeepCopy() }

func (in *DNSEndpoint) DeepCopy() *DNSEndpoint {
	if in == nil {
		return nil
	}
	out := *in
	in.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.Endpoints != nil {
		out.Spec.Endpoints = make([]Endpoint, len(in.Spec.Endpoints))
		for i, e := range in.Spec.Endpoints {
			out.Spec.Endpoints[i] = e
			if e.Targets != nil {
				out.Spec.Endpoints[i].Targets = append([]string(nil), e.Targets...)
			}
		}
	}
	return &out
}

func (in *DNSEndpointList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := *in
	in.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]DNSEndpoint, len(in.Items))
		for i := range in.Items {
			out.Items[i] = *in.Items[i].DeepCopy()
		}
	}
	return &out
}

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

package dnsendpoint

import (
	"context"
	"maps"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
)

// Writer keeps one DNSEndpoint equal to what a PublicAddress publishes.
type Writer struct {
	client.Client
	Scheme *runtime.Scheme
}

// Desired is the DNSEndpoint a PublicAddress with a corroborated address wants.
func Desired(pa *ddnsv1alpha1.PublicAddress) *DNSEndpoint {
	records := slices.Clone(pa.Spec.Publish.Records)
	slices.Sort(records)
	endpoints := make([]Endpoint, 0, len(records))
	for _, name := range records {
		endpoints = append(endpoints, Endpoint{
			DNSName:    name,
			Targets:    []string{pa.Status.Address},
			RecordType: "A",
			RecordTTL:  pa.Spec.Publish.TTL,
		})
	}
	return &DNSEndpoint{
		TypeMeta: metav1.TypeMeta{APIVersion: GroupVersion.String(), Kind: "DNSEndpoint"},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: pa.Spec.Publish.DNSEndpoint.Namespace,
			Name:      pa.Spec.Publish.DNSEndpoint.Name,
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "external-ddns"},
		},
		Spec: DNSEndpointSpec{Endpoints: endpoints},
	}
}

// Write creates or updates the DNSEndpoint, owned by the PublicAddress.
func (w *Writer) Write(ctx context.Context, pa *ddnsv1alpha1.PublicAddress) error {
	desired := Desired(pa)
	ep := &DNSEndpoint{ObjectMeta: metav1.ObjectMeta{Namespace: desired.Namespace, Name: desired.Name}}
	_, err := controllerutil.CreateOrPatch(ctx, w.Client, ep, func() error {
		ep.Spec = desired.Spec
		if ep.Labels == nil {
			ep.Labels = map[string]string{}
		}
		maps.Copy(ep.Labels, desired.Labels)
		return controllerutil.SetControllerReference(pa, ep, w.Scheme)
	})
	return err
}

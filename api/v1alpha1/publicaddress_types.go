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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ObserverKind is the direction an observer looks from.
// +kubebuilder:validation:Enum=inside-out;outside-in
type ObserverKind string

const (
	// InsideOut asks the gateway what address it holds.
	InsideOut ObserverKind = "inside-out"
	// OutsideIn asks the internet what address it sees.
	OutsideIn ObserverKind = "outside-in"
)

// Condition types and reasons.
const (
	ConditionObserved     = "Observed"
	ConditionCorroborated = "Corroborated"
	ConditionPublished    = "Published"

	ReasonObserved           = "Observed"
	ReasonNoPublicAddress    = "NoPublicAddress"
	ReasonAllObserversFailed = "AllObserversFailed"
	ReasonCorroborated       = "Corroborated"
	ReasonInsufficientKinds  = "InsufficientKinds"
	ReasonKindsDisagree      = "KindsDisagree"
	ReasonPending            = "Pending"
	ReasonPublished          = "Published"
	ReasonNoAddress          = "NoAddress"
	ReasonWriteFailed        = "WriteFailed"
)

// PublicAddressSpec defines the desired state of PublicAddress.
type PublicAddressSpec struct {
	// Interval between observation rounds.
	// +kubebuilder:default="60s"
	// +optional
	Interval metav1.Duration `json:"interval,omitzero"`

	// +kubebuilder:default={requiredRounds: 3}
	// +optional
	Corroboration CorroborationSpec `json:"corroboration,omitzero"`

	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=name
	Observers []ObserverSpec `json:"observers"`

	Publish PublishSpec `json:"publish"`
}

// CorroborationSpec tunes how much agreement a change needs.
type CorroborationSpec struct {
	// Consecutive agreeing rounds before a new address is trusted.
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +optional
	RequiredRounds int32 `json:"requiredRounds,omitempty"`
}

// ObserverSpec configures one observer. Exactly one method is set; the
// observer's kind follows from it.
// +kubebuilder:validation:XValidation:rule="(has(self.static) ? 1 : 0) + (has(self.unifi) ? 1 : 0) + (has(self.stun) ? 1 : 0) + (has(self.http) ? 1 : 0) == 1",message="exactly one of static, unifi, stun, http must be set"
type ObserverSpec struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +optional
	Static *StaticObserver `json:"static,omitempty"`
	// +optional
	Unifi *UnifiObserver `json:"unifi,omitempty"`
	// +optional
	STUN *STUNObserver `json:"stun,omitempty"`
	// +optional
	HTTP *HTTPObserver `json:"http,omitempty"`
}

// Kind returns the direction the configured method observes from.
func (o ObserverSpec) Kind() ObserverKind {
	if o.STUN != nil || o.HTTP != nil {
		return OutsideIn
	}
	return InsideOut
}

// StaticObserver asserts an address (inside-out).
type StaticObserver struct {
	// +kubebuilder:validation:Format=ipv4
	Address string `json:"address"`
}

// UnifiObserver reads the WAN address from a UniFi Network gateway (inside-out).
type UnifiObserver struct {
	// Base URL of the gateway, e.g. https://192.0.2.1.
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`
	// +kubebuilder:default=default
	// +optional
	Site string `json:"site,omitempty"`
	// +optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
	// Secret holding the API key.
	APIKeySecretRef SecretKeyReference `json:"apiKeySecretRef"`
}

// SecretKeyReference names one key of a Secret in any namespace.
type SecretKeyReference struct {
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:default=api-key
	// +optional
	Key string `json:"key,omitempty"`
}

// STUNObserver asks STUN servers for the reflexive address (outside-in).
type STUNObserver struct {
	// host:port list; the first answer wins.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Servers []string `json:"servers"`
}

// HTTPObserver fetches a URL whose body is the caller's address (outside-in). Use https in production.
type HTTPObserver struct {
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`
}

// PublishSpec describes the DNSEndpoint the address is written to.
type PublishSpec struct {
	DNSEndpoint ObjectReference `json:"dnsEndpoint"`
	// Record TTL in seconds.
	// +kubebuilder:default=300
	// +kubebuilder:validation:Minimum=1
	// +optional
	TTL int64 `json:"ttl,omitempty"`
	// DNS names to publish as A records.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=set
	Records []string `json:"records"`
}

// ObjectReference names a namespaced object.
type ObjectReference struct {
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// PublicAddressStatus defines the observed state of PublicAddress.
type PublicAddressStatus struct {
	// Last corroborated address. Never cleared, only replaced.
	// +optional
	Address string `json:"address,omitempty"`
	// Last round in which Address was corroborated.
	// +optional
	ObservedAt *metav1.Time `json:"observedAt,omitempty"`
	// A different address seen in agreement, not yet for enough rounds.
	// +optional
	Candidate *Candidate `json:"candidate,omitempty"`
	// Per-observer outcome of the last round.
	// +listType=map
	// +listMapKey=name
	// +optional
	Observations []Observation `json:"observations,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Candidate is an address waiting for enough agreeing rounds.
type Candidate struct {
	Address string `json:"address"`
	Rounds  int32  `json:"rounds"`
}

// Observation is one observer's answer in the last round.
type Observation struct {
	Name string       `json:"name"`
	Kind ObserverKind `json:"kind"`
	// +optional
	Address string `json:"address,omitempty"`
	// +optional
	ObservedAt *metav1.Time `json:"observedAt,omitempty"`
	// +optional
	Error string `json:"error,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=pa
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.status.address`
// +kubebuilder:printcolumn:name="Observed",type=string,JSONPath=`.status.conditions[?(@.type=="Observed")].status`
// +kubebuilder:printcolumn:name="Corroborated",type=string,JSONPath=`.status.conditions[?(@.type=="Corroborated")].status`
// +kubebuilder:printcolumn:name="Published",type=string,JSONPath=`.status.conditions[?(@.type=="Published")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PublicAddress is one site's public address: observed, corroborated, published.
type PublicAddress struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PublicAddressSpec `json:"spec"`

	// +optional
	Status PublicAddressStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PublicAddressList contains a list of PublicAddress.
type PublicAddressList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PublicAddress `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &PublicAddress{}, &PublicAddressList{})
		return nil
	})
}

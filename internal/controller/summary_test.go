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

package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
)

func conds(observed, corroborated, published string, reasons ...string) []metav1.Condition {
	out := []metav1.Condition{
		{Type: ddnsv1alpha1.ConditionObserved, Status: metav1.ConditionStatus(observed)},
		{Type: ddnsv1alpha1.ConditionCorroborated, Status: metav1.ConditionStatus(corroborated)},
		{Type: ddnsv1alpha1.ConditionPublished, Status: metav1.ConditionStatus(published)},
	}
	for i, r := range reasons {
		out[i].Reason = r
	}
	return out
}

func TestSummarize(t *testing.T) {
	cases := []struct {
		name string
		st   ddnsv1alpha1.PublicAddressStatus
		want string
	}{
		{"published", ddnsv1alpha1.PublicAddressStatus{Conditions: conds("True", "True", "True")}, "Published"},
		{"pending", ddnsv1alpha1.PublicAddressStatus{
			Candidate:  &ddnsv1alpha1.Candidate{Address: "203.0.113.1", Rounds: 2},
			Conditions: conds("True", "False", "False", "", ddnsv1alpha1.ReasonPending, ddnsv1alpha1.ReasonNoAddress),
		}, "Pending 2/3"},
		{"disagree", ddnsv1alpha1.PublicAddressStatus{
			Conditions: conds("True", "False", "True", "", ddnsv1alpha1.ReasonKindsDisagree),
		}, "KindsDisagree"},
		{"no address wins over the rest", ddnsv1alpha1.PublicAddressStatus{
			Conditions: conds("False", "False", "False", ddnsv1alpha1.ReasonNoPublicAddress, ddnsv1alpha1.ReasonInsufficientKinds, ddnsv1alpha1.ReasonNoAddress),
		}, "NoPublicAddress"},
		{"write failed", ddnsv1alpha1.PublicAddressStatus{
			Conditions: conds("True", "True", "False", "", "", ddnsv1alpha1.ReasonWriteFailed),
		}, "WriteFailed"},
		{"no conditions yet", ddnsv1alpha1.PublicAddressStatus{}, "Published"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarize(&tc.st, 3); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

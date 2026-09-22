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

// Package corroborate turns one round of observations into a status change.
package corroborate

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
	"github.com/reidops/external-ddns/internal/observer"
)

// Input is one observer's answer.
type Input struct {
	Name    string
	Kind    observer.Kind
	Address netip.Addr
	Err     error
}

// Verdict classifies a round.
type Verdict string

const (
	Agreement       Verdict = "Agreement"
	Disagreement    Verdict = "Disagreement"
	Uncorroborated  Verdict = "Uncorroborated"
	NoPublicAddress Verdict = "NoPublicAddress"
	AllFailed       Verdict = "AllFailed"
)

// Result of one round.
type Result struct {
	Verdict Verdict
	// Address is set for Agreement only.
	Address netip.Addr
	// Detail is a one-line summary for condition messages.
	Detail string
}

// Round decides what one round of observations says. Kinds must agree
// with each other, and at least two kinds must have decided.
func Round(inputs []Input) Result {
	byKind := map[observer.Kind][]Input{}
	var ok, noAddr int
	for _, in := range inputs {
		if in.Err == nil {
			ok++
			byKind[in.Kind] = append(byKind[in.Kind], in)
		} else if errors.Is(in.Err, observer.ErrNoAddress) {
			noAddr++
		}
	}
	if ok == 0 {
		if noAddr > 0 {
			return Result{Verdict: NoPublicAddress, Detail: summarize(inputs)}
		}
		return Result{Verdict: AllFailed, Detail: summarize(inputs)}
	}

	decided := map[observer.Kind]netip.Addr{}
	var undecided []string
	for kind, ins := range byKind {
		a := ins[0].Address
		agree := true
		for _, in := range ins[1:] {
			if in.Address != a {
				agree = false
				break
			}
		}
		if agree {
			decided[kind] = a
		} else {
			undecided = append(undecided, string(kind))
		}
	}
	slices.Sort(undecided)

	detail := summarize(inputs)
	if len(decided) < 2 {
		if len(undecided) > 0 {
			detail = fmt.Sprintf("%s disagree internally; %s", strings.Join(undecided, ", "), detail)
		}
		return Result{Verdict: Uncorroborated, Detail: detail}
	}
	var first netip.Addr
	for _, a := range decided {
		if !first.IsValid() {
			first = a
		} else if a != first {
			return Result{Verdict: Disagreement, Detail: detail}
		}
	}
	return Result{Verdict: Agreement, Address: first, Detail: detail}
}

func summarize(inputs []Input) string {
	parts := make([]string, 0, len(inputs))
	for _, in := range inputs {
		switch {
		case in.Err != nil:
			parts = append(parts, fmt.Sprintf("%s(%s)=error", in.Name, in.Kind))
		default:
			parts = append(parts, fmt.Sprintf("%s(%s)=%s", in.Name, in.Kind, in.Address))
		}
	}
	return strings.Join(parts, " ")
}

// Params for Apply.
type Params struct {
	RequiredRounds int32
	Generation     int64
	Now            metav1.Time
}

// Apply folds a Result into status: address, candidate, observedAt and the
// Observed and Corroborated conditions. Returns true when a new address was
// promoted.
func Apply(st *ddnsv1alpha1.PublicAddressStatus, r Result, p Params) bool {
	if p.RequiredRounds < 1 {
		p.RequiredRounds = 1
	}
	set := func(typ string, status metav1.ConditionStatus, reason, msg string) {
		meta.SetStatusCondition(&st.Conditions, metav1.Condition{
			Type: typ, Status: status, Reason: reason, Message: msg,
			ObservedGeneration: p.Generation, LastTransitionTime: p.Now,
		})
	}

	switch r.Verdict {
	case AllFailed:
		st.Candidate = nil
		set(ddnsv1alpha1.ConditionObserved, metav1.ConditionFalse, ddnsv1alpha1.ReasonAllObserversFailed, r.Detail)
		set(ddnsv1alpha1.ConditionCorroborated, metav1.ConditionFalse, ddnsv1alpha1.ReasonInsufficientKinds, "no observations")
		return false
	case NoPublicAddress:
		st.Candidate = nil
		set(ddnsv1alpha1.ConditionObserved, metav1.ConditionFalse, ddnsv1alpha1.ReasonNoPublicAddress, r.Detail)
		set(ddnsv1alpha1.ConditionCorroborated, metav1.ConditionFalse, ddnsv1alpha1.ReasonInsufficientKinds, "no observations")
		return false
	case Uncorroborated:
		st.Candidate = nil
		set(ddnsv1alpha1.ConditionObserved, metav1.ConditionTrue, ddnsv1alpha1.ReasonObserved, r.Detail)
		set(ddnsv1alpha1.ConditionCorroborated, metav1.ConditionFalse, ddnsv1alpha1.ReasonInsufficientKinds, r.Detail)
		return false
	case Disagreement:
		st.Candidate = nil
		set(ddnsv1alpha1.ConditionObserved, metav1.ConditionTrue, ddnsv1alpha1.ReasonObserved, r.Detail)
		set(ddnsv1alpha1.ConditionCorroborated, metav1.ConditionFalse, ddnsv1alpha1.ReasonKindsDisagree, r.Detail)
		return false
	}

	set(ddnsv1alpha1.ConditionObserved, metav1.ConditionTrue, ddnsv1alpha1.ReasonObserved, r.Detail)
	addr := r.Address.String()
	if addr == st.Address {
		st.Candidate = nil
		now := p.Now
		st.ObservedAt = &now
		set(ddnsv1alpha1.ConditionCorroborated, metav1.ConditionTrue, ddnsv1alpha1.ReasonCorroborated, r.Detail)
		return false
	}
	if st.Candidate == nil || st.Candidate.Address != addr {
		st.Candidate = &ddnsv1alpha1.Candidate{Address: addr}
	}
	st.Candidate.Rounds++
	if st.Candidate.Rounds < p.RequiredRounds {
		set(ddnsv1alpha1.ConditionCorroborated, metav1.ConditionFalse, ddnsv1alpha1.ReasonPending,
			fmt.Sprintf("observers agree on %s; %d/%d rounds before publishing", addr, st.Candidate.Rounds, p.RequiredRounds))
		return false
	}
	st.Address = addr
	now := p.Now
	st.ObservedAt = &now
	st.Candidate = nil
	set(ddnsv1alpha1.ConditionCorroborated, metav1.ConditionTrue, ddnsv1alpha1.ReasonCorroborated, r.Detail)
	return true
}

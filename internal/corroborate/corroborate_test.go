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

package corroborate

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
	"github.com/reidops/external-ddns/internal/observer"
)

var (
	a1 = netip.MustParseAddr("203.0.113.1")
	a2 = netip.MustParseAddr("203.0.113.2")
)

func in(name string, kind observer.Kind, addr netip.Addr) Input {
	return Input{Name: name, Kind: kind, Address: addr}
}

func fail(name string, kind observer.Kind, err error) Input {
	return Input{Name: name, Kind: kind, Err: err}
}

func TestRound(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name    string
		inputs  []Input
		verdict Verdict
		address netip.Addr
	}{
		{"agree", []Input{in("gw", ddnsv1alpha1.InsideOut, a1), in("stun", ddnsv1alpha1.OutsideIn, a1)}, Agreement, a1},
		{"three agree", []Input{in("gw", ddnsv1alpha1.InsideOut, a1), in("stun", ddnsv1alpha1.OutsideIn, a1), in("echo", ddnsv1alpha1.OutsideIn, a1)}, Agreement, a1},
		{"kinds disagree", []Input{in("gw", ddnsv1alpha1.InsideOut, a1), in("stun", ddnsv1alpha1.OutsideIn, a2)}, Disagreement, netip.Addr{}},
		{"one kind", []Input{in("stun", ddnsv1alpha1.OutsideIn, a1), in("echo", ddnsv1alpha1.OutsideIn, a1)}, Uncorroborated, netip.Addr{}},
		{"one kind after failure", []Input{fail("gw", ddnsv1alpha1.InsideOut, boom), in("stun", ddnsv1alpha1.OutsideIn, a1)}, Uncorroborated, netip.Addr{}},
		{"kind undecided", []Input{in("gw", ddnsv1alpha1.InsideOut, a1), in("stun", ddnsv1alpha1.OutsideIn, a1), in("echo", ddnsv1alpha1.OutsideIn, a2)}, Uncorroborated, netip.Addr{}},
		{"all failed", []Input{fail("gw", ddnsv1alpha1.InsideOut, boom), fail("stun", ddnsv1alpha1.OutsideIn, boom)}, AllFailed, netip.Addr{}},
		{"no address", []Input{fail("gw", ddnsv1alpha1.InsideOut, observer.ErrNoAddress), fail("stun", ddnsv1alpha1.OutsideIn, boom)}, NoPublicAddress, netip.Addr{}},
		{"empty", nil, AllFailed, netip.Addr{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Round(tc.inputs)
			if got.Verdict != tc.verdict {
				t.Fatalf("verdict %s, want %s (%s)", got.Verdict, tc.verdict, got.Detail)
			}
			if got.Address != tc.address {
				t.Fatalf("address %s, want %s", got.Address, tc.address)
			}
		})
	}
}

func cond(st *ddnsv1alpha1.PublicAddressStatus, typ string) (metav1.ConditionStatus, string) {
	c := meta.FindStatusCondition(st.Conditions, typ)
	if c == nil {
		return "", ""
	}
	return c.Status, c.Reason
}

func TestApply(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	p := Params{RequiredRounds: 3, Generation: 1, Now: now}
	agree := func(a netip.Addr) Result { return Result{Verdict: Agreement, Address: a} }

	t.Run("first address needs N rounds", func(t *testing.T) {
		st := &ddnsv1alpha1.PublicAddressStatus{}
		for i := 1; i < 3; i++ {
			if Apply(st, agree(a1), p) {
				t.Fatal("promoted early")
			}
			if st.Address != "" || st.Candidate == nil || st.Candidate.Rounds != int32(i) {
				t.Fatalf("round %d: address %q candidate %+v", i, st.Address, st.Candidate)
			}
			if s, r := cond(st, ddnsv1alpha1.ConditionCorroborated); s != metav1.ConditionFalse || r != ddnsv1alpha1.ReasonPending {
				t.Fatalf("corroborated %s/%s", s, r)
			}
		}
		if !Apply(st, agree(a1), p) {
			t.Fatal("not promoted")
		}
		if st.Address != a1.String() || st.Candidate != nil || st.ObservedAt == nil || !st.ObservedAt.Equal(&now) {
			t.Fatalf("after promotion: %+v", st)
		}
		if s, _ := cond(st, ddnsv1alpha1.ConditionCorroborated); s != metav1.ConditionTrue {
			t.Fatal("not corroborated")
		}
	})

	t.Run("same address refreshes", func(t *testing.T) {
		st := &ddnsv1alpha1.PublicAddressStatus{Address: a1.String()}
		if Apply(st, agree(a1), p) {
			t.Fatal("promoted")
		}
		if st.ObservedAt == nil || st.Candidate != nil {
			t.Fatalf("%+v", st)
		}
	})

	t.Run("disagreement holds and clears candidate", func(t *testing.T) {
		st := &ddnsv1alpha1.PublicAddressStatus{Address: a1.String()}
		Apply(st, agree(a2), p)
		if st.Candidate == nil {
			t.Fatal("no candidate")
		}
		Apply(st, Result{Verdict: Disagreement, Detail: "x"}, p)
		if st.Address != a1.String() || st.Candidate != nil {
			t.Fatalf("%+v", st)
		}
		if s, r := cond(st, ddnsv1alpha1.ConditionCorroborated); s != metav1.ConditionFalse || r != ddnsv1alpha1.ReasonKindsDisagree {
			t.Fatalf("corroborated %s/%s", s, r)
		}
		if s, _ := cond(st, ddnsv1alpha1.ConditionObserved); s != metav1.ConditionTrue {
			t.Fatal("observed should be true")
		}
	})

	t.Run("candidate switch restarts count", func(t *testing.T) {
		st := &ddnsv1alpha1.PublicAddressStatus{Address: a1.String()}
		Apply(st, agree(a2), p)
		Apply(st, agree(a2), p)
		Apply(st, agree(netip.MustParseAddr("203.0.113.3")), p)
		if st.Candidate.Rounds != 1 {
			t.Fatalf("rounds %d", st.Candidate.Rounds)
		}
	})

	t.Run("no address is a condition", func(t *testing.T) {
		st := &ddnsv1alpha1.PublicAddressStatus{Address: a1.String()}
		Apply(st, Result{Verdict: NoPublicAddress}, p)
		if st.Address != a1.String() {
			t.Fatal("address cleared")
		}
		if s, r := cond(st, ddnsv1alpha1.ConditionObserved); s != metav1.ConditionFalse || r != ddnsv1alpha1.ReasonNoPublicAddress {
			t.Fatalf("observed %s/%s", s, r)
		}
	})

	t.Run("all failed", func(t *testing.T) {
		st := &ddnsv1alpha1.PublicAddressStatus{}
		Apply(st, Result{Verdict: AllFailed}, p)
		if s, r := cond(st, ddnsv1alpha1.ConditionObserved); s != metav1.ConditionFalse || r != ddnsv1alpha1.ReasonAllObserversFailed {
			t.Fatalf("observed %s/%s", s, r)
		}
	})
}

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
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
	"github.com/reidops/external-ddns/internal/corroborate"
	"github.com/reidops/external-ddns/internal/dnsendpoint"
	"github.com/reidops/external-ddns/internal/observer"
)

const defaultInterval = time.Minute

// ObserverFactory builds an observer from its spec.
type ObserverFactory interface {
	Build(spec ddnsv1alpha1.ObserverSpec) (observer.Observer, error)
}

// PublicAddressReconciler runs one observation round per reconcile.
type PublicAddressReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
	Observers ObserverFactory
	Writer    *dnsendpoint.Writer
	// ObserveTimeout bounds one round; defaults to 10s.
	ObserveTimeout time.Duration
	// Now is overridable for tests.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=ddns.reidops.com,resources=publicaddresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=ddns.reidops.com,resources=publicaddresses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=externaldns.k8s.io,resources=dnsendpoints,verbs=get;create;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

func (r *PublicAddressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	pa := &ddnsv1alpha1.PublicAddress{}
	if err := r.Get(ctx, req.NamespacedName, pa); err != nil {
		if client.IgnoreNotFound(err) == nil {
			forgetMetrics(req.Name)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !pa.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	now := metav1.NewTime(r.now())
	inputs, reports := r.observe(ctx, pa)
	result := corroborate.Round(inputs)
	previous := pa.Status.Address
	promoted := corroborate.Apply(&pa.Status, result, corroborate.Params{
		RequiredRounds: pa.Spec.Corroboration.RequiredRounds,
		Generation:     pa.Generation,
		Now:            now,
	})
	pa.Status.Observations = reports

	switch {
	case promoted:
		log.Info("Address changed", "from", previous, "to", pa.Status.Address)
		r.event(pa, corev1.EventTypeNormal, "AddressChanged", "Observe", "%s -> %s", orNone(previous), pa.Status.Address)
	case result.Verdict == corroborate.Disagreement:
		r.event(pa, corev1.EventTypeWarning, ddnsv1alpha1.ReasonKindsDisagree, "Observe", "%s", result.Detail)
	}

	r.publish(ctx, pa, now)
	pa.Status.Summary = summarize(&pa.Status, pa.Spec.Corroboration.RequiredRounds)
	r.record(pa, result, inputs)

	if err := r.Status().Update(ctx, pa); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: interval(pa)}, nil
}

func (r *PublicAddressReconciler) observe(ctx context.Context, pa *ddnsv1alpha1.PublicAddress) ([]corroborate.Input, []ddnsv1alpha1.Observation) {
	timeout := r.ObserveTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	inputs := make([]corroborate.Input, len(pa.Spec.Observers))
	reports := make([]ddnsv1alpha1.Observation, len(pa.Spec.Observers))
	var wg sync.WaitGroup
	for i, spec := range pa.Spec.Observers {
		inputs[i] = corroborate.Input{Name: spec.Name, Kind: spec.Kind()}
		obs, err := r.Observers.Build(spec)
		if err != nil {
			inputs[i].Err = err
			continue
		}
		wg.Go(func() {
			o, err := obs.Observe(ctx)
			if err != nil {
				inputs[i].Err = err
				return
			}
			inputs[i].Address = o.Address
			reports[i].ObservedAt = &metav1.Time{Time: o.ObservedAt}
		})
	}
	wg.Wait()

	for i, in := range inputs {
		reports[i].Name = in.Name
		reports[i].Kind = in.Kind
		if in.Err != nil {
			reports[i].Error = in.Err.Error()
		} else {
			reports[i].Address = in.Address.String()
		}
	}
	return inputs, reports
}

func (r *PublicAddressReconciler) publish(ctx context.Context, pa *ddnsv1alpha1.PublicAddress, now metav1.Time) {
	cond := metav1.Condition{Type: ddnsv1alpha1.ConditionPublished, ObservedGeneration: pa.Generation, LastTransitionTime: now}
	switch err := r.write(ctx, pa); {
	case pa.Status.Address == "":
		cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, ddnsv1alpha1.ReasonNoAddress, "waiting for a corroborated address"
	case err != nil:
		cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, ddnsv1alpha1.ReasonWriteFailed, err.Error()
		r.event(pa, corev1.EventTypeWarning, "PublishFailed", "Publish", "%v", err)
	default:
		cond.Status, cond.Reason = metav1.ConditionTrue, ddnsv1alpha1.ReasonPublished
		cond.Message = fmt.Sprintf("%s in %s/%s (%d records)", pa.Status.Address,
			pa.Spec.Publish.DNSEndpoint.Namespace, pa.Spec.Publish.DNSEndpoint.Name, len(pa.Spec.Publish.Records))
	}
	meta.SetStatusCondition(&pa.Status.Conditions, cond)
}

func (r *PublicAddressReconciler) write(ctx context.Context, pa *ddnsv1alpha1.PublicAddress) error {
	if pa.Status.Address == "" {
		return nil
	}
	if r.Writer == nil {
		return errors.New("no writer configured")
	}
	return r.Writer.Write(ctx, pa)
}

func (r *PublicAddressReconciler) record(pa *ddnsv1alpha1.PublicAddress, result corroborate.Result, inputs []corroborate.Input) {
	name := pa.Name
	for _, in := range inputs {
		res := "ok"
		switch {
		case errors.Is(in.Err, observer.ErrNoAddress):
			res = "no_address"
		case in.Err != nil:
			res = "error"
		}
		observations.WithLabelValues(name, in.Name, res).Inc()
	}
	if pa.Status.ObservedAt != nil {
		observedTimestamp.WithLabelValues(name).Set(float64(pa.Status.ObservedAt.Unix()))
	}
	if pa.Status.Address != "" {
		addressInfo.DeletePartialMatch(prometheus.Labels{labelName: name})
		addressInfo.WithLabelValues(name, pa.Status.Address).Set(1)
	}
	v := 0.0
	if result.Verdict == corroborate.Agreement && result.Address.String() == pa.Status.Address {
		v = 1
	}
	corroborated.WithLabelValues(name).Set(v)
}

func (r *PublicAddressReconciler) event(pa *ddnsv1alpha1.PublicAddress, typ, reason, action, format string, args ...any) {
	if r.Recorder != nil {
		r.Recorder.Eventf(pa, nil, typ, reason, action, format, args...)
	}
}

func (r *PublicAddressReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func interval(pa *ddnsv1alpha1.PublicAddress) time.Duration {
	if d := pa.Spec.Interval.Duration; d > 0 {
		return d
	}
	return defaultInterval
}

// summarize is the STATUS column: Published, "Pending n/N" while a candidate
// counts down, else the reason of the first False condition.
func summarize(st *ddnsv1alpha1.PublicAddressStatus, requiredRounds int32) string {
	for _, typ := range []string{ddnsv1alpha1.ConditionObserved, ddnsv1alpha1.ConditionCorroborated, ddnsv1alpha1.ConditionPublished} {
		c := meta.FindStatusCondition(st.Conditions, typ)
		if c == nil || c.Status == metav1.ConditionTrue {
			continue
		}
		if c.Reason == ddnsv1alpha1.ReasonPending && st.Candidate != nil {
			return fmt.Sprintf("%s %d/%d", c.Reason, st.Candidate.Rounds, max(requiredRounds, 1))
		}
		return c.Reason
	}
	return ddnsv1alpha1.ReasonPublished
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func (r *PublicAddressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Rounds are driven by RequeueAfter and spec changes, not by the status
	// writes this controller makes.
	return ctrl.NewControllerManagedBy(mgr).
		For(&ddnsv1alpha1.PublicAddress{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("publicaddress").
		Complete(r)
}

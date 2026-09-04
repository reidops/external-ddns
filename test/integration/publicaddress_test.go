//go:build integration

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

package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
	"github.com/reidops/external-ddns/internal/dnsendpoint"
	"github.com/reidops/external-ddns/internal/observer"
)

const (
	a1 = "203.0.113.1"
	a2 = "203.0.113.2"
	a3 = "203.0.113.3"
)

func newPA(name string) *ddnsv1alpha1.PublicAddress {
	return &ddnsv1alpha1.PublicAddress{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: ddnsv1alpha1.PublicAddressSpec{
			Interval:      metav1.Duration{Duration: 500 * time.Millisecond},
			Corroboration: ddnsv1alpha1.CorroborationSpec{RequiredRounds: 2},
			Observers: []ddnsv1alpha1.ObserverSpec{
				{Name: name + "-gw", Static: &ddnsv1alpha1.StaticObserver{Address: a1}},
				{Name: name + "-echo", HTTP: &ddnsv1alpha1.HTTPObserver{URL: "http://echo.invalid"}},
			},
			Publish: ddnsv1alpha1.PublishSpec{
				DNSEndpoint: ddnsv1alpha1.ObjectReference{Namespace: "dns", Name: name},
				TTL:         120,
				Records:     []string{"vpn.example.test", "*.example.test"},
			},
		},
	}
}

func get(name string) *ddnsv1alpha1.PublicAddress {
	pa := &ddnsv1alpha1.PublicAddress{}
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKey{Name: name}, pa)).To(Succeed())
	return pa
}

func condition(pa *ddnsv1alpha1.PublicAddress, typ string) (metav1.ConditionStatus, string) {
	c := meta.FindStatusCondition(pa.Status.Conditions, typ)
	if c == nil {
		return "", ""
	}
	return c.Status, c.Reason
}

func endpoint(name string) (*dnsendpoint.DNSEndpoint, error) {
	ep := &dnsendpoint.DNSEndpoint{}
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: "dns", Name: name}, ep)
	return ep, err
}

func targets(name string) []string {
	ep, err := endpoint(name)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ep.Spec.Endpoints {
		out = append(out, e.Targets...)
	}
	return out
}

var _ = Describe("PublicAddress", func() {
	var name string

	BeforeEach(func() {
		name = "pa-" + time.Now().Format("150405.000")
		script.set(name+"-gw", a1)
		script.set(name+"-echo", a1)
	})

	AfterEach(func() {
		pa := &ddnsv1alpha1.PublicAddress{ObjectMeta: metav1.ObjectMeta{Name: name}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, pa))).To(Succeed())
		ep := &dnsendpoint.DNSEndpoint{ObjectMeta: metav1.ObjectMeta{Namespace: "dns", Name: name}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ep))).To(Succeed())
	})

	It("publishes after the required agreeing rounds", func() {
		Expect(k8sClient.Create(ctx, newPA(name))).To(Succeed())

		Eventually(func(g Gomega) {
			pa := get(name)
			g.Expect(pa.Status.Address).To(Equal(a1))
			g.Expect(pa.Status.ObservedAt).NotTo(BeNil())
			g.Expect(pa.Status.Candidate).To(BeNil())
			s, _ := condition(pa, ddnsv1alpha1.ConditionPublished)
			g.Expect(s).To(Equal(metav1.ConditionTrue))
			g.Expect(pa.Status.Observations).To(HaveLen(2))
			g.Expect(pa.Status.Summary).To(Equal("Published"))
		}).Should(Succeed())

		ep, err := endpoint(name)
		Expect(err).NotTo(HaveOccurred())
		Expect(ep.Spec.Endpoints).To(Equal([]dnsendpoint.Endpoint{
			{DNSName: "*.example.test", Targets: []string{a1}, RecordType: "A", RecordTTL: 120},
			{DNSName: "vpn.example.test", Targets: []string{a1}, RecordType: "A", RecordTTL: 120},
		}))
		Expect(ep.OwnerReferences).To(HaveLen(1))
		Expect(ep.OwnerReferences[0].Kind).To(Equal("PublicAddress"))
		Expect(ep.OwnerReferences[0].Name).To(Equal(name))
		Expect(*ep.OwnerReferences[0].Controller).To(BeTrue())
	})

	It("follows a rotation only after the required rounds", func() {
		pa := newPA(name)
		pa.Spec.Corroboration.RequiredRounds = 4
		pa.Spec.Interval = metav1.Duration{Duration: time.Second}
		Expect(k8sClient.Create(ctx, pa)).To(Succeed())
		Eventually(func() string { return get(name).Status.Address }).Should(Equal(a1))

		script.set(name+"-gw", a2)
		script.set(name+"-echo", a2)
		Eventually(func(g Gomega) {
			pa := get(name)
			g.Expect(pa.Status.Candidate).NotTo(BeNil())
			g.Expect(pa.Status.Candidate.Address).To(Equal(a2))
			g.Expect(pa.Status.Address).To(Equal(a1))
			s, r := condition(pa, ddnsv1alpha1.ConditionCorroborated)
			g.Expect(s).To(Equal(metav1.ConditionFalse))
			g.Expect(r).To(Equal(ddnsv1alpha1.ReasonPending))
			g.Expect(pa.Status.Summary).To(MatchRegexp(`^Pending [1-3]/4$`))
		}).Should(Succeed())
		Expect(targets(name)).To(ConsistOf(a1, a1))

		Eventually(func() string { return get(name).Status.Address }, 15*time.Second).Should(Equal(a2))
		Eventually(func() []string { return targets(name) }).Should(ConsistOf(a2, a2))
	})

	It("holds the last address on disagreement", func() {
		Expect(k8sClient.Create(ctx, newPA(name))).To(Succeed())
		Eventually(func() string { return get(name).Status.Address }).Should(Equal(a1))

		script.set(name+"-gw", a2)
		script.set(name+"-echo", a3)
		Eventually(func(g Gomega) {
			pa := get(name)
			s, r := condition(pa, ddnsv1alpha1.ConditionCorroborated)
			g.Expect(s).To(Equal(metav1.ConditionFalse))
			g.Expect(r).To(Equal(ddnsv1alpha1.ReasonKindsDisagree))
			g.Expect(pa.Status.Summary).To(Equal(ddnsv1alpha1.ReasonKindsDisagree))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			pa := get(name)
			g.Expect(pa.Status.Address).To(Equal(a1))
			g.Expect(pa.Status.Candidate).To(BeNil())
			g.Expect(targets(name)).To(ConsistOf(a1, a1))
		}).Should(Succeed())

		script.set(name+"-gw", a3)
		Eventually(func() string { return get(name).Status.Address }).Should(Equal(a3))
	})

	It("does not trust a single kind", func() {
		Expect(k8sClient.Create(ctx, newPA(name))).To(Succeed())
		Eventually(func() string { return get(name).Status.Address }).Should(Equal(a1))

		script.set(name+"-gw", a2)
		script.fail(name+"-echo", errors.New("timeout"))
		Eventually(func(g Gomega) {
			pa := get(name)
			s, r := condition(pa, ddnsv1alpha1.ConditionCorroborated)
			g.Expect(s).To(Equal(metav1.ConditionFalse))
			g.Expect(r).To(Equal(ddnsv1alpha1.ReasonInsufficientKinds))
			s, _ = condition(pa, ddnsv1alpha1.ConditionObserved)
			g.Expect(s).To(Equal(metav1.ConditionTrue))
			s, _ = condition(pa, ddnsv1alpha1.ConditionPublished)
			g.Expect(s).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
		Consistently(func() string { return get(name).Status.Address }).Should(Equal(a1))
	})

	It("reports no public address as a condition", func() {
		Expect(k8sClient.Create(ctx, newPA(name))).To(Succeed())
		Eventually(func() string { return get(name).Status.Address }).Should(Equal(a1))
		before := get(name).Status.ObservedAt

		script.fail(name+"-gw", observer.ErrNoAddress)
		script.fail(name+"-echo", observer.ErrNoAddress)
		Eventually(func(g Gomega) {
			pa := get(name)
			s, r := condition(pa, ddnsv1alpha1.ConditionObserved)
			g.Expect(s).To(Equal(metav1.ConditionFalse))
			g.Expect(r).To(Equal(ddnsv1alpha1.ReasonNoPublicAddress))
			g.Expect(pa.Status.Address).To(Equal(a1))
			g.Expect(pa.Status.ObservedAt.Time).To(BeTemporally("==", before.Time))
		}).Should(Succeed())
	})

	It("recreates a deleted DNSEndpoint", func() {
		Expect(k8sClient.Create(ctx, newPA(name))).To(Succeed())
		Eventually(func() []string { return targets(name) }).Should(ConsistOf(a1, a1))

		ep, err := endpoint(name)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Delete(ctx, ep)).To(Succeed())
		Eventually(func() []string { return targets(name) }).Should(ConsistOf(a1, a1))
	})

	It("is validated and defaulted by the API server", func() {
		By("rejecting two methods on one observer")
		pa := newPA(name)
		pa.Spec.Observers[0].STUN = &ddnsv1alpha1.STUNObserver{Servers: []string{"x:1"}}
		Expect(k8sClient.Create(ctx, pa)).To(MatchError(ContainSubstring("exactly one of")))

		By("rejecting an observer with no method")
		pa = newPA(name)
		pa.Spec.Observers[0] = ddnsv1alpha1.ObserverSpec{Name: "none"}
		Expect(k8sClient.Create(ctx, pa)).To(MatchError(ContainSubstring("exactly one of")))

		By("rejecting a malformed static address")
		pa = newPA(name)
		pa.Spec.Observers[0].Static.Address = "not-an-ip"
		Expect(k8sClient.Create(ctx, pa)).NotTo(Succeed())

		By("rejecting an empty record list")
		pa = newPA(name)
		pa.Spec.Publish.Records = nil
		Expect(k8sClient.Create(ctx, pa)).NotTo(Succeed())

		By("defaulting interval, rounds, ttl and secret key")
		pa = newPA(name)
		pa.Spec.Interval = metav1.Duration{}
		pa.Spec.Corroboration = ddnsv1alpha1.CorroborationSpec{}
		pa.Spec.Publish.TTL = 0
		pa.Spec.Observers = append(pa.Spec.Observers, ddnsv1alpha1.ObserverSpec{
			Name: "u",
			Unifi: &ddnsv1alpha1.UnifiObserver{
				URL:             "https://192.0.2.1",
				APIKeySecretRef: ddnsv1alpha1.SecretKeyReference{Namespace: "dns", Name: "unifi"},
			},
		})
		Expect(k8sClient.Create(ctx, pa)).To(Succeed())
		got := get(name)
		Expect(got.Spec.Interval.Duration).To(Equal(time.Minute))
		Expect(got.Spec.Corroboration.RequiredRounds).To(Equal(int32(3)))
		Expect(got.Spec.Publish.TTL).To(Equal(int64(300)))
		Expect(got.Spec.Observers[2].Unifi.Site).To(Equal("default"))
		Expect(got.Spec.Observers[2].Unifi.APIKeySecretRef.Key).To(Equal("api-key"))
	})
})

var _ = Describe("Observer factory", func() {
	It("reads the UniFi API key from a Secret", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-KEY") != "s3cret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"subsystem":"wan","wan_ip":"` + a1 + `"}]}`))
		}))
		defer srv.Close()

		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "dns", Name: "unifi"},
			StringData: map[string]string{"api-key": "s3cret"},
		})).To(Succeed())

		obs, err := observer.Factory{Secrets: k8sClient}.Build(ddnsv1alpha1.ObserverSpec{
			Name: "gw",
			Unifi: &ddnsv1alpha1.UnifiObserver{
				URL:             srv.URL,
				APIKeySecretRef: ddnsv1alpha1.SecretKeyReference{Namespace: "dns", Name: "unifi"},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(obs.Kind()).To(Equal(ddnsv1alpha1.InsideOut))
		o, err := obs.Observe(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(o.Address.String()).To(Equal(a1))
	})
})

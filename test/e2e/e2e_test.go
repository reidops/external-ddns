//go:build e2e

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

package e2e

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
	"github.com/reidops/external-ddns/internal/dnsendpoint"
)

const (
	managerNamespace = "external-ddns-system"
	e2eNamespace     = "external-ddns-e2e"
	paName           = "site"
	echoA            = "203.0.113.7"
	echoB            = "203.0.113.8"
)

func getPA() *ddnsv1alpha1.PublicAddress {
	pa := &ddnsv1alpha1.PublicAddress{}
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKey{Name: paName}, pa)).To(Succeed())
	return pa
}

func condition(pa *ddnsv1alpha1.PublicAddress, typ string) (metav1.ConditionStatus, string) {
	c := meta.FindStatusCondition(pa.Status.Conditions, typ)
	if c == nil {
		return "", ""
	}
	return c.Status, c.Reason
}

func getEndpoint() (*dnsendpoint.DNSEndpoint, error) {
	ep := &dnsendpoint.DNSEndpoint{}
	err := k8sClient.Get(ctx, client.ObjectKey{Namespace: e2eNamespace, Name: paName}, ep)
	return ep, err
}

func targets() []string {
	ep, err := getEndpoint()
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ep.Spec.Endpoints {
		out = append(out, e.Targets...)
	}
	return out
}

// consumed reports whether external-dns has processed the current generation.
func consumed() bool {
	ep, err := getEndpoint()
	return err == nil && ep.Generation > 0 && ep.Status.ObservedGeneration == ep.Generation
}

// answer points the echo Service at fixture a or b, or at nothing.
func answer(which string) {
	svc := &corev1.Service{}
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKey{Namespace: e2eNamespace, Name: "echo"}, svc)).To(Succeed())
	svc.Spec.Selector = map[string]string{"app": "echo", "answer": which}
	ExpectWithOffset(1, k8sClient.Update(ctx, svc)).To(Succeed())
}

func setStatic(addr string) {
	pa := getPA()
	pa.Spec.Observers[0].Static.Address = addr
	ExpectWithOffset(1, k8sClient.Update(ctx, pa)).To(Succeed())
}

var metricRe = regexp.MustCompile(`(?m)^external_ddns_(\w+)\{[^}]*name="site"[^}]*\} (\S+)$`)

// metrics scrapes the manager through the API server's pod proxy.
func metrics() map[string]float64 {
	pods := &corev1.PodList{}
	ExpectWithOffset(1, k8sClient.List(ctx, pods, client.InNamespace(managerNamespace),
		client.MatchingLabels{"app.kubernetes.io/name": "external-ddns"})).To(Succeed())
	ExpectWithOffset(1, pods.Items).NotTo(BeEmpty())
	raw, err := clientset.CoreV1().Pods(managerNamespace).
		ProxyGet("http", pods.Items[0].Name, "8080", "/metrics", nil).DoRaw(ctx)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	out := map[string]float64{}
	for _, m := range metricRe.FindAllStringSubmatch(string(raw), -1) {
		v, _ := strconv.ParseFloat(m[2], 64)
		out[m[1]] = v
	}
	return out
}

var _ = Describe("external-ddns", Ordered, func() {
	AfterAll(func() {
		pa := &ddnsv1alpha1.PublicAddress{ObjectMeta: metav1.ObjectMeta{Name: paName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, pa))).To(Succeed())
		answer("a")
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			dump()
		}
	})

	It("runs the manager", func() {
		Eventually(func(g Gomega) {
			pods := &corev1.PodList{}
			g.Expect(k8sClient.List(ctx, pods, client.InNamespace(managerNamespace),
				client.MatchingLabels{"app.kubernetes.io/name": "external-ddns"})).To(Succeed())
			g.Expect(pods.Items).To(HaveLen(1))
			g.Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodRunning))
		}).Should(Succeed())
	})

	It("publishes a corroborated address that external-dns consumes", func() {
		answer("a")
		Expect(k8sClient.Create(ctx, &ddnsv1alpha1.PublicAddress{
			ObjectMeta: metav1.ObjectMeta{Name: paName},
			Spec: ddnsv1alpha1.PublicAddressSpec{
				Interval:      metav1.Duration{Duration: 2 * time.Second},
				Corroboration: ddnsv1alpha1.CorroborationSpec{RequiredRounds: 2},
				Observers: []ddnsv1alpha1.ObserverSpec{
					{Name: "gateway", Static: &ddnsv1alpha1.StaticObserver{Address: echoA}},
					{Name: "echo", HTTP: &ddnsv1alpha1.HTTPObserver{URL: fmt.Sprintf("http://echo.%s.svc/", e2eNamespace)}},
				},
				Publish: ddnsv1alpha1.PublishSpec{
					DNSEndpoint: ddnsv1alpha1.ObjectReference{Namespace: e2eNamespace, Name: paName},
					TTL:         60,
					Records:     []string{"*.e2e.example", "vpn.e2e.example"},
				},
			},
		})).To(Succeed())

		Eventually(func(g Gomega) {
			pa := getPA()
			g.Expect(pa.Status.Address).To(Equal(echoA))
			s, _ := condition(pa, ddnsv1alpha1.ConditionPublished)
			g.Expect(s).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
		Expect(targets()).To(ConsistOf(echoA, echoA))
		Eventually(consumed).Should(BeTrue(), "external-dns should stamp observedGeneration")

		m := metrics()
		Expect(m["corroborated"]).To(Equal(1.0))
		Expect(m["public_address_info"]).To(Equal(1.0))
		Expect(m["public_address_observed_timestamp_seconds"]).To(BeNumerically(">", 0))
	})

	It("follows a rotation", func() {
		setStatic(echoB)
		answer("b")
		Eventually(func() string { return getPA().Status.Address }).Should(Equal(echoB))
		Eventually(targets).Should(ConsistOf(echoB, echoB))
		Eventually(consumed).Should(BeTrue())
	})

	It("holds the record when kinds disagree", func() {
		answer("a")
		Eventually(func(g Gomega) {
			s, r := condition(getPA(), ddnsv1alpha1.ConditionCorroborated)
			g.Expect(s).To(Equal(metav1.ConditionFalse))
			g.Expect(r).To(Equal(ddnsv1alpha1.ReasonKindsDisagree))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(getPA().Status.Address).To(Equal(echoB))
			g.Expect(targets()).To(ConsistOf(echoB, echoB))
			g.Expect(metrics()["corroborated"]).To(Equal(0.0))
		}).Should(Succeed())

		answer("b")
		Eventually(func() float64 { return metrics()["corroborated"] }).Should(Equal(1.0))
	})

	It("stops advancing the observed timestamp when the outside view is gone", func() {
		answer("none")
		Eventually(func(g Gomega) {
			s, r := condition(getPA(), ddnsv1alpha1.ConditionCorroborated)
			g.Expect(s).To(Equal(metav1.ConditionFalse))
			g.Expect(r).To(Equal(ddnsv1alpha1.ReasonInsufficientKinds))
		}).Should(Succeed())
		frozen := metrics()["public_address_observed_timestamp_seconds"]
		Consistently(func() float64 { return metrics()["public_address_observed_timestamp_seconds"] }).Should(Equal(frozen))
		Expect(targets()).To(ConsistOf(echoB, echoB))

		answer("b")
		Eventually(func() float64 { return metrics()["public_address_observed_timestamp_seconds"] }).Should(BeNumerically(">", frozen))
	})

	It("garbage-collects the DNSEndpoint with the PublicAddress", func() {
		pa := &ddnsv1alpha1.PublicAddress{ObjectMeta: metav1.ObjectMeta{Name: paName}}
		Expect(k8sClient.Delete(ctx, pa)).To(Succeed())
		Eventually(func() bool {
			_, err := getEndpoint()
			return apierrors.IsNotFound(err)
		}).Should(BeTrue())
	})
})

func dump() {
	pa := &ddnsv1alpha1.PublicAddress{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: paName}, pa); err == nil {
		GinkgoWriter.Printf("PublicAddress status: %+v\n", pa.Status)
	}
	if ep, err := getEndpoint(); err == nil {
		GinkgoWriter.Printf("DNSEndpoint: gen=%d observed=%d spec=%+v\n", ep.Generation, ep.Status.ObservedGeneration, ep.Spec)
	}
	pods := &corev1.PodList{}
	if err := k8sClient.List(ctx, pods, client.InNamespace(managerNamespace)); err == nil {
		for _, p := range pods.Items {
			raw, err := clientset.CoreV1().Pods(managerNamespace).GetLogs(p.Name, &corev1.PodLogOptions{TailLines: ptr(int64(50))}).DoRaw(ctx)
			if err == nil {
				GinkgoWriter.Printf("--- %s logs\n%s\n", p.Name, raw)
			}
		}
	}
}

func ptr[T any](v T) *T { return &v }

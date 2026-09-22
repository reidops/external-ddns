//go:build integration

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

// Package integration runs the reconciler against envtest with scripted
// observers. What envtest cannot do: garbage collection, so ownership
// cascade is asserted in e2e.
package integration

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
	"github.com/reidops/external-ddns/internal/controller"
	"github.com/reidops/external-ddns/internal/dnsendpoint"
	"github.com/reidops/external-ddns/internal/observer"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	script    = &scriptedObservers{answers: map[string]answer{}}
)

type answer struct {
	addr netip.Addr
	err  error
}

// scriptedObservers answers by observer name from what specs have set.
type scriptedObservers struct {
	mu      sync.Mutex
	answers map[string]answer
}

func (s *scriptedObservers) set(name string, addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers[name] = answer{addr: netip.MustParseAddr(addr)}
}

func (s *scriptedObservers) fail(name string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers[name] = answer{err: err}
}

func (s *scriptedObservers) Build(spec ddnsv1alpha1.ObserverSpec) (observer.Observer, error) {
	return &scripted{name: spec.Name, kind: spec.Kind(), s: s}, nil
}

type scripted struct {
	name string
	kind observer.Kind
	s    *scriptedObservers
}

func (o *scripted) Name() string        { return o.name }
func (o *scripted) Kind() observer.Kind { return o.kind }
func (o *scripted) Observe(context.Context) (observer.Observation, error) {
	o.s.mu.Lock()
	a, ok := o.s.answers[o.name]
	o.s.mu.Unlock()
	if !ok {
		return observer.Observation{}, context.DeadlineExceeded
	}
	if a.err != nil {
		return observer.Observation{}, a.err
	}
	return observer.Observation{Address: a.addr, ObservedAt: time.Now()}, nil
}

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Integration Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.Background())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "hack", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		if dir := firstEnvtestBinaryDir(); dir != "" {
			testEnv.BinaryAssetsDirectory = dir
		}
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ddnsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(dnsendpoint.AddToScheme(scheme))

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: ctrl.ObjectMeta{Name: "dns"}})).To(Succeed())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Client: client.Options{Cache: &client.CacheOptions{
			DisableFor: []client.Object{&corev1.Secret{}, &dnsendpoint.DNSEndpoint{}},
		}},
	})
	Expect(err).NotTo(HaveOccurred())

	Expect((&controller.PublicAddressReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		Recorder:       mgr.GetEventRecorder("external-ddns"),
		Observers:      script,
		Writer:         &dnsendpoint.Writer{Client: mgr.GetClient(), Scheme: mgr.GetScheme()},
		ObserveTimeout: time.Second,
	}).SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()

	SetDefaultEventuallyTimeout(20 * time.Second)
	SetDefaultEventuallyPollingInterval(200 * time.Millisecond)
	SetDefaultConsistentlyDuration(3 * time.Second)
	SetDefaultConsistentlyPollingInterval(200 * time.Millisecond)
})

var _ = AfterSuite(func() {
	cancel()
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})

func firstEnvtestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].IsDir() {
			return filepath.Join(basePath, entries[i].Name())
		}
	}
	return ""
}

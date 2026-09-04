//go:build e2e

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

// Package e2e drives the cluster `make dev-up` produces: the chart installed,
// external-dns consuming DNSEndpoints, and two echo fixtures behind one
// Service. It proves what envtest cannot: the image runs, external-dns
// consumes what is written, and ownership garbage-collects.
package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ddnsv1alpha1 "github.com/reidops/external-ddns/api/v1alpha1"
	"github.com/reidops/external-ddns/internal/dnsendpoint"
)

var (
	ctx       context.Context
	k8sClient client.Client
	clientset *kubernetes.Clientset
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "e2e suite")
}

var _ = BeforeSuite(func() {
	ctx = context.Background()

	// Never guess the cluster; `make test-e2e` sets this to Kind's kubeconfig.
	Expect(os.Getenv("KUBECONFIG")).NotTo(BeEmpty(), "KUBECONFIG must point at the e2e cluster")
	cfg := ctrl.GetConfigOrDie()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ddnsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(dnsendpoint.AddToScheme(scheme))

	var err error
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	clientset, err = kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)
	SetDefaultConsistentlyDuration(8 * time.Second)
	SetDefaultConsistentlyPollingInterval(time.Second)
})

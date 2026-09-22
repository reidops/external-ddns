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

package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const labelName = "name"

var (
	observedTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "external_ddns_public_address_observed_timestamp_seconds",
		Help: "Unix time of the last round that corroborated the published address.",
	}, []string{labelName})
	addressInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "external_ddns_public_address_info",
		Help: "1 for the currently published address.",
	}, []string{labelName, "address"})
	corroborated = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "external_ddns_corroborated",
		Help: "1 when the last round corroborated the published address.",
	}, []string{labelName})
	observations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "external_ddns_observation_total",
		Help: "Observer answers by result: ok, no_address, error.",
	}, []string{labelName, "observer", "result"})
)

func init() {
	metrics.Registry.MustRegister(observedTimestamp, addressInfo, corroborated, observations)
}

func forgetMetrics(name string) {
	l := prometheus.Labels{labelName: name}
	observedTimestamp.DeletePartialMatch(l)
	addressInfo.DeletePartialMatch(l)
	corroborated.DeletePartialMatch(l)
	observations.DeletePartialMatch(l)
}

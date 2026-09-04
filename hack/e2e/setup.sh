#!/usr/bin/env bash
# Installs the DNSEndpoint CRD, external-dns (in-memory) and the echo
# fixtures into the current kubecontext.
set -euo pipefail
cd "$(dirname "$0")/../.."

kubectl apply -f hack/crds/dnsendpoints.externaldns.k8s.io.yaml
kubectl apply -f hack/e2e/namespace.yaml
kubectl apply -f hack/e2e/external-dns.yaml
kubectl apply -f hack/e2e/echo.yaml
kubectl -n external-ddns-e2e rollout status deployment/external-dns --timeout=180s
kubectl -n external-ddns-e2e rollout status deployment/echo-a --timeout=180s
kubectl -n external-ddns-e2e rollout status deployment/echo-b --timeout=180s

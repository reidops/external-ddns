# external-ddns

A Kubernetes controller that discovers a site's public IPv4 address,
corroborates it across independent methods, and records it as a `PublicAddress`
resource. Publication is left to
[external-dns](https://github.com/kubernetes-sigs/external-dns): the controller
writes a `DNSEndpoint`, external-dns writes the provider.

Not affiliated with `kubernetes-sigs/external-dns`, despite the name.

## Why

DDNS updaters fuse discovery and publication: one probe, one process, one
provider, and the address never exists anywhere else. Every consumer that needs
it rediscovers it, nothing reconciles the answers, and a dead updater freezes
records silently.

This splits the two halves. Discovery is corroborated across *kinds* of
observer (what the gateway holds vs. what the internet sees) and debounced over
rounds. The result is a cluster object with conditions, events and metrics.
external-dns already does publication well, so it keeps that job.

See [`docs/design.md`](docs/design.md).

## Install

```sh
helm install external-ddns oci://ghcr.io/reidops/charts/external-ddns \
  --namespace external-ddns --create-namespace
```

The chart ships the `PublicAddress` CRD. The `DNSEndpoint` CRD comes from your
external-dns install; run external-dns with `--source=crd`.

```yaml
apiVersion: ddns.reidops.com/v1alpha1
kind: PublicAddress
metadata:
  name: site
spec:
  observers:
    - name: gateway
      unifi:
        url: https://192.0.2.1
        insecureSkipVerify: true
        apiKeySecretRef: {namespace: external-ddns, name: unifi}
    - name: stun
      stun:
        servers: [stun.l.google.com:19302, stun.cloudflare.com:3478]
    - name: echo
      http:
        url: https://api.ipify.org
  publish:
    dnsEndpoint: {namespace: external-ddns, name: site}
    ttl: 300
    records: ["*.example.com", "vpn.example.com"]
```

```
$ kubectl get publicaddress
NAME   ADDRESS       STATUS        AGE
site                 Pending 2/3   2m
site   203.0.113.7   Published     5m
```

Observers: `static`, `unifi` (inside-out); `stun`, `http` (outside-in). At
least one of each kind must agree before anything is published.
`insecureSkipVerify` is in the example because a UniFi controller serves its
own certificate; drop it where the gateway presents one the cluster trusts.

## Develop

```sh
make help
make test              # unit
make test-integration  # envtest
make test-e2e          # kind: chart, external-dns (in-memory), fixtures
make dev-up            # the e2e cluster, left running
make ci                # everything a pull request must pass
```

Release candidates from a working tree:

```sh
make docker-buildx VERSION=0.1.0-rc.1
make helm-push VERSION=0.1.0-rc.1
```

Pull requests are squash-merged; the title must be a conventional commit, and
semantic-release cuts the version from it.

## Licence

Apache-2.0.

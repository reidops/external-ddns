# external-ddns design

Status: v1 design, implemented alongside this document.

## 1. Problem

A site with a dynamic public address needs that address in DNS. Every existing
DDNS updater fuses discovery and publication in one process: the address is
discovered privately, written to a provider, and never exists anywhere else.
Consequences:

- Every other component that needs the address rediscovers it independently.
  Nothing reconciles the answers, so they go stale together and the system
  agrees with itself while being wrong about the world.
- A single probe is trusted. One flapping or captive-portal answer becomes a
  published record.
- A dead updater freezes records at their last value, silently.
- Each updater reimplements record ownership, wildcards, provider APIs.

external-dns already solves publication: providers, credentials, ownership
records, per-record reconciliation. It deliberately does not solve discovery
(`--default-targets` is a static startup value). This project is the missing
half.

## 2. Scope

**Goals**

- Discover the public IPv4 address of the network the controller runs in.
- Corroborate it across independent observation methods before trusting it.
- Publish it as a Kubernetes object other components can read or watch.
- Hand the records to external-dns through its `DNSEndpoint` CRD.
- Make "no longer observing" loud: conditions, events, metrics.

**Non-goals**

- Talking to any DNS provider. external-dns owns that.
- IPv6 (v1 publishes A records only; the model extends to AAAA).
- Port mapping, NAT traversal, anything beyond reading one address.
- Multiple addresses per site.

## 3. Requirements

| ID | Requirement |
|----|-------------|
| R1 | Never publish an address a single observation kind produced. Agreement across kinds (§5) is required. |
| R2 | Never publish a change on one round. A new address must be observed for N consecutive rounds. |
| R3 | On disagreement, hold the last corroborated address and raise a condition. A stale loud record beats a wrong quiet one. |
| R4 | "No public address" (CGNAT, upstream down, every observer failing) is a condition, never a value. |
| R5 | Publication goes only through `DNSEndpoint`; the controller carries no provider credentials. |
| R6 | The time of the last corroborated observation is exposed as a metric so staleness can be alerted on. |
| R7 | Discovery methods are pluggable behind one interface. Publication is not. |
| R8 | Poll interval and record TTL are configured together; recovery time is their sum. |
| R9 | One controller instance per site, leader-elected. |

## 4. Architecture

```
            inside-out observers            outside-in observers
          (what the gateway holds)        (what the internet sees)
          static | unifi | ...            stun | http  | ...
                    \                          /
                     v                        v
                 +------------------------------+
                 |   corroborator (per round)   |
                 |   agree across kinds, N×     |
                 +------------------------------+
                                |
                                v
                 +------------------------------+
                 |  PublicAddress  (cluster CR) |
                 |  .status.address, conditions |
                 +------------------------------+
                        |               |
                        v               v
               +----------------+   metrics, Events
               |  DNSEndpoint   |
               |  (namespaced)  |
               +----------------+
                        |
                        v
                   external-dns  ---->  DNS provider
```

One reconciler, driven by `PublicAddress`. Each reconcile is one observation
round; it requeues after `spec.interval`. State needed between rounds (the
pending candidate and its count) lives in `status`, so a restart resumes
rather than re-debounces from zero.

Packages:

```
api/v1alpha1/        PublicAddress types
internal/observer/   Observer interface + static, unifi, stun, http
internal/corroborate/ pure decision function: observations -> outcome
internal/dnsendpoint/ DNSEndpoint writer (concrete, isolated)
internal/controller/ reconciler wiring the above
```

## 5. Observers

```go
type Kind string // "inside-out" | "outside-in"

type Observation struct {
    Address    netip.Addr
    ObservedAt time.Time
}

type Observer interface {
    Name() string
    Kind() Kind
    Observe(ctx context.Context) (Observation, error)
}
```

An observer returns exactly one address or an error. `ErrNoAddress` is the
typed error for "answered, but there is no public address" (empty WAN lease,
private address from STUN, CGNAT range).

| Name | Kind | Method |
|------|------|--------|
| `static` | inside-out | Value from spec. For sites with a fixed address, and as the assertion half where no gateway API exists. |
| `unifi` | inside-out | UniFi Network API, `GET /proxy/network/api/s/{site}/stat/health`, `wan_ip` of the `wan` subsystem. API key from a Secret. |
| `stun` | outside-in | RFC 5389 Binding request; `XOR-MAPPED-ADDRESS`. Minimal in-tree client, no dependency. Several servers, first answer wins. |
| `http` | outside-in | GET a URL whose body is the caller's address as text. HTTPS in production; plain HTTP exists for fixtures. |

The axis is direction, not vendor. Two observers of the same kind fail
together: two outside-in probes behind the same NAT agree by construction,
and a gateway holding a lease it cannot use is invisible to two inside-out
probes. Cross-kind agreement is the corroboration.

Not observers: DNS-based "whoami" queries (transparent resolver interception
on port 53 makes them return the resolver's address), and anything needing
UPnP/NAT-PMP to be enabled (grants port mapping to the whole LAN). NAT-PMP
and UPnP IGD `GetExternalIPAddress` are candidate inside-out observers for a
later version, off by default.

## 6. Corroboration

Pure function over one round's observations plus previous status.

```
per kind:
  values = successful observations of that kind
  kind value = the one value all agree on, else undecided

if fewer than two kinds decided     -> Uncorroborated
if decided kinds disagree           -> Disagreement(values)
else                                 -> Agreement(address)
```

Applied to status:

```
                         Agreement(a)
   a == status.address  ------------->  refresh observedAt
   a != status.address  ------------->  candidate == a ? count++ : candidate=a, count=1
                                         count >= N ? promote a, clear candidate

   Disagreement          ------------->  hold address, clear candidate,
                                         Corroborated=False/KindsDisagree
   Uncorroborated        ------------->  hold address, clear candidate,
                                         Corroborated=False/InsufficientKinds
   every observer ErrNoAddress -------->  hold address, Observed=False/NoPublicAddress
```

`N` is `spec.corroboration.requiredRounds`, default 3. With a 60 s interval
a rotation is trusted after ~3 min and reaches resolvers after TTL more.

Nothing ever clears `status.address`. It is replaced by a corroborated
successor or it stands. A controller with no corroborated history publishes
nothing; that shows as `Published=False/NoAddress`.

## 7. API

Group `ddns.reidops.com`, version `v1alpha1`, kind `PublicAddress`,
cluster-scoped. One object per site; the name is free.

```yaml
apiVersion: ddns.reidops.com/v1alpha1
kind: PublicAddress
metadata:
  name: site
spec:
  interval: 60s
  corroboration:
    requiredRounds: 3
  observers:
    - name: gateway
      unifi:
        url: https://192.0.2.1
        site: default
        insecureSkipVerify: true
        apiKeySecretRef: {namespace: external-ddns, name: unifi, key: api-key}
    - name: stun
      stun:
        servers: [stun.l.google.com:19302, stun.cloudflare.com:3478]
    - name: echo
      http:
        url: https://api.ipify.org
  publish:
    dnsEndpoint: {namespace: external-ddns, name: site}
    ttl: 300
    records:
      - "*.example.com"
      - "vpn.example.com"
status:
  address: 203.0.113.7
  observedAt: "2026-01-01T00:00:00Z"
  candidate: {address: 203.0.113.8, rounds: 1}
  observations:
    - {name: gateway, kind: inside-out, address: 203.0.113.7, observedAt: ...}
    - {name: stun, kind: outside-in, address: 203.0.113.7, observedAt: ...}
    - {name: echo, kind: outside-in, error: "context deadline exceeded"}
  conditions:
    - {type: Observed,     status: "True"}
    - {type: Corroborated, status: "True"}
    - {type: Published,    status: "True"}
```

Exactly one of `static`, `unifi`, `stun`, `http` per observer (CEL rule).
Observer `kind` is derived from which one is set.

Conditions:

| Type | False reasons |
|------|---------------|
| `Observed` | `NoPublicAddress`, `AllObserversFailed` |
| `Corroborated` | `InsufficientKinds`, `KindsDisagree`, `Pending` (candidate below N) |
| `Published` | `NoAddress`, `WriteFailed` |

Printer columns: `ADDRESS`, `OBSERVED`, `CORROBORATED`, `PUBLISHED`, `AGE`.

## 8. Publication

The writer owns one `DNSEndpoint` at `spec.publish.dnsEndpoint`, one endpoint
per record: `dnsName`, `recordType: A`, `targets: [status.address]`,
`recordTTL: spec.publish.ttl`. Create-or-patch every round, so a deleted
object comes back; owner reference to the `PublicAddress` so deletion cascades
and external-dns removes the records under its own policy.

`DNSEndpoint` is declared as a local Go type covering only the fields written,
registered under `externaldns.k8s.io/v1alpha1`. The upstream CRD manifest is
a test fixture, not shipped; the cluster gets it from its external-dns
install. external-dns must run with `--source=crd` and a namespace or label
filter that admits the object; a dedicated instance with its own
`txt-owner-id` is the expected deployment.

The writer is concrete. One sink exists; an interface would be speculation.

## 9. Observability

Metrics, prefix `external_ddns_`:

| Metric | Type |
|--------|------|
| `public_address_observed_timestamp_seconds{name}` | gauge, last corroborated observation |
| `public_address_info{name,address}` | gauge 1 for the published address |
| `observation_total{name,observer,result}` | counter, `result` in ok/error/no_address |
| `corroborated{name}` | gauge 0/1 |

Events on promotion, disagreement, and publish failure.

Alerts shipped with the chart:

```
absent(external_ddns_public_address_observed_timestamp_seconds)          # controller gone
time() - external_ddns_public_address_observed_timestamp_seconds > 900   # observing, not corroborating
external_ddns_corroborated == 0   for 10m                                # disagreement or shortfall
```

A dead controller produces no series, so absence is the first rule; the age
rule covers a live controller that cannot corroborate. Those alerts are the
reason this is a controller and not a CronJob: a dead process must be a
signal, not a frozen record.

## 10. Deployment

Helm chart `charts/external-ddns`: CRD (rendered on every install/upgrade,
never removed on uninstall), Deployment, RBAC copied verbatim from the
generated role, optional ServiceMonitor and PrometheusRule. Image
`ghcr.io/reidops/external-ddns`, chart `oci://ghcr.io/reidops/charts`.

One replica with leader election. RBAC: `publicaddresses` and status, `dnsendpoints`
in all namespaces, Secrets `get` only (API reader, not cache).

Interval and TTL: worst-case recovery is `interval × N + TTL`. Defaults 60 s,
3, 300 s give ~8 min. Lowering TTL is the lever; polling faster than TTL buys
little.

## 11. Testing

| Layer | Tool | Proves |
|-------|------|--------|
| unit | `go test` | corroboration table, STUN encoding against a loopback responder, UniFi/http parsing against `httptest`, writer output |
| integration | envtest | CRD validation, status subresource, condition transitions, `DNSEndpoint` shape |
| e2e | kind | chart installs; external-dns (in-memory provider, CRD source) consumes the object; rotation, disagreement and staleness observed end to end |

e2e needs the internet only for image pulls: `static` plus an in-cluster echo
fixture are the two kinds, and the suite changes what the fixture answers.

## 12. Decisions

- **Observers are an interface, the sink is not.** Several observers exist on
  day one; one sink does. Same test applied to both.
- **CRD over ConfigMap.** Conditions, printer columns and Events are the
  operator interface during an incident.
- **State in status.** The debounce candidate is observable and survives
  restarts. It is derived state; losing it costs N rounds, never correctness.
- **No provider abstraction.** external-dns's provider and webhook system is
  that abstraction.
- **Rejected: `--default-targets`.** Static at startup; a rotation becomes a
  pod roll.
- **Rejected: consumers discovering their own address.** Multiplies
  independent answers, which is the fault being fixed.

## 13. Open

- Default observers for a fresh install with no gateway API: `static` plus
  outside-in is the only cross-kind pair, and `static` is an assertion. A
  rotation surfaces as a disagreement, which is the intended outcome, but
  self-healing needs a real inside-out observer. NAT-PMP is the candidate.
- Whether `Published` should also verify the record resolves from outside
  (DoH), closing the loop from the far side.

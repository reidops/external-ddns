# CLAUDE.md

Controller that observes a site's public address, corroborates it across
observer kinds, and writes it to a `DNSEndpoint` for external-dns. Read
[docs/design.md](docs/design.md) before changing behaviour; the invariants
there (never publish uncorroborated, never clear the address, no address is a
condition) are load-bearing.

- Everything runs through `make`; CI calls the same targets. `make ci` is the gate.
- Generated files (`config/crd/bases`, `config/rbac/role.yaml`, `zz_generated.*`,
  `charts/external-ddns/generated/`) come from `make manifests generate`. Never edit them.
- `internal/dnsendpoint` declares external-dns's type; it is not ours and produces no CRD.
- Pull requests squash-merge; the title is a conventional commit and drives the release.
- Docs and comments stay brief. Explain decisions, not code.

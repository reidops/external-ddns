# Security

## Reporting a vulnerability

Report privately through GitHub: **Security → Report a vulnerability** on this
repository. It reaches the maintainer without disclosing anything publicly.
Please do not open a public issue for a vulnerability.

Expect an acknowledgement within a week. This is a small project maintained by
one person, so a fix arrives when it arrives, but you will be told where it
stands and credited in the release notes unless you would rather not be.

## What is in scope

The controller, its chart and its container image. Note the two properties
that are deliberate rather than defects:

- **The controller holds no DNS provider credentials.** It writes a
  `DNSEndpoint`; external-dns owns publication and the credentials for it.
- **The metrics endpoint is unauthenticated by default** on the chart's
  install, bound in-cluster and scraped by Prometheus. The kustomize install
  under `config/` wires the authn/authz filter instead.

## Supported versions

The latest release. Fixes go on top of `main` and are released from there; old
minor versions are not patched.

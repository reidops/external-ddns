# Contributing

Issues and pull requests are welcome. This is a small project with one
maintainer; an issue before a large change saves both of us the work.

## The shape of a change

- **`make ci` is the gate.** It runs what the workflow runs: `verify`, `lint`,
  `test`, `test-integration`, `helm-lint`. `make test-e2e` needs Docker and
  brings up a Kind cluster with external-dns in memory.
- **Generated files are never edited by hand.** `config/crd/bases`,
  `config/rbac/role.yaml`, `zz_generated.*` and `charts/external-ddns/generated/`
  come from `make manifests generate`, and `make verify` fails when the tree
  disagrees with them.
- **Behaviour is decided in [`docs/design.md`](docs/design.md).** Its
  invariants are load-bearing: never publish an uncorroborated address, never
  clear a published one, no address is a condition rather than a value. A
  change to any of them belongs in that document first.
- **Comments and docs say why.** What the code does is the code's job.

## Pull requests

Pull requests are squash-merged, so the title becomes the commit subject and
semantic-release reads it to cut the version. It must be a
[conventional commit](https://www.conventionalcommits.org): `feat:` and `fix:`
release, everything else does not. Start the subject lowercase and write it in
the imperative.

A pull request from a fork runs the full CI graph. A branch pushed to this
repository is checked on the push instead, so its pull request shows the title
check alone.

The graph lives in `.github/workflows/test.yml` and a release runs it too,
against the image it is about to publish. Anything added to it therefore gates
releases as well as branches.

## Licence and copyright

Contributions are licensed under Apache-2.0, as
[section 5](LICENSE) of the licence provides. You keep the copyright in what
you write; there is no CLA.

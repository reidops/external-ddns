#!/bin/sh
# Fails unless every place that names the Go toolchain names the same minor
# version. GOTOOLCHAIN=local only catches a toolchain older than go.mod; this
# catches the rest, such as an image built by a compiler the tests never ran.
set -eu
cd "$(dirname "$0")/.."

ref=""
refsrc=""
status=0

# check <source> <raw version>: one version per source, reduced to major.minor
# and compared with the first one read, go.mod's.
check() {
	if [ -z "$2" ]; then
		echo "$1: no Go version found" >&2
		status=1
		return
	fi
	if [ "$(printf '%s\n' "$2" | wc -l)" -ne 1 ]; then
		echo "$1: more than one Go version found: $(printf '%s' "$2" | tr '\n' ' ')" >&2
		status=1
		return
	fi
	minor=$(printf '%s\n' "$2" | sed -nE 's/^([0-9]+\.[0-9]+)([^0-9].*)?$/\1/p')
	if [ -z "$minor" ]; then
		echo "$1: cannot read a Go version from '$2'" >&2
		status=1
		return
	fi
	printf '%-28s %s\n' "$1" "$minor"
	if [ -z "$ref" ]; then
		ref=$minor
		refsrc=$1
	elif [ "$minor" != "$ref" ]; then
		echo "mismatch: $1 has Go $minor, $refsrc has Go $ref" >&2
		status=1
	fi
}

check "go.mod go" \
	"$(sed -nE 's/^go[[:space:]]+([^[:space:]]+).*/\1/p' go.mod)"
if grep -q '^toolchain[[:space:]]' go.mod; then
	check "go.mod toolchain" \
		"$(sed -nE 's/^toolchain[[:space:]]+go([^[:space:]]+).*/\1/p' go.mod)"
fi
check "mise.toml go" \
	"$(sed -nE 's/^go[[:space:]]*=[[:space:]]*"([^"]*)".*/\1/p' mise.toml)"
check ".github/workflows/test.yml" \
	"$(sed -nE 's/^[[:space:]]*container:[[:space:]]*golang:([^[:space:]]+).*/\1/p' .github/workflows/test.yml)"
check "Dockerfile" \
	"$(sed -nE 's/^FROM[[:space:]].*golang:([^[:space:]]+).*/\1/p' Dockerfile)"

if [ "$status" -ne 0 ]; then
	echo "Go versions disagree or cannot be read: go.mod, mise.toml, test.yml's container and the Dockerfile move together." >&2
fi
exit "$status"

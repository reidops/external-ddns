# --platform=$BUILDPLATFORM: compile natively, cross-compile via GOARCH.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ENV GOTOOLCHAIN=local

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o manager ./cmd

FROM gcr.io/distroless/static:nonroot
LABEL org.opencontainers.image.source=https://github.com/reidops/external-ddns
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]

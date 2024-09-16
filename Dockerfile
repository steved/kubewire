FROM --platform=$BUILDPLATFORM cgr.dev/chainguard/go:latest-dev AS builder

WORKDIR /src

ARG ldflags
ARG TARGETOS TARGETARCH

RUN --mount=target=. \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "${ldflags} -extldflags '-static'" -o /out/kubewire .

FROM cgr.dev/chainguard/wolfi-base
WORKDIR /

RUN apk add --no-cache \
      curl \
      iproute2 \
      iptables \
      iputils \
      net-tools \
      wireguard-tools

COPY --from=builder /out/kubewire .

ENTRYPOINT ["/kubewire", "agent"]

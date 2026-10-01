# syntax=docker/dockerfile:1.7
# Multi-stage build: a static binary on a distroless, non-root base.

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/vaanarsena ./cmd/vaanarsena && \
    CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/vaanarsena-agent ./cmd/vaanarsena-agent && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/vsctl ./cmd/vsctl

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.title="VaanarSena" \
      org.opencontainers.image.description="Open source device management for Apple, Windows, Android, ChromeOS and Linux" \
      org.opencontainers.image.source="https://github.com/dmdhrumilmistry/VaanarSena" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/vaanarsena /usr/local/bin/vaanarsena
# vsctl lets CI jobs run the image to apply manifests.
COPY --from=build /out/vsctl /usr/local/bin/vsctl
# The agent binary ships in the image so operators can serve or copy it.
COPY --from=build /out/vaanarsena-agent /usr/local/share/vaanarsena/vaanarsena-agent
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/vaanarsena"]
CMD ["serve"]

FROM docker.io/library/golang:1.27.1-bookworm@sha256:e114385c58e7280d2f56d6cf374219dda33fef8eb77f72e9f290ccae6160bd90 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod GOWORK=off go mod download
COPY . .
ARG VERSION=0.1.0-rc.1
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags="-s -w -buildid= -X main.version=${VERSION}" -o /clo-edge ./cmd/clo-edge

FROM scratch
COPY --from=builder /clo-edge /clo-edge
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.txt /usr/share/doc/clo-edge/
USER 65532:65532
ENTRYPOINT ["/clo-edge"]
LABEL org.opencontainers.image.source="https://github.com/RaveTechnology/clo-edge"
LABEL org.opencontainers.image.licenses="Apache-2.0"

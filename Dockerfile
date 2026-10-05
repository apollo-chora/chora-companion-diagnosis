# syntax=docker/dockerfile:1.6
#
# chora-companion-diagnosis Dockerfile — the companion_diagnosis crew: two
# subscriber-only ADK Go agent binaries (companion_extractor transcriber +
# companion_diagnoser Growth-Edge analyser, ADR-205 WS-2 / ADR-254 D6).
#
# Build context = this repository. Shared Chora modules (chora-adk-common,
# chora-common, chora-contracts) are resolved through the Go module proxy, not
# a workspace. ONE image, TWO binaries; the compose service selects which runs
# via its `command` (chora-companion-extractor -> /app/companion_extractor,
# chora-companion-diagnoser -> /app/companion_diagnoser).

ARG GO_VERSION=1.26.6
ARG ALPINE_VERSION=3.23
ARG CREW_NAME=chora-companion-diagnosis
ARG GIT_SHA=unknown
ARG BUILD_TIME=unknown

############################
# Stage 1 — build
############################
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG CREW_NAME
ARG GIT_SHA
ARG BUILD_TIME

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY . .

RUN go mod download

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64
RUN go build -trimpath -ldflags "-s -w" -o /out/companion_extractor ./cmd/companion_extractor \
 && go build -trimpath -ldflags "-s -w" -o /out/companion_diagnoser ./cmd/companion_diagnoser

############################
# Stage 2 — runtime
############################
FROM gcr.io/distroless/static-debian12:nonroot

ARG CREW_NAME
ARG GIT_SHA
ARG BUILD_TIME

LABEL org.opencontainers.image.title="${CREW_NAME}" \
      org.opencontainers.image.source="https://github.com/apollo-chora/chora-companion-diagnosis" \
      org.opencontainers.image.revision="${GIT_SHA}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.vendor="Chora Platform" \
      org.opencontainers.image.licenses="UNLICENSED" \
      io.chora.crew="${CREW_NAME}" \
      io.chora.git-sha="${GIT_SHA}" \
      io.chora.build-time="${BUILD_TIME}"

WORKDIR /app

COPY --from=builder /out/companion_extractor /app/companion_extractor
COPY --from=builder /out/companion_diagnoser /app/companion_diagnoser

USER nonroot:nonroot
# Default ENTRYPOINT is the diagnoser; the per-member service `command`
# overrides it (the extractor service runs /app/companion_extractor).
# ADR-254 D6: subscriber-only binaries, NO arguments.
ENTRYPOINT ["/app/companion_diagnoser"]

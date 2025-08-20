FROM golang:1.24-alpine AS ignite-builder

# Install dependencies needed for ignite and building
RUN apk add --no-cache \
    libc6-compat \
    curl \
    bash

# Set environment variables
ENV EVNODE_VERSION=v1.0.0-beta.2.0.20250819153439-29b8ddf5a753
ENV IGNITE_VERSION=v29.3.1
ENV IGNITE_EVOLVE_APP_VERSION=main

RUN curl -sSL https://get.ignite.com/cli@${IGNITE_VERSION}! | bash

WORKDIR /workspace

COPY . ./ev-abci

RUN ignite scaffold chain gm --no-module --skip-git --address-prefix gm

WORKDIR /workspace/gm

RUN ignite app install github.com/ignite/apps/evolve@${IGNITE_EVOLVE_APP_VERSION} && \
    ignite evolve add

RUN go mod edit -replace github.com/evstack/ev-node=github.com/evstack/ev-node@${EVNODE_VERSION} && \
    go mod edit -replace github.com/evstack/ev-abci=/workspace/ev-abci && \
    go mod tidy

RUN ignite chain build --skip-proto

# create lightweight runtime image
FROM alpine:latest

RUN apk add --no-cache ca-certificates

# create non-root user
RUN addgroup -g 10001 -S gm && \
    adduser -u 10001 -S gm -G gm

WORKDIR /home/gm

# copy the built binary from the builder stage
COPY --from=ignite-builder /go/bin/gmd /usr/local/bin/gmd

RUN chown -R gm:gm /home/gm
USER gm

# expose common ports
EXPOSE 26657 26656 9090 1317

CMD ["gmd"]

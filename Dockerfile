ARG BASE_IMAGE=ghcr.io/evstack/base-ignite-image:latest
FROM ${BASE_IMAGE} AS ignite-builder

# Set environment variables
ARG EVNODE_VERSION=v1.0.0-beta.2.0.20250818133040-d096a24e7052
ENV EVNODE_VERSION=${EVNODE_VERSION}

WORKDIR /workspace

COPY . ./ev-abci

WORKDIR /workspace/gm

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

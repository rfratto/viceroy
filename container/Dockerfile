# The viceroy worker image can't be built until the base image is built; the
# specific base image to build is chosen by make, choosing between a worker
# that only supports building for Linux (the default), or a worker that
# also supports darwin, freebsd, and windows.
#
# See the README for building instructions.
FROM rfratto/viceroy/base AS environment

RUN  apt-get update      \
  && apt-get install -yq \
      fuse               \
      libsystemd-dev     \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

FROM golang:1.17-bullseye AS builder

COPY dist/*-linux-* /dist/
RUN sh -c '\
  export PLATFORM=$(go env GOOS)-$(go env GOARCH)$(go env GOARM) \
  && cp /dist/viceroyworkerd-$PLATFORM /go/bin/viceroyworkerd \
  && cp /dist/viceroyfs-$PLATFORM /go/bin/viceroyfs \
  '

FROM environment as final
COPY --from=builder /go/bin/viceroyworkerd /usr/bin/viceroyworkerd
COPY --from=builder /go/bin/viceroyfs /usr/bin/viceroyfs
COPY container/viceroycc /usr/bin/viceroycc

ENV LOG_LEVEL=info
ENV VICEROYWORKERD_CHROOT=/mnt/voverlay

CMD /usr/bin/viceroyworkerd \
  -log.level=$LOG_LEVEL \
  -listen-addr=tcp://0.0.0.0:12194 \
  -overlay-listen-addr=0.0.0.0:13613 \
  -overlay-dir=$VICEROYWORKERD_CHROOT

# gRPC servers:
#   viceroyworkerd: 12194
#   viceroyfs:      13613
# HTTP status servers:
#   viceroyworkerd: 8080
#   viceroyfs:      8081
EXPOSE 8080/tcp 8081/tcp 12194/tcp 13613/tcp

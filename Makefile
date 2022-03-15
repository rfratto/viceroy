ROOT=$(shell git rev-parse --show-toplevel)
CONTAINER_NAME ?= rfratto/viceroy:latest
BASE_CONTAINER_NAME ?= rfratto/viceroy/base

GO_PLATFORM=$(shell go env GOOS)-$(shell go env GOARCH)$(shell go env GOARM)

LOCAL_BINARIES  := viceroycc viceroyd
WORKER_BINARIES := viceroyworkerd viceroyfs
BINARIES        := $(LOCAL_BINARIES) $(WORKER_BINARIES)

.PHONY: local-binaries worker-binaries $(BINARIES)
$(BINARIES):
	go build -o dist/$@-$(GO_PLATFORM) ./cmd/$@

.PHONY: container container/base
ifeq ($(shell go env GOOS),linux)
container: container/base $(WORKER_BINARIES)
	docker build -t $(CONTAINER_NAME) -f container/Dockerfile $(ROOT)

ifeq ($(FULL_BASE_IMAGE),)
container/base:
	docker build -t $(BASE_CONTAINER_NAME) -f container/base-linuxonly/Dockerfile container/base-linuxonly
else
container/base:
	docker build -t $(BASE_CONTAINER_NAME) --build-arg OSXCROSS_SDK_URL=${OSXCROSS_SDK_URL} -f container/base-full/Dockerfile container/base-full
endif

else
container:
	GOOS=linux $(MAKE) container
endif

install:
	go install $(foreach binary,$(LOCAL_BINARIES),./cmd/$(binary))

run-viceroyd: container
	go run ./cmd/viceroyd -listen-addr tcp://0.0.0.0:12194 -log.level=debug

run-worker: container
	docker run --env LOG_LEVEL=debug --cap-add=SYS_ADMIN --device /dev/fuse --name test-viceroy-worker \
		--rm -it $(CONTAINER_NAME)

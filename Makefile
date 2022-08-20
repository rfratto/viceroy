ROOT=$(shell git rev-parse --show-toplevel)
CONTAINER_NAME ?= rfratto/viceroy:latest

.PHONY: all
all: build

.PHONY: build
build:
	docker build -t ${CONTAINER_NAME} --build-arg OSXCROSS_SDK_URL=${OSXCROSS_SDK_URL} .

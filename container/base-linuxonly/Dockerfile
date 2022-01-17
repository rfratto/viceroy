# The linuxonly base image only supports cross compiling for Linux.
FROM debian:bullseye-slim

RUN  export HOST=$(dpkg --print-architecture) \
  && dpkg --add-architecture amd64    \
  && dpkg --add-architecture i386     \
  && dpkg --add-architecture armel    \
  && dpkg --add-architecture armhf    \
  && dpkg --add-architecture arm64    \
  && apt-get update                   \
  && apt-get install -yq              \
        clang:$HOST                   \
        crossbuild-essential-amd64    \
        crossbuild-essential-i386     \
        crossbuild-essential-armel    \
        crossbuild-essential-armhf    \
        crossbuild-essential-arm64    \
        libssl-dev                    \
        openssl                       \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

COPY ./viceroycc-linux /usr/bin/viceroycc-linux

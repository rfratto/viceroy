FROM debian:bullseye-slim as environment

RUN  export HOST=$(dpkg --print-architecture) \
  && dpkg --add-architecture amd64    \
  && dpkg --add-architecture i386     \
  && dpkg --add-architecture armel    \
  && dpkg --add-architecture armhf    \
  && dpkg --add-architecture arm64    \
  && dpkg --add-architecture ppc64el  \
  && apt-get update                   \
  && apt-get install -yq              \
        clang:$HOST                   \
        crossbuild-essential-amd64    \
        crossbuild-essential-i386     \
        crossbuild-essential-armel    \
        crossbuild-essential-armhf    \
        crossbuild-essential-arm64    \
        crossbuild-essential-ppc64el  \
        libssl-dev                    \
        openssl                       \
        mingw-w64:$HOST               \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

FROM environment as osxcross

# osxcross-specific deps
RUN  export HOST=$(dpkg --print-architecture) \
  && apt-get update       \
  && apt-get install -yq  \
        cmake:$HOST       \
        curl:$HOST        \
        git:$HOST         \
        liblzma-dev:$HOST \
        libxml2-dev:$HOST \
        llvm-dev:$HOST    \
        make:$HOST        \
        patch:$HOST       \
        tar:$HOST         \
        xz-utils:$HOST    \
        zlib1g-dev:$HOST  \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

ARG OSXCROSS_SDK_URL
ENV OSXCROSS_REV=be2b79f444aa0b43b8695a4fb7b920bf49ecc01c \
    OSXCROSS_SDK_NAME=MacOSX11.1.sdk.tar.xz

ADD ${OSXCROSS_SDK_URL} /tmp/${OSXCROSS_SDK_NAME}

RUN  mkdir -p /tmp/osxcross && cd /tmp/osxcross                                           \
  && curl -sSL "https://codeload.github.com/tpoechtrager/osxcross/tar.gz/${OSXCROSS_REV}" \
      | tar -C /tmp/osxcross --strip=1 -xzf -                                             \
  && mv /tmp/${OSXCROSS_SDK_NAME} tarballs/${OSXCROSS_SDK_NAME}                           \
  && UNATTENDED=yes ./build.sh                                                            \
  && mv target /usr/osxcross

FROM environment

COPY --from=osxcross /usr/osxcross /usr/osxcross
ENV PATH /usr/osxcross/bin:$PATH

COPY ./viceroycc-darwin  /usr/bin/viceroycc-darwin
COPY ./viceroycc-freebsd /usr/bin/viceroycc-freebsd
COPY ./viceroycc-linux   /usr/bin/viceroycc-linux
COPY ./viceroycc-windows /usr/bin/viceroycc-windows

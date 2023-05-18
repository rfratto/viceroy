FROM debian:bullseye-slim as environment

RUN  export HOST=$(dpkg --print-architecture) \
  && dpkg --add-architecture amd64    \
  && dpkg --add-architecture i386     \
  && dpkg --add-architecture armel    \
  && dpkg --add-architecture armhf    \
  && dpkg --add-architecture arm64    \
  && dpkg --add-architecture ppc64el  \
  && dpkg --add-architecture s390x    \
  && apt-get update                   \
  && apt-get install -yq              \
        clang:$HOST                   \
        llvm:$HOST                    \
        lld:$HOST                     \
        crossbuild-essential-amd64    \
        crossbuild-essential-i386     \
        crossbuild-essential-armel    \
        crossbuild-essential-armhf    \
        crossbuild-essential-arm64    \
        crossbuild-essential-ppc64el  \
        crossbuild-essential-s390x    \
        libssl-dev                    \
        openssl                       \
        mingw-w64:$HOST               \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

FROM environment as freebsd

RUN  export HOST=$(dpkg --print-architecture) \
  && apt-get update            \
  && apt-get install -yq       \
        curl:$HOST             \
        libarchive-tools:$HOST \
        xz-utils:$HOST         \
  && apt-get clean && rm -rf /var/lib/apt/lists/*

ENV FREEBSD_AMD64_URL=https://download.freebsd.org/releases/amd64/amd64/ISO-IMAGES/12.4/FreeBSD-12.4-RELEASE-amd64-dvd1.iso.xz \
     FREEBSD_I386_URL=https://download.freebsd.org/releases/i386/i386/ISO-IMAGES/12.4/FreeBSD-12.4-RELEASE-i386-dvd1.iso.xz \
    FREEBSD_ARM64_URL=https://download.freebsd.org/releases/arm64/aarch64/ISO-IMAGES/12.4/FreeBSD-12.4-RELEASE-arm64-aarch64-dvd1.iso.xz

# Unpack amd64 toolchain /usr/freebsd/x86_64-pc-freebsd12
RUN mkdir -p /tmp/freebsd-amd64                                         \
 && curl -fsSL "${FREEBSD_AMD64_URL}" -o /tmp/freebsd-amd64/dvd1.iso.xz \
 && mkdir -p /usr/freebsd/x86_64-pc-freebsd12                           \
 && cd /tmp/freebsd-amd64                                               \
 && xz -d dvd1.iso.xz                                                   \
 && bsdtar -xf dvd1.iso lib usr/include usr/lib                         \
 && mv lib /usr/freebsd/x86_64-pc-freebsd12/                            \
 && mv usr /usr/freebsd/x86_64-pc-freebsd12/                            \
 && rm -rf /tmp/freebsd-amd64

# Unpack i386 toolchain to /usr/freebsd/i386-pc-freebsd12
RUN mkdir -p /tmp/freebsd-i386                                         \
 && curl -fsSL "${FREEBSD_I386_URL}" -o /tmp/freebsd-i386/dvd1.iso.xz  \
 && mkdir -p /usr/freebsd/i386-pc-freebsd12                            \
 && cd /tmp/freebsd-i386                                               \
 && xz -d dvd1.iso.xz                                                  \
 && bsdtar -xf dvd1.iso lib usr/include usr/lib                        \
 && mv lib /usr/freebsd/i386-pc-freebsd12/                             \
 && mv usr /usr/freebsd/i386-pc-freebsd12/                             \
 && rm -rf /tmp/freebsd-i386

# Unpack arm64 toolchain to /usr/freebsd/aarch64-pc-freebsd12
RUN mkdir -p /tmp/freebsd-arm64                                         \
 && curl -fsSL "${FREEBSD_ARM64_URL}" -o /tmp/freebsd-arm64/dvd1.iso.xz \
 && mkdir -p /usr/freebsd/aarch64-pc-freebsd12                          \
 && cd /tmp/freebsd-arm64                                               \
 && xz -d dvd1.iso.xz                                                   \
 && bsdtar -xf dvd1.iso lib usr/include usr/lib                         \
 && mv lib /usr/freebsd/aarch64-pc-freebsd12/                           \
 && mv usr /usr/freebsd/aarch64-pc-freebsd12/                           \
 && rm -rf /tmp/freebsd-arm64

# Create binutils to use for building FreeBSD targets. Specifically, we do this
# to pass to the -B flag of clang to ensure that builds for FreeBSD always use
# ld.lld.
#
# See https://mcilloni.ovh/2021/02/09/cxx-cross-clang/
RUN mkdir -p /usr/freebsd/bin                                \
 && ln -sf /usr/bin/llvm-ar-11      /usr/freebsd/bin/ar      \
 && ln -sf /usr/bin/ld.lld          /usr/freebsd/bin/ld      \
 && ln -sf /usr/bin/llvm-objcopy-11 /usr/freebsd/bin/objcopy \
 && ln -sf /usr/bin/llvm-objdump-11 /usr/freebsd/bin/objdump \
 && ln -sf /usr/bin/llvm-ranlib-11  /usr/freebsd/bin/ranlib  \
 && ln -sf /usr/bin/llvm-strings-11 /usr/freebsd/bin/strings

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
ENV OSXCROSS_REV=50e86ebca7d14372febd0af8cd098705049161b9 \
    OSXCROSS_SDK_NAME=MacOSX12.3.sdk.tar.xz

ADD ${OSXCROSS_SDK_URL} /tmp/${OSXCROSS_SDK_NAME}

RUN  mkdir -p /tmp/osxcross && cd /tmp/osxcross                                           \
  && curl -sSL "https://codeload.github.com/tpoechtrager/osxcross/tar.gz/${OSXCROSS_REV}" \
      | tar -C /tmp/osxcross --strip=1 -xzf -                                             \
  && mv /tmp/${OSXCROSS_SDK_NAME} tarballs/${OSXCROSS_SDK_NAME}                           \
  && UNATTENDED=yes ./build.sh                                                            \
  && mv target /usr/osxcross

FROM environment

COPY --from=freebsd  /usr/freebsd  /usr/freebsd
COPY --from=osxcross /usr/osxcross /usr/osxcross
ENV PATH /usr/osxcross/bin:$PATH

COPY rootfs/viceroycc         /usr/bin/viceroycc
COPY rootfs/viceroycc-darwin  /usr/bin/viceroycc-darwin
COPY rootfs/viceroycc-freebsd /usr/bin/viceroycc-freebsd
COPY rootfs/viceroycc-linux   /usr/bin/viceroycc-linux
COPY rootfs/viceroycc-windows /usr/bin/viceroycc-windows

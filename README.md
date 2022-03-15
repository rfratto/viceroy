<p align="center"><img src="docs/assets/logo_and_name.png" alt="Viceroy logo"></p>

**WORK IN PROGRESS**: Viceroy is a very early work in progress and is subject
to breaking changes. It's also subject to not really working all that well yet.
Submit issues if you find bugs!

Viceroy makes cross-compiling C code fast and easy by proxying build commands
to Docker containers.

It was designed for use with Go applications which need to use Cgo, but it can
be used generally anywhere where you may need to compile C code.

Using Viceroy with Go can be much faster than using a dedicated Docker build
container because Docker is only used to build what the host machine can't.
Preliminary testing of Viceroy showed that cross-compiling
[Grafana Agent](https://github.com/grafana/agent) on an M1 Max MacBook Pro goes
from a 10 minute build to an 80 second one.

## Goals

* Improve iteration speed of Go projects that need Cgo
* Compile C and C++ code for many OS and architectures
* Install C dependencies in build workers
* Usable in standalone mode inside of Docker/Dockerfiles

## TODO

- [ ] Support for crosscompiling to freebsd
- [ ] Worker container images on Dockerhub
- [ ] Installers (Homebrew, etc.)
- [ ] Managing packages installed on workers
- [ ] `pkg-config` support

## Supported Platforms

Viceroy examines the following environment variables to determine which target
system to cross-compile for:

* `VICEROYOS` (or `GOOS`)
* `VICEROYARCH` (or `GOARCH`)
* `VICEROYARM` (or `GOARM`)

These environment variables determine which compiler toolchain to use and some
defaults for environment variables (such as `LD_LIBRARY_PATH`). The environment
variables will default to values appropriate for the worker container's
environment when unspecified.

The following target platforms are currently supported:

|          | linux | darwin | freebsd | windows |
| -------- | ----- | ------ | ------- | ------- |
| amd64    |     x |      x |         |       x |
| 386      |     x |        |         |       x |
| armv5    |     x |        |         |         |
| armv6    |     x |        |         |         |
| armv7    |     x |        |         |         |
| arm64    |     x |      x |         |         |

Support for `darwin` and `freebsd` will come in future commmits.

## Installation

### Host machine

Building from source is currently required. Installers for various systems will
eventually be made available as the project matures.

Go 1.17 is required to install from source. Run `make install` to install the
binaries.

Until pushed images are available on Dockerhub, you must also run
`make FULL_BASE_IMAGE=1 container` to build the worker container locally.

`viceroyd` may be run with `viceroyd -listen-addr tcp://127.0.0.1:12194`. It is
currently left as an exercise to the reader for how to configure this as a
permanent system service.

### Docker Container

Viceroy can be installed on Debian-based systems in "standalone mode," which
enables viceroycc to function completely locally without the need for daemons.
This is useful when you want to unify your build process by also using Viceroy
from within Dockerfiles.

TODO

## Usage

Usage is the same regardless of Viceroy being installed on your host machine or
inside of a Docker container:

1. Install Viceroy
2. `export CC=viceroycc`
3. Compile!

If using Cgo, your build command would look similar to
`CGO_ENABLED=1 CC_FOR_TARGET=viceroycc GOOS=<target> GOARCH=<target> go build <package>`

### Installing packages on worker containers

TODO

## Legal note

OSX/Darwin/Apple builds:
**[Please ensure you have read and understood the Xcode license
   terms before continuing.](https://www.apple.com/legal/sla/docs/xcode.pdf)**

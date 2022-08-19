<p align="center"><img src="docs/assets/logo_and_name.png" alt="Viceroy logo"></p>

Viceroy is a base Docker image which provides a `viceroycc` binary to cross
compile to multiple architectures. It was designed to help ease the burden of
cross compiling Go projects which have C dependencies.

Viceroy is not very useful on its own; it should be extended to add libraries
and other tools needed to build projects.

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
| amd64    |     x |      x |       x |       x |
| 386      |     x |        |       x |       x |
| armv5    |     x |        |         |         |
| armv6    |     x |        |         |         |
| armv7    |     x |        |         |         |
| arm64    |     x |      x |       x |         |
| ppc64le  |     x |        |         |         |

## Usage

1. Use `rfratto/viceroy` as your Docker base image.
2. Set `CC=viceroycc`.
3. Set `VICEROYOS`, `VICEROYARCH`, and `VICEROYARM` as appropriate.
4. Compile!

## Legal note

OSX/Darwin/Apple builds:
**[Please ensure you have read and understood the Xcode license
   terms before continuing.](https://www.apple.com/legal/sla/docs/xcode.pdf)**

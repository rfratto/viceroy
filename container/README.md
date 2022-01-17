# Viceroy worker container

This directory contains code to build Viceroy worker containainers. Viceroy
worker containers expect a `viceroycc` to be installed in the system PATH.

While named the same as the `viceroycc` installed on the host machine, the
`viceroycc` installed on containers determines which compiler toolchain to use
and directly proxies commands to the appropriate toolchain.

The base `rfratto/viceroy/base` image for the worker container must be built
before the worker container itself. This base image can take one of two forms:

* `base-linuxonly`: Cross-compilers for various Linux architectures.
* `base-full`: Cross-compilers for all supported operating systems.

To build the worker container with the `base-linuxonly` base image, run
`make container` from the root of the repository.

To build the worker container with the `base-full` base image, run
`make FULL_BASE_IMAGE=1 container` from the root of the repository.

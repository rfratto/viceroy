## Architecture

Usage of Viceroy centers around `viceroyd`, a Daemon that manages build
containers and proxies connections. You interact with `viceroyd` from three
commands:

* `viceroycc`: Proxy to the worker container's `clang`. Set `CC=viceroycc` to
  use while compiling.
* `viceroypc`: Proxy to the worker container's `pkg-config`. Set
  `PKG_CONFIG=viceroypc` to use while compiling.
* `viceroyctl`: Various commands to manage worker containers.

The worker containers `viceroyd` run two daemons:

* `viceroyworkerd`: The worker daemon that can receive and process build
  requests.
* `viceroyfs`: A FUSE driver which creates a unique viceroy-specific overlay
  combining the container and the host's filesystems.

### `viceroyfs` overlay

Not only do C build commands tend to reference files from the host filesystem,
it also expects that the build output be written to the host filesystem. A
traditional overlay can't be used here, since it treats the lower filesystem as
read-only.

We instead use a special kind of overlay, specific for Viceroy:

* The upper filesystem (the worker container's filesystem) is preferred when
  reading or writing to an existing file that exists on both the upper and
  lower filesystem.

* The lower filesystem (the host filesystem) is preferred when creating new
  files or directories.

These special rules allow Viceroy to work. It is not recommended to use
viceroyfs for anything beyond compiling C code, as specialized use cases aren't
tested and things may break in weird and unexpected ways.

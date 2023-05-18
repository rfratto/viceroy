v0.4.0 (2023-05-18)
-------------------

### Enhancements

* Update macOS SDK to macOS 12.3.
* Update FreeBSD SDK to 12.4.

v0.3.0 (2023-02-27)
-------------------

### Enhancements

* Support s390x for Linux.

v0.2.1 (2022-08-22)
-------------------

### Bug fixes

* Invocations of viceroycc for amd64 Darwin will now work properly.

v0.2.0 (2022-08-20)
-------------------

> **NOTE**: As of this release, per-architecture images are pushed as
> `<release>-<arch>`. These images are artifacts of the build process and can
> be ignored in favor of the `<release>` image, which will be a manifest
> containing all supported architectures.

### Enhancements

* Include ARM64 images.

v0.1.0 (2022-08-19)
-------------------

Initial release!

### Features

* Expose `viceroycc` binary which proxies build commands to one of many C
  compilers depending on values of the `VICEROYOS`/`GOOS`, `VICEROYARCH`/`GOARCH`,
  and `VICEROYARM`/`GOARM` environment variables.

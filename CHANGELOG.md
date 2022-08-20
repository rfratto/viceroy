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

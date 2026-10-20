# Changelog

Notable changes to hrc, in the format of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.1] - 2026-10-20

### Fixed

- A cancelled job on Windows no longer stops hrc or interrupts other jobs: the
  Worker runs in a console of its own, away from the runner's Ctrl+C.

## [0.1.0] - 2026-10-19

Initial release of hrc, a controller for GitHub Actions runners on your own
Linux, macOS and Windows machines: one daemon per host with a declarative
config.

- **Runner scale sets.** Each job gets a fresh just-in-time runner from a scale
  set per configured group; label changes recreate the scale set.
- **Capacity from host resources.** Each runner takes `quota` (CPU and memory),
  the host keeps `reserve`, and hrc runs as many runners as fit.
- **Warm directories.** With `reuseDirs`, runners stay ephemeral but reuse their
  directories, so workspaces and build caches survive between jobs.
- **GitHub App or PAT**, with secrets from a file, an environment variable or
  the config.
- **Graceful shutdown**: running jobs are cancelled and reported before hrc
  exits.
- **Packaged as a service**: systemd or OpenRC, launchd, WinSW 2.x.

Builds for `amd64` and `arm64` on Linux, macOS and Windows, bundling GitHub
Actions runner 2.338.0, with checksums in `SHASUMS256.txt` and build provenance
attestations.

[Unreleased]: https://github.com/mrdimidium/hrc/compare/release-0.1.1...HEAD
[0.1.1]: https://github.com/mrdimidium/hrc/compare/release-0.1.0...release-0.1.1
[0.1.0]: https://github.com/mrdimidium/hrc/releases/tag/release-0.1.0

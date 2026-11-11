Changelog
=========

Notable changes to hrc, in the format of
`Keep a Changelog <https://keepachangelog.com/en/1.1.0/>`__. Versions follow
`Semantic Versioning <https://semver.org/>`__.

`Unreleased`_
-------------

`0.4.0`_ - 2026-11-09
---------------------

Added
~~~~~

- Builtin Ansible collection with install and remove roles for Linux
  (systemd/OpenRC), macOS and Windows. The collection is included in
  ``SHASUMS256``; binary sources can be mirrored or vendored.
- Release metadata: a plain ``VERSION`` file in binary archives, a machine-readable
  ``manifest.json`` with artifact hashes and sizes, and a detached GPG signature
  for ``SHASUMS256``.
- Public release mirror at ``https://pkg.dimidiumlabs.io/hrc``, with immutable
  versioned builds and ``stable`` and ``dev`` channel manifests. GitHub Releases
  carry the same artifacts.

Changed
~~~~~~~

- Tie each slot's workspace and process group to the same lifetime. With
  ``reuseDirs``, workspaces and build servers survive between jobs, but controller
  shutdown now terminates remaining slot processes and removes all slot
  directories, including idle reusable slots. Without ``reuseDirs``, both are
  cleaned up after each runner.
- Replace bundled ``control`` and ``control.ps1`` scripts with explicit installation,
  upgrade and removal instructions in ``docs/installation.rst``, or the Ansible
  roles. Existing installations must use the new procedure for upgrades.
- Move documentation and the changelog to ``docs/``.
- Change the Go module path from ``gilti.dev/hrc`` to ``dimidiumlabs.io/hrc`` and move
  implementation packages from ``pkg/`` to ``internal/``; they are no longer
  available for external import.
- Use Bazel directly for builds, formatting, linting and license checks, without
  mise or the Aspect CLI. Workspace stamping no longer requires system Python.
- Update Go to 1.27.3, ``actions/scaleset`` to 0.5.0 and other build and runtime
  dependencies. Automate dependency and lock-file updates with Renovate.

Fixed
~~~~~

- Use the correct macOS launchd service ID, ``io.dimidiumlabs.hrc``.
- Stabilize Windows process startup and cleanup tests.

`0.3.0`_ - 2026-10-28
---------------------

Changed
~~~~~~~

- Update the bundled GitHub Actions runner from 2.338.0 to 2.338.2 and Node.js
  24 from 24.21.0 to 24.22.1.
- Supports building without a bundled .NET runtime.
- Separate the shared Worker runtime from per-job state:

  - Install ``hrc-worker`` beside ``hrc`` and share read-only resources across jobs
    instead of copying the runtime into each slot. Refresh the runtime on every
    upgrade, even at the same upstream version.

  - Pass Worker settings through ``RUNNER_CONFIG`` instead of writing a ``.runner``
    file.

  - Keep diagnostics under ``dirLogs/jobs/<jobId>``, one directory per job. Logs
    survive temporary workspace removal and slot reuse; retention is managed
    by the administrator.

  - Use each slot as the work root without a nested ``_work`` directory.
  - Existing configs require ``pathWorker`` and ``dirRunner`` updates; old
    installations and workspaces are retained for manual migration.

`0.2.0`_ - 2026-10-27
---------------------

Changed
~~~~~~~

- Build the .NET Worker and its dependencies directly with Bazel, including the
  .NET runtime in the release archive, instead of using upstream runner build
  scripts.
- Download and package Node.js 20 and 24 through Bazel, including musl variants
  on Linux. Ship only the Node.js executable and license from each distribution.
- Move platform-specific Worker process management into a shared process
  package, with tests for process startup and cleanup.

Added
~~~~~

- A changelog used to generate release notes.
- CI builds and tests for release branches.

`0.1.1`_ - 2026-10-20
---------------------

Fixed
~~~~~

- A cancelled job on Windows no longer stops hrc or interrupts other jobs: the
  Worker runs in a console of its own, away from the runner's Ctrl+C.

`0.1.0`_ - 2026-10-19
---------------------

Initial release of hrc, a controller for GitHub Actions runners on your own
Linux, macOS and Windows machines: one daemon per host with a declarative
config.

- **Runner scale sets.** Each job gets a fresh just-in-time runner from a scale
  set per configured group; label changes recreate the scale set.
- **Capacity from host resources.** Each runner takes ``quota`` (CPU and memory),
  the host keeps ``reserve``, and hrc runs as many runners as fit.
- **Warm directories.** With ``reuseDirs``, runners stay ephemeral but reuse their
  directories, so workspaces and build caches survive between jobs.
- **GitHub App or PAT**, with secrets from a file, an environment variable or
  the config.
- **Graceful shutdown**: running jobs are cancelled and reported before hrc
  exits.
- **Packaged as a service**: systemd or OpenRC, launchd, WinSW 2.x.

Builds for ``amd64`` and ``arm64`` on Linux, macOS and Windows, bundling GitHub
Actions runner 2.338.0, with checksums in ``SHASUMS256.txt`` and build provenance
attestations.

.. _Unreleased: https://github.com/mrdimidium/hrc/compare/release-0.4.0...HEAD
.. _0.4.0: https://github.com/mrdimidium/hrc/compare/release-0.3.0...release-0.4.0
.. _0.3.0: https://github.com/mrdimidium/hrc/compare/release-0.2.0...release-0.3.0
.. _0.2.0: https://github.com/mrdimidium/hrc/compare/release-0.1.1...release-0.2.0
.. _0.1.1: https://github.com/mrdimidium/hrc/compare/release-0.1.0...release-0.1.1
.. _0.1.0: https://github.com/mrdimidium/hrc/releases/tag/release-0.1.0

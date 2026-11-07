# Hosted Runner Controller

`hrc` is a small controller for GitHub runners on your own Windows, Linux, and
macOS machines. It automates runner registration, startup, updates, and cleanup
for a small fleet of build hosts.

Each machine is an independent `hrc` host running a daemon with a declarative
config. By design, `hrc` does not form clusters, communicate with other hosts,
or persist controller state. As jobs arrive, the daemon starts ephemeral runners
with a shared, read-only runtime and separate workspaces, then cleans them up
when they finish. Jobs run directly on the host, while installed tools and shared caches
persist between builds.

A runner slot owns a workspace and a process group. With `reuseDirs`, both can
be reused across jobs during one controller run. Graceful controller shutdown
expires all slots, including idle reusable ones: remaining processes are killed
and slot directories are deleted. On all three platforms, process cleanup uses
the inherited `RUNNER_TRACKING_GROUP` environment variable and is best effort.
Windows also retains a Job Object as a crash-cleanup safety net. On Linux and
macOS, cleanup cannot run if the controller is killed with `SIGKILL` or crashes.

Although ephemeral build environments offer many advantages, stateful build
hosts are useful for a personal CI setup: they keep toolchains ready and caches
local, and accommodate Windows and macOS alongside Linux, whereas ARC supports
only Linux. Managing runners on these hosts also makes concurrent builds easier:
each GitHub runner handles one job at a time, requiring multiple instances.

> [!WARNING]
> Shared, stateful runners are suitable only for private use with trusted code,
> since builds share the host's environment and persistent state.

## Installation

See the [installation guide](./installation.md) for macOS, Linux, and Windows,
then [configure and manage the service](./usage.md).

## License

Source code is licensed under [BSD-3-Clause](../LICENSE). Documentation is
licensed under [CC-BY-4.0](../LICENSES/CC-BY-4.0.txt).

The bundled GitHub runner is MIT licensed; its license and .NET dependency
notices are included in the runner payload.

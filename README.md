# Hosted Runner Controller

`hrc` is a small controller for GitHub and Forgejo runners on your own Windows,
Linux, and macOS machines. It automates runner registration, startup, updates,
and cleanup for a small fleet of build hosts.

Each machine is an independent `hrc` host running a daemon with a declarative
config. By design, `hrc` does not form clusters, communicate with other hosts,
or persist controller state. As jobs arrive, the daemon starts ephemeral runners
with separate installations and workspaces, then cleans them up when they
finish. Jobs run directly on the host, while installed tools and shared caches
persist between builds.

Although ephemeral build environments offer many advantages, stateful build
hosts are useful for a personal CI setup: they keep toolchains ready and caches
local, and accommodate Windows and macOS alongside Linux, whereas ARC supports
only Linux. Managing runners on these hosts also makes concurrent builds easier:
each GitHub runner handles one job at a time, requiring multiple instances;
Forgejo supports concurrent jobs, with caveats.

> [!WARNING]
> Shared, stateful runners are suitable only for private use with trusted code,
> since builds share the host's environment and persistent state.

## Installation

Download the archive for your OS/architecture, verify it against
`SHASUMS256.txt`, and extract it into an empty directory.

On Linux, macOS and BSD:

```sh
sudo ./control install
```

Follow the printed instructions to configure `hrc.zon` and start the service.
Linux supports systemd/OpenRC; NetBSD requires pkgsrc's `daemonize`.
BSD hosts support Forgejo only; remove the GitHub example group.

On Windows, download [WinSW 2.x](https://github.com/winsw/winsw/tree/v2)
separately and run PowerShell as Administrator:

```powershell
.\control.ps1 install -WinSWPath C:\Downloads\WinSW-x64.exe
# Configure %ProgramData%\hrc\hrc.zon and provider secrets first.
Start-Service hrc
```

Run `install` from a newer archive to upgrade, or `remove` to uninstall.
Existing configuration is preserved; removal also retains secrets and build data.

## License

Source code is licensed under [BSD-3-Clause](./LICENSE).
Documentation is licensed under [CC-BY-4.0](./LICENSES/CC-BY-4.0.txt).

# Hosted Runner Controller

`hrc` is a small controller for GitHub runners on your own Windows, Linux, and
macOS machines. It automates runner registration, startup, updates, and cleanup
for a small fleet of build hosts.

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
each GitHub runner handles one job at a time, requiring multiple instances.

> [!WARNING]
> Shared, stateful runners are suitable only for private use with trusted code,
> since builds share the host's environment and persistent state.

## Installation

macOS / Linux:

```sh
case $(uname -s) in
    Darwin) os=macos ;;
    Linux) os=linux ;;
esac
case $(uname -m) in
    x86_64) arch=x86_64 ;;
    arm64|aarch64) arch=aarch64 ;;
esac

gh release download --repo mrdimidium/hrc --pattern "hrc-$arch-$os-*.tar.gz" --pattern SHASUMS256.txt
set -- hrc-"$arch"-"$os"-*.tar.gz
archive=$1
shasum -a 256 "$archive"

# Compare the checksum with SHASUMS256.txt.
gh attestation verify "$archive" --repo mrdimidium/hrc

mkdir hrc-install
tar -xzf "$archive" -C hrc-install

cd hrc-install
sudo ./control install

# Configure hrc.zon and provider secrets, then start the service using the printed commands.

# Upgrade: run from a newly extracted release archive.
sudo ./control install

# Uninstall (configuration, secrets, runner versions and build data are retained).
sudo ./control remove
```

Windows (PowerShell as Administrator):

```powershell
$arch = switch ($machine) { AMD64 { 'x86_64' } ARM64 { 'aarch64' } }
$machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }

gh release download --repo mrdimidium/hrc --pattern "hrc-$arch-windows-*.tar.gz" --pattern SHASUMS256.txt

$archive = (Get-Item "hrc-$arch-windows-*.tar.gz").FullName
Get-FileHash $archive -Algorithm SHA256

# Compare the checksum with SHASUMS256.txt.
gh attestation verify $archive --repo mrdimidium/hrc

New-Item -ItemType Directory hrc-install
tar -xzf $archive -C hrc-install

Set-Location hrc-install

# Download WinSW 2.x: https://github.com/winsw/winsw/tree/v2
.\control.ps1 install -WinSWPath C:\Downloads\WinSW-x64.exe

# Configure %ProgramData%\hrc\hrc.zon and provider secrets.
Start-Service hrc

# Upgrade: run from a newly extracted release archive.
.\control.ps1 install -WinSWPath C:\Downloads\WinSW-x64.exe

# Uninstall (configuration, secrets, runner versions and build data are retained).
.\control.ps1 remove
```

## License

Source code is licensed under [BSD-3-Clause](./LICENSE). Documentation is
licensed under [CC-BY-4.0](./LICENSES/CC-BY-4.0.txt).

The bundled GitHub runner is MIT licensed; its license and .NET dependency
notices are included in the runner payload.

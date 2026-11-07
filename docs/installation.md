# Installation

## Contents

- [Manual installation](#manual-installation)
  - [Prerequisites](#prerequisites)
  - [Download](#download)
  - [Installation](#installation-1)
  - [Upgrading](#upgrading)
  - [Removal](#removal)

## Manual installation

Use a fresh working directory and the same shell session. After installation,
follow [configuration and operation](./usage.md) to configure and start the
service. For an existing installation, follow the
[upgrade procedure](#upgrading).

### Prerequisites

Release archives include the Worker's .NET 8 runtime and Node.js 20/24. A
separate .NET runtime, SDK or Node.js installation is not required.

Install Git and any other tools needed by your workflows separately. Docker is
needed only for Linux container jobs, service containers and container actions.

Linux archives require glibc; Alpine/musl hosts are not supported.

**Windows**

Run PowerShell as Administrator. `tar` is included with current Windows
versions. Download WinSW 2.x (x64 example):

```powershell
New-Item -ItemType Directory -Force C:\Downloads | Out-Null
Invoke-WebRequest 'https://github.com/winsw/winsw/releases/download/v2.12.0/WinSW-x64.exe' -OutFile C:\Downloads\WinSW-x64.exe
```

Verify the downloaded WinSW executable before using it.

**macOS**

```sh
# macOS 13.5 or later: no additional packages required.
```

**Ubuntu / Debian**

```sh
sudo apt-get update
sudo apt-get install -y ca-certificates curl tar gzip libc6 libgcc-s1 \
    libstdc++6 libkrb5-3 libgssapi-krb5-2 tzdata zlib1g

. /etc/os-release
if [ "$ID" = ubuntu ] && [ "$VERSION_ID" = 22.04 ]; then
    sudo apt-get install -y libicu70 libssl3 liblttng-ust1
elif [ "$ID" = ubuntu ] && [ "$VERSION_ID" = 24.04 ]; then
    sudo apt-get install -y libicu74 libssl3t64 liblttng-ust1t64
elif [ "$ID" = debian ] && [ "$VERSION_ID" = 12 ]; then
    sudo apt-get install -y libicu72 libssl3 liblttng-ust1
elif [ "$ID" = debian ] && [ "$VERSION_ID" = 13 ]; then
    sudo apt-get install -y libicu76 libssl3t64 liblttng-ust1t64
else
    printf 'Package versions for %s %s are not listed here.\n' "$ID" "$VERSION_ID" >&2
fi
```

**RHEL / AlmaLinux / Rocky Linux / CentOS Stream / Fedora**

```sh
sudo dnf install -y ca-certificates curl tar gzip glibc libgcc libstdc++ \
    krb5-libs tzdata zlib openssl-libs libicu lttng-ust
```

**openSUSE Leap 16**

```sh
sudo zypper -n install ca-certificates curl tar gzip glibc libgcc_s1 \
    libstdc++6 krb5 timezone zlib libopenssl3 libicu lttng-ust
```

### Download

**macOS / Linux / Windows (Git Bash)**

```sh
case $(uname -s) in Darwin) os=darwin ;; Linux) os=linux ;; MINGW*|MSYS*) os=windows ;; esac
case ${PROCESSOR_ARCHITEW6432:-${PROCESSOR_ARCHITECTURE:-$(uname -m)}} in x86_64|AMD64) arch=amd64 ;; arm64|aarch64|ARM64) arch=arm64 ;; esac

version=$(curl -fsL https://github.com/mrdimidium/hrc/releases/latest/download/VERSION)
release="https://github.com/mrdimidium/hrc/releases/download/release-$version"
archive="hrc-$arch-$os-$version.tar.gz"
curl -fLO "$release/$archive" -O "$release/SHASUMS256.txt"
case $os in darwin) shasum -a 256 -c --ignore-missing SHASUMS256.txt ;; *) sha256sum -c --ignore-missing SHASUMS256.txt ;; esac
```

Stop if checksum verification fails.

**Windows (PowerShell as Administrator)**

```powershell
$machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($machine) { AMD64 { 'amd64' } ARM64 { 'arm64' } }

$version = (Invoke-RestMethod 'https://github.com/mrdimidium/hrc/releases/latest/download/VERSION').Trim()
$release = "https://github.com/mrdimidium/hrc/releases/download/release-$version"
$archive = "hrc-$arch-windows-$version.tar.gz"
Invoke-WebRequest "$release/SHASUMS256.txt" -OutFile SHASUMS256.txt
Invoke-WebRequest "$release/$archive" -OutFile $archive
Get-FileHash $archive -Algorithm SHA256
```

Compare the hash with the archive's entry in `SHASUMS256.txt` before continuing.

**Optional: verify build provenance**

With [GitHub CLI](https://cli.github.com/), in either shell:

```sh
gh attestation verify "$archive" -R mrdimidium/hrc
```

**Extract the archive**

macOS / Linux / Git Bash:

```sh
mkdir hrc-install
cd hrc-install
tar -xzf "../$archive"
```

PowerShell:

```powershell
New-Item -ItemType Directory hrc-install
Set-Location hrc-install
tar -xzf "..\$archive"
```

After extraction in Git Bash, switch to PowerShell as Administrator in the
`hrc-install` directory.

### Installation

**Linux**

The preinstall hook prepares the account and directories and stops an existing
systemd/OpenRC service. Other init systems require manual service setup.

```sh
sudo ./scripts/preinstall.sh
sudo install -d -m 0755 -o root -g root /usr/local/bin /usr/local/lib/hrc
sudo install -m 0755 -o root -g root hrc hrc-worker /usr/local/bin/
sudo cp -R runner/. /usr/local/lib/hrc/
sudo chown -R root:root /usr/local/lib/hrc
sudo chmod -R u=rwX,go=rX /usr/local/lib/hrc
```

Install the definition for your init system. For systemd:

```sh
sudo install -m 0644 -o root -g root services/hrc.systemd /etc/systemd/system/hrc.service
sudo systemctl daemon-reload
```

For OpenRC:

```sh
sudo install -m 0755 -o root -g root services/hrc.alpine /etc/init.d/hrc
```

**macOS**

The preinstall hook prepares the account and directories and unloads an existing
launchd service.

```sh
sudo ./scripts/preinstall.sh
sudo install -d -m 0755 -o root -g wheel /usr/local/bin /usr/local/lib/hrc
sudo install -m 0755 -o root -g wheel hrc hrc-worker /usr/local/bin/
sudo cp -R runner/. /usr/local/lib/hrc/
sudo chown -R root:wheel /usr/local/lib/hrc
sudo chmod -R u=rwX,go=rX /usr/local/lib/hrc
sudo install -m 0644 -o root -g wheel services/org.hrc.plist /Library/LaunchDaemons/org.hrc.plist
sudo plutil -lint /Library/LaunchDaemons/org.hrc.plist
```

**Windows (PowerShell as Administrator)**

```powershell
$installRoot = "$env:ProgramFiles\hrc"
$dataRoot = "$env:ProgramData\hrc"
```

Create directories on the first installation:

```powershell
New-Item -ItemType Directory -Path $installRoot, "$installRoot\lib", $dataRoot, "$dataRoot\secrets", "$dataRoot\state", "$dataRoot\logs" | Out-Null
```

The service uses `LocalService`: read/execute access, with write access only to
state and logs. SYSTEM and Administrators retain full access.

```powershell
icacls $installRoot /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-19:(OI)(CI)RX'
icacls $dataRoot /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-19:(OI)(CI)RX'
icacls "$dataRoot\state" /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-19:(OI)(CI)M'
icacls "$dataRoot\logs" /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-19:(OI)(CI)M'
```

Copy the program files and fill the WinSW paths:

```powershell
Copy-Item .\hrc.exe, .\hrc-worker.exe -Destination $installRoot -Force
Copy-Item .\runner -Destination "$installRoot\lib\hrc" -Recurse

[xml] $xml = Get-Content .\services\hrc.winsw.xml -Raw
$xml.service.executable = [string]"$installRoot\hrc.exe"
$xml.service.arguments = $xml.service.arguments.Replace('%SERVICE_CONFIG%', "$dataRoot\hrc.json")
$xml.service.workingdirectory = [string]"$dataRoot\state"
$xml.service.logpath = [string]"$dataRoot\logs"
$xml.Save("$installRoot\hrc-service.xml")
```

Register WinSW on the first installation only:

```powershell
Copy-Item C:\Downloads\WinSW-x64.exe -Destination "$installRoot\hrc-service.exe"
& "$installRoot\hrc-service.exe" install
```

Continue with [configuration and operation](./usage.md).

### Upgrading

[Download, verify, and extract](#download) a new release, then run the commands
below from the extracted directory. Keep existing configuration, secrets and
data; do not repeat the configuration examples in the
[usage guide](./usage.md#configuration).

**Linux**

The preinstall hook stops the systemd/OpenRC service, retaining enablement. For
other init systems, stop the controller yourself. Remove the old runtime:

```sh
sudo ./scripts/preinstall.sh
sudo rm -rf /usr/local/lib/hrc
```

Repeat the file and service definition commands from
[installation](#installation-1), then run `sudo systemctl start hrc.service` or
`sudo rc-service hrc start`.

**macOS**

Unload the service and remove the old runtime:

```sh
sudo ./scripts/preinstall.sh
sudo rm -rf /usr/local/lib/hrc
```

Repeat the file and launchd definition commands from
[installation](#installation-1), then start:

```sh
sudo launchctl bootstrap system /Library/LaunchDaemons/org.hrc.plist
```

**Windows (PowerShell as Administrator)**

Set the installation paths, stop the service and remove the old runtime:

```powershell
$installRoot = "$env:ProgramFiles\hrc"
$dataRoot = "$env:ProgramData\hrc"
& "$installRoot\hrc-service.exe" stopwait
Remove-Item "$installRoot\lib\hrc" -Recurse -Force
```

Repeat the program file and WinSW XML copy commands from
[installation](#installation-1), then `Start-Service hrc`. Do not recreate the
directories or configuration, or register WinSW again.

### Removal

Before deleting installed files, stop and disable or unregister the service.
Configuration, secrets, accounts and build data can be retained.

- **systemd:** `sudo systemctl disable --now hrc.service`. After deleting the
  unit, run `sudo systemctl daemon-reload`.
- **OpenRC:** `sudo rc-service hrc stop` and `sudo rc-update del hrc default`
  (and remove it from any other runlevels).
- **launchd:** `sudo launchctl bootout system/org.hrc` (omit if already
  unloaded) and `sudo launchctl disable system/org.hrc`.
- **Windows (PowerShell as Administrator):**
  `& "$env:ProgramFiles\hrc\hrc-service.exe" stopwait` and
  `& "$env:ProgramFiles\hrc\hrc-service.exe" uninstall`.

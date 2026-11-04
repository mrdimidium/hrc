# Installation

## Contents

- [Manual installation](#manual-installation)
  - [Download](#download)
  - [Installation](#installation-1)
  - [Configuration](#configuration)

## Manual installation

Use a fresh working directory and the same shell session. Start the service
only after configuration. On upgrades, keep the existing configuration and data.

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
On Windows, also download and verify a compatible
[WinSW 2.x executable](https://github.com/winsw/winsw/tree/v2); the example below
uses `C:\Downloads\WinSW-x64.exe`.

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

**Upgrade:** from the new archive, run `sudo ./scripts/preinstall.sh` first.
For other init systems, stop the controller yourself. Then remove the old runtime:

```sh
sudo rm -rf /usr/local/lib/hrc
```

Repeat the file and service definition installation commands, then run
`sudo systemctl start hrc.service` or `sudo rc-service hrc start`.

Before deleting installed files, run `sudo systemctl disable --now hrc.service`,
or `sudo rc-service hrc stop` and `sudo rc-update del hrc default` (and any other
runlevels). After deleting a systemd unit, run `sudo systemctl daemon-reload`.

**macOS**

The preinstall hook prepares the account and directories and unloads an
existing launchd service.

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

Keep state and job directory paths free of spaces.

**Upgrade:** from the new archive, run `sudo ./scripts/preinstall.sh` first,
then remove the old runtime:

```sh
sudo rm -rf /usr/local/lib/hrc
```

Repeat the file and launchd definition installation commands, then start:

```sh
sudo launchctl bootstrap system /Library/LaunchDaemons/org.hrc.plist
```

Before deleting installed files, run `sudo launchctl bootout system/org.hrc`
(omit if already unloaded) and `sudo launchctl disable system/org.hrc`.

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

**Upgrade:** set the same paths, then stop the service and remove the old runtime:

```powershell
& "$installRoot\hrc-service.exe" stopwait
Remove-Item "$installRoot\lib\hrc" -Recurse -Force
```

Repeat the program file and WinSW XML copy commands, then `Start-Service hrc`.
Do not recreate the configuration or register WinSW again.

Before deleting installed files, run `& "$installRoot\hrc-service.exe" stopwait`
and `& "$installRoot\hrc-service.exe" uninstall`. Configuration and data can be
kept on all platforms.

### Configuration

First installation only. Replace the host ID, repository URL, group name, labels
(`ARM64` or `X64`), and source token paths in the examples.

Services do not inherit your terminal's `GITHUB_TOKEN`; these examples use a
file-based PAT. For GitHub App authentication, replace `pat` with `app` and
install its private key with the same permissions.

**macOS / Linux**

```sh
case $(uname -s) in Darwin) config_dir=/usr/local/etc/hrc; log_dir=/Library/Logs/hrc; runner_os=macOS ;; Linux) config_dir=/etc/hrc; log_dir=/var/log/hrc; runner_os=linux ;; esac
case $(uname -m) in x86_64) runner_arch=X64 ;; arm64|aarch64) runner_arch=ARM64 ;; esac

sudo tee "$config_dir/hrc.json" > /dev/null <<EOF
{
  "id": "build-01",
  "pathWorker": "/usr/local/bin/hrc-worker",
  "dirRunner": "/usr/local/lib/hrc",
  "dirBuilds": "jobs",
  "dirLogs": "$log_dir",
  "groups": [
    {
      "url": "https://github.com/your-org/your-repo",
      "name": "hrc-builds",
      "group": "default",
      "labels": ["$runner_os", "$runner_arch"],
      "pat": {"type": "file", "value": "$config_dir/secrets/github-token"}
    }
  ]
}
EOF
sudo chown root:hrc "$config_dir/hrc.json"
sudo chmod 0640 "$config_dir/hrc.json"
sudo install -m 0640 -o root -g hrc /path/to/github-token "$config_dir/secrets/github-token"
```

Start with your init system. **systemd:**

```sh
sudo systemctl enable --now hrc.service
```

**OpenRC:**

```sh
sudo rc-update add hrc default
sudo rc-service hrc start
```

**launchd:**

```sh
sudo launchctl enable system/org.hrc
sudo launchctl bootstrap system /Library/LaunchDaemons/org.hrc.plist
```

**Windows (PowerShell as Administrator)**

Use `$installRoot` and `$dataRoot` from installation. Write UTF-8 without a BOM:

```powershell
$config = @{
    id = 'build-01'
    pathWorker = "$installRoot\hrc-worker.exe"
    dirRunner = "$installRoot\lib\hrc"
    dirBuilds = 'jobs'
    dirLogs = "$dataRoot\logs"
    groups = @(
        @{
            url = 'https://github.com/your-org/your-repo'
            name = 'hrc-builds'
            group = 'default'
            labels = @('windows', 'X64')
            pat = @{ type = 'file'; value = "$dataRoot\secrets\github-token" }
        }
    )
} | ConvertTo-Json -Depth 10
[IO.File]::WriteAllText("$dataRoot\hrc.json", $config + "`n", (New-Object Text.UTF8Encoding($false)))

Copy-Item C:\path\to\github-token -Destination "$dataRoot\secrets\github-token"
icacls "$dataRoot\secrets\github-token" /reset
Start-Service hrc
```

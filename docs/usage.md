# Configuration and operation

## Contents

- [Configuration](#configuration)
- [Service management](#service-management)
- [Logs](#logs)

[Install hrc](./installation.md) before configuring it. Windows examples require
PowerShell as Administrator; set these paths in each new session:

```powershell
$installRoot = "$env:ProgramFiles\hrc"
$dataRoot = "$env:ProgramData\hrc"
```

## Configuration

First installation only. Replace the host ID, repository URL, group name, labels
(`ARM64` or `X64`), and source token paths in the examples.

Services do not inherit your terminal's `GITHUB_TOKEN`; these examples use a
file-based PAT. For GitHub App authentication, replace `pat` with `app` and
install its private key with the same permissions.

On macOS, keep state and job directory paths free of spaces.

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
  "groups": [{
    "url": "https://github.com/your-org/your-repo",
    "name": "hrc-builds",
    "group": "default",
    "labels": ["$runner_os", "$runner_arch"],
    "pat": {"type": "file", "value": "$config_dir/secrets/github-token"}
  }]
}
EOF
sudo chown root:hrc "$config_dir/hrc.json"
sudo chmod 0640 "$config_dir/hrc.json"
sudo install -m 0640 -o root -g hrc /path/to/github-token "$config_dir/secrets/github-token"
```

**Windows**

Write UTF-8 without a BOM:

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
```

## Service management

Start only after configuring providers and secrets. Restart after configuration
changes. Stopping or restarting the controller terminates active jobs and cleans
up their workspaces; it does not wait for jobs to finish.

**systemd**

Enable at boot, start, and check status:

```sh
sudo systemctl enable --now hrc.service
sudo systemctl status hrc.service
```

Stop: `sudo systemctl stop hrc.service`. Restart: `sudo systemctl restart hrc.service`.

**OpenRC**

Enable at boot, start, and check status:

```sh
sudo rc-update add hrc default
sudo rc-service hrc start
sudo rc-service hrc status
```

Stop: `sudo rc-service hrc stop`. Restart: `sudo rc-service hrc restart`.

**launchd**

Enable, load, and check status:

```sh
sudo launchctl enable system/io.dimidiumlabs.hrc
sudo launchctl bootstrap system /Library/LaunchDaemons/io.dimidiumlabs.hrc.plist
sudo launchctl print system/io.dimidiumlabs.hrc
```

Stop: `sudo launchctl bootout system/io.dimidiumlabs.hrc`. To restart, boot out and bootstrap
again. If already unloaded, only bootstrap is needed.

**Windows**

```powershell
Start-Service hrc
Get-Service hrc
```

Stop: `Stop-Service hrc`. Restart: `Restart-Service hrc`.
The installed WinSW service starts manually; use `Set-Service hrc -StartupType Automatic`
if it should start at boot.

## Logs

Controller output:

| Service manager | Location |
| --- | --- |
| systemd | `sudo journalctl -u hrc.service -f` |
| OpenRC | `/var/log/hrc/stdout.log`, `/var/log/hrc/stderr.log` |
| launchd | `/Library/Logs/hrc/stdout.log`, `/Library/Logs/hrc/stderr.log` |
| WinSW | `C:\ProgramData\hrc\logs` |

Job logs are stored under `<dirLogs>/jobs/` as configured in `hrc.json`.

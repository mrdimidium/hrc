# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('install', 'remove', 'help')]
    [string] $Action = 'help',
    [string] $WinSWPath,
    [string] $InstallRoot = "$env:ProgramFiles\hrc",
    [string] $DataRoot = "$env:ProgramData\hrc",
    # Exercise file placement without changing ACLs, accounts or services.
    [switch] $Staging
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($Action -eq 'help') {
    Write-Output 'Usage: .\control.ps1 install -WinSWPath <downloaded WinSW.exe> | remove'
    Write-Output 'Configure hrc.json before starting the hrc service. Removal retains data and configuration.'
    return
}
if (-not $Staging) {
    if ($env:OS -ne 'Windows_NT') { throw 'Windows is required.' }
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Run PowerShell as Administrator.'
    }
}
$InstallRoot = [IO.Path]::GetFullPath($InstallRoot)
$DataRoot = [IO.Path]::GetFullPath($DataRoot)
if ($InstallRoot -eq $DataRoot) { throw 'Program and data directories must be separate.' }
$wrapper = Join-Path $InstallRoot 'hrc-service.exe'
$receipt = Join-Path $InstallRoot 'INSTALL.json'
$config = Join-Path $DataRoot 'hrc.json'

function Invoke-Wrapper([string] $Command) {
    & $wrapper $Command
    if ($LASTEXITCODE -ne 0) { throw "WinSW $Command failed ($LASTEXITCODE)." }
}
function Get-ManagedService {
    $service = Get-Service -Name hrc -ErrorAction SilentlyContinue
    if ($service) {
        $details = Get-CimInstance Win32_Service -Filter "Name='hrc'"
        if ($details.PathName -ne ('"' + $wrapper + '"') -and $details.PathName -ne $wrapper) {
            throw 'The hrc service belongs to another installation.'
        }
    }
    return $service
}
function Set-PrivateDirectory([string] $Path, [string] $ServiceAccess) {
    # Use SIDs so this also works on localized Windows installations.
    & icacls.exe $Path /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' "*S-1-5-19:(OI)(CI)$ServiceAccess"
    if ($LASTEXITCODE -ne 0) { throw "Cannot set permissions on $Path." }
}

if ($Action -eq 'remove') {
    if (-not (Test-Path -LiteralPath $receipt)) { Write-Output 'hrc: no managed installation found'; return }
    $record = Get-Content -LiteralPath $receipt -Raw | ConvertFrom-Json
    if ($record.DataRoot -ne $DataRoot) { throw 'Use the DataRoot recorded in INSTALL.json.' }
    if (-not $Staging) {
        $service = Get-ManagedService
        if ($service) {
            if ($service.Status -ne 'Stopped') { Invoke-Wrapper 'stopwait' }
            $service.Dispose()
            Invoke-Wrapper 'uninstall'
        }
    }
    foreach ($file in @('hrc.exe', 'hrc-worker.exe', 'hrc-service.exe', 'hrc-service.xml', 'TARGET', 'INSTALL.json', 'control.ps1')) {
        $path = Join-Path $InstallRoot $file
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Force }
    }
    $resources = Join-Path $InstallRoot 'lib/hrc'
    if (Test-Path -LiteralPath $resources) { Remove-Item -LiteralPath $resources -Recurse -Force }
    $library = Join-Path $InstallRoot 'lib'
    if ((Test-Path -LiteralPath $library) -and -not (Get-ChildItem -LiteralPath $library -Force)) { Remove-Item -LiteralPath $library }
    if (-not (Get-ChildItem -LiteralPath $InstallRoot -Force)) { Remove-Item -LiteralPath $InstallRoot }
    Write-Output 'hrc: removed; configuration, secrets, build data and logs retained'
    return
}

foreach ($file in @('hrc.exe', 'hrc-worker.exe', 'control.ps1', 'TARGET', 'runner/VERSION', 'runner/LICENSE', 'runner/scripts/hashFiles.js', 'runner/scripts/macosRunInvoker.js', 'runner/externals/node24/bin/node.exe', 'hrc.json', 'services/hrc.winsw.xml')) {
    if (-not (Test-Path -LiteralPath (Join-Path $PSScriptRoot $file) -PathType Leaf)) {
        throw "Incomplete archive: missing $file"
    }
}
$target = (Get-Content -LiteralPath (Join-Path $PSScriptRoot 'TARGET') -Raw).Trim()
if ($target -notmatch '^(amd64|arm64)-windows$') { throw "Unsupported archive target: $target" }
if (-not $Staging) {
    $machine = [Environment]::GetEnvironmentVariable('PROCESSOR_ARCHITEW6432')
    if (-not $machine) { $machine = $env:PROCESSOR_ARCHITECTURE }
    $arch = switch ($machine) { 'AMD64' { 'amd64' }; 'ARM64' { 'arm64' }; default { throw "Unsupported architecture: $machine" } }
    if ($target -ne "$arch-windows") { throw "Archive target $target does not match $arch-windows" }
}
$runnerVersion = (Get-Content -LiteralPath (Join-Path $PSScriptRoot 'runner/VERSION') -Raw).Trim()
if ($runnerVersion -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') { throw 'Invalid bundled runner version.' }
$runner = Join-Path $InstallRoot 'lib/hrc'
if (-not $WinSWPath) { $WinSWPath = $wrapper }
if (-not (Test-Path -LiteralPath $WinSWPath -PathType Leaf)) { throw 'Supply -WinSWPath pointing to a verified WinSW executable.' }
$WinSWPath = [IO.Path]::GetFullPath($WinSWPath)
if ((Test-Path -LiteralPath $InstallRoot) -and -not (Test-Path -LiteralPath $receipt)) {
    throw 'InstallRoot already exists and is not managed by control.'
}
if (Test-Path -LiteralPath $receipt) {
    $record = Get-Content -LiteralPath $receipt -Raw | ConvertFrom-Json
    if ($record.DataRoot -ne $DataRoot) { throw 'Changing DataRoot requires removing the previous installation first.' }
}
$service = $null
if (-not $Staging) {
    $service = Get-ManagedService
    if ($service) {
        if ($service.Status -ne 'Stopped') { Invoke-Wrapper 'stopwait' }
        $service.Dispose()
    }
}
foreach ($directory in @($InstallRoot, $DataRoot, (Join-Path $DataRoot 'secrets'), (Join-Path $DataRoot 'state'), (Join-Path $DataRoot 'logs'))) {
    [void](New-Item -ItemType Directory -Path $directory -Force)
}
if (-not $Staging) {
    Set-PrivateDirectory $InstallRoot 'RX'
    Set-PrivateDirectory $DataRoot 'RX'
    Set-PrivateDirectory (Join-Path $DataRoot 'state') 'M'
    Set-PrivateDirectory (Join-Path $DataRoot 'logs') 'M'
}
@{ DataRoot = $DataRoot } | ConvertTo-Json | Set-Content -LiteralPath $receipt -Encoding UTF8
foreach ($file in @('hrc.exe', 'hrc-worker.exe', 'control.ps1', 'TARGET')) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot $file) -Destination (Join-Path $InstallRoot $file) -Force
}
# Replace the shared resources after stopping the service, even at the same
# upstream version. Keep a rollback copy until the replacement succeeds.
[void](New-Item -ItemType Directory -Path (Split-Path $runner -Parent) -Force)
$temporaryRunner = $runner + '.install-' + [Guid]::NewGuid().ToString('N')
$previousRunner = $temporaryRunner + '.previous'
try {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'runner') -Destination $temporaryRunner -Recurse
    if (Test-Path -LiteralPath $runner) { Move-Item -LiteralPath $runner -Destination $previousRunner }
    try {
        Move-Item -LiteralPath $temporaryRunner -Destination $runner
    } catch {
        if (Test-Path -LiteralPath $previousRunner) { Move-Item -LiteralPath $previousRunner -Destination $runner }
        throw
    }
    if (Test-Path -LiteralPath $previousRunner) { Remove-Item -LiteralPath $previousRunner -Recurse -Force }
} finally {
    if (Test-Path -LiteralPath $temporaryRunner) { Remove-Item -LiteralPath $temporaryRunner -Recurse -Force }
}
if ($WinSWPath -ne $wrapper) { Copy-Item -LiteralPath $WinSWPath -Destination $wrapper -Force }
if (-not (Test-Path -LiteralPath $config)) {
    $settings = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'hrc.json') -Raw | ConvertFrom-Json
    $settings.pathWorker = [string](Join-Path $InstallRoot 'hrc-worker.exe')
    $settings.dirRunner = [string]$runner
    $settings.dirLogs = [string](Join-Path $DataRoot 'logs')
    [IO.File]::WriteAllText($config, ($settings | ConvertTo-Json -Depth 20) + "`n", (New-Object Text.UTF8Encoding($false)))
} elseif (-not (Select-String -LiteralPath $config -Pattern '"pathWorker"\s*:' -Quiet)) {
    Write-Warning "Existing config retained; before starting, set pathWorker to $(Join-Path $InstallRoot 'hrc-worker.exe') and dirRunner to $runner"
}
[xml] $xml = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'services/hrc.winsw.xml') -Raw
# The XML adapter accepts only plain strings, not the PSObject-wrapped strings
# that cmdlets such as Join-Path return in Windows PowerShell 5.1.
$xml.service.executable = [string](Join-Path $InstallRoot 'hrc.exe')
$xml.service.arguments = $xml.service.arguments.Replace('%SERVICE_CONFIG%', $config)
$xml.service.workingdirectory = [string](Join-Path $DataRoot 'state')
$xml.service.logpath = [string](Join-Path $DataRoot 'logs')
$xml.Save((Join-Path $InstallRoot 'hrc-service.xml'))
if (-not $Staging -and -not $service) { Invoke-Wrapper 'install' }
Write-Output "hrc: installed; configure $config, then run: Start-Service hrc"
Write-Output "Removal: & '$InstallRoot\control.ps1' remove -InstallRoot '$InstallRoot' -DataRoot '$DataRoot'"

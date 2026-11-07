# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

# Resolve the root VERSION and Git metadata into stable Bazel stamp values.
# Keep compatible with Windows PowerShell 5.1; pwsh is not required.
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$Root = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))

function Parse-Version {
    param([string] $Value)
    $pattern = '^(?<major>0|[1-9][0-9]*)\.(?<minor>0|[1-9][0-9]*)\.(?<patch>0|[1-9][0-9]*)(-(?<pre>[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?\z'
    $match = [regex]::Match($Value, $pattern)
    if (-not $match.Success -or $match.Groups['pre'].Value -match '(^|\.)0[0-9]+(\.|$)') {
        throw "invalid semantic version: $Value"
    }
    return '{0}.{1}.{2}' -f $match.Groups['major'].Value, $match.Groups['minor'].Value, $match.Groups['patch'].Value
}

function Get-GitValue {
    param([string[]] $Arguments)
    try {
        $value = & git -C $Root @Arguments 2>$null
        if ($LASTEXITCODE -eq 0) {
            return ($value -join "`n").Trim()
        }
    } catch {
        # Git or its metadata may be absent in a source archive.
    }
    return $null
}

function Resolve-Mode {
    param([string] $Base)
    $ref = $env:GITHUB_REF
    if ($ref -and $ref.StartsWith('refs/tags/release-')) {
        if ($ref.Substring('refs/tags/release-'.Length) -cne $Base) {
            throw "release tag $ref does not match version $Base"
        }
        return 'release'
    }
    if ($ref -ceq 'refs/heads/main') { return 'dev' }
    return 'snapshot'
}

function Resolve-Version {
    param([string] $Base, [string] $Mode, [string] $Commit)
    $core = Parse-Version $Base
    if ($Mode -ceq 'release') { return $Base }
    if ($Mode -cne 'dev' -and $Mode -cne 'snapshot') {
        throw 'HRC_BUILD_MODE must be release, dev or snapshot'
    }

    $ref = 'HEAD'
    if ($Mode -ceq 'snapshot') {
        $ref = Get-GitValue @('merge-base', 'HEAD', 'origin/main')
        if (-not $ref) { $ref = 'HEAD' }
    }
    $tag = Get-GitValue @('describe', '--tags', '--abbrev=0', '--match', 'release-[0-9]*', $ref)
    $range = $ref
    if ($tag) { $range = "${tag}..$ref" }
    $cnt = Get-GitValue @('rev-list', '--count', '--first-parent', $range)
    if (-not $cnt) { $cnt = '65536' }
    if ($cnt -notmatch '^[0-9]+$') { throw "invalid Git commit count: $cnt" }
    $pre = 'dev.{0}' -f [long]::Parse($cnt)
    if ($Mode -ceq 'snapshot') { $pre += '.snapshot' }
    return "${core}-${pre}+$Commit"
}

function Main {
    $base = $env:HRC_VERSION
    if (-not $base) {
        $base = [System.IO.File]::ReadAllText((Join-Path $Root 'VERSION')).Trim()
    }
    $mode = $env:HRC_BUILD_MODE
    if (-not $mode) { $mode = Resolve-Mode $base }
    $commit = $env:HRC_COMMIT
    if (-not $commit) { $commit = Get-GitValue @('rev-parse', '--short=12', 'HEAD') }
    if (-not $commit) { $commit = 'unknown' }
    $version = Resolve-Version $base $mode $commit
    Write-Output "STABLE_HRC_VERSION $version"
}

try {
    Main
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}

# Merge latest XTLS/Xray-core into current branch.
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)

$forkPrefix = @(
    'app/httpapi/',
    'contrib/',
    'docs/HTTPAPI.md',
    'docs/httpapi.html',
    'infra/conf/httpapi',
    'infra/conf/xray.go',
    'main/distro/all/all.go',
    'README.md',
    '.gitattributes'
)

function Test-ForkPath([string]$path) {
    foreach ($p in $forkPrefix) {
        if ($path -eq $p.TrimEnd('/') -or $path.StartsWith($p)) { return $true }
    }
    return $false
}

$modified = @(git diff --name-only 2>$null) + @(git diff --name-only --cached 2>$null)
$modified = $modified | Select-Object -Unique
$drift = @($modified | Where-Object { -not (Test-ForkPath $_) })

if ($drift.Count -gt 5) {
    Write-Host ''
    Write-Warning "Found $($drift.Count) modified files outside fork paths (first 10):"
    $drift | Select-Object -First 10 | ForEach-Object { Write-Host "  $_" }
    Write-Host ''
    Write-Host 'Run .\contrib\clean-upstream-drift.ps1 before merge to avoid conflicts.'
    $ans = Read-Host 'Continue merge anyway? [y/N]'
    if ($ans -notmatch '^[yY]') { exit 1 }
}

if (-not (git remote | Select-String -Pattern '^upstream$')) {
    git remote add upstream https://github.com/XTLS/Xray-core.git
}

Write-Host 'Fetching upstream...'
git fetch upstream

$branch = (git branch --show-current)
Write-Host "Merging upstream/main into $branch ..."
git merge upstream/main

Write-Host ''
Write-Host 'If conflicts occur, see contrib/MODIFICATIONS.md'
Write-Host 'Only 2 upstream files should need manual fix:'
Write-Host '  - main/distro/all/all.go  (httpapi import)'
Write-Host '  - infra/conf/xray.go      (HttpAPI field + Override + Build)'

# Merge latest XTLS/Xray-core into current branch.
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)

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
Write-Host 'Only 2 upstream files should need manual fix: main/distro/all/all.go and infra/conf/xray.go'

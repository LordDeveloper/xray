# Reset line-ending / accidental edits on upstream files.
# Keeps fork-owned paths intact.
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)

$keep = @(
    'app/httpapi',
    'contrib',
    'docs',
    'infra/conf/httpapi.go',
    'infra/conf/httpapi_bridge.go',
    'infra/conf/httpapi_apply.go',
    'infra/conf/httpapi_export.go',
    'infra/conf/httpapi_config_patch.go',
    'common/userconn',
    'app/dispatcher/default.go',
    'app/router/router.go',
    'infra/conf/xray.go',
    'main/distro/all/all.go',
    'README.md',
    '.gitattributes',
    'config.json'
)

Write-Host 'Saving fork paths to temporary stash...'
git stash push -u -m 'fork-httpapi-keep' -- @keep

Write-Host 'Resetting all other tracked changes to HEAD...'
git checkout -- .
git clean -fd --exclude=xray.exe --exclude=xray

Write-Host 'Restoring fork paths...'
git stash pop

Write-Host 'Done. Run: git status'
Write-Host 'Expected: only fork paths under app/httpapi, infra/conf/httpapi*, docs, contrib, xray.go, all.go'

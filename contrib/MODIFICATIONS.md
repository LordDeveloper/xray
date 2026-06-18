# Fork touchpoints (upstream merge checklist)

Custom HTTP API is implemented as a **native Xray app** (same pattern as `metrics`).  
`main/run.go` is **not** modified.

## New / fork-owned paths (no upstream conflict)

```
app/httpapi/                    # app handler, proto, apiserver
infra/conf/httpapi.go             # JSON config + Build()
infra/conf/httpapi_bridge.go      # config builders for apiserver (init hook)
contrib/
docs/
```

## Upstream files to re-apply after merge

| File | Change |
|------|--------|
| `main/distro/all/all.go` | Add: `_ "github.com/xtls/xray-core/app/httpapi"` |
| `infra/conf/xray.go` | `HttpAPI *HTTPAPIConfig` on `Config`, `Override()`, and `Build()` block after `Metrics` |

### `main/distro/all/all.go`

```go
_ "github.com/xtls/xray-core/app/httpapi"
```

(place with other optional apps, e.g. after `app/metrics`)

### `infra/conf/xray.go`

Struct field (with other top-level config blocks):

```go
HttpAPI *HTTPAPIConfig `json:"httpapi"`
```

In `Override()`:

```go
if o.HttpAPI != nil {
    c.HttpAPI = o.HttpAPI
}
```

In `Build()` (after metrics):

```go
if c.HttpAPI != nil {
    httpapiConf, err := c.HttpAPI.Build()
    if err != nil {
        return nil, errors.New("failed to build httpapi configuration").Base(err)
    }
    config.App = append(config.App, serial.ToTypedMessage(httpapiConf))
}
```

## Config example

```json
{
  "httpapi": {
    "listen": "127.0.0.1:8080",
    "username": "admin",
    "password": "secret",
    "config_path": "/etc/xray/config.json"
  }
}
```

`config_path` is optional (default: platform config path). Used by `POST /api/config/import` when `path` form field is empty.

## Git workflow

```bash
git remote add upstream https://github.com/XTLS/Xray-core.git   # once
git fetch upstream
git merge upstream/main
# fix only the 2 files above if conflict
```

## Clean accidental line-ending drift (Windows)

If `git status` shows hundreds of modified files you did not edit:

```powershell
.\contrib\clean-upstream-drift.ps1
```

Then commit only fork files on branch `fork-httpapi`.

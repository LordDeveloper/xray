# Fork touchpoints (upstream merge checklist)

Custom HTTP API is a **native Xray app** (same pattern as `metrics`).  
**Do not modify** `main/run.go`, `app/proxyman`, or other upstream packages for HTTP API-only features.

```
app/httpapi/                          # app, proto, bootstrap
app/httpapi/apiserver/                # HTTP server, handlers, docs UI, xray_api_local.go
common/userconn/                      # track + kick live user TCP sessions
infra/conf/httpapi.go                 # JSON config + Build()
infra/conf/httpapi_bridge.go          # ConfigBridge init (no import cycle)
infra/conf/httpapi_apply.go           # hot-reload after config import
infra/conf/httpapi_export.go          # runtime → conf helpers (reference; not used for auto-save)
infra/conf/httpapi_config_patch.go    # validate request JSON + patch config file
contrib/                              # this checklist + merge scripts
docs/HTTPAPI.md                       # API reference
docs/httpapi.html                     # optional static docs
config.json                           # local example (optional, not committed)
```

**Rule:** If you add a feature, create or extend files under the paths above — not random upstream files.

---

## Upstream files — re-apply after merge

| File | What to keep from the fork |
|------|----------------------------|
| `main/distro/all/all.go` | Blank import: `_ "github.com/xtls/xray-core/app/httpapi"` |
| `infra/conf/xray.go` | `HttpAPI` field, `Override()`, `Build()` block |
| `app/dispatcher/default.go` | `trackUserSession` + `userconn.Track` in `getLink` / `WrapLink` |
| `app/router/router.go` | `RemoveRuleAt`, stricter `RemoveRule` (error if tag missing) |

### `main/distro/all/all.go`

Add one line with other optional apps (e.g. after `app/metrics`):

```go
_ "github.com/xtls/xray-core/app/httpapi"
```

### `infra/conf/xray.go`

**1. Struct field** (with `Metrics`, `API`, …):

```go
HttpAPI *HTTPAPIConfig `json:"httpapi"`
```

**2. `Override()`:**

```go
if o.HttpAPI != nil {
    c.HttpAPI = o.HttpAPI
}
```

**3. `Build()`** (after metrics block):

```go
if c.HttpAPI != nil {
    httpapiConf, err := c.HttpAPI.Build()
    if err != nil {
        return nil, errors.New("failed to build httpapi configuration").Base(err)
    }
    config.App = append(config.App, serial.ToTypedMessage(httpapiConf))
}
```

When upstream changes `xray.go` / `all.go`, resolve conflicts by **keeping upstream changes** and **re-inserting only the blocks above**.

---

## Config example

```json
{
  "httpapi": {
    "listen": "127.0.0.1:8080",
    "username": "admin",
    "password": "secret",
    "config_path": "./config.json"
  }
}
```

- `config_path` — optional; used by import/persist when form `path` is empty.
- `username` / `password` — optional Basic Auth (both must be non-empty to enable).

---

## Git workflow (pull without conflict)

### 1. Before pull — remove accidental drift

If `git status` shows hundreds of modified files you did not edit (common on Windows / CRLF):

```powershell
.\contrib\clean-upstream-drift.ps1
git status   # should show only app/httpapi, infra/conf/httpapi*, docs, contrib, xray.go, all.go
```

### 2. Merge upstream

```powershell
.\contrib\sync-upstream.ps1
```

Or manually:

```bash
git fetch upstream
git merge upstream/main
```

### 3. After merge — fix conflicts (if any)

Only expect conflicts in:

- `main/distro/all/all.go` → re-add the `httpapi` import line
- `infra/conf/xray.go` → re-add `HttpAPI` field + `Override` + `Build` blocks

Then:

```bash
go build ./main
```

### 4. Commit fork changes on branch `fork-httpapi`

Keep fork commits separate from upstream merges when possible:

```bash
git checkout fork-httpapi
# ... work only in fork-owned paths ...
git add app/httpapi infra/conf/httpapi*.go docs contrib
git commit -m "httpapi: ..."
```

---

## Architecture (why pull stays clean)

```
config.json ──► infra/conf/xray.go (2 touchpoints only)
                      │
                      ▼
              app/httpapi (Xray app)
                      │
                      ▼
              app/httpapi/apiserver ──bridge──► infra/conf/httpapi_*.go
                      │
         xray_api_local.go (same protobuf ops as main/commands/all/api, in-process)
                      │
                      ▼
              core.Instance (inbound/outbound/router/stats)
```

**Runtime mutations** use protobuf types from upstream (`AddUserOperation`, `AddInboundRequest`, `AddRuleRequest`, …) via `xray_api_local.go` — the same messages as `xray api` CLI, but **without** gRPC dial and **without** editing `app/proxyman` or other upstream packages.

**Do not add** files under `app/proxyman/`, `app/router/` (except using existing exported helpers like `router/command.NewRoutingServer`), or `main/` for HTTP API features.

`apiserver` must **not** import `infra/conf` directly (import cycle). All conf builders/export/persist register via `ConfigBridge` in `httpapi_bridge.go` / `init()` hooks.

---

## Line endings

`.gitattributes` forces LF for `*.go`, `*.md`, `*.json`. After clone on Windows:

```bash
git config core.autocrlf input
```

---

## Quick conflict checklist

| After `git merge upstream/main` | Action |
|---------------------------------|--------|
| Conflict in `all.go` | Add `_ "github.com/xtls/xray-core/app/httpapi"` |
| Conflict in `xray.go` | Restore `HttpAPI` field + Override + Build |
| Conflict in `app/httpapi/*` | Keep **your** version (fork-owned) |
| Conflict elsewhere | Usually accidental edit — prefer **upstream** version |

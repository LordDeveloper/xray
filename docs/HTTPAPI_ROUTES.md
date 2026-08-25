# Xray HTTP API — Routes Reference (for Agent & Panel)

مراجع مصرف‌کنندگان:

| پروژه | مسیر | نقش |
|--------|------|-----|
| **Agent** | `C:\developments\php\NetinjaBot\cores\agent` | کلاینت مستقیم این روت‌ها (`xray_http.py`) |
| **Panel** | `C:\developments\php\NetinjaBot\` | معمولاً از طریق Agent (`node_api`)؛ هسته native `app/Vpn/Cores/Xray` هم مستقیم وصل می‌شود |

جزئیات کامل narrative: [`HTTPAPI.md`](./HTTPAPI.md) · Docs زنده روی نود: `GET /docs/`  
نسخه فورک هدف: **v1.0.2+** (مثلاً `v1.0.3`) — `LordDeveloper/xray`

Auth: HTTP Basic وقتی `httpapi.username` و `httpapi.password` هر دو ست باشند.

---

## جریان پیشنهادی provision (هر دو پروژه)

```
1) POST /api/inbounds/edit          preserve_clients=true   ← فقط فرمت (port/stream/TLS/…)
2) POST /api/inbounds/users/upsert  chunk 100–200           ← همگام کاربران
3) POST /api/rules/replace          یک request              ← ریجن / کل rules
```

این سه مرحله جایگزین یک call سنگین است که پشت reverse-proxy به **502** می‌خورد.

---

## ۱) Logger

| Method | Path | Body / Query | Response |
|--------|------|--------------|----------|
| `POST` | `/api/logger/restart` | — | `{ "status": "ok" }` |

---

## ۲) Stats

| Method | Path | Query | Response (خلاصه) |
|--------|------|-------|------------------|
| `GET` | `/api/stats` | `name`, `reset` | `{ "stat": { "name", "value" } }` |
| `GET` | `/api/stats/query` | `pattern`, `reset`, `grouped`, `online_only` | `{ "stat": [...] }` یا grouped |
| `GET` | `/api/stats/sys` | — | uptime, mem, goroutines, … |
| `GET` | `/api/stats/online` | `email` | تعداد آنلاین یک کاربر |
| `GET` | `/api/stats/online/iplist` | `email` | `{ "name", "ips": { ip: lastSeen } }` |
| `GET` | `/api/stats/online/users` | — | `{ "users": ["email", …] }` |
| `GET` | `/api/stats/online/all` | `email` (تکرارپذیر، اختیاری) | `{ "users": [{ "email", "ips" }] }` |
| `GET` | `/api/stats/online/traffic` | `reset` | ترافیک uplink/downlink کاربران آنلاین |

---

## ۳) Inbounds

### `POST /api/inbounds/add`

```json
{ "inbounds": [ { "tag", "protocol", "listen", "port", "settings", "streamSettings", … } ] }
```

→ `{ "status": "ok" }`

### `POST /api/inbounds/edit` ★ فرمت / provision

| فیلد | پیش‌فرض | معنی |
|------|---------|------|
| `preserve_clients` | `false` | `true` = کلاینت‌ها حفظ؛ فقط متا آپدیت. `false`/حذف = replace کامل قدیمی |

```json
{
  "preserve_clients": true,
  "inbounds": [{
    "tag": "inbound-123",
    "listen": "0.0.0.0",
    "port": 443,
    "protocol": "vless",
    "streamSettings": { "network": "ws", "wsSettings": { "path": "/ws" } },
    "sniffing": { "enabled": true, "destOverride": ["http", "tls"] },
    "allocate": {},
    "settings": {
      "decryption": "none",
      "fallbacks": []
    }
  }]
}
```

با `preserve_clients=true`:

- `settings.clients` / `users` در request **نادیده** گرفته می‌شوند
- clients روی runtime و `config.json` حفظ می‌شوند
- فقط: listen / port / protocol / streamSettings / sniffing / allocate و فیلدهای غیرکاربری settings

**Response:**

```json
{
  "status": "ok",
  "inbounds": [{ "tag": "inbound-123", "clients_count": 5000 }]
}
```

با `preserve_clients=false`: `{ "status": "ok" }` (رفتار قدیمی remove+add)

**Agent:** `XrayHttpClient.edit_inbounds(..., preserve_clients=True)`  
**Panel (native):** `InboundApi::edit` باید `preserve_clients` را پاس بدهد  
**Panel (node_api):** `updateInbound` / `refreshInbound` با `preserve_clients=true`

### `POST /api/inbounds/remove`

```json
{ "tags": ["tag1", "tag2"] }
```

### `GET /api/inbounds/list`

| Query | معنی |
|-------|------|
| `tags_only=1` یا `isOnlyTags=true` | فقط تگ‌ها |

---

## ۴) Inbound Users ★ sync کاربران

### قوانین مشترک add / edit / upsert / remove

| قانون | مقدار |
|--------|--------|
| سقف در هر request | **200** client یا email |
| بیشتر از 200 | **413** `payload_too_large` |
| خطای یک user | کل batch fail نمی‌شود مگر `atomic=true` |
| با `atomic=true` روی add/upsert | در صورت خطا، users اضافه‌شده در همان request rollback می‌شوند |

**Response مشترک:**

```json
{
  "succeeded": 190,
  "failed": 10,
  "errors": [{ "email": "bad@example.com", "message": "..." }],
  "added_users": 190
}
```

Aliasهای سازگاری: `added_users` / `updated_users` / `removed_users` (+ `dropped_connections` برای remove).

### `POST /api/inbounds/users/add`

```json
{
  "atomic": false,
  "inbounds": [{
    "tag": "vless-in",
    "settings": {
      "clients": [
        { "id": "uuid", "email": "user@example.com" }
      ]
    }
  }]
}
```

### `POST /api/inbounds/users/edit`

همان شکل add — به‌روزرسانی با `email`.

### `POST /api/inbounds/users/upsert` ★ sync پنل

اگر email وجود داشت → edit، وگرنه → add.  
**Chunk پیشنهادی: 100–200.**

### `POST /api/inbounds/users/remove`

```json
{
  "atomic": false,
  "tag": "vless-in",
  "emails": ["a@x.com", "b@x.com"]
}
```

→ شامل `removed_users`, `dropped_connections`.

### `GET /api/inbounds/users`

| Query | لازم |
|-------|------|
| `tag` | بله |
| `email` | اختیاری (یک کاربر) |

→ `{ "users": [ … ] }`

### `GET /api/inbounds/users/count?tag=`

→ `{ "count": N }`

**Agent:** `add_users` / `edit_users` / `upsert_users` / `remove_users` + batch API به‌سمت پنل  
**Panel:** از Agent `.../clients/batch` یا native `UserApi` با upsert و partial success

---

## ۵) Outbounds

| Method | Path | Body |
|--------|------|------|
| `POST` | `/api/outbounds/add` | `{ "outbounds": [ … ] }` |
| `POST` | `/api/outbounds/edit` | `{ "outbounds": [ … ] }` (tag موجود) |
| `POST` | `/api/outbounds/remove` | `{ "tags": [ … ] }` |
| `GET` | `/api/outbounds/list` | `?tags_only=1` |

→ mutations: `{ "status": "ok" }`

---

## ۶) Routing Rules

### `POST /api/rules/add`

| Query | پیش‌فرض |
|-------|---------|
| `should_append` | `false` (prepend). `true`/`1` = append |

```json
{ "routing": { "rules": [ { "type":"field", "ruleTag":"r1", "outboundTag":"direct", … } ] } }
```

### `POST /api/rules/edit`

```json
{
  "rule_tag": "my-rule",
  "rule": { "type":"field", "ruleTag":"my-rule", "outboundTag":"direct", … }
}
```

(`routing` با یک rule هم قبول است.)

### `POST /api/rules/replace` ★ ریجن / کل rules

جایگزینی اتمیک کل `routing.rules` در runtime + `config.json`.

```json
{
  "rules": [
    {
      "type": "field",
      "ruleTag": "node:1",
      "outboundTag": "proxy",
      "domain": ["geosite:google"]
    }
  ],
  "domainStrategy": "AsIs",
  "domainMatcher": "hybrid"
}
```

یا:

```json
{ "routing": { "rules": [ … ], "domainStrategy": "AsIs" } }
```

- اگر `balancers` در body نباشد، balancers موجود حفظ می‌شوند  
→ `{ "status": "ok", "count": N }`

**Agent:** `replace_rules` → این روت  
**Panel:** یک بار از طریق Agent `PUT .../routing/rules` (نه حلقه remove+add)

### `POST /api/rules/remove`

```json
{
  "rule_tags": ["a", "b"],
  "ruleTags": [],
  "tags": [],
  "indices": []
}
```

→ `{ "removed": N, "warnings": [...]? }`

### `GET /api/rules/list`

→ `{ "rules": [ … ] }` (فرمت config یا خلاصه runtime)

---

## ۷) Balancer / Source IP / Config / Docs

| Method | Path | Body / Query |
|--------|------|--------------|
| `GET` | `/api/balancer/info` | `?tag=` |
| `POST` | `/api/balancer/override` | `{ "balancer_tag", "target" }` |
| `POST` | `/api/sourceip/block` | `{ "outbound", "source_ips", "inbound"?, "rule_tag"?, "reset"? }` |
| `POST` | `/api/config/import` | multipart: `file` (الزامی), `path`? |
| `GET` | `/docs/` | UI تعاملی (بدون Basic روی خود صفحه docs) |

---

## ۸) کدهای خطا (مفید برای Agent/Panel)

| HTTP | معنی رایج |
|------|-----------|
| `400` | validation / bad JSON / atomic fail |
| `401` | unauthorized |
| `404` | inbound/rule/stat not found |
| `413` | بیش از 200 user در یک request |
| `500` | خطای داخلی؛ گاهی `runtime_applied=true` + `code=config_save_failed` یعنی runtime اعمال شد ولی ذخیره فایل شکست |

Body خطا معمولاً:

```json
{
  "error": "...",
  "code": "validation_failed|not_found|payload_too_large|…",
  "details": ["..."],
  "runtime_applied": true
}
```

---

## ۹) نقشه مسئولیت پروژه‌ها

| کار | Agent (`cores/agent`) | Panel (`NetinjaBot`) |
|-----|----------------------|----------------------|
| صدا زدن مستقیم `/api/inbounds/edit?preserve…` | بله (`xray_http.py`) | فقط اگر core=`xray` native؛ وگرنه از Agent |
| chunk users upsert ≤200 | بله + API پایدار `/api/v1/.../clients/batch` | فراخوانی batch از NodeApi |
| `rules/replace` یک‌بار | بله | provision ریجن → یک replace از Agent |
| جلوگیری از 502 روی «بروزرسانی نود» | timeout معقول + عدم wipe | جریان سه‌مرحله‌ای؛ نـفرستادن هزاران client در edit فرمت |
| docs/OpenAPI Agent | هم‌تراز این فایل | — |

---

## ۱۰) چک‌لیست Acceptance سریع

1. inbound با چند هزار client: `edit` + `preserve_clients=true` فقط port/ws → `clients_count` ثابت  
2. `users/upsert` با 200 + چند خطای جزئی → `succeeded`/`failed`؛ بقیه wipe نشوند  
3. `rules/replace` با لیست بزرگ → یک request؛ بدون 502 سمت پنل  
4. add/remove/list قبلی سبز بمانند  

---

*این فایل خلاصه قرارداد برای یکپارچه‌سازی است. برای مثال curl و توضیح فیلدبه‌فیلد به [`HTTPAPI.md`](./HTTPAPI.md) مراجعه کنید.*

# HTTP API Reference

The HTTP API provides a REST/JSON interface to control and inspect a running Xray instance. It exposes the same capabilities as the `xray api` CLI commands and is intended for automation, dashboards, and third-party integrations.

---

## Table of Contents

1. [Configuration](#configuration)
2. [Conventions](#conventions)
3. [Logger](#logger)
4. [Statistics](#statistics)
5. [Inbounds](#inbounds)
6. [Inbound Users](#inbound-users)
7. [Outbounds](#outbounds)
8. [Routing Rules](#routing-rules)
9. [Balancer](#balancer)
10. [Source IP Blocking](#source-ip-blocking)
11. [Quick Reference](#quick-reference)

---

## Configuration

Enable the HTTP API by adding an `httpapi` block to your Xray config (e.g. `config.json`):

```json
{
  "httpapi": {
    "listen": "127.0.0.1:8080",
    "username": "admin",
    "password": "strong-password"
  }
}
```

| Field         | Type   | Description                                                                                     |
|--------------|--------|-------------------------------------------------------------------------------------------------|
| `listen`     | string | Bind address and port for the API server (e.g. `127.0.0.1:8080`, `0.0.0.0:8080`).              |
| `username`   | string | (Optional) HTTP Basic auth username. Auth is **disabled** unless **both** `username` and `password` are non-empty. |
| `password`   | string | (Optional) HTTP Basic auth password. Used together with `username`. |

Minimal config (no authentication):

```json
{
  "httpapi": {
    "listen": "127.0.0.1:8080"
  }
}
```

After Xray starts, the log will show: `HTTP API listening on 127.0.0.1:8080`.

When **both** `username` and `password` are set, every HTTP API request must include valid HTTP Basic credentials.

### Authentication

- **Scheme:** HTTP Basic (`Authorization: Basic base64(username:password)`).
- If `username` and `password` are both non-empty, every endpoint in this document requires valid credentials.
- On authentication failure, the server responds with `401 Unauthorized` and a JSON body `{"error":"unauthorized"}` and may include a `WWW-Authenticate` header.

**Example using curl:**

```bash
curl -u admin:strong-password 'http://127.0.0.1:8080/api/stats/sys'
```

**Example using PHP (with `XrayAPI`):**

```php
<?php
// Use the XrayAPI class from the "PHP example client" section below.
$xray = new XrayAPI();
$res  = $xray->get('/api/stats/sys');

echo $res['body'];
```

---

## Conventions

- **Content type:** All responses use `Content-Type: application/json`.
- **Success:** Successful responses use HTTP status `200` unless otherwise noted.
- **Errors:** Error responses include a JSON body of the form `{"error": "description"}` with appropriate status codes (`400` Bad Request, `404` Not Found, `500` Internal Server Error).
- **Method:** Sending a request with an unsupported HTTP method returns `405 Method Not Allowed`.
- **Base URL:** In the examples below, replace `http://127.0.0.1:8080` with your configured `listen` address if different.

### Shared example helpers

To avoid repeating `curl` and PHP boilerplate in every section, you can use small helpers and just change the path and payload.

#### Bash / curl helper

```bash
API_BASE='http://127.0.0.1:8080'          # HTTP API base URL
API_AUTH='-u admin:strong-password'       # Leave empty if you don't use auth

api() {
  local method="$1"
  local path="$2"
  shift 2
  curl $API_AUTH -X "$method" "$API_BASE$path" "$@"
}

# examples:
api GET  /api/stats/sys
api POST /api/logger/restart
```

#### PHP example client

You can wrap the HTTP calls in a small class to avoid repeating boilerplate. For example:

```php
<?php
final class XrayAPI
{
    public function __construct(
        private string $baseUrl = 'http://127.0.0.1:8080',
        private string $username = 'admin',
        private string $password = 'strong-password',
    ) {}

    private function request(string $method, string $path, array $fields = [], array $headers = []): array
    {
        $url = rtrim($this->baseUrl, '/') . $path;
        $ch  = curl_init($url);

        $curlOptions = [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_HTTPAUTH       => CURLAUTH_BASIC,
            CURLOPT_USERPWD        => $this->username . ':' . $this->password,
            CURLOPT_CUSTOMREQUEST  => $method,
        ];

        if ($fields !== []) {
            $curlOptions[CURLOPT_POSTFIELDS] = $fields;
        }
        if ($headers !== []) {
            $curlOptions[CURLOPT_HTTPHEADER] = $headers;
        }

        curl_setopt_array($ch, $curlOptions);
        $response = curl_exec($ch);
        if ($response === false) {
            throw new RuntimeException('Curl error: ' . curl_error($ch));
        }
        $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
        curl_close($ch);

        return ['status' => $status, 'body' => $response];
    }

    public function get(string $path): array
    {
        return $this->request('GET', $path);
    }

    public function post(string $path, array $fields = [], array $headers = []): array
    {
        return $this->request('POST', $path, $fields, $headers);
    }
}

// Example:
// $xray = new XrayAPI();
// $res  = $xray->get('/api/stats/sys');
// var_dump($res);
```

---

## Logger

### Restart logger

Restarts the Xray logging subsystem.

| Method | Path                 |
|--------|----------------------|
| POST   | `/api/logger/restart` |

**Request body:** None.

**Example:**

```bash
curl -X POST 'http://127.0.0.1:8080/api/logger/restart'
```

**Success response (200):** `{"status":"ok"}`

---

## Statistics

### Get a single counter

Returns the current value of a named stats counter. Optionally resets the counter after reading.

| Method | Path       |
|--------|------------|
| GET    | `/api/stats` |

| Query parameter | Type    | Description                                      |
|-----------------|---------|--------------------------------------------------|
| `name`          | string  | Counter name (e.g. `inbound>>>tag>>>traffic>>>downlink`). |
| `reset`         | boolean | If `true` or `1`, reset the counter after reading. Default: `false`. |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/stats?name=inbound>>>my-in>>>traffic>>>downlink'
```

**Success response (200):** `{"stat":{"name":"...","value":12345}}`  
**Error (404):** Counter not found.

---

### Query counters by pattern

Returns all counters whose names match the given substring pattern. Optionally resets matching counters after reading.

| Method | Path             |
|--------|------------------|
| GET    | `/api/stats/query` |

| Query parameter | Type    | Description                    |
|-----------------|---------|--------------------------------|
| `pattern`       | string  | Substring to match in counter names. |
| `reset`         | boolean | If `true` or `1`, reset matched counters after reading. |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/stats/query?pattern=user>>>'
```

**Success response (200):** `{"stat":[{"name":"...","value":...}, ...]}`

---

### System statistics

Returns runtime and memory statistics for the Xray process (goroutines, alloc, uptime, GC, etc.).

| Method | Path          |
|--------|---------------|
| GET    | `/api/stats/sys` |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/stats/sys'
```

**Success response (200):** JSON object with fields such as `uptime`, `numGoroutine`, `alloc`, `totalAlloc`, `sys`, `mallocs`, `frees`, `liveObjects`, `numGC`, `pauseTotalNs`.

---

### Online session count for a user

Returns the current number of active sessions for a user (counter name is derived from the user email).

| Method | Path              |
|--------|-------------------|
| GET    | `/api/stats/online` |

| Query parameter | Type   | Description        |
|-----------------|--------|--------------------|
| `email`         | string | User email address. |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/stats/online?email=user@example.com'
```

**Success response (200):** `{"stat":{"name":"user>>>user@example.com>>>online","value":2}}`

---

### Online IP list for a user

Returns the list of source IPs currently associated with a user’s sessions and their timestamps.

| Method | Path                    |
|--------|-------------------------|
| GET    | `/api/stats/online/iplist` |

| Query parameter | Type   | Description        |
|-----------------|--------|--------------------|
| `email`         | string | User email address. |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/stats/online/iplist?email=user@example.com'
```

**Success response (200):** `{"name":"user>>>user@example.com>>>online","ips":{"1.2.3.4":1700000000,...}}`

---

### All online users

Returns the list of all users currently with at least one active session.

| Method | Path                     |
|--------|--------------------------|
| GET    | `/api/stats/online/users` |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/stats/online/users'
```

**Success response (200):** `{"users":["user1@example.com","user2@example.com"]}`

---

### All online users with IPs

Single-call endpoint that returns **every online user** (by email only) and, for each user, the **source IPs** of their active sessions with **last-seen Unix timestamps**. Use this when you need a full snapshot of who is online and from which IPs (e.g. dashboards, abuse checks, multi-IP display).

- **Combines** the data from `GET /api/stats/online/users` and repeated `GET /api/stats/online/iplist?email=...` in one response.
- **Asynchronous:** IP lists are fetched in parallel per user, so total response time stays low even with many users.
- **Output:** Only the user email (e.g. `user@example.com`) is returned, not the internal counter name.

| Method | Path                    |
|--------|-------------------------|
| GET    | `/api/stats/online/all`  |

| Query parameter | Type   | Description |
|-----------------|--------|-------------|
| `email`         | string | Optional, repeatable. If present, only these users are returned (e.g. `?email=user1@example.com&email=user2@example.com`). |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/stats/online/all'
curl 'http://127.0.0.1:8080/api/stats/online/all?email=user1@example.com&email=user2@example.com'
```

**Success response (200):**

```json
{
  "users": [
    {
      "email": "user1@example.com",
      "ips": {
        "1.2.3.4": 1700000000,
        "5.6.7.8": 1700000001
      }
    },
    {
      "email": "user2@example.com",
      "ips": {
        "10.0.0.1": 1700000002
      }
    }
  ]
}
```

| Response field   | Type   | Description |
|------------------|--------|-------------|
| `users`          | array  | List of online-user entries. |
| `users[].email` | string | User email (same as in config). |
| `users[].ips`    | object | Map of source IP → last-seen Unix time (seconds). Empty object if no IPs recorded. |

When there are no online users (or no matching users when filtering), the response is `{"users":[]}`.

---

## Inbounds

### Add inbounds

Adds one or more inbounds from a JSON payload. Structure must match Xray inbound config format.

| Method | Path               |
|--------|--------------------|
| POST   | `/api/inbounds/add` |

**Request body:**

```json
{
  "inbounds": [
    {
      "tag": "my-inbound",
      "protocol": "vless",
      "listen": "0.0.0.0",
      "port": 443,
      "settings": { ... },
      "sniffing": { ... }
    }
  ]
}
```

**Example:**

```bash
curl -X POST 'http://127.0.0.1:8080/api/inbounds/add' \
  -H 'Content-Type: application/json' \
  -d '{"inbounds":[{"tag":"new-in","protocol":"vless","listen":"0.0.0.0","port":8443,"settings":{"clients":[],"decryption":"none"}}]}'
```

**Success response (200):** `{"status":"ok"}`

---

### Remove inbounds

Removes inbounds by tag.

| Method | Path                  |
|--------|-----------------------|
| POST   | `/api/inbounds/remove` |

**Request body:**

```json
{
  "tags": ["tag1", "tag2"]
}
```

**Example:**

```bash
curl -X POST 'http://127.0.0.1:8080/api/inbounds/remove' \
  -H 'Content-Type: application/json' \
  -d '{"tags":["old-inbound"]}'
```

**Success response (200):** `{"status":"ok"}`

---

### List inbounds

Returns the list of configured inbounds. Can return only tags or full handler config (tag, receiver settings, proxy settings).

| Method | Path                |
|--------|---------------------|
| GET    | `/api/inbounds/list` |

| Query parameter | Type    | Description                                                                 |
|-----------------|---------|-----------------------------------------------------------------------------|
| `tags_only`     | `1`     | Return only tags.                                                                 |
| `isOnlyTags`    | `true`  | Same as `tags_only=1`.                                                             |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/inbounds/list'
curl 'http://127.0.0.1:8080/api/inbounds/list?tags_only=1'
```

**Success response (200):** `{"inbounds":[{"tag":"..."}, ...]}` or full config objects when not using tags-only.

---

## Inbound Users

### Add users to inbounds

Adds users to existing inbounds. Only **tag** and **settings** (with `clients`) are required. The protocol is inferred from the existing inbound; you do not send `listen`, `port`, `protocol`, or other fields.

| Method | Path                      |
|--------|---------------------------|
| POST   | `/api/inbounds/users/add`  |

**Request body:**

```json
{
  "inbounds": [
    {
      "tag": "vless-in",
      "settings": {
        "clients": [
          {
            "id": "uuid-here",
            "email": "newuser@example.com"
          }
        ]
      }
    }
  ]
}
```

| Field     | Required | Description |
|----------|----------|-------------|
| `tag`    | Yes      | Inbound tag to add users to (must already exist). |
| `settings` | Yes    | Protocol settings; must include `clients` (or protocol-specific user list). |

Supported inbounds: VLESS, VMess, Trojan, Shadowsocks, Shadowsocks 2022.

**Example:**

```bash
curl -X POST 'http://127.0.0.1:8080/api/inbounds/users/add' \
  -H 'Content-Type: application/json' \
  -d '{"inbounds":[{"tag":"vless-in","settings":{"clients":[{"id":"xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx","email":"user@example.com"}]}}]}'
```

**Success response (200):** `{"added_users":1}`

---

### Remove users from an inbound

Removes users from a single inbound by email.

| Method | Path                         |
|--------|------------------------------|
| POST   | `/api/inbounds/users/remove` |

**Request body:**

```json
{
  "tag": "vless-in",
  "emails": ["user1@example.com", "user2@example.com"]
}
```

| Field   | Type     | Required | Description                    |
|---------|----------|----------|--------------------------------|
| `tag`   | string   | Yes      | Inbound tag.                   |
| `emails`| string[] | Yes      | User emails to remove.         |

**Example:**

```bash
curl -X POST 'http://127.0.0.1:8080/api/inbounds/users/remove' \
  -H 'Content-Type: application/json' \
  -d '{"tag":"vless-in","emails":["user@example.com"]}'
```

**Success response (200):** `{"removed_users":1}`

---

### Get inbound user(s)

Returns one user (by email) or all users for an inbound.

| Method | Path                 |
|--------|----------------------|
| GET    | `/api/inbounds/users` |

| Query parameter | Type   | Description                                      |
|-----------------|--------|--------------------------------------------------|
| `tag`           | string | Inbound tag (required).                           |
| `email`         | string | If set, return only this user; otherwise all users. |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/inbounds/users?tag=vless-in'
curl 'http://127.0.0.1:8080/api/inbounds/users?tag=vless-in&email=user@example.com'
```

**Success response (200):** `{"users":[...]}` (array of user objects)

---

### Get inbound user count

Returns the number of users configured for an inbound.

| Method | Path                      |
|--------|---------------------------|
| GET    | `/api/inbounds/users/count` |

| Query parameter | Type   | Description        |
|-----------------|--------|--------------------|
| `tag`           | string | Inbound tag (required). |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/inbounds/users/count?tag=vless-in'
```

**Success response (200):** `{"count":5}`

---

## Outbounds

### Add outbounds

Adds one or more outbounds. Body structure must match Xray outbound config format.

| Method | Path                 |
|--------|----------------------|
| POST   | `/api/outbounds/add`  |

**Request body:**

```json
{
  "outbounds": [
    {
      "tag": "my-out",
      "protocol": "freedom",
      "settings": {}
    }
  ]
}
```

**Example:**

```bash
curl -X POST 'http://127.0.0.1:8080/api/outbounds/add' \
  -H 'Content-Type: application/json' \
  -d '{"outbounds":[{"tag":"new-out","protocol":"freedom","settings":{}}]}'
```

**Success response (200):** `{"status":"ok"}`

---

### Remove outbounds

Removes outbounds by tag.

| Method | Path                    |
|--------|-------------------------|
| POST   | `/api/outbounds/remove`  |

**Request body:**

```json
{
  "tags": ["tag1", "tag2"]
}
```

**Success response (200):** `{"status":"ok"}`

---

### List outbounds

Returns the list of configured outbounds (excluding the internal API outbound). Each item includes tag, sender settings, and proxy settings.

| Method | Path                  |
|--------|-----------------------|
| GET    | `/api/outbounds/list`  |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/outbounds/list'
```

**Success response (200):** `{"outbounds":[{"tag":"...","senderSettings":...,"proxySettings":...}, ...]}`

---

## Routing Rules

### Add rules

Adds routing rules. Uses the same `routing.rules` structure as in Xray config. `should_append` controls whether to append to or replace existing rules (behavior depends on router implementation).

| Method | Path              |
|--------|-------------------|
| POST   | `/api/rules/add`   |

**Request body:**

```json
{
  "routing": {
    "rules": [
      {
        "type": "field",
        "ruleTag": "my-rule",
        "outboundTag": "blocked",
        "domain": ["geosite:category-ads"]
      }
    ]
  },
  "should_append": false
}
```

**Example:**

```bash
curl -X POST 'http://127.0.0.1:8080/api/rules/add' \
  -H 'Content-Type: application/json' \
  -d '{"routing":{"rules":[{"type":"field","ruleTag":"r1","outboundTag":"direct","domain":["geosite:google"]}]},"should_append":true}'
```

**Success response (200):** `{"status":"ok"}`

---

### Remove rules

Removes routing rules by rule tag.

| Method | Path                |
|--------|---------------------|
| POST   | `/api/rules/remove`  |

**Request body:**

```json
{
  "rule_tags": ["ruleTag1", "ruleTag2"]
}
```

**Success response (200):** `{"status":"ok"}`

---

### List rules

Returns the list of routing rules (outbound tag and rule tag per rule).

| Method | Path              |
|--------|-------------------|
| GET    | `/api/rules/list`  |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/rules/list'
```

**Success response (200):** `{"rules":[{"tag":"outbound-tag","ruleTag":"rule-tag"}, ...]}`

---

## Balancer

### Get balancer info

Returns override target and principle target(s) for a balancer.

| Method | Path                 |
|--------|----------------------|
| GET    | `/api/balancer/info`  |

| Query parameter | Type   | Description      |
|-----------------|--------|------------------|
| `tag`           | string | Balancer tag.    |

**Example:**

```bash
curl 'http://127.0.0.1:8080/api/balancer/info?tag=my-balancer'
```

**Success response (200):** `{"balancer":{"override":{"target":"..."},"principle_target":{"tag":["..."]}}}` (fields may be omitted if not applicable)

---

### Override balancer target

Forces a balancer to always select a specific outbound tag.

| Method | Path                      |
|--------|---------------------------|
| POST   | `/api/balancer/override`   |

**Request body:**

```json
{
  "balancer_tag": "my-balancer",
  "target": "outbound-tag"
}
```

Use an empty `target` (or omit it) to clear the override if the implementation supports it.

**Example:**

```bash
curl -X POST 'http://127.0.0.1:8080/api/balancer/override' \
  -H 'Content-Type: application/json' \
  -d '{"balancer_tag":"my-balancer","target":"direct-out"}'
```

**Success response (200):** `{"status":"ok"}`

---

## Source IP Blocking

Adds or updates a routing rule that blocks traffic from specified source IPs (optionally scoped to an inbound). If `reset` is true, an existing rule with the same `rule_tag` is removed before adding the new rule.

| Method | Path                  |
|--------|-----------------------|
| POST   | `/api/sourceip/block`  |

**Request body:**

```json
{
  "outbound": "blocked",
  "inbound": "socks",
  "rule_tag": "sourceIpBlock",
  "reset": false,
  "source_ips": ["1.2.3.4", "5.6.7.8"]
}
```

| Field       | Type     | Required | Description                                                                 |
|------------|----------|----------|-----------------------------------------------------------------------------|
| `outbound` | string   | Yes      | Outbound tag used for blocked traffic (e.g. a blackhole outbound).         |
| `inbound`  | string   | No       | Inbound tag to match; omit or empty to match all inbounds.                  |
| `rule_tag` | string   | No       | Rule tag for the generated rule; default: `sourceIpBlock`.                   |
| `reset`    | boolean  | No       | If `true`, remove existing rule with this `rule_tag` before adding.        |
| `source_ips` | string[] | Yes    | Source IPs (or CIDRs) to block.                                             |

#### Examples

- **curl (with helper):**

  ```bash
  api POST /api/sourceip/block \
    -H 'Content-Type: application/json' \
    -d '{"outbound":"blocked","inbound":"socks","rule_tag":"sourceIpBlock","reset":false,"source_ips":["1.2.3.4","5.6.7.8"]}'
  ```

- **PHP (with `XrayAPI` class):**

  ```php
  <?php
  // Assuming XrayAPI class from the "PHP example client" section is available.
  $xray = new XrayAPI();

  $payload = json_encode([
      'outbound'   => 'blocked',
      'inbound'    => 'socks',
      'rule_tag'   => 'sourceIpBlock',
      'reset'      => false,
      'source_ips' => ['1.2.3.4', '5.6.7.8'],
  ], JSON_UNESCAPED_SLASHES);

  $res = $xray->post(
      '/api/sourceip/block',
      $payload,
      ['Content-Type: application/json']
  );
  ```

**Success response (200):** `{"status":"ok"}`

---

## Quick Reference

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/logger/restart` | Restart logger |
| GET | `/api/stats` | Get single counter |
| GET | `/api/stats/query` | Query counters by pattern |
| GET | `/api/stats/sys` | System/runtime stats |
| GET | `/api/stats/online` | Online count for one user |
| GET | `/api/stats/online/iplist` | Online IP list for one user |
| GET | `/api/stats/online/users` | All online users |
| GET | `/api/stats/online/all` | All online users with their IPs (async) |
| POST | `/api/inbounds/add` | Add inbounds |
| POST | `/api/inbounds/remove` | Remove inbounds |
| GET | `/api/inbounds/list` | List inbounds |
| POST | `/api/inbounds/users/add` | Add users to inbounds |
| POST | `/api/inbounds/users/remove` | Remove users from inbound |
| GET | `/api/inbounds/users` | Get user(s) of an inbound |
| GET | `/api/inbounds/users/count` | Get user count of an inbound |
| POST | `/api/outbounds/add` | Add outbounds |
| POST | `/api/outbounds/remove` | Remove outbounds |
| GET | `/api/outbounds/list` | List outbounds |
| POST | `/api/rules/add` | Add routing rules |
| POST | `/api/rules/remove` | Remove routing rules |
| GET | `/api/rules/list` | List routing rules |
| GET | `/api/balancer/info` | Get balancer info |
| POST | `/api/balancer/override` | Override balancer target |
| POST | `/api/sourceip/block` | Block by source IP(s) |
| POST | `/api/config/import` | Import provided config JSON file into a config path |

---

## Config Import

### Import config.json file

Writes a full Xray configuration JSON document (sent as a file) to disk. This endpoint is intended for panels and tools that generate `config.json` themselves and want to save it to a specific path via the API.

> Note: This endpoint **does not** read the current in‑memory runtime state from the core; it only writes the file you send.

| Method | Path                |
|--------|---------------------|
| POST   | `/api/config/import` |

**Preferred request (multipart/form-data):**

- `file`: `config.json` file (required)
- `path`: target path on disk (optional; if empty, uses the first config file Xray was started with)

#### Examples

- **curl (with helper):**

  ```bash
  api POST /api/config/import \
    -F 'file=@/path/to/config.json' \
    -F 'path=/etc/xray/config.json'
  ```

- **PHP (with `XrayAPI` class):**

  ```php
  <?php
  // Assuming XrayAPI class from the "PHP example client" section is available.
  $xray = new XrayAPI();

  $fields = [
      'file' => new CURLFile(__DIR__ . '/config.json', 'application/json', 'config.json'),
      'path' => '/etc/xray/config.json', // optional
  ];

  $res = $xray->post('/api/config/import', $fields);
  ```

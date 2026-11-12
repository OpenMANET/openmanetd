# API Authentication

OpenMANETd exposes a small session-based authentication layer in front of the
ConnectRPC API server (port **8087**). The same authentication model works for
the built-in Web UI and for direct API callers — curl, scripts, and ConnectRPC
clients in any language.

The Web UI reaches the auth and RPC surfaces through the frontend server at
`http(s)://<node>:8080/8081`, which reverse-proxies `/auth/*` and `/rpc/*` to
the API server. Routing browser traffic through the frontend keeps it on a
single origin, which is required for HTTPS pages (mic/speaker access) to call
the plain-HTTP API server without mixed-content blocking.

External API callers — curl, scripts, ConnectRPC clients in other languages —
continue to talk directly to `http://<node>:8087` with the same endpoints.
Both paths converge on the same `SessionStore`, so a token minted by either
origin works for both.

## Auth model

1. `POST /auth/login` with a username and password.
2. The server validates credentials through PAM (`/etc/pam.d/login` by default)
   and mints a 256-bit random session token.
3. The token is returned two ways:
   - As an `HttpOnly` cookie named `session` — used automatically by browsers.
   - In the JSON response body under `"token"` — used by non-browser clients.
4. Subsequent requests attach the token as **either**:
   - the `session` cookie, or
   - an `Authorization: Bearer <token>` header.

When both are present, the `Authorization` header wins. This keeps a stale
browser cookie from overriding an explicit header in mixed-client setups.

### Default credentials

A fresh node boots with user `root` and **no password**. Empty passwords are
accepted because `/etc/pam.d/login` ships with `pam_unix.so nullok`. Set a
password as soon as possible via either the Web UI (Settings → Passphrase) or
`POST /auth/change-password` — both paths work whether the current password is
empty or not.

### Disabling authentication

Setting `auth.enable: false` in the daemon config (the default) turns off the
whole auth stack:

- `POST /auth/login`, `/auth/logout`, and `/auth/change-password` are **not
  registered**.
- `GET /auth/check` returns `{"authenticated": true, "username": "admin",
  "authEnabled": false}` so the Web UI bypasses the login gate.
- All API endpoints are reachable without any credentials.

Only do this on an isolated network where open API access is acceptable.

## Endpoint reference

All endpoints use plain HTTP/JSON (not ConnectRPC). They are served from the
API origin, e.g. `http://<node>:8087`.

### `POST /auth/login`

Request body:

```json
{"username": "root", "password": ""}
```

Response `200 OK`:

```json
{"username": "root", "token": "<64-char hex>"}
```

Response headers include `Set-Cookie: session=<token>; HttpOnly; SameSite=Lax`.

Error responses:

| Status | When |
| ------ | ---- |
| `400`  | request body is not valid JSON |
| `401`  | credentials rejected by PAM |
| `422`  | `username` is empty (`password` may be empty) |
| `405`  | non-POST method |

### `GET /auth/check`

No request body. Always returns `200 OK`.

```json
{"authenticated": true, "username": "root", "authEnabled": true}
```

`authEnabled` is `false` when the daemon was started with `auth.enable: false`;
clients should hide session-dependent UI in that case.

### `POST /auth/logout`

Reads the token from either the cookie or `Authorization: Bearer` header.
Invalidates the session and clears the cookie. Returns `204 No Content`.

### `POST /auth/change-password`

Requires a valid session (cookie or Bearer header). The current password is
re-verified against PAM — an attacker with a session cookie alone cannot
rotate the password.

Request body:

```json
{"currentPassword": "", "newPassword": "alpha123"}
```

An empty `currentPassword` is allowed for first-time setup when `pam_unix
nullok` is in effect.

Response `204 No Content` on success.

Error responses:

| Status | When |
| ------ | ---- |
| `400`  | request body is not valid JSON |
| `401`  | no session, or current password rejected by PAM |
| `422`  | new password is empty or contains `:` / `\n` / `\r` |
| `500`  | the underlying `chpasswd` invocation failed |

## Worked examples (curl)

```sh
# 1. Log in as root with the default empty password.
TOKEN=$(curl -s http://<node>:8087/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"root","password":""}' | jq -r .token)

# 2. Call a ConnectRPC endpoint with the Bearer header.
curl -X POST http://<node>:8087/openmanet.gnss.v1.GnssService/GetGpsStatus \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{}'

# 3. Set a passphrase (first-time setup — currentPassword is empty).
curl -X POST http://<node>:8087/auth/change-password \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"currentPassword":"","newPassword":"alpha123"}'
# → 204 No Content

# 4. Log out (invalidates $TOKEN; subsequent requests with it will 401).
curl -X POST http://<node>:8087/auth/logout \
  -H "Authorization: Bearer $TOKEN"
```

## ConnectRPC clients in other languages

### JavaScript / TypeScript

ConnectRPC's `createConnectTransport` accepts a header interceptor. Attach the
Bearer token per request:

```js
import { createConnectTransport } from "@connectrpc/connect-web";

const bearerInterceptor = (next) => async (req) => {
  req.header.set("Authorization", `Bearer ${sessionToken}`);
  return next(req);
};

const transport = createConnectTransport({
  baseUrl: "http://node:8087",
  interceptors: [bearerInterceptor],
});
```

You do **not** need `credentials: "include"` when using the Bearer header —
that option is only required for the cookie-based browser path.

### Go

```go
import "connectrpc.com/connect"

client := gnssv1connect.NewGnssServiceClient(
    http.DefaultClient,
    "http://node:8087",
    connect.WithInterceptors(
        connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
            return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
                req.Header().Set("Authorization", "Bearer "+sessionToken)
                return next(ctx, req)
            }
        }),
    ),
)
```

## Token lifetime and eviction

- Default session lifetime: **24 hours** (`auth.sessionMaxAge`, seconds).
- Maximum concurrent sessions per daemon: **16** (`auth.sessionMaxSize`).
  When full, the session with the oldest `LastAccess` time is evicted.
- Expired sessions are swept every 5 minutes by a background goroutine.
- An open web terminal (`/api/terminal/ws`) ends when the session that opened
  it logs out, is evicted, or expires, and when the browser stops answering
  WebSocket pings for 60 seconds.
- Session storage is in-memory only — all sessions are lost on daemon
  restart. Callers that see a 401 after a restart should re-authenticate.

## Security notes

- Session cookies are written **without** the `Secure` attribute so they work
  on both `http://` and `https://` origins on a local LAN. Do not expose the
  API to the public internet without putting it behind HTTPS and adjusting
  this policy at the reverse proxy.
- Browser access is limited to LAN origins. The API server grants CORS (with
  credentials) and Private Network Access, and the frontend server accepts
  WebSocket upgrades on `/ws` and `/api/terminal/ws`, only when the `Origin`
  header is absent, matches the request `Host`, is a loopback, RFC 1918,
  link-local, IPv6 ULA, or `100.64.0.0/10` (Tailscale) address, or is a
  single-label name or a name under `.lan`, `.local`, or `.home.arpa`. Pages
  served from any other site cannot call the API with the operator's session
  or open those WebSockets. Origin checks do not defend against DNS
  rebinding.
- Session tokens are generated with `crypto/rand`, 32 bytes / 256 bits of
  entropy per token, hex-encoded.
- The error message for `POST /auth/login` is deliberately vague
  (`"invalid credentials"`) to avoid leaking whether a given username exists.
- `chpasswd` is invoked with the username + password fed over **stdin** as
  `user:password\n`. The daemon rejects `:`, `\n`, and `\r` in either field
  to prevent record-splitting attacks.

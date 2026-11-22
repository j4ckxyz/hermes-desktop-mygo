# Hermes Agent API reference for Hermes Desktop (Go)

Researched on 2026-11-22 from a clone of `github.com/NousResearch/hermes-agent` at commit
**`8635a6f55652b0ffab12fd45f7dad83dc8a3ed7b`** (2026-11-22 00:56 -0800).

How to read this document:

- **Citations.** Every `path:line` is relative to the repository root at that commit. The docs pages are cited from `website/docs/…`. That tree is the source of the official site at `https://hermes-agent.nousresearch.com/docs/` (`website/docusaurus.config.ts:11-12`). The page URL is the docs path without its `.md` suffix, for example `…/docs/user-guide/features/web-dashboard`.
  - I did not fetch the live site. Every docs claim below is cited from the docs source at this commit.
- **Not exercised.** Nothing was run. The Hermes server was not started and no package was installed.
- **⚠️ UNVERIFIED** marks anything I could not confirm in source. The [open questions](#9-unconfirmed--open-questions) section lists them all.
- **Contract schemas.** The JSON-RPC schemas come from the generated OpenRPC contract `apps/shared/src/gateway-contract.openrpc.json`. That file is generated from `tui_gateway/contracts/*.py` by `scripts/gen_gateway_contracts.py`, according to its `info.description`.
- **Schema notation:** `*field` means required, `field=x` means the default is `x`, and `a|b` means a union.
- **Two kinds of examples.** Examples marked **verbatim** are copied from a test or source file. Examples marked **assembled** are built by me from the schema's exact field names, with invented values. Use only the field names, never the values, as ground truth for the assembled ones.

---

## 0. Summary

- **One instance serves three HTTP surfaces.** A chat client should use the **dashboard/gateway listener**:
  - REST under `/api/*` for lists, history, rename, delete and so on.
  - One JSON-RPC 2.0 WebSocket at `/api/ws` for live chat.

  `apps/desktop` uses exactly this split.
- **The OpenAI-compatible API server (`:8642`, `API_SERVER_KEY`) is a separate surface.** It cannot attach to sessions owned by Desktop, the CLI or the TUI. It is a fallback for an "API key only" mode, not a full client surface ([§1.1](#11-the-three-server-surfaces)).
- **`/api/ws` speaks two dialects.** It is **canonical** when the listener has a session authority, which is the gateway-served listener. It is **legacy** for a standalone `hermes dashboard` or `hermes serve`. The first frame says which: `gateway.ready.payload.session_authority === true` means canonical.
- **Auth.** A deployment token works as `X-Hermes-Session-Token` or a Bearer header on REST, and as `?token=` on the WebSocket. Username and password give cookies or native bearer tokens, plus single-use WebSocket tickets.

---

## 1. Transports and base URLs

### 1.1 The three server surfaces

| Surface | Started by | Default bind | What it serves | Auth |
|---|---|---|---|---|
| **Dashboard / backend server** | `hermes dashboard` (opens the SPA) or `hermes serve` (headless, "powers the desktop app and remote backends") | `127.0.0.1:9119` (`hermes_cli/web_server.py:1430`; docs `website/docs/user-guide/features/web-dashboard.md:27`, `website/docs/reference/cli-commands.md:1979-1981`) | `/api/*` REST, `/api/ws` JSON-RPC WebSocket, `/api/pty`, the SPA, `/auth/*` | Loopback: session token. Non-loopback: auth gate (§2) |
| **Gateway listener / Webapp** | `hermes gateway` (with `webapp.enabled: true` for a stable port) | `webapp.port` default **9120** on `127.0.0.1` (`gateway/webapp_listener.py:33-34`). With the Webapp off it is a private loopback listener on an ephemeral port (`gateway/webapp_listener.py:52-55`) | The same dashboard routers plus `/api/ws` **with a session authority attached** (`gateway/run_api.py:1-13`, `:110`) | Same gate as the dashboard |
| **API server** (gateway platform `api_server`) | `hermes gateway` with `API_SERVER_ENABLED=true` | `127.0.0.1:8642` (`gateway/platforms/api_server.py:170-171`) | OpenAI-compatible `/v1/*`, plus `/api/sessions*`, `/v1/runs*`, `/api/jobs*` with SSE | `Authorization: Bearer $API_SERVER_KEY` only |

Notes:

- **The gateway serves the dashboard routes too.** The docs describe a supported "move a dashboard backend to the gateway" migration, after which the gateway serves the old address and port (`website/docs/user-guide/features/web-dashboard.md`, section "Moving a dashboard backend to the gateway"). `hermes serve` exits with code 78 when this profile's gateway already holds the port (`website/docs/reference/cli-commands.md:1981`). A client must work against both shapes.
- **Canonical versus legacy.** The canonical wire exists only when `app.state.session_authority` is set. That happens only on the gateway listener (`gateway/run_api.py:110`). `tui_gateway/ws.py:378-387` returns `None` (legacy `server.dispatch`) when no authority is attached, and `gateway.ready` advertises `session_authority: true` only in the canonical case (`tui_gateway/ws.py:657-664`).
- **API-server sessions are walled off.** A request on the API server that names a session owned by another surface is refused with HTTP 409 `permission_denied` before admission (`website/docs/user-guide/features/api-server.md:502-512`). The docs say: "To work on a chat from another surface, attach through the gateway WebSocket (a `prompt.submit` on the shared session) instead."

### 1.2 URL shapes

- **REST:** `http(s)://HOST:PORT[/prefix]/api/...`. A reverse-proxy sub-path is honoured via `X-Forwarded-Prefix` or `dashboard.public_url` (`website/docs/user-guide/features/web-dashboard.md`, "Public URL override").
- **WebSocket:** `ws(s)://HOST:PORT[/prefix]/api/ws?ticket=…` or `?token=…`. Desktop builds these URLs at `apps/desktop/electron/connection-config.ts:93-107`.
  - The scheme follows the base URL: `https` gives `wss`.
  - A ticket may instead be sent in the subprotocol header `Sec-WebSocket-Protocol: hermes-gateway-v1, hermes-gateway-ticket.<ticket>`. That needs exactly one ticket protocol together with `hermes-gateway-v1`. The server then accepts with subprotocol `hermes-gateway-v1` (`hermes_cli/web_server_boundary.py:159-173`; `hermes_cli/web_server_chat.py:371-420`).
- **Profiles.**
  - **REST:** dashboard REST takes `?profile=<name>`, or `"profile"` in a JSON write body. An unknown name returns 404 (`web-dashboard.md`, "REST API → Profile-scoped endpoints").
  - **WebSocket:** a dashboard-authenticated socket gets `profile_scope: 'host'`, so it may address any served sibling profile through the `profile` parameter that most methods accept (`hermes_cli/web_routers/chat_ws.py:675-678`).
  - **API server:** profiles go under the `/p/<profile>/…` prefix, and each one needs its own `API_SERVER_KEY` (`api-server.md:795-818`).

### 1.3 Local versus remote, and TLS

- **Remote access needs a non-loopback bind.** A loopback bind accepts only loopback WebSocket peers (`hermes_cli/web_server_boundary.py:176-196`). Any non-loopback bind engages the auth gate, and the server refuses to start without an auth provider. RFC1918, CGNAT and link-local addresses count as public (`hermes_cli/web_server_boundary.py:113-120`). `--insecure` is ignored (`:117-118`).
- **TLS: the server binds plain HTTP** (uvicorn). TLS is expected from a reverse proxy or Tailscale Serve, with `dashboard.public_url` (or `webapp.public_url`) plus `dashboard.trusted_proxies` (`web-dashboard.md`, "Public URL override"). Cookies get `Secure` and the `__Host-` / `__Secure-` name prefixes only over HTTPS (`hermes_cli/dashboard_auth/cookies.py:17-18`, `:58-62`). ⚠️ UNVERIFIED: whether uvicorn can be given a certificate directly. I found no `ssl_certfile` option.
- **The API server is also plain HTTP** (aiohttp). The docs recommend server-to-server use (`api-server.md:886`).
- **Local Desktop has a native path that a Go client should not copy.** It dials `hermes gateway ensure --json` and authenticates with private `X-Hermes-Gateway-Ticket` grants minted out of band (`apps/desktop/docs/canonical-gateway-protocol.md:9-16`; `hermes_cli/web_server_chat.py:244-251`). These grants are not available to a third-party remote client.

### 1.4 Host, Origin and CORS checks that affect a non-browser client

**Host check (REST).** `host_header_middleware` returns HTTP **400** `{"detail":"Invalid Host header. Dashboard requests must use the bound hostname or the configured public hostname."}` when `Host` doesn't match the bound interface (`hermes_cli/web_server.py:385-402`). Accepted values (`hermes_cli/web_server_boundary.py:84-110`):

- With a `0.0.0.0` or `::` bind, any Host is accepted.
- With a loopback bind, only loopback names are accepted.
- With a specific-IP bind, only that IP is accepted.
- An exact `public_url` host is always accepted.

So a Go client must send the Host it actually dialled, which `net/http` does by default. Don't rewrite Host.

**Host and Origin check (WebSocket).** `ws_host_origin_reason` repeats the Host check (`hermes_cli/web_server_boundary.py:253-289`).

- An **absent `Origin` is allowed** (`:270-272`).
- Non-HTTP origins (`file://`, `null`, `app://`) are allowed.
- An `http(s)` Origin must match the bound host, and on loopback it must also match the port.
- **Recommendation: send no `Origin` header from Go.** Note that `gorilla/websocket` and `nhooyr/coder websocket` do not add one unless you set it.
- A failure closes the socket with **4403** before accept (`hermes_cli/web_routers/chat_ws.py:102-115`).

**CORS (dashboard).** Only trusted loopback origins on the bound port are allowed (`hermes_cli/web_server.py:579-593`; `hermes_cli/web_server_boundary.py:237-250`). This matters only for browsers.

**Cookie-write CSRF guard (gated mode).** This applies to non-GET requests authenticated by cookie.

- On the standalone dashboard, the write is refused (403) only when `Sec-Fetch-Site` is `same-site` or `cross-site` (`hermes_cli/dashboard_auth/middleware.py:84-107`, `:250-262`).
- On the gateway Webapp, writes must come from its own origin. A request with **no `Origin` passes when there is also no `Sec-Fetch-Site`, or `Sec-Fetch-Site: none`** (`hermes_cli/dashboard_auth/request_utils.py:55-87`).
- So a Go client that sends neither header is fine.
- Bearer-authenticated requests skip the cookie checks (`middleware.py:305-307`).

**API server CORS.** A request **without `Origin` is always allowed**. An `Origin` that is not in `API_SERVER_CORS_ORIGINS` gets 403, including preflight (`gateway/platforms/api_server.py:649`, `:661-677`).

---

## 2. Authentication

### 2.1 Discover the auth shape first (public, no credentials)

**`GET /api/status`**

- Public (`hermes_cli/dashboard_auth/public_paths.py:9-27`).
- The auth block (`hermes_cli/web_routers/status.py:405-433`):
  - `auth_required: bool`
  - `auth_providers: [str]`: every registered provider name, for example `"basic"`, `"nous"`, `"self-hosted"`, `"session-token"`
  - `auth_flows: [str]`: `"cookie"` whenever gated, plus `"native_pkce"` when at least one interactive provider exists
  - `peer_access: int`
- Desktop maps `auth_required: true` to its "oauth" (cookie/ticket) mode and `false` to "token" mode (`apps/desktop/electron/connection-config.ts:1130-1132`).

**`GET /api/auth/providers`**

- Public (`hermes_cli/dashboard_auth/middleware.py:44-50`).
- Returns `{"providers":[{"name","display_name","supports_password"}]}`, listing interactive providers only (`hermes_cli/dashboard_auth/routes.py:214-228`).
- Returns 503 `{"detail":"no auth providers registered"}` when no session provider exists. A token-only deployment returns an empty list.
- The `basic` provider is `name="basic"`, `display_name="Username & Password"`, `supports_password=True` (`plugins/dashboard_auth/basic/__init__.py:126-128`).

**Decision table** (mirrors Desktop's `resolveLoginStrategy`, `apps/desktop/electron/native-oauth.ts:97-112`, and `oauthGuardMayHardFail`, `apps/desktop/electron/native-auth-decisions.ts:229-237`):

| `/api/status` | Meaning | Use |
|---|---|---|
| `auth_required:false` | Loopback bind, or a Desktop-owned loopback backend | Session token (§2.2a), loopback only |
| `auth_required:true`, `auth_providers` ∋ `"session-token"` | Deployment token provisioned | Token mode (§2.2a) |
| `auth_required:true`, `/api/auth/providers` has `supports_password:true` | Username/password | §2.3 |
| `auth_required:true`, OAuth providers only | Nous / OIDC | Native PKCE via the system browser (§2.3.3). Out of scope here |

### 2.2 Mode A: a key from the instance's `.env`

There are **two different keys** for two different surfaces.

#### 2.2a `HERMES_DASHBOARD_SESSION_TOKEN` (dashboard/gateway listener): **recommended for "API key" mode**

**Where the value comes from**

- The env var, or `file:<path>` to read it from a file. Unset, a random token is minted for each start and injected into the SPA HTML (`hermes_cli/web_server_session_token.py:1-38`; `hermes_cli/web_server.py:227-228`).
- A line in `~/.hermes/.env` is used unless the launching parent injected its own value (`website/docs/reference/environment-variables.md:580`).
- When set, the bundled `session-token` provider registers automatically: `name="session-token"`, `interactive_login=False`, constant-time compare (`plugins/dashboard_auth/session_token/__init__.py:101-128`). It can be disabled with `dashboard.session_token_auth.enabled: false` (`:55-80`).

**REST: send either header**

- `X-Hermes-Session-Token: <token>`
  - Ungated: `hermes_cli/web_server.py:300-311`.
  - Gated: tried only when no Bearer header and no session cookie is present (`hermes_cli/dashboard_auth/middleware.py:174-194`, `:318-325`).
- `Authorization: Bearer <token>`
  - It wins when both are sent (`web-dashboard.md`, "Deployment session-token provider").
  - On the gateway Webapp, this token is one of the two kinds that work on their own as a bearer (`hermes_cli/webapp_sign_in.py:127-141`).

**WebSocket**

- `…/api/ws?token=<token>`.
- Ungated: constant-time compare against the process token (`hermes_cli/web_server_chat.py:357-367`).
- Gated: verified against the session providers (`:311-355`).
- Alternative: mint a ticket with `POST /api/auth/ws-ticket` (§2.4) and use `?ticket=`.

**Which endpoints accept it**

- Every non-public `/api/*` route, and `/api/ws`.
- Ungated: everything under `/api/` except the public list (`hermes_cli/web_server.py:461-478`).
- Gated: everything the gate protects (`middleware.py:281-360`).
- `GET /api/auth/me` returns `user_id:"deployment-token"` for it (`plugins/dashboard_auth/session_token/__init__.py:122-128`; the shape is at `routes.py:551-580`).

**Lifetime.** The session object reports `expires_at = now + 24h` (`_SESSION_HORIZON_SECONDS`, `plugins/dashboard_auth/session_token/__init__.py:40`). The token itself doesn't expire until it is rotated. There is **no refresh and no logout**.

**Errors**

| Case | Response | Source |
|---|---|---|
| Ungated, missing or wrong token | 401 `{"detail":"Unauthorized"}` | `web_server.py:476` |
| Gated, missing token | 401 `{"error":"unauthenticated","detail":"Unauthorized","reason":"no_cookie","login_url":"/login"}` | `middleware.py:110-125`, `:328` |
| Gated, wrong bearer | 401 `{"error":"session_expired","detail":"Unauthorized","reason":"invalid_or_expired_session","login_url":…}` | `middleware.py:118-124`, `:278` |
| WebSocket, bad credential | Close **4401** | `chat_ws.py:109-111` |

#### 2.2b `API_SERVER_KEY` (API server, `:8642`)

**Configuration**

- In `~/.hermes/.env`: `API_SERVER_ENABLED=true`, `API_SERVER_KEY=…`, optionally `API_SERVER_PORT` and `API_SERVER_HOST` (`api-server.md:19-28`, `:826-835`; `environment-variables.md:554-558`).
- The same settings can sit in `gateway.api_server.*` in `config.yaml`. Env vars win (`api-server.md:837-854`).
- **The server refuses to start** when the key is missing, a placeholder, or shorter than 16 characters (`gateway/platforms/api_server.py:3873-3902`).
- `.env.example` at this commit has no `API_SERVER_*` or `HERMES_DASHBOARD_*` lines.

**Header.** Only `Authorization: Bearer <API_SERVER_KEY>` is accepted, compared in constant time (`gateway/platforms/api_server.py:1429-1450`). Named profiles at `/p/<profile>/…` need their own key and fail closed without one (`:1433-1441`).

**Failure** is **401**:

```json
{"error":{"message":"Invalid gateway API key (API_SERVER_KEY)","type":"gateway_auth_error","code":"gateway_auth_failed"}}
```

(`api_server.py:1423-1427`)

**Endpoints.** Everything except `GET /health` and `/v1/health` needs the key.

**This key does not work on the dashboard or `/api/ws`.** I found no code path linking `API_SERVER_KEY` to the dashboard auth providers. ⚠️ UNVERIFIED as a negative: no code was found, but I did not test it.

### 2.3 Mode B: dashboard username and password

**Server configuration.** In `config.yaml` set `dashboard.basic_auth.{username, password_hash|password, secret, session_ttl_seconds}`. The env overrides are `HERMES_DASHBOARD_BASIC_AUTH_USERNAME`, `_PASSWORD`, `_PASSWORD_HASH`, `_SECRET` and `_TTL_SECONDS` (`plugins/dashboard_auth/basic/__init__.py:302-345`; `web-dashboard.md`, "Username/password provider").

**Token lifetimes**

- Access token: default **12 h** (`_DEFAULT_TTL_SECONDS`).
- Refresh token: **30 days** (`_REFRESH_TTL_SECONDS`, `plugins/dashboard_auth/basic/__init__.py:39-40`).
- Both are stateless HMAC tokens. Used refresh tokens stay valid, with no reuse detection (`:129-131`).
- Logout revokes the login through `$HERMES_HOME/dashboard_auth/basic-revoked-sessions.json` (`:185-219`).
- Changing the username, password or secret ends every session (`web-dashboard.md`, "Username/password provider").

There are two ways to log in.

#### 2.3.1 Cookie login: what the browser SPA and Desktop's embedded window do

**Request**

```
POST /auth/password-login
Content-Type: application/json

{"provider":"basic","username":"admin","password":"…","next":""}
```

(body model `_PasswordLoginBody`, `hermes_cli/dashboard_auth/routes.py:458-463`; handler `:466-512`)

**Success** is `200 {"ok":true,"next":"/"}` plus these `Set-Cookie` headers (`routes.py:109-116`; `cookies.py:36-41`, `:106-122`):

| Cookie | Lifetime | Holds |
|---|---|---|
| `hermes_session_at` | Access-token TTL | The access token |
| `hermes_session_rt` | 30 days | The refresh token |
| `hermes_session_provider` | 30 days | Provider name (`"basic"`) |

- All three are `HttpOnly; SameSite=Lax; Path=/`.
- Over HTTPS the names become `__Host-hermes_session_at` (or `__Secure-…` behind a path prefix) and gain `Secure` (`cookies.py:17-18`, `:58-62`).
- On the gateway Webapp each name gets a `_<16 hex>` gateway suffix, and a `hermes_webapp_signin` binding cookie is added (`cookies.py:41`, `:65-67`; `apps/desktop/electron/connection-config.ts:43-47`).
- **Use a real cookie jar** (`net/http/cookiejar`) and don't hard-code the names.

**Using the session**

- Send the cookies on every REST call.
- **Refresh is transparent.** When the access token is missing or expired and the refresh token is valid, the gate rotates the token and re-sets the cookies on that same response (`middleware.py:333-352`, `:222-236`).
- When refresh fails, the response is 401 `{"error":"session_expired",…,"reason":"invalid_or_expired_session"}` and the cookies are cleared (`middleware.py:239-247`).
- Desktop probes liveness by minting a ticket, not by checking cookies (`apps/desktop/electron/main.ts:6764-6790`).

**Errors** (`routes.py:470-499`)

| Status | Detail | When |
|---|---|---|
| 429 | `"Too many login attempts. Try again shortly."` | More than **10 attempts per 60 s per client IP** (`routes.py:405-406`) |
| 404 | `"Unknown provider"` | Unknown or non-password provider |
| **401** | `"Invalid credentials"` | Bad username or password. The same message is used for both (no enumeration oracle) |
| 503 | `"Provider unreachable: …"` | |
| 500 | `"Provider misconfigured"` | |
| 400 | provider mismatch | Only when a native broker cookie is present |

FastAPI wraps these as `{"detail": "<message>"}`.

**CSRF.** There is no CSRF token. Protection relies on SameSite=Lax plus the `Sec-Fetch-Site` and Origin rules in §1.4. A Go client that sends neither header passes.

**Logout:** `POST /auth/logout`.

- It reads the session cookies, revokes them on every provider (best-effort), clears the cookies, and returns **302** to `/login` (`routes.py:515-540`).
- A Go client should not follow the redirect.
- It is a public path (`middleware.py:44-47`), so an expired session can still log out.

#### 2.3.2 Headless native login: password login to bearer tokens, with no browser and no cookies after login

This uses the RFC 8252 broker that the gateway implements for native apps. For a password provider, `/auth/native/authorize` stashes a broker handle in the PKCE cookie and redirects to `/login` (`routes.py:307-355`). `/auth/password-login` then returns the loopback redirect containing a one-time code instead of setting cookies (`routes.py:487-511`, `:128-144`).

**apps/desktop does not use this path for password providers.** It opens an embedded webview instead (`apps/desktop/electron/native-oauth.ts:84-112`; `main.ts:14850-14860`). Its comments claim `/auth/native/authorize` rejects password providers (`native-auth-decisions.ts:219-221`), but the server code at this commit accepts them (`routes.py:297-305`, `:347-351`). ⚠️ UNVERIFIED end to end: not exercised. Fall back to §2.3.1 if it fails.

Steps (keep a cookie jar for steps 1-2 only):

1. **Authorize.** `GET /auth/native/authorize?provider=basic&code_challenge=<S256(verifier)>&code_challenge_method=S256&redirect_uri=http://127.0.0.1:<any-port>/cb&state=<random>`.
   - The `redirect_uri` must be `http://127.0.0.1[:port]/…` or `http://[::1]…`. `localhost`, userinfo and fragments are rejected with 400 (`routes.py:256-291`).
   - Response: `302 Location: /login` with `Set-Cookie: hermes_session_pkce=…` (10 minutes).
   - Don't follow the redirect.
2. **Log in.** `POST /auth/password-login {"provider":"basic","username":…,"password":…}` carrying that PKCE cookie.
   - Response: `{"ok":true,"next":"http://127.0.0.1:<port>/cb?code=<gw_code>&state=<state>"}`.
   - Parse `code` and check `state`. You do not need to run a listener.
   - The code TTL is **120 s** and the pending-authorization TTL is **600 s** (`hermes_cli/dashboard_auth/native_flow.py:28-29`).
3. **Exchange.** `POST /auth/native/token {"code":…,"code_verifier":…}` returns:

   ```json
   {"access_token":"…","refresh_token":"…","token_type":"Bearer","expires_at":<unix>,"provider":"basic","user_id":"…"}
   ```

   (`routes.py:120-126`, `:605-623`). Any failure is 400 `"Invalid or expired authorization code."`
4. **Use.** Send `Authorization: Bearer <access_token>` on REST (`middleware.py:300-307`, `:265-278`).
   - **The gate never refreshes a bearer server-side.** On 401, call refresh yourself (`middleware.py:159-163`; `native-auth-decisions.ts:239-252`).
   - For the WebSocket, mint a ticket (§2.4) or pass `?token=<access_token>` (`web_server_chat.py:311-355`).
5. **Refresh.** `POST /auth/native/refresh {"refresh_token":…,"provider":"basic"}` returns the same body as step 3.
   - It returns 401 `{"error":"session_expired","detail":"Refresh token expired or invalid; start a new sign-in."}` when the token is rejected, and 503 when the provider is unreachable (`routes.py:626-656`).
6. **Logout.** ⚠️ There is no bearer logout.
   - `/auth/logout` revokes only tokens it finds in cookies (`routes.py:517-520`).
   - Desktop never calls `/auth/logout`. Its sign-out is local (no call site in `apps/desktop/electron`).
   - A Go client can discard its tokens, or send them to `/auth/logout` as `hermes_session_at` / `hermes_session_rt` cookies. ⚠️ UNVERIFIED: the cookie names are `__Host-`-prefixed over HTTPS.

#### 2.3.3 OAuth providers (Nous Portal, self-hosted OIDC)

These use the same native broker, with the upstream IdP opened in the system browser and a loopback listener (`apps/desktop/electron/native-oauth-login.ts:70-140`). The Nous access token lasts about 15 minutes, with a 24 h rotating refresh token (`connection-config.ts:29-37`). They are out of scope for v1 but use the same endpoints as §2.3.2.

#### 2.3.4 Identity probe

`GET /api/auth/me` returns `{user_id, email, display_name, org_id, provider, expires_at}` (`routes.py:551-580`).

- Without a session in gated mode it returns 401 `{"detail":"Unauthorized"}`.
- In loopback mode it returns `provider:"loopback"`, `user_id:"local"`.

### 2.4 WebSocket credentials and close codes

`POST /api/auth/ws-ticket`

- Requires a session (cookie, bearer or session-token header).
- Returns `{"ticket":"…","ttl_seconds":30}`. The ticket is **single-use**, valid for **30 s**, and good for one socket (`routes.py:583-602`; `hermes_cli/dashboard_auth/ws_tickets.py:21`, `:52-64`).
- Without a session it returns 401 `{"detail":"Unauthorized"}` (`routes.py:544-548`).
- Mint a new ticket for every connect and every reconnect.

Credentials `/api/ws` accepts (`hermes_cli/web_server_chat.py:204-367`):

| Mode | Accepted |
|---|---|
| Ungated (loopback) | `?token=<process session token>`. On the Webapp, a one-use socket ticket first |
| Gated | `?ticket=` or subprotocol ticket. Or `?token=<provider-verified access token>` (a native bearer or the deployment token). The legacy process token is **rejected** in gated mode |

Close codes, all sent **before** accept, so the HTTP upgrade succeeds and the socket closes immediately:

| Code | Meaning | Source |
|---|---|---|
| **4401** | Credential missing, invalid, expired or already used | `chat_ws.py:109-111` |
| **4403** | Request guard refused (Host/Origin mismatch, non-loopback peer on a loopback bind), or chat disabled | `chat_ws.py:106-108`, `:112-114`; `web-dashboard.md:239` |
| 4403 | A profile-bound ticket names a profile this listener does not serve | `chat_ws.py:688-690` |
| 1013 | A profile-bound ticket reached a listener with no authority | `chat_ws.py:680-682` |

---

## 3. Health, version and instance info

| Call | Auth | Returns |
|---|---|---|
| `GET /api/health` | public | `{"ok":true,"version":"<base>","displayVersion":"<display>","commit":"<sha>","auth_required":bool}` (`hermes_cli/web_routers/status.py:123-134`). Cheap; Desktop's boot probe |
| `GET /api/status` | public, redacted when signed out | `version`, `release_date`, `gateway_running`, `gateway_state`, `overall` (`"ok"`/`"degraded"`), `components`, `auth_required`, `auth_providers`, `auth_flows` (signed-out Webapp subset, `status.py:533-535`). Signed in, it also has `active_sessions`, `active_agents`, `gateway_busy`, `profiles`, `install_id`, `memory`, `disk`, … (`status.py:562-675`) |
| WS `runtime.describe` (canonical) | socket | `{*instance_id, *profile_id, *authority_epoch, *capabilities:[…], *session_create:{sources, parameters}, skin}` (`gateway/session_controls.py:577-590`) |
| WS `ping` (canonical) | socket | `{pong:true}`. Params must be empty (`session_controls.py:570-575`) |
| API server `GET /health` | none | `{"status":"ok","platform":"hermes-agent","version":"<v>"}` (`api_server.py:2270-2273`) |
| API server `GET /v1/capabilities` | bearer | Feature flags and an endpoint map (`api_server.py:2357-2381`) |

`runtime.describe` capabilities are `durable-admission-v1`, `event-replay-v1`, `local-cli-create-v1`, `acp-editor-policy-v1`, `acp-session-mcp-v1`, `clarify-batch-v1`, `plugin-rpc-v1`, `session-client-v1`, `session-transcript-v1`, `session-list-picker-v1` and `session-list-cursor-v1`.

**`/api/status` alone is not proof of a working connection.** The docs say so explicitly (`web-dashboard.md:189-191`).

**Validate a connection in this order:**

1. `GET /api/status` to read the auth shape.
2. Authenticate.
3. Make one authenticated REST call (`GET /api/auth/me`).
4. Open `/api/ws`, wait for `gateway.ready`, and call `runtime.describe` (canonical) or `ping`.

---

## 4. Profiles = agents

**An "agent" is a profile.** A profile is an isolated `HERMES_HOME` with its own config, `.env`, `SOUL.md`, memory, skills and sessions. The docs say: "Every profile on every registered connection is an *agent*" (`website/docs/user-guide/multi-connection-desktop.md:179-187`; `website/docs/user-guide/profiles.md:5-19`).

**Personalities are different.** They are prompt overlays inside a profile (`display.personality`, built-ins such as `helpful` and `concise`; `hermes_cli/personality.py:1-25`). The active one appears as `SessionLiveInfo.personality`.

**`agents.list` is not a persona list.** It returns background processes `{processes}`.

### 4.1 Listing profiles

**REST:** `GET /api/profiles` returns `{"profiles":[…]}` (`hermes_cli/web_routers/profiles.py:760`; serializer `:84-102`). Row fields:

`name`, `path`, `is_default`, `model`, `provider`, `has_env`, `skill_count`, `gateway_running`, `description`, `description_auto`, `display_name`, `bot_title`, `ui_meta`, `distribution_name`, `distribution_version`, `distribution_source`, `has_alias`

`GET /api/profiles/active` returns `{"active","current"}` (`profiles.py:812`).

**WebSocket:** `profiles.list {profile?, include_sessions=true}` returns `{*profiles:[ProfileRow], bot_mode_protocol=true, install_id=""}` (OpenRPC `CanonicalProfilesResult` / `ProfilesListResult`). `ProfileRow` (`tui_gateway/contracts/profiles_vault_complete_foreign_subagents.py:153-170`):

```
*name, *path, is_default=false, model, provider, description="", display_name="", skill_count=0,
previous_names:[str], last_session:{id,title,preview,started_at,last_active,message_count,live_message_count}|null,
worker_session, canonical_session, ui_meta_revisions:{}, ui_meta:{}|null, has_avatar=false
```

**Fields useful for display**

- **Name:** `display_name || name`. `name` is the id; there is no separate id field.
- **Model:** `model` and `provider`.
- **Avatar:** when `has_avatar`, call WS `profiles.get_asset {name, asset:"avatar"}`. It returns `{found, mime, size, data:"data:<mime>;base64,…"}`, or `{found:false}` (`tui_gateway/methods_profiles.py:451-466`). `avatar` is the only supported asset (`:415-421`). I found no REST avatar route.
- **Colour:** there is no first-class field. Desktop's bot roster stores appearance in `ui_meta["hermes-bots"]` as `{title, color, shape, custom, sectionId, …}` (`apps/desktop/src/plugins/hermes-bots/types.ts:66-80`; read at `apps/desktop/src/plugins/hermes-bots/data.ts:358`). ⚠️ This is plugin-owned and untyped in the contract. Treat it as optional.

**Sessions across profiles:** `GET /api/profiles/sessions?limit≤500&offset&order=recent&profile=all|<name>&min_messages&archived&source&exclude_sources` (`profiles.py:397-464`; used by `apps/desktop/src/api/sessions.ts:149-175`).

---

## 5. Sessions

Use **dashboard REST** for reads and metadata edits, as Desktop does (`apps/desktop/src/api/sessions.ts`). Use the **WebSocket** for creating a session, chatting, and live state. Canonical WS equivalents are listed for completeness.

### 5.1 List

**REST:** `GET /api/sessions` (`hermes_cli/web_routers/sessions.py:205-311`). Query parameters:

| Parameter | Values |
|---|---|
| `limit` | Default 20, range 0-100 |
| `offset` | |
| `min_messages` | |
| `archived` | `exclude` \| `only` \| `include` |
| `order` | `created` \| `recent`. Any other value is 400 |
| `source`, `sources` (CSV), `exclude_sources` (CSV) | Source filters |
| `cwd_prefix` | |
| `full` | When false, drops `system_prompt` and `model_config` |
| `profile` | The per-agent filter |

- Response: `{"sessions":[…],"total":N,"limit","offset","storage":{}}`. Pinned rows are always back-filled.
- Desktop calls it as `?limit=40&offset=0&min_messages=0&archived=exclude&order=recent` (`apps/desktop/src/api/sessions.ts:115-134`).

**Row (assembled from the column list).** Rows carry the stored columns of `persistence/conversation/sessions.py:151-164` plus the list extras at `hermes_cli/web_routers/sessions.py:249-258` and `:564-580`. Useful subset:

```json
{
  "id": "20261122_101500_ab12cd",
  "title": "Fix the flaky test",
  "source": "gui",
  "model": "anthropic/claude-sonnet-4.6",
  "started_at": 1795370100.0,
  "ended_at": null,
  "last_active": 1795370700.0,
  "message_count": 14,
  "tool_call_count": 5,
  "input_tokens": 12000,
  "output_tokens": 900,
  "estimated_cost_usd": 0.04,
  "cwd": "/home/u/code/app",
  "git_branch": "main",
  "parent_session_id": null,
  "archived": false,
  "pinned": true,
  "hidden": false,
  "unread": false,
  "preview": "the test in ci fails about 1 in 5 runs…",
  "is_active": false,
  "profile": "default",
  "is_default_profile": true,
  "_lineage_root_id": "20261122_101500_ab12cd"
}
```

**Canonical WS:** `session.list {profile?, scope:'live'|'stored'='live', limit=200, title?, include_hidden=false, workspace?, picker=false, query?, cursor?}` returns `{*sessions:[CanonicalSessionListRow], *scope, next_cursor}`.

- Row: `*session_id, *id, *title, *source, *started_at, *message_count, *running, model, resolved_id, root_title, preview, last_reply, cwd, git_branch, workspace, last_active_at, pinned` (OpenRPC `CanonicalListParams` / `CanonicalSessionListRow`; `tui_gateway/contracts/canonical_sessions.py:179-226`).
- Use `scope:"stored"` for history and `next_cursor` for paging.

**Legacy WS:** `session.list {profile, title, limit, include_hidden}` returns `{sessions:[{id, resolved_id, title, preview, started_at, message_count, live_message_count, source, _lineage_root_id}]}`. It has no offset or cursor. ⚠️ The row fields come from the fork's read of the handler, not re-checked.

### 5.2 Get one session and its history

**REST:** `GET /api/sessions/{id}?profile=` returns the full row plus `profile` and `is_default_profile`. It returns 404 `{"detail":"Session not found"}` when absent (`sessions.py:613-637`).

**REST history:** `GET /api/sessions/{id}/messages?profile&limit≤500&offset&order=oldest|latest&include_compacted=false&inline_images=true` (`sessions.py:803-863`).

- With no limit, it returns the latest 500 in chronological order.
- **Remote clients should pass `inline_images=false`.** Images then become `[image]` placeholders instead of data URIs (`api-server.md:726`).
- Response:

  ```json
  {"session_id","profile","messages":[…],"pagination":{"limit","offset","order","returned"}}
  ```

- Paging helpers: `GET …/timeline?limit≤500&after_row_id` (`sessions.py:866`) and `GET …/messages/around?row_id&limit≤120` (`sessions.py:891`).

**Message row.** The keys are `_WIRE_ROW_KEYS` (`persistence/conversation/messages.py:827-832`):

`id` (int row id), `session_id`, `role`, `content`, `tool_call_id`, `tool_calls`, `tool_name`, `effect_disposition`, `timestamp`, `token_count`, `finish_reason`, `reasoning`, `reasoning_content`, `reasoning_details`, `codex_reasoning_items`, `codex_message_items`, `platform_message_id`, `observed`, `active`, `compacted`, `api_content`, `display_kind`, `display_metadata`, `message_uid`, `absorbed_message_uids`, `tool_call_uids`, `tool_call_uid`

- Optional `_compressed_summary: true`.
- The display projection adds tool labels and hides untyped `[System: …]` scaffold rows (`sessions.py:729-800`).

**Tool-call and tool-result shape.** These are OpenAI-style. The assistant row carries `tool_calls`, and each result is a `role:"tool"` row linked by `tool_call_id`. **Verbatim** from `tests/gateway/test_session_api.py:529-545` (API-server transcript; same storage):

```json
[
  {"role": "user", "content": "search then summarize"},
  {"role": "assistant", "content": "Let me search for that:",
   "tool_calls": [{"id": "call_1", "type": "function",
                   "function": {"name": "web_search", "arguments": "{}"}}]},
  {"role": "tool", "content": "results", "tool_call_id": "call_1", "tool_name": "web_search"},
  {"role": "assistant", "content": "Here is the summary."}
]
```

**WS history (canonical):** `session.transcript {session_id, cursor?, limit=100, order:'oldest'|'latest'='oldest', inline_images=false}` returns `{*session_id, *rows:[StoredTranscriptRow], *next_cursor, *reset, running, …}`.

- `StoredTranscriptRow`: `*role, text, content, tool_call_id, timestamp, row_id, display_kind, display_metadata, name, context, args, labels, reasoning, *row_kind:'message'|'compaction', compacted_messages, truncated` (OpenRPC).
- **This is a display projection.** Tool rows carry `name`, `context` (an 80-character preview), `args` and `labels`. **They have no result `content`**, except for `write_file`, `patch`, `skill_manage` and `clarify` (`tui_gateway/session_history.py:333-344`).
- `tool_calls` are not exposed.
- Use REST `/messages` when you need tool results.

### 5.3 Create

There is **no dashboard REST create.** Create over the WebSocket.

**Canonical:** `session.create`

- Params: `{request_id, source:'gui', profile?, cwd?, model?, provider?, title?, hidden=false, reasoning?, toolsets?, service_tier?, cwd_explicit?, client:{name, version?}?, …}` (OpenRPC `CanonicalCreateParams`; Desktop sends `source:"gui"`, `apps/desktop/docs/canonical-gateway-protocol.md:33-37`).
- Keep `request_id` stable across retries of the same intent.
- Returns `CanonicalSnapshot` (§6.4).

**Legacy:** `session.create`

- Params: `{profile, cols, source, cwd, cwd_explicit, messages, parent_session_id, title, model, provider, reasoning_effort, fast, service_tier, close_on_disconnect=false, hidden=false, follow_profile_config=false, idempotency_key}`.
- Returns `{session_id, stored_session_id, message_count, messages, info:SessionLiveInfo}`.

### 5.4 Rename, pin, archive, mark unread, delete (fenced mutations)

Every REST write needs a **mutation identity** taken from a fresh snapshot:

1. `GET /api/sessions/{id}/mutation-snapshot?profile=` returns `{"session_id","exists","runtime_revision","runtime_generation"}` (`sessions.py:640-656`).
2. Build the identity as `{request_id: <uuid4>, expected_revision: runtime_revision, expected_generation: runtime_generation}`. Include `expected_generation` only when `exists` (`apps/shared/src/session-http-mutations.ts:21-36`).
3. Write:
   - **Rename, pin, archive, unread, hidden:** `PATCH /api/sessions/{id}` with a JSON body. The fields are `title`, `pinned`, `archived`, `unread`, `hidden` and `profile`, plus the identity (`hermes_cli/web_models.py:283-293`; `sessions.py:996-1006`).
     - **Pin exists.** It is a durable "keep" flag that is exempt from auto-archive.
     - Desktop sends `{archived:true, pinned:false}` when archiving (`apps/desktop/src/api/sessions.ts:436-438`).
   - **Delete:** `DELETE /api/sessions/{id}?profile&request_id&expected_revision&expected_generation`. **The identity goes in the query string** (`sessions.py:916-944`; `apps/desktop/src/api/sessions.ts:421-427`). An already-deleted session returns `{"ok":true,"already_absent":true}`.
   - **Bulk delete:** `POST /api/sessions/bulk-delete {"ids","profile"}` (`sessions.py:499`).
4. Refusals (`hermes_cli/web_server_sessions.py:17`, `:247-275`):

   | Status | Code |
   |---|---|
   | 409 | `client_upgrade_required` (no identity) |
   | 409 | `mutation_identity_required` (partial identity) |
   | 409 | `revision_conflict`, `stale_generation`, `session_busy` |
   | 403 | `permission_denied`, `profile_mismatch` |
   | 404 | `not_found` |
   | 400 | `invalid_params` |
   | 503 | `runtime_draining`, `storage_unavailable` |

   - Success is `{"ok":true,…receipt}`. ⚠️ The receipt's inner fields were not traced.
   - Retry the same identity only after an ambiguous failure (timeout). After a definite refusal, take a new snapshot (`session-http-mutations.ts:44-52`).

**Canonical WS alternative:** `session.mutate {session_id, request_id, expected_revision, operation, payload, expected_generation?}` returns `{session_id, operation, revision}`.

- Operations: `rename`, `archive`, `sidebar`, `branch`, `delete`, `rewind`, `reset`, `model`, `reasoning`, `workspace`, `edit_policy`, `compress`, `import` (`tui_gateway/contracts/canonical.py:86-112`).
- Rename is `operation:"rename", payload:{"title":…}` (`apps/desktop/src/api/canonical-protocol.ts:443-444`).
- Pin is `operation:"sidebar", payload:{"pinned":true}` (`tests/gateway/test_session_mutation_coordinator.py:24`).
- `expected_revision` is the snapshot's `revision`.

### 5.5 Search

`GET /api/sessions/search?q=&limit≤100&profile&source&sources&exclude_sources` returns `{"results":[…]}` (`sessions.py:314-430`). ID matches come first, then FTS5 full-text matches, deduped by compression lineage.

- Result fields: `snippet`, `role`, `source`, `model`, `session_started`, `last_active`, `session_id` (the lineage tip), `lineage_root`, `profile`, `is_default_profile`.
- When a summary row exists, also: `id`, `title`, `started_at`, `ended_at`, `is_active`, `message_count`, `tool_call_count`, `input_tokens`, `output_tokens`, `preview`, `parent_session_id`, `archived`.

Canonical alternative: `session.list {scope:"stored", query:"…"}`.

### 5.6 Export

- **REST:** `GET /api/sessions/{id}/export` streams `{…session row…, "messages":[all wire rows]}` (`sessions.py:1011-1043`).
- **Canonical WS:** `session.export {session_id, format="json", redact=false}` returns `{format, filename, content}`.

---

## 6. Sending a message and streaming the reply (`/api/ws`)

### 6.1 Wire format

- **Framing:** one JSON-RPC 2.0 object per WebSocket text frame (`tui_gateway/ws.py:1-4`).
  - Ids may be numbers or strings. Desktop uses numbers; the shared default is `"r<N>"` (`apps/shared/src/json-rpc-channel.ts:235`).
  - ⚠️ Batch arrays are not supported: I found no list handling in `handle_ws`.
  - Bad JSON gets `{"jsonrpc":"2.0","error":{"code":-32700,"message":"parse error"},"id":null}` (`ws.py:700-707`).
- **Events** are notifications with `method:"event"`, built at `tui_gateway/server.py:575-578`:

  ```json
  {"jsonrpc":"2.0","method":"event","params":{"type":"<event>","session_id":"<sid or \"\">","payload":{…},"seq":<int>}}
  ```

  - `seq` is per session and monotonic. Events with no session carry no `seq` (`tui_gateway/event_replay.py:72-96`).
  - On the canonical wire, `params` also carries `replay_epoch`, and during a turn also `authority_epoch`, `execution_generation` and `admission_id` (`gateway/session_events.py:75-81`; `gateway/session_authority.py:1230-1240`).
- **Server-to-client requests** are real JSON-RPC requests whose `id` is the string `"srq-<12 hex>"` (`tui_gateway/server_requests.py:75`, `:96-98`):

  ```json
  {"jsonrpc":"2.0","id":"srq-…","method":"<name>","params":{"session_id":…, …}}
  ```

  - Answer with a JSON-RPC **response** carrying the same `id`. To decline, reply `-32601`.
  - Tell them apart from events by: a string `id` plus a `method` that is not `"event"` (`apps/shared/src/json-rpc-channel.ts:61-62`; `tui_gateway/server.py:564`).
  - **Verbatim** from `tests/tui_gateway/test_protocol.py:261-265`: the server sends `{"jsonrpc":"2.0","id":"srq-…","method":"sudo","params":{"session_id":"s1"}}` and the client replies `{"jsonrpc":"2.0","id":"srq-…","result":{"value":"hunter2"}}`.
- **Errors:**
  - Canonical refusals: `{"code":4001,"message":"<reason>","data":{"reason":"<reason>",…}}`.
  - Unknown method: `-32601` with `data.reason:"unknown_method"`.
  - Storage failure: `5001 storage_unavailable` (`gateway/session_controls.py:327-411`).
  - Handler crash: `-32603`.

### 6.2 Connection handshake and keepalive

1. **Connect** with credentials (§2.4).
2. **The first server frame is `gateway.ready`.** Payload: `{*skin, *change_events:true, *replay_epoch:"<hex>", heartbeat:true, session_authority?:true}` (`tui_gateway/ws.py:657-664`; contract `GatewayReadyPayload`).
   - **`session_authority:true` selects the canonical wire.** Desktop reads it here (`apps/desktop/src/api/client.ts:590-594`).
   - **Verbatim** (`apps/desktop/src/test/ws-gateway-fixture.ts:19-21`): `{"jsonrpc":"2.0","method":"event","params":{"type":"gateway.ready","payload":{"session_authority":true}}}`
3. **On `gateway.ready`, send** `{"jsonrpc":"2.0","id":1,"method":"client.capabilities","params":{"server_requests":true}}` (`apps/shared/src/json-rpc-channel.ts:546-575`).
   - Result: `{server_requests:[…], declines_not_shown:true, canonical_clarify_batch:true}`.
   - **Without this call, every approval, clarify, sudo or secret request for this socket fails fast** (`tui_gateway/methods_voice.py:526-545`).
   - Do not send `desktop_local:true`. It is for local Desktop only.
4. **Canonical only:** call `runtime.describe`.
5. **Heartbeat:** every 15 s send `{"jsonrpc":"2.0","id":"heartbeat-N","method":"gateway.ping","params":{}}`. The server answers `{"jsonrpc":"2.0","result":{"ok":true},"id":"heartbeat-N"}` inline, ahead of the request queue (`ws.py:710-714`; verbatim reply at `tests/tui_gateway/test_tui_gateway_ws.py:167-171`). Desktop treats 45 s with no answer as a dead socket (`json-rpc-channel.ts:176-177`, `:579-651`).
6. **Client timeouts** (Desktop's defaults): request 120 s, `approval.respond` 300 s, connect 15 s (`apps/shared/src/json-rpc-gateway.ts:41-59`).
7. **Ordering:** requests run serially in arrival order. Token events (`message.delta`, `reasoning.delta`, `thinking.delta`) are coalesced over about 33 ms, and order is preserved (`ws.py:105-131`, `:177-218`).

### 6.3 Submit

**Canonical:** `prompt.submit`

- Params: `{*session_id, *text, submission_id?, input_id?, queued=false, attachments:[{*path, *mime}]?, display_kind?, title_preview?, model_override?, profile?, …}` (OpenRPC `CanonicalSubmitParams`, `CanonicalAttachment`).
- Result `AdmissionStatus`: `{*admission_id, *ref:{profile_id, session_id}, *sequence, *status:'queued'|'started'|'unknown'|'terminal', *outcome, *authority_epoch, *execution_generation}`, or `{voice_stopped:true}`.
- **The reply streams as events, not in this result.**

**Legacy:** `prompt.submit`

- Params: `{*session_id, text, queued, interrupted, surface, display_kind, title_preview, …}`.
- Result: `{status:"streaming"|"queued"|"steered"|"redirected", user_row_id, admission_id?, …}` (OpenRPC `PromptSubmitParams` / `PromptSubmitResult`).

**While the agent is busy:**

- Canonical `session.steer {session_id, execution_generation, text}` returns `{status:'queued'|'redirected'|'rejected', text, execution_generation, authority_epoch}`. `session.redirect` takes the same parameters.
- Or send `prompt.submit` with `queued:true`.

### 6.4 Snapshot returned by canonical `session.create` and `session.resume`

`CanonicalSnapshot` (`tui_gateway/contracts/canonical_sessions.py:152-176`):

```
*session_id, *stored_session_id, *messages:[TranscriptMessage], messages_omitted, display_messages,
*message_count, *running, *authority_epoch, *replay_epoch, *last_sequence, *subscription_id,
*revision, *execution_generation, *pending:[CanonicalPendingAdmission], *prompts:[CanonicalPendingPrompt],
*info:CanonicalSnapshotInfo, pending_connection, todo_state, open_requests
```

- `TranscriptMessage` = `*role, text, content, tool_call_id, timestamp, row_id, display_kind, display_metadata, name, context, args, labels, reasoning`.
- **Keep `revision`** for `session.mutate`.
- **Keep `execution_generation`** for interrupt, steer and prompt responses.
- **Keep `subscription_id`** for `session.detach`.

### 6.5 Stream events

Payload schemas come from OpenRPC `x-notifications` (generated from `tui_gateway/contracts/events.py`). Events marked "canonical" are published only by the session authority and are absent from `x-notifications`.

| Event | Payload fields | Notes |
|---|---|---|
| `message.start` | `display_kind, display_text, notification_category, text, turn_id` (all optional) | Turn began. Canonical publishes it at claim time (`gateway/session_authority.py:1240-1242`) |
| `message.delta` | `*text: string\|null, rendered, verbose, turn_id, segment, offset` | Token delta. `text:null` marks a segment boundary. `offset` counts UTF-16 units already sent, for de-duplication (`contracts/events.py:164-178`) |
| `message.interim` | `*text, *already_streamed, turn_id, segment` | Commentary beside tool calls. Skip it when `already_streamed` is true |
| `reasoning.delta` | `*text, rendered, verbose` | Streamed reasoning. Canonical sends it only when the session shows reasoning (`gateway/local_viewer_events.py:50-54`) |
| `reasoning.available` | `*text, rendered, verbose` | A whole reasoning block, from non-streaming providers |
| `thinking.delta` | `*text, …` | Spinner text ("kawaii thinking"). `""` clears it (`local_viewer_events.py:43`). Not model reasoning |
| `tool.generating` | `*name` | The model is emitting a tool call's arguments |
| `tool.start` | `*tool_id, *name, context, args, args_text, preview, labels` | Canonical adds `tool_call_id` and `tool_name` (`gateway/run_turn_progress.py:97-108`) |
| `tool.progress` | `*tool_id, *name, *progress, total, percent, message` | MCP progress |
| `tool.complete` | `*tool_id, *name, args, duration_s, result, summary, result_text, inline_diff, todos, revision, labels, image_path` | Legacy `result` is JSON-parsed when possible (`tui_gateway/tool_progress.py:328-374`). Canonical adds `is_error: bool` and a bounded `result` (`run_turn_progress.py:111-123`) |
| `tool.output_risk` | `tool_id, name, risk, findings, redacted` | Verbatim at `tests/tui_gateway/test_tui_gateway_server.py:1691-1700` |
| `todo.updated` | `*todos, *revision` | |
| `status.update` | `*kind, *text` | `kind` is one of status, lifecycle, compacting, goal, loop, heartbeat, process, … |
| `session.usage` | `usage:Usage`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `context_tokens`, `context_length`, `model`, `provider`, `rate_limit`, … | Mid-turn tick. `message.complete.usage` is authoritative |
| `message.complete` | `text, usage:Usage, status:'complete'\|'error'\|'interrupted', reasoning, warning, response_previewed, response_reused, response_transformed, billing, failure_reason, rendered, error, recoverable, error_surface:{layer,code,retryable,provider,model,resets_at,usage_limit}, partial, persisted_turn, paused` | **Done.** Canonical adds `content` (= text), `admission_id`, and `outcome` (`completed`, `cancelled`, failed, `unknown`) (`gateway/session_settlement_recovery.py:40-92`) |
| `error` | `*message` | Session-level failure outside a turn |
| `notice` | `*message` | |
| `session.info` | `SessionLiveInfo` (model, provider, reasoning_effort, running, title, cwd, personality, usage, …) | Live settings snapshot |
| `session.title` | `*session_id, *title` | Auto-title |
| `sessions.changed` | `{}` | Refetch the session list |
| `notification.show` / `notification.clear` | `*text, *level, *kind, ttl_ms, key, id, detail` / `{key}` | Toasts |
| `approval.request` (canonical) | `CanonicalPendingPrompt` | §7.3 |
| `clarify.request` (canonical) | `CanonicalPendingPrompt` | §7.3 |
| `approval.settled` / `clarify.settled` (canonical) | `{prompt_id, execution_generation}` | Clear the card (`gateway/session_pending_controls.py:60-67`) |
| `approval.cancelled` | `*session_id, *stored_session_id, *reason, *cancelled_count, *request_ids` | |
| `request.cancel` | `*id, *method, *reason` (`timeout`, `interrupted`, `shutdown`, `resolved`, `session_closed`) | Server withdrew a server request (`tui_gateway/contracts/server_requests.py:372-389`) |
| `session.updated` (canonical) | Mutation receipt, carries `revision` | |
| `session.replay_gap` | `{replay_epoch, latest_seq}` | Canonical overflow. Re-snapshot (`gateway/session_events.py:94-100`) |
| `subagent.*`, `moa.*`, `agent.print`, `reaction`, `pet.*`, `voice.*`, … | | Optional. See `x-notifications` |

`Usage` (OpenRPC):

```
model="", input=0, output=0, reasoning=0, prompt=0, completion=0, total=0, calls=0,
compressions, context_used, context_max, context_percent, context_source, context_estimated,
cache_hit_pct, cache_read, cache_write, avg_latency_s, avg_tps, active_subagents,
dev_credits_spent_micros, cost_usd, cost_status, plan_gauge
```

It is built at `tui_gateway/server.py:1270-1300`.

### 6.6 Example stream (fixtures)

**Assembled** from the contracts above. Field names are exact, and the values are invented. It shows a canonical turn as seen on the wire (`session_id` `"s1"`, generation 3):

```json
{"jsonrpc":"2.0","method":"event","params":{"authority_epoch":7,"execution_generation":3,"admission_id":"adm_9f2c","type":"message.start","session_id":"s1","payload":{},"replay_epoch":"c0ffee","seq":41}}
{"jsonrpc":"2.0","method":"event","params":{"authority_epoch":7,"execution_generation":3,"admission_id":"adm_9f2c","type":"reasoning.delta","session_id":"s1","payload":{"text":"Need to list files first."},"replay_epoch":"c0ffee","seq":42}}
{"jsonrpc":"2.0","method":"event","params":{"authority_epoch":7,"execution_generation":3,"admission_id":"adm_9f2c","type":"tool.generating","session_id":"s1","payload":{"name":"terminal"},"replay_epoch":"c0ffee","seq":43}}
{"jsonrpc":"2.0","method":"event","params":{"authority_epoch":7,"execution_generation":3,"admission_id":"adm_9f2c","type":"tool.start","session_id":"s1","payload":{"tool_id":"call-1","name":"terminal","context":"ls","tool_call_id":"call-1","tool_name":"terminal","args":{"command":"ls"}},"replay_epoch":"c0ffee","seq":44}}
{"jsonrpc":"2.0","method":"event","params":{"authority_epoch":7,"execution_generation":3,"admission_id":"adm_9f2c","type":"tool.complete","session_id":"s1","payload":{"tool_id":"call-1","name":"terminal","context":"ls","tool_call_id":"call-1","tool_name":"terminal","args":{"command":"ls"},"is_error":false,"result":"README.md\nsrc\n"},"replay_epoch":"c0ffee","seq":45}}
{"jsonrpc":"2.0","method":"event","params":{"authority_epoch":7,"execution_generation":3,"admission_id":"adm_9f2c","type":"message.delta","session_id":"s1","payload":{"text":"Your project has "},"replay_epoch":"c0ffee","seq":46}}
{"jsonrpc":"2.0","method":"event","params":{"authority_epoch":7,"execution_generation":3,"admission_id":"adm_9f2c","type":"message.delta","session_id":"s1","payload":{"text":"a README and src/."},"replay_epoch":"c0ffee","seq":47}}
{"jsonrpc":"2.0","method":"event","params":{"authority_epoch":7,"execution_generation":3,"admission_id":"adm_9f2c","type":"message.complete","session_id":"s1","payload":{"text":"Your project has a README and src/.","content":"Your project has a README and src/.","admission_id":"adm_9f2c","outcome":"completed","status":"complete","usage":{"model":"anthropic/claude-sonnet-4.6","input":1200,"output":40,"reasoning":12,"prompt":1200,"completion":40,"total":1240,"calls":2}},"replay_epoch":"c0ffee","seq":48}}
```

Verbatim fragments you can use to anchor fixtures:

- **Legacy tool events.** `tests/tui_gateway/test_hosted_room_member_activity_hook.py:59`, `:65`:
  - `("tool.start","room-sid",{"tool_id":"call-1","name":"terminal","args":{"command":"ls"}})`
  - `("tool.complete","room-sid",{"tool_id":"call-1","name":"terminal","result":"ok"})`
- **Legacy turn sequence.** `tests/tui_gateway/test_tui_gateway_server.py:802-805`:
  - `message.start`, then `message.delta {"text":"hi"}`, then `message.complete {"text":"hi","usage":…,"status":"complete"}`, then `session.info`.
- **Replay ring items, which are `params` objects.** `apps/desktop/src/api/canonical-reconnect-replay-owner.test.ts:18-24`:
  - `{type:'message.delta', session_id:'review-chat', seq:1, replay_epoch:'epoch-alpha', payload:{text:'before '}}`
- **Canonical completion with an unknown outcome.** Verbatim payload from `gateway/session_managed_worker.py:788-790`:

  ```json
  {"text":"Worker execution is unknown.","content":"Worker execution is unknown.","admission_id":"…","outcome":"unknown","status":"error"}
  ```

### 6.7 Stop or interrupt

- **Canonical:** `session.interrupt {session_id, execution_generation}`. Use the generation from the snapshot or from the event stamps. Result: `{ref, instance_id, authority_epoch, revision, execution_generation, execution_state:'idle'|'running'|'waiting'|'unknown'|'terminal'}`.
  - Cancel a queued (not yet started) input with `prompt.cancel {session_id, admission_id}`, which returns an `AdmissionStatus`.
  - The turn then ends with `message.complete` whose `status` is `"interrupted"` and `outcome` is `"cancelled"` (`session_settlement_recovery.py:42-46`).
- **Legacy:** `session.interrupt {session_id}` returns `{status:"interrupted"|"not_interrupted", …}`.
- **Pending approvals are dropped on interrupt.** They resolve as deny, and the server emits `approval.cancelled` (contract summary).

### 6.8 Resume and reattach (including a turn that is still running)

**Open the session**

- Canonical: `session.resume {session_id, omit_messages?, defer_history?, display_projection?, profile?}` returns a `CanonicalSnapshot`.
  - `running:true` means a turn is in flight.
  - `prompts` lists the approval or clarify cards still pending.
  - `open_requests` lists unanswered server requests. Re-deliver them to your handlers (`apps/shared/src/json-rpc-channel.ts:454-498`).
- Legacy: `session.resume {session_id, …}` returns `{…, running, inflight:{assistant, streaming, user, …}, pending_approval, open_requests, …}`.

**Live tail.** Resume subscribes this socket. Frames published after the cursor the socket last replayed are written to it before it joins the fan-out (`gateway/session_authority.py:392-406`; `gateway/session_events.py:114-125`).

**Catch up after a reconnect:** `session.events.since {session_id, last_seen:<seq>, replay_epoch?}`.

- Canonical params also accept `last_sequence`.
- Result: `{events:[params objects], latest_seq, truncated, count, epoch, open_requests}`. Canonical adds `last_sequence`, `replay_epoch` and `snapshot_required`.
- If `truncated` or `snapshot_required` is set, or the epoch changed, refetch the history or re-resume rather than applying the events (`json-rpc-gateway.ts:589-660`).
- Dispatch only events whose `seq` is above your watermark (`:721-740`).
- Ring limits: 512 events or 4 MiB per session, 64 sessions, 64 MiB per process (`tui_gateway/event_replay.py:30-36`).

**Disconnect behaviour.**

- Canonical: a viewer leaving never stops a turn (`ws.py:479-483`). Use `session.detach {session_id, subscription_id}` to unsubscribe cleanly.
- Legacy: sessions created with `close_on_disconnect` are reaped. Other sessions are detached and reaped after 20 s (`HERMES_TUI_WS_ORPHAN_REAP_GRACE_S`). A running turn is interrupted only after 600 s with no viewer (`tui_gateway/server.py:146-161`).

⚠️ UNVERIFIED: how a client that attaches **for the first time** mid-turn recovers the deltas emitted before its `last_sequence`. The snapshot has no `inflight` field on the canonical wire. `session.events.since` with `last_seen:0` and the snapshot's `replay_epoch` should return the ring, if it is still retained.

### 6.9 API-key mode alternative: API-server SSE

Use this only when the client has just `API_SERVER_KEY`. It is limited to API-owned sessions (§1.1).

**Create a session:** `POST /api/sessions {title?, model?, provider?, …}` returns 201 `{"object":"hermes.session","session":{…}}`. The id defaults to `api_<unix>_<8hex>` (`api_server.py:2578-2628`).

**Stream a turn:** `POST /api/sessions/{id}/chat/stream {"message":"…"}`. The field `input` is also accepted (`api_server.py:588-596`, `:3078-3200`).

- Frames are `event: <name>\ndata: <json>\n\n`.
- A `: keepalive` comment is sent every 10 s (`:189`, `:211-217`).
- Every payload is stamped with `session_id`, `run_id`, `seq` and `ts` (`:986-992`).

Event order:

1. `run.started {user_message, runtime}`
2. `message.started {message:{id:"msg_…", role:"assistant"}}`
3. During the turn:
   - `assistant.delta {message_id, delta}`
   - `assistant.commentary {message_id, text, already_streamed}`
   - `tool.started` / `tool.completed` / `tool.failed {message_id, tool_name, preview, args}`
   - `tool.progress {tool_name:"_thinking", delta}` (reasoning appears only this way)
   - `approval.request`
4. `assistant.completed {message_id, content, completed, partial, interrupted, …}`
5. `run.completed` | `run.failed` | `run.cancelled`, carrying `{…, messages:[turn transcript], usage, runtime}`
6. `error {message}` on an exception
7. Always `done {}` last

**Stop:** `POST /v1/runs/{run_id}/stop` returns `{"run_id","status":"stopping"}` (`api_server_runs.py:1494-1523`). A client disconnect also interrupts the turn (`api_server.py:3170-3177`).

**Approve:** `POST /v1/runs/{run_id}/approval`.

- Legacy body: `{"choice","request_id"?}`.
- Canonical body: exactly `{"request_id","execution_generation","choice"}` (`api_server.py:1358-1421`; `api_server_authority_runs.py:114-134`).

**Other routes:** list, get, patch, delete, messages and fork under `/api/sessions*`. The list is `{"object":"list","data":[…],"limit","offset","has_more"}` with no sort or profile parameter (`api_server.py:2524-2795`).

**Resumable runs:** `POST /v1/runs` plus `GET /v1/runs/{id}/events` (SSE with `id:` lines, resumable via `Last-Event-ID` or `?last_seq=`). Events are `message.delta`, `tool.started`, `tool.completed`, `reasoning.available`, `approval.request`, `run.completed`, … (`api_server_runs.py:1256-1346`; `api-server.md:537-569`).

---

## 7. Other things a chat UI needs

### 7.1 Models

- **WebSocket:** `model.options {session_id?, refresh=false, explicit_only=false, include_unconfigured=false}` returns `{providers:[…], model, provider}`.
- **REST:** `GET /api/model/options?profile&refresh&include_unconfigured&explicit_only` returns the same shape (`hermes_cli/web_routers/models.py:161`). A provider row has `slug`, `name`, `is_current`, `is_user_defined`, `models`, `total_models`, `capabilities`, `featured_models`, `aliases`, `pricing`, `limit`, `usage` and `source` (`hermes_cli/inventory.py:154`, `:204-208`, `:676-677`).
- `GET /api/model/info` is public (`models.py:113`).
- **Per-session switch:**
  - Canonical: `session.mutate operation:"model"`. ⚠️ The payload keys were not traced.
  - Per turn: `prompt.submit.model_override`.

### 7.2 Attachments

- **Images**, as Desktop does it on the canonical wire:
  1. `POST /api/chat/image-upload {"data_url":"data:image/png;base64,…","filename":"x.png"}` returns `{path, mime_type}` (`hermes_cli/web_routers/files.py:717-745`; `apps/desktop/src/app/session/hooks/use-prompt-actions/upload-server-image.ts:34-47`).
  2. Send `prompt.submit … attachments:[{"path":<path>,"mime":<mime_type>}]`.
  - Alternative: canonical `image.attach_bytes {session_id, content_base64, filename, ext}` returns `{*attached, *mime, path, name, width, height, token_estimate, …}`.
- **Files:**
  1. `POST /api/chat/file-upload` (multipart field `file`, `?profile=`; 16 MiB cap, 413 when over) returns `{"ok":true,"path","size","staged_upload":{install_id, path, profile_home}}` (`hermes_cli/web_routers/uploads.py:22-55`; `hermes_constants.py:27`).
  2. Legacy WS `file.attach {session_id, name, path|data_url|staged_upload}` returns `{attached, name, path, ref_path, ref_text, uploaded}`. Put `ref_text` into the prompt text.
  - PDFs: `pdf.attach {session_id, content_base64|path, first_page, last_page}`.
  - ⚠️ Whether `file.attach` is reachable on a canonical socket depends on legacy fallback (`tui_gateway/ws_legacy_fallback.py:25-60`). Not traced for this method.
- **API server:** inline images only. Files return `400 unsupported_content_type` (`api-server.md:212`, `:944`).

### 7.3 Approvals, clarify and other prompts

**Canonical wire.** The prompt arrives as event `approval.request` or `clarify.request`. The payload is `CanonicalPendingPrompt`: `{*kind:'approval'|'clarify', *prompt_id, *execution_generation, *choices:[str], command, description, edit, question, multi_select, timeout_s}` (`gateway/session_pending_controls.py:18-50`).

- Approval `choices` is `["once","deny"]` plus `"session"` and/or `"always"` when allowed. It is only `["once","deny"]` when an `edit` is attached.
- Answer:
  - `approval.respond {session_id, execution_generation, prompt_id, choice}`
  - `clarify.respond {session_id, execution_generation, prompt_id, answer}` (an empty answer means skipped)
  - Both return `{status:'resolved'|'already_resolved', prompt_id}`.
- The card clears on `*.settled`.

**Verbatim** request and answer from `apps/desktop/src/api/canonical-protocol-wire.test.ts:18`, `:32-33`:

```json
{"jsonrpc":"2.0","method":"event","params":{"type":"approval.request","session_id":"s","payload":{"prompt_id":"p","execution_generation":4,"choices":["once","deny"]}}}
```

The client then sends `approval.respond` with params `{"session_id":"s","prompt_id":"p","execution_generation":4,"choice":"once"}`.

**Legacy wire.** The prompt arrives as a **server request** `approval`.

- Params: `{session_id, request_id, command, description, choices, allow_permanent, allow_session, smart_denied, tool_name, …}`.
- Reply with the JSON-RPC result `{choice, all?}` (`tui_gateway/contracts/server_requests.py:166-196`; `tui_gateway/server.py:698-725`).
- Or call `approval.respond {session_id, choice, request_id?, all?}`, which returns `{resolved:int}`.

**Native server requests on both wires.** Sudo and secret prompts stay native server requests even on the canonical wire (`session_pending_controls.py:1-4`). Batch clarify does too.

| Request | Params | Result |
|---|---|---|
| `clarify` (batch) | `{session_id, questions:[{qid, question, choices?, multi_select}], answers?}` | `{answers:{qid: str\|null}}` |
| `sudo` | `{session_id, command}` | `{value}` |
| `secret` | `{session_id, env_var, prompt, metadata?}` | `{value}`. `""` means skipped |

- Answer any of these by JSON-RPC response, or later with `request.answer {id, result}`, which returns `{status:'ok'|'expired'}`.
- A desktop-only request you can't render (`preview.*`, `terminal.read`, `theme.*`, `tour`, `vault.*`, `window.read`, `setup_beat`): reply with error `-32601`.

### 7.4 Notifications and change signals

- `notification.show` / `notification.clear` for toasts.
- `sessions.changed`, `skills.changed`, `cron.changed`, `projects.changed` and similar mean "refetch".
- `gateway.ready.change_events:true` advertises that these are sent.

### 7.5 Rate limits

| Limit | Response | Source |
|---|---|---|
| Password login: 10 per 60 s per client IP | 429 | `routes.py:405-406` |
| Native authorizations | Per-IP pending cap and store cap (503) | `native_flow.py:101-118` |
| API server concurrent runs (`gateway.api_server.max_concurrent_runs`, default 10) | 429 `{"error":{"message":"Too many concurrent runs (max N)","type":"rate_limit_error","code":"rate_limit_exceeded"}}`, `Retry-After: 1` | `api_server.py:3521-3536` |
| API server store busy | 503, `Retry-After` | |

I found no rate limit on `/api/ws` or `/api/*` REST beyond these.

---

## 8. Recommended client approach

**Surface: the same one `apps/desktop` uses for a remote gateway.**

- Dashboard REST (`/api/*`) handles discovery, profiles, the session list, history, search, export, fenced rename/pin/archive/delete, and uploads.
- One JSON-RPC WebSocket (`/api/ws`) per (connection, profile) handles create, resume, submit, stream, interrupt, approvals and replay (`multi-connection-desktop.md:199-202`).
- Target the canonical wire, the one served by `hermes gateway` / the Webapp. Keep a thin legacy adapter for standalone `hermes dashboard` / `hermes serve`. Branch on `gateway.ready.payload.session_authority`.
- Keep the API server (`:8642`) only as a degraded "API key only" mode.

**Go client checklist**

- Send no `Origin` and no `Sec-Fetch-*` headers.
- Keep the dialled `Host`.
- Use `net/http/cookiejar` for cookie mode.
- WebSocket library: `github.com/coder/websocket` or `gorilla/websocket`, with a 15 s `gateway.ping` heartbeat.
- After `gateway.ready`, send `client.capabilities`.
- Track `seq` and `replay_epoch` per session.
- Implement `-32601` replies for unknown server requests.

**Auth mode 1, token from `.env`** (Desktop's "Session token"):

1. The user pastes `HERMES_DASHBOARD_SESSION_TOKEN`.
2. REST: send `Authorization: Bearer <token>`, or `X-Hermes-Session-Token: <token>`. `X-Hermes-Session-Token` is Desktop's choice and survives proxies that use `Authorization` (`web_server.py:300-311`).
3. WebSocket: `?token=<token>`. Or prefer `POST /api/auth/ws-ticket` with the same header, then `?ticket=`, so the token never appears in a URL.
4. On 401 or close 4401, ask for a new token. Rotation invalidates saved copies (`web-dashboard.md`, "Deployment session-token provider").

**Auth mode 2, username and password:**

1. Check `GET /api/status` for `auth_required:true`, then `GET /api/auth/providers` for a `supports_password` provider.
2. Preferred, cookieless after login: the headless native flow (§2.3.2).
   - Store `access_token`, `refresh_token`, `expires_at` and `provider` in the OS keychain.
   - REST: `Authorization: Bearer`.
   - On 401, `POST /auth/native/refresh` once, then retry. If refresh returns 401 `session_expired`, re-prompt for the password.
   - WebSocket: mint `POST /api/auth/ws-ticket` with the bearer before every dial.
3. Fallback: cookie login (§2.3.1) with a persistent cookie jar.
   - Refresh is automatic on any REST call.
   - WebSocket: mint a ticket with the cookies.
   - Logout: `POST /auth/logout` (don't follow the 302), then drop the jar.

---

## 9. Unconfirmed / open questions

1. ~~**Headless native password flow (§2.3.2).**~~ **Confirmed live** (0.23.3): authorize → password login → `/auth/native/token` returns an access and a refresh token; Hermes Desktop uses it for Dashboard login.
2. **Bearer logout.** `/auth/logout` revokes only cookie-borne tokens. Is there a supported revoke for native bearer tokens? None was found.
3. **First attach mid-turn on the canonical wire.** The snapshot has no `inflight` field. Is replay from `last_seen:0` the intended way to rebuild a partially streamed reply?
4. **Standalone `hermes dashboard` / `hermes serve` speak only the legacy wire.** Confirm that remote instances in practice are gateway-served (canonical), or that the legacy wire must be fully supported.
5. **Legacy-method fallback on canonical sockets.** Exact availability of `file.attach`, `model.options` and `profiles.get_asset` on a remote canonical socket (`tui_gateway/ws_legacy_fallback.py:25-60`) was not traced per method.
6. **Mutation receipts.** The inner fields of the REST PATCH/DELETE receipt (`{"ok":true,…}`), and the payload keys for `session.mutate` operations other than `rename` and `sidebar`.
7. **Avatar and colour.** Profile colour exists only in plugin-owned `ui_meta["hermes-bots"].color`. Is there an intended first-class field?
8. **Legacy `session.list` row fields and ordering** were taken from a delegated read, not re-checked line by line.
9. **TLS.** Can the dashboard or gateway listener terminate TLS itself? I found only the reverse-proxy and Tailscale guidance.
10. **`API_SERVER_KEY` on the dashboard.** I found no path accepting it on `/api/*` or `/api/ws`. This is a negative from search, not a test.
11. **JSON-RPC batch frames** are assumed unsupported.
12. **`client` identity on `session.create`.** `{name, version}` exists in the schema. Its effect, for example on labels, was not traced.
13. **`HERMES_DASHBOARD_SESSION_TOKEN` from `.env`.** The docs say a `.env` line is used unless the parent injected one (`environment-variables.md:580`). The load order before `resolve_session_token()` runs at import was not traced.

---

## 10. Live findings (a real instance, 2026-11-22)

Tested against a remote instance reporting `version 0.23.3` (behind HTTPS on a tailnet). Credentials and the address are kept out of this file.

- **`GET /api/health` and `GET /api/status` answer as documented**: `auth_required:true`, `auth_providers:["basic"]`, `auth_flows:["cookie","native_pkce"]`.
- **A path with a double slash (`//api/health`) is redirected to `/login` (302)**, even for public routes. The client strips the trailing slash from the address the user types (`NormalizeURL`), so this never happens in the app.
- **No `session-token` provider was registered** on this instance, so the "API key" mode cannot work there:
  - `X-Hermes-Session-Token: <key>` → 401 `{"error":"unauthenticated","reason":"no_cookie"}` (the header is not consulted without the provider).
  - `Authorization: Bearer <key>` → 401 `{"error":"session_expired","reason":"invalid_or_expired_session"}`.
  - At tag `v0.23.3` the provider exists (`plugins/dashboard_auth/session_token/__init__.py`) but registers only when `HERMES_DASHBOARD_SESSION_TOKEN` is set in the server's environment. The connect screen now detects `auth_required` without `session-token` and says so, pointing to Dashboard login.
- **`/v1/*` and `/health` are not served by the dashboard listener** (302 to `/login?next=…`); the API server (`:8642`) was not reachable on the same host. A 64-character `API_SERVER_KEY` therefore has nowhere to go at that address.

### 10.1 More live findings

- **The instance speaks the legacy wire** (`gateway.ready` without `session_authority`). Its stream for a command tool call is, in order: `message.start`, `session.title`, `thinking.delta`, `reasoning.delta`…, `tool.generating`, `tool.start`, `session.usage`, `tool.complete`, `message.delta`…, `message.complete` (`internal/hermes/hermestest/testdata/live_legacy_0233.jsonl`, sanitized).
- **`compacted` comes as `0`/`1`**, not a boolean, in `/api/sessions/{id}/messages`; the client accepts either (`hermes.Flag`).
- **8,835 sessions**: a 500-row page of `/api/profiles/sessions` takes 2–3 s, so the app shows a 200-row first page at once, pages the rest in the background, and on later launches stops at the first page with nothing new (a full pass, which also finds deletions, once a day).
- **The runtime id matters on the legacy wire.** `prompt.submit` must name the id `session.resume` returned; the app resumes a chat before its first submit when the chat's own resume has not finished.

### 10.2 Server requests the app answers

| Request | Card | Answer |
|---|---|---|
| `approval` (legacy) | the command, the choices | `{choice}` |
| `sudo` | the command, a masked password field | `{value}` (`""` = skipped) |
| `secret` | the variable and the agent's prompt, a masked field | `{value}` (`""` = skipped) |
| `clarify` (batch) | each question: choice chips (single or multi) or a text field | `{answers: {qid: str|null}, outcome: "submitted"}`; Skip sends `{outcome: "cancelled"}` (no answers = cancel all) |
| `request.cancel` event | the card goes | n/a |
| anything else (`preview.*`, `vault.*`, …) | none | `-32601` |

Passwords and secrets go from the card's field straight to the socket; the form is wiped once sent and nothing is stored or logged.

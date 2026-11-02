# Paprika cloud connector: design

Date: 2026-11-01. Status: approved. Next step: implementation plan.

## Intent

Build one remote MCP server that talks to Paprika's cloud sync service. Claude reaches it as a claude.ai custom connector, so it works the same from Claude web, Claude Desktop, Claude mobile, and Claude Code. No local server and no mobile-to-desktop proxy sit in the Paprika path.

### Why

- **The earlier local design lost data.** A Python server that wrote Paprika's macOS Core Data SQLite store directly created recipes flagged as already synced. On 2026-10-18 Paprika's next sync treated about seven of them as deleted elsewhere and hard-deleted them. That server is gone and is out of scope here.
- **Usage is multi-device.** Anything tied to one Mac's database doesn't serve mobile.
- **The owner prefers Go and minimal maintained code.** No existing project could be adopted as-is, so we fork the best small Go project and own it.

## Goals

- Recipes: search, list by category, read, create, update. Categories are given by name and mapped to Paprika category UIDs. Unknown category names are rejected and the write does not happen.
- Categories: list as a tree with counts.
- Meal plan: list, add, and remove entries, for built-in types including Snacks and for user-created custom types.
- Writes appear on all Paprika devices without manual sync, and survive later syncs.
- Runs on Google Cloud Run, with the recipe cache persisted to a Google Cloud Storage bucket.
- One connector registration serves every Claude client, and only the owner can use it.

## Non-goals

- **Grocery list, pantry, menus, photos.** The owner explicitly excluded groceries.
- **Recipe deletion or trash.**
- **Creating categories.** The owner chose to reject unknown names.
- **Recovering the recipes lost on 2026-10-18.**
- **Upstreaming.** Contributions back to the fork's source are optional courtesy, not a dependency.

## Anti-goals

These make the project a failure even if it technically works:

- Any write path that can silently lose or corrupt data in Paprika.
- Anyone other than the owner reaching the owner's Paprika account through the connector.
- A heavy dependency tree. Each new dependency needs a reason.
- Secrets in the repo, in files on disk, or in logs.
- Request patterns that get the account or IP throttled by paprikaapp.com.

## Decisions made

| Decision | Choice | Reason |
|---|---|---|
| Base project | Fork [joostlensen-web/paprika-3-mcp](https://github.com/joostlensen-web/paprika-3-mcp), MIT | Go, 3.1k LOC, already ships Streamable HTTP, correct category name-to-UID mapping, meal types by `type_uid`, URL-encoded login, rejects unknown categories |
| Rejected: jimternet/paprika-3-mcp | Not used | stdio only, categories broken (treats UIDs as names), meal types limited to three, login not URL-encoded |
| Rejected: bojanrajkovic/mcp-paprika | Not used | TypeScript, ~250 dependencies, 22k LOC, 2.0 still in beta five months on. Used as the **reference spec** for hash and wire format |
| Rejected: Composio | Not used | No Paprika toolkit; would mean storing Paprika credentials with another third party |
| Ownership | Own the fork outright | Upstream has issues disabled, a single 2026 account, no collaboration history |
| Language and SDK | Go, `mark3labs/mcp-go` (fork currently on v1.3.0) | Owner's preferred stack; already in the fork |
| Hosting | Google Cloud Run in a new GCP project owned only by the owner | Owner's default platform; isolates logs, secrets, and IAM from anything shared, including law-practice resources |
| Cache | Persisted to a private GCS bucket | Avoids re-downloading every recipe on each cold start |
| Secrets | Secret Manager, injected as env vars | Never on disk or in the repo |
| Access control | OAuth sign-in with Google, plus an email allowlist containing only the owner | The owner's claude.ai account is a **Team plan** and the owner is admin. Org custom connectors are available to every member, so a secret URL alone would expose the Paprika account to the org |
| Client delivery | claude.ai custom connector, added by the owner as org admin | Syncs to web, desktop, mobile, and Claude Code |

## How it works

Paprika has no public API. Its cloud is the private sync backend its own apps use. The server speaks that protocol on one side and MCP on the other.

```
Claude (web, desktop, mobile, Code)
   │  MCP Streamable HTTP, Authorization: Bearer <token from Google sign-in>
   ▼
Go binary on Cloud Run ── cache ──▶ GCS bucket
   │  sync API calls
   ▼
paprikaapp.com/api/v2/sync/*
   │
   ▼
Paprika apps pull changes on next sync, prompted by /sync/notify
```

### Paprika sync contract essentials

References, in order of trust:

1. bojanrajkovic/mcp-paprika at tag `v1.6.3`: `src/paprika/recipe-hash.ts`, `src/paprika/client.ts`, `src/paprika/types.ts`, `docs/wire-captures/*.har.json` (real mitmproxy captures of the macOS app).
1. [aarons22/paprika-tools API_REFERENCE.md](https://github.com/aarons22/paprika-tools/blob/main/API_REFERENCE.md).
1. [mattdsteele gist](https://gist.github.com/mattdsteele/7386ec363badfdeaad05a418b9a1f30a).

Key points:

- **Login:** `POST https://paprikaapp.com/api/v1/account/login/`, form-encoded `email` and `password`, returns a long-lived JWT used as `Authorization: Bearer` on v2 endpoints. The apps' own v2 login requires a signed license blob and is not usable.
- **Recipe reads:** `GET /v2/sync/recipes/` returns only UID and hash per recipe. `GET /v2/sync/recipe/{uid}/` returns the full record.
- **Recipe writes:** `POST /v2/sync/recipe/{uid}/`, multipart field `data` containing the gzipped JSON of the **full** record. No partial updates. `created` is `YYYY-MM-DD HH:MM:SS`.
- **UIDs:** UIDs this server generates are uppercase UUID4. Existing UIDs vary: app-created recipe UIDs have a non-UUID4 tail, and meal type UIDs are 64 hex characters. Never validate input UIDs as UUID4.
- **Recipe `categories`** is an array of category UIDs, never names.
- **Meals:** `POST /v2/sync/meals/` with a gzipped JSON array. The macOS app posts `uid, recipe_uid, name, date ("YYYY-MM-DD 00:00:00"), type, type_uid, order_flag, deleted`. GET also returns `is_ingredient` and `scale`. Removal is a soft delete. Snacks is a built-in type (`original_type: 3`); custom types have `original_type: null`.
- **After any write:** `POST /v2/sync/notify/` so devices sync promptly.
- **No DELETE verb exists.** Everything is soft.

## Work items

### 1. Fork setup

- Fork into the owner's personal GitHub account, keeping the name `paprika-3-mcp`. The module path becomes `github.com/<owner>/paprika-3-mcp`.
- Copy this design doc into the fork at `docs/superpowers/specs/2026-11-01-paprika-cloud-connector-design.md`.
- CI on every push and PR: `go test -race ./...`, `golangci-lint run`, `gofmt` check. Zero warnings.
- Dependabot for Go modules, GitHub Actions, and the container base image.
- `prek install` with hooks for gofmt and golangci-lint.
- Keep the existing MIT LICENSE and its © 2025 Lucas Stephens copyright; the fork descends from soggycactus/paprika-3-mcp through joostlensen. Add attribution for code or fixtures ported from mcp-paprika (MIT).

### 2. Recipe hash parity

Replace the fork's hash, which is SHA-256 of a key-sorted JSON object in lowercase hex. Port mcp-paprika's verified algorithm:

- Hash input is a JSON **array** of 27 values in alphabetical order of wire key: `categories, cook_time, created, deleted, description, difficulty, directions, hash, image_url, in_trash, ingredients, is_pinned, name, notes, nutritional_info, on_favorites, photo, photo_hash, photo_large, prep_time, rating, scale, servings, source, source_url, total_time, uid`.
- `categories` is the UID list sorted ascending. `hash` is `null`. `deleted` and `in_trash` are always `false`.
- `image_url, photo, photo_hash, photo_large, scale` are emitted as stored, so `null` stays `null`. The Go `Recipe` type must preserve null for these, for example with `*string`, both for hashing and for round-tripping updates.
- Other string fields are `""` when empty.
- Serialize compactly, escape `/` as `\/`, and **do not** HTML-escape. Go's `json.Marshal` escapes `<`, `>`, `&` by default, so use an `Encoder` with `SetEscapeHTML(false)`.
- Go's encoder also always escapes U+2028 and U+2029, which `JSON.stringify` doesn't. Un-escape those two after encoding.
- Output is uppercase hex SHA-256.
- Port mcp-paprika's hash fixtures as table tests. They were generated by Paprika's own framework.
- A wrong hash costs an extra fetch on other devices' next sync, not data. Parity still matters because it proves the write shape matches the app's.

### 3. Auth and retry hygiene toward Paprika

- On HTTP 401, log in again once and retry the request once.
- Keep the fork's single retry layer, with exponential backoff on 429 and 5xx, honoring `Retry-After`.
- Add a global request rate limit; the fork has none. Keep its cap of 3 concurrent recipe downloads and its abort after 5 failed fetches.

### 4. GCS-backed cache

- On startup, load the cache object from GCS, then refresh by hash diff: fetch only recipes whose hash changed, drop recipes no longer listed or in trash.
- Cache contents: full recipe records keyed by UID, plus categories and meal types.
- Write the cache back after any refresh that changed it and after each successful recipe write. Use a GCS generation-match precondition so a stale instance can't overwrite a newer cache.
- A missing or corrupt cache object is not fatal. Fall back to a full fetch.
- Meals are not cached; read them live.

### 5. Meal removal

Add a remove-meal tool. Implement as fetch-then-flag: read the existing meal and post the **full** record with `deleted: true`. Never post a record with zeroed fields. The fork's `Meal` struct has no `deleted` field; add it.

### 6. OAuth sign-in for the connector

Replace the fork's secret-path check entirely. The MCP endpoint is a plain `/mcp` path that requires a valid bearer token whose Google identity is on the allowlist.

**Spike first**, before building, to choose between two designs:

- **Preferred: Google as the authorization server directly.** The server publishes protected-resource metadata (RFC 9728) at `/.well-known/oauth-protected-resource` naming Google as the authorization server. The owner creates a Google OAuth client and enters its client ID and secret in the connector's advanced settings. The server validates each Google access token, for example via Google's tokeninfo endpoint, checks the email against the allowlist, and caches the result briefly. Unknowns to resolve: whether Claude accepts Google's OIDC discovery document as authorization-server metadata, and whether Google tolerates the `resource` parameter Claude may send.
- **Fallback: a thin authorization server inside the binary that brokers to Google sign-in.** It serves its own metadata, `/authorize`, `/callback`, and `/token`, and issues its own tokens after Google confirms an allowlisted email. mspoorendonk/paprika-mcp and mcp-paprika both use this pattern and are references. Prefer stateless signed tokens with the signing key in Secret Manager, so restarts don't force a new sign-in.

Either way:

- Redirect URIs to register: `https://claude.ai/api/mcp/auth_callback` and `https://claude.com/api/mcp/auth_callback`.
- The allowlist holds the owner's Google account email. It is configuration, not a secret.
- A signed-in user not on the allowlist gets a clear 403, never Paprika data.
- No token, code, or Google secret is ever logged.

### 7. Cloud Run deployment

- Container: static Go binary on a distroless base, non-root.
- Dedicated service account with `secretmanager.secretAccessor` on this service's secrets and object read/write on the cache bucket only.
- Secrets: `PAPRIKA_USERNAME`, `PAPRIKA_PASSWORD`, plus the Google OAuth client secret and token-signing key if the fallback OAuth design is chosen.
- Bucket: private, uniform bucket-level access, single region matching the service.
- Region `us-central1`; `min-instances=0` because the GCS cache makes cold starts cheap; `max-instances=1` so only one writer touches the cache and Paprika sees one client.

### 8. Testing without a test Paprika account

There is no throwaway Paprika account. All live testing runs against the owner's real library, so live testing is staged and conservative.

1. **Automated tests never call Paprika.** Use `httptest` servers with payloads taken from mcp-paprika's HAR captures. CI runs only these.
1. **Backup before any live write.** The owner exports the full library from the Paprika Mac app. The builder also takes a read-only JSON snapshot of all recipes and meals through the API, stored outside the repo.
1. **Read-only live checks first:** list, search, read, categories, meal types, meal plan. Then a zero-risk hash check: compute the hash locally for real recipes and compare it with the hash Paprika already stores. Parity should be confirmed this way before any write.
1. **Live writes only on marked test data.** Test recipes are named with a `[TEST]` prefix. Test meals use a far-future date such as 2030-01-05 and a `[TEST]` name.
1. **Cleanup:** test meals are removed with the remove-meal tool. Test recipes are trashed by the owner in the Paprika app, since the server has no delete tool.

### 9. Connector registration and acceptance

The owner adds the connector in claude.ai as org admin, using the service URL and, for the preferred OAuth design, the Google client ID and secret. Acceptance tests:

1. Signing in with the owner's Google account succeeds. Signing in with any other Google account is refused.
1. Create a `[TEST]` recipe with an existing category from Claude web. It appears on the phone within a minute, in the right category.
1. Creating a recipe with an unknown category name is rejected, and nothing is written.
1. Open Paprika on the Mac, let it sync twice, and confirm the test recipe is still there.
1. Update the test recipe's notes from Claude mobile. The edit shows on the Mac, and nothing else on the recipe changes.
1. The hash Paprika returns for the test recipe equals the hash the server computed.
1. Add a Snacks meal and a Dinner meal on the test date, one linked to the test recipe, then remove one. The plan shows the right result on all devices.
1. Restart the service. The first request loads from the GCS cache without re-downloading every recipe, as shown in logs.
1. All tools work from Claude Code through the claude.ai connector.
1. Test data is cleaned up.

## Left to the builder

- Tool names and descriptions, as long as Claude can find categories and meal types by name.
- Cache serialization format and object naming.
- Deployment mechanism: manual `gcloud`, Cloud Build, or GitHub Actions with Workload Identity Federation.
- Google OAuth consent screen type and scopes, as long as the allowlist is enforced by the server.
- Local development credentials, as long as they arrive as env vars from the macOS Keychain or `op run`, never a file. Ask before invoking `op`, which needs biometric approval.
- Go toolchain version and whether to bump dependencies during the fork.
- Logging library and log levels, as long as no credential or token is ever logged.

## Owner actions the builder will need

- Create the GitHub fork, or approve the builder doing it.
- Create the GCP project and attach billing.
- Create the Google OAuth client in Cloud Console with the settings the builder specifies.
- Put `PAPRIKA_USERNAME` and `PAPRIKA_PASSWORD` into Secret Manager without exposing them to the builder.
- Export the Paprika library before the first live write.
- Add the connector in claude.ai admin settings.
- Trash `[TEST]` recipes in the Paprika app after acceptance.

## Risks

- **Paprika can close the door.** If Hindsight Labs retires the v1 login or blocks unknown clients, every third-party Paprika tool stops. Failure is loud, not silent.
- **Terms of service.** Paprika's terms prohibit reverse engineering. The owner accepts this risk for personal use.
- **Last writer wins.** The fork already re-fetches a recipe from Paprika's cloud before updating it. The remaining risk is an edit made on a device that hasn't synced to the cloud yet, which a Claude edit can overwrite.
- **Connector OAuth on mobile.** A public report says the OAuth flow hangs on mobile ([#112](https://github.com/anthropics/claude-ai-mcp/issues/112)). Signing in once on web may cover mobile, since connections are per account. Unverified.
- **Shared egress IPs.** Cloud Run egress shares Google IPs. If Paprika throttles by IP, add Cloud NAT with a static IP.

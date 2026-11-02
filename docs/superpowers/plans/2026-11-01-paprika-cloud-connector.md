# Paprika Cloud Connector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the forked `paprika-3-mcp` into a single remote MCP server on Cloud Run, gated by Google sign-in plus an email allowlist, with hash-correct Paprika writes and a GCS-backed cache.

**Architecture:** One Go binary (`cmd/server`) serves MCP over stateless Streamable HTTP at `/mcp`. An `internal/auth` package validates bearer tokens and enforces the allowlist before any MCP handler runs. `internal/paprika` speaks Paprika's sync API through a transport chain (auth → retry → rate limit). `internal/mcpserver` holds the tools and a recipe cache persisted through `internal/cachestore` to one GCS object.

**Tech Stack:** Go (toolchain per `go.mod`), `github.com/mark3labs/mcp-go`, `golang.org/x/time/rate`, stdlib HTTP for Google OAuth and the GCS JSON API, distroless container, Cloud Run, Secret Manager, GCS.

**Spec:** `docs/superpowers/specs/2026-11-01-paprika-cloud-connector-design.md`. Read it before any task. Where this plan and the spec disagree, the spec wins; stop and report.

## Global Constraints

- **Paprika calls:** automated tests and CI never call `paprikaapp.com`. Tests use `httptest` servers or the existing `roundTripFunc` fakes in `internal/paprika/unit_test.go`.
- **Live writes:** none until the owner confirms a Paprika export (Owner gate C). After that, writes only touch `[TEST]`-prefixed recipes and `[TEST]` meals dated `2030-01-05`.
- **Secrets:** never handle, echo, log, or write the Paprika password, a Google client secret, a signing key, an access token, or an auth code. The owner puts secrets into Secret Manager. Ask before running `op`.
- **gcloud:** every command passes `--configuration=paprika-mcp`. The machine's default configuration belongs to the owner's law practice and must never be used.
- **GitHub:** use CSB (`mcp__csb__*` tools, `csb gh ...`). Ask before bare `gh`. Never push to `main`; one PR per PR group below.
- **Dependencies:** each new module needs a one-line justification in its PR description. Look up the current stable version when adding a module, action, or base image; never assume from memory.
- **Lint:** `go test -race ./...`, `golangci-lint run`, `gofmt -l .` clean. Zero warnings. Don't change linter settings to silence anything.
- **Comments:** Go doc comments only where they add information; no history or ticket references.
- **UIDs:** never validate input UIDs as UUID4. Generated UIDs are uppercase UUID4.
- **Logging:** `log/slog` JSON handler on stderr. Never log request headers, query strings of OAuth endpoints, form bodies, or Paprika response bodies from login.
- **Region and scale:** `us-central1`, `min-instances=0`, `max-instances=1`.
- **Allowlist:** `ALLOWED_EMAILS` is a comma-separated list compared case-insensitively after trimming. It is configuration, not a secret.

## Decisions This Plan Makes Beyond the Spec

The owner should confirm these at plan review.

- **Remove the stdio binary.** Delete `cmd/paprika-3-mcp`, the macOS keychain path, `lumberjack`, `.goreleaser.yaml`, `.github/workflows/release.yaml`, and `vercel.json`. The remote server is the only entry point.
- **Drop MCP resources and the background refresh ticker.** Cloud Run throttles CPU outside requests, so a ticker stalls. The cache refreshes on demand when a tool call finds it older than one minute, which the fork already does. The tools already cover every read the resources offered.
- **Use the GCS JSON API over plain HTTP** instead of `cloud.google.com/go/storage`. That SDK pulls in dozens of modules; two REST calls and a metadata-server token come to about 120 lines.
- **Rate limit at 4 requests/second, burst 3,** across all Paprika calls, retries included. The spec leaves the number open.
- **Deploy with checked-in `gcloud` scripts** (`deploy/setup.sh`, `deploy/deploy.sh`). No CI deploy, no Workload Identity Federation.
- **Log in to Paprika lazily** on the first request rather than at startup. Startup can't fail on Paprika, and 401 re-login uses the same code path.

## PR Groups

| PR | Tasks | Branch |
|---|---|---|
| 1 | Design doc, this plan, Task 1 | `feat/cloud-connector-design` |
| 2 | Task 2 | `feat/google-auth` |
| — | Task 3 (spike, never merged) | `spike/google-oauth` |
| 3 | Task 4, only if the spike fails | `feat/oauth-broker` |
| 4 | Tasks 5–7 | `feat/paprika-client` |
| 5 | Task 8 | `feat/gcs-cache` |
| 6 | Tasks 9–10 | `feat/cloud-run` |
| 7 | Task 11 | `feat/snapshot-tool` |

Each PR branches from `main` after the previous PR merges. The owner merges.

## Owner Gates

Batch each gate into one ask, with the exact settings below filled in.

**Gate A, before Task 3:**

1. Choose the Google account that owns the GCP project and is the only allowlisted email. Use a personal account, not `m@peters.law`.
1. Create GCP project `paprika-mcp-<suffix>` under that account, with no law-practice organization as parent, and link billing.
1. Run in this session: `! gcloud config configurations create paprika-mcp && gcloud auth login <account> && gcloud config set project <project-id>`.
1. Google Auth Platform → Branding: app name `Paprika MCP`, support email = that account. Audience: External, **Publishing status: In production**, because Testing status expires refresh tokens after 7 days. Data access: scopes `openid` and `.../auth/userinfo.email` only. Both are non-sensitive, so Google needs no verification.
1. Clients → Create → Web application `paprika-mcp-connector`. Authorized redirect URIs:
   - `https://claude.ai/api/mcp/auth_callback`
   - `https://claude.com/api/mcp/auth_callback`
   - `https://paprika-mcp-<PROJECT_NUMBER>.us-central1.run.app/oauth/callback` (used only by the fallback design; the project number is on the console dashboard)
1. Send the builder the client ID, which is not a secret. Keep the client secret for the claude.ai connector form.

**Gate B, before Task 11:**

1. Create the secrets without showing them to the builder:
   `gcloud secrets create paprika-username --configuration=paprika-mcp --replication-policy=automatic --data-file=-` (type the value, then Ctrl-D), and the same for `paprika-password`.
1. Fallback design only: create `google-oauth-client-secret` the same way. Then create the signing key with `openssl rand -base64 48 | tr -d '\n' | gcloud secrets create token-signing-key --configuration=paprika-mcp --replication-policy=automatic --data-file=-`.
1. Give the builder the `op://` references for the Paprika username and password, for the local snapshot tool. The builder still asks before each `op run`.

**Gate C, before Task 12:**

1. Export the full library from the Paprika Mac app and confirm the export completed.
1. Add the connector in claude.ai: Admin settings → Connectors → Add custom connector. URL `https://paprika-mcp-<PROJECT_NUMBER>.us-central1.run.app/mcp`. Under Advanced settings, enter the client ID and secret that Task 3's findings name.

## Review Focus

1. **Token for another app.** A valid Google access token for an allowlisted email, issued to a different OAuth client, must get 401, not data. Test in Task 2.
1. **Email edge cases.** `Owner@Example.com` against an allowlist entry `owner@example.com` passes. `email_verified=false` fails. A token without the email scope fails. Test in Task 2.
1. **Text that JSON encoders disagree on.** Recipe text containing `<`, `&`, `/`, U+2028, emoji, `\b`, and a tab must hash exactly like Paprika. Test in Task 5.
1. **Null round trip on update.** A recipe fetched with `photo: null, scale: null, image_url: ""` and updated must post those same values back, and every untouched field must be byte-identical. Test in Task 5.
1. **Deleted or unknown meals.** `list_meals` must skip meals with `deleted: true`. `remove_meal` on an unknown or already-deleted UID must return a tool error and write nothing. Test in Task 7.

---

### Task 1: Repo hygiene, module path, CI

**Files:**
- Modify: `go.mod`, every `.go` import of `github.com/soggycactus/paprika-3-mcp` → `github.com/NeatChaos/paprika-3-mcp`
- Delete: `cmd/paprika-3-mcp/`, `.goreleaser.yaml`, `.github/workflows/release.yaml`, `vercel.json`
- Modify: `Makefile` (`build`, `test`, `lint` targets only), `.gitignore` (drop `.vercel`)
- Create: `.github/workflows/ci.yaml`, `.github/dependabot.yml`, `.golangci.yml`, `.pre-commit-config.yaml`

**Interfaces:**
- Produces: module path `github.com/NeatChaos/paprika-3-mcp`, used by every later task.

- [ ] **Step 1:** Rename the module and imports: `go mod edit -module github.com/NeatChaos/paprika-3-mcp`, then rewrite the imports. Delete the files listed above, then run `go mod tidy`; `lumberjack` must leave `go.mod`.
- [ ] **Step 2:** Run `go build ./... && go test ./...`. Expected: PASS.
- [ ] **Step 3:** Add `.golangci.yml` as `version: "2"` with the default linter set and no disables. Run `golangci-lint run`, then fix every finding in code; `errcheck` on `defer c.notify(ctx)` and `io.Copy` is expected. Expected: `0 issues.`
- [ ] **Step 4:** Add `ci.yaml`. It triggers on `push` and `pull_request` and sets `permissions: contents: read`. Steps: `actions/checkout` with `persist-credentials: false`, `actions/setup-go` with `go-version-file: go.mod`, `gofmt -l .` failing when its output is non-empty, `go test -race ./...`, and `golangci/golangci-lint-action`. Pin every action to a full commit SHA with a version comment.
- [ ] **Step 5:** Add `dependabot.yml` covering `gomod` and `github-actions`, weekly, each with `cooldown: default-days: 3` and a single group per ecosystem. The `docker` ecosystem is added in Task 9 alongside the Dockerfile.
- [ ] **Step 6:** Add `.pre-commit-config.yaml` with local hooks `gofmt -l` and `golangci-lint run`, then run `prek install`. Run `actionlint .github/workflows/` and `zizmor .github/workflows/`. Expected: no findings.
- [ ] **Step 7:** Commit `chore: own the fork; remove stdio and Vercel paths; add CI`.

### Task 2: Google bearer-token auth (`internal/auth`)

**Files:**
- Create: `internal/auth/google.go`, `internal/auth/middleware.go`, `internal/auth/metadata.go`
- Test: `internal/auth/google_test.go`, `internal/auth/middleware_test.go`

**Interfaces:**
- Produces:
  - `type Verifier interface { Verify(ctx context.Context, token string) (email string, err error) }`
  - `var ErrInvalidToken, ErrNotAllowed error`
  - `type Allowlist map[string]bool`; `func ParseAllowlist(csv string) (Allowlist, error)` (errors when empty); `func (a Allowlist) Allows(email string) bool`
  - `func NewGoogleVerifier(clientID string, allow Allowlist, tokeninfoURL string, hc *http.Client, now func() time.Time) *GoogleVerifier`, where `GoogleVerifier` implements `Verifier`
  - `func Middleware(v Verifier, resourceMetadataURL string, next http.Handler) http.Handler`
  - `func ProtectedResourceHandler(resource string, authorizationServers []string, scopes []string) http.Handler`
  - `const GoogleTokeninfoURL = "https://oauth2.googleapis.com/tokeninfo"`

- [ ] **Step 1: Write failing tests.** Run them against an `httptest` tokeninfo server.
  - `TestGoogleVerifierAcceptsAllowlistedToken`: tokeninfo returns `{"aud":"cid","azp":"cid","email":"Owner@Example.com","email_verified":"true","exp":"<now+3600>","scope":"openid https://www.googleapis.com/auth/userinfo.email"}`, allowlist `owner@example.com`. The verifier returns `owner@example.com` and nil.
  - `TestGoogleVerifierRejectsOtherClient`: `aud` and `azp` are both `"other"`. Expect `errors.Is(err, ErrInvalidToken)`.
  - `TestGoogleVerifierRejectsUnverifiedEmail`: `email_verified` is `"false"`. Expect `ErrInvalidToken`.
  - `TestGoogleVerifierRejectsMissingEmail`: no `email` field. Expect `ErrInvalidToken`.
  - `TestGoogleVerifierRejectsNotAllowlisted`: email `stranger@example.com`. Expect `ErrNotAllowed`.
  - `TestGoogleVerifierTokeninfoErrors`: tokeninfo answers 400, then 500, then a closed server. Expect `ErrInvalidToken` for the 400. Expect a non-nil error that is not `ErrNotAllowed` for the 500 and the closed server.
  - `TestGoogleVerifierCachesResult`: two `Verify` calls with the same token produce one tokeninfo hit. After the fake clock advances 6 minutes, a third call produces a second hit.
  - `TestGoogleVerifierSendsTokenInBody`: the fake asserts `r.Method == POST`, the token arrives as form field `access_token`, and `r.URL.RawQuery == ""`.
  - `TestMiddleware`: a missing header gets 401 with `WWW-Authenticate: Bearer resource_metadata="<url>"`. A malformed header (`Basic x`) gets 401. A verifier returning `ErrInvalidToken` gets 401. `ErrNotAllowed` gets 403 with body `this Google account is not allowed`, and `next` is not called. Any other error gets 503. On success `next` is called.
  - `TestProtectedResourceHandler`: GET returns JSON with `resource`, `authorization_servers`, `scopes_supported`, and `bearer_methods_supported: ["header"]`.
- [ ] **Step 2:** Run `go test ./internal/auth/`. Expected: FAIL, undefined symbols.
- [ ] **Step 3:** Implement. `GoogleVerifier` POSTs form `access_token=<token>` to tokeninfo, accepts when `aud` or `azp` equals `clientID`, and requires `email_verified == "true"`. It caches by `sha256(token)` until the earlier of `exp` and `now+5m`, and prunes expired entries on insert. Middleware sets the verified email in the request context via `func EmailFromContext(ctx context.Context) (string, bool)`.
- [ ] **Step 4:** Run `go test -race ./internal/auth/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(auth): Google bearer-token verification with email allowlist`. Open PR 2.

### Task 3: OAuth spike (Google as authorization server directly)

Requires Owner gate A. Branch `spike/google-oauth` from Task 2's branch. Never merged.

**Files:**
- Create (spike branch only): `cmd/oauthspike/main.go`, `cmd/oauthspike/Dockerfile`
- Create (on `main`, via a docs PR): `docs/superpowers/specs/2026-11-01-oauth-spike-findings.md`

**Interfaces:**
- Consumes: everything Task 2 produces.

- [ ] **Step 1:** Write `cmd/oauthspike/main.go`. It reads `GOOGLE_CLIENT_ID`, `ALLOWED_EMAILS`, `PUBLIC_URL`, and `PORT`, and serves:
  - `/mcp`: `auth.Middleware`, then a stateless mcp-go Streamable HTTP server with one tool, `whoami`, which returns `EmailFromContext`.
  - `/.well-known/oauth-protected-resource` and `/.well-known/oauth-protected-resource/mcp`: `ProtectedResourceHandler(PUBLIC_URL+"/mcp", ["https://accounts.google.com"], ["openid","email"])`.
  - Every request logs method, path, status, and the names of query parameters. It never logs values.
- [ ] **Step 2:** Enable `run.googleapis.com`, `cloudbuild.googleapis.com`, and `artifactregistry.googleapis.com`. Deploy with `gcloud run deploy paprika-mcp --source cmd/oauthspike --region us-central1 --allow-unauthenticated --max-instances 1 --set-env-vars ... --configuration=paprika-mcp`. The service name matches the final service so the redirect URI from Gate A stays valid.
- [ ] **Step 3:** Ask the owner to add the connector in claude.ai with the Google client ID and secret. Then observe from logs and the owner's report:
  1. Does Claude fetch the PRM, then discover Google's metadata from `/.well-known/oauth-authorization-server` or `/.well-known/openid-configuration`?
  1. Does Google accept the authorize request, including any `resource` parameter Claude sends?
  1. Does `whoami` return the owner's email? Does a second Google account get 403?
  1. Does `whoami` still work more than 65 minutes later without a manual reconnect? Google issues refresh tokens only for `access_type=offline`, which Claude may not send.
  1. Does it work from Claude mobile after the web sign-in?
  1. Does the connector form accept a client ID with no secret? The fallback needs to know.
- [ ] **Step 4:** Write the findings doc with a yes or no and evidence for each question. Its verdict: **preferred** only when questions 1–5 all pass, otherwise **fallback**. Commit it on a docs branch and open a PR.
- [ ] **Step 5:** Delete the spike branch. Leave the Cloud Run service running; Task 10 replaces its image.

### Task 4: Fallback authorization broker (`internal/auth/broker`)

Execute only if Task 3's verdict is **fallback**. Otherwise skip it and use `GoogleVerifier` in Task 9.

**Files:**
- Create: `internal/auth/token.go`, `internal/auth/broker.go`
- Test: `internal/auth/token_test.go`, `internal/auth/broker_test.go`

**Interfaces:**
- Consumes: `Allowlist`, `GoogleVerifier`, `Verifier`, `ErrInvalidToken`, `ErrNotAllowed` from Task 2.
- Produces:
  - `type Claims struct { Type string; Email, ClientID, RedirectURI, CodeChallenge, State string; ID string; Expires time.Time }`, where `Type` is one of `"flow"`, `"code"`, `"access"`, `"refresh"`
  - `func Sign(key []byte, c Claims) string` and `func Parse(key []byte, token string, wantType string, now time.Time) (Claims, error)`. The format is `base64url(json(claims)) + "." + base64url(HMAC-SHA256(key, first part))`, compared in constant time.
  - `type BrokerConfig struct { PublicURL, ClientID, GoogleClientID, GoogleClientSecret string; SigningKey []byte; Allow Allowlist; GoogleAuthURL, GoogleTokenURL, TokeninfoURL string; HTTPClient *http.Client; Now func() time.Time }`
  - `func NewBroker(cfg BrokerConfig) (*Broker, error)`, which errors when the key is under 32 bytes or any field is empty
  - `func (b *Broker) Routes(mux *http.ServeMux)`, which registers `/.well-known/oauth-authorization-server`, `/authorize`, `/oauth/callback`, and `/token`
  - `(*Broker)` implements `Verifier` and accepts only `Type == "access"`

- [ ] **Step 1: Write failing tests** against fake Google authorize, token, and tokeninfo servers.
  - `TestTokenRoundTrip` covers a valid token, a tampered payload, a tampered MAC, an expired token, and the wrong `wantType`, for example a refresh token presented as an access token. Each failure case returns `ErrInvalidToken`.
  - `TestAuthorizeRejects` covers an unknown `client_id`, a `redirect_uri` outside the two claude callbacks, a missing `code_challenge`, and `code_challenge_method=plain`. Each gets 400, and none redirects.
  - `TestFullFlow`: `/authorize` returns a 302 to Google with `scope=openid email` and a signed flow `state`. `/oauth/callback?code=g&state=<flow>` exchanges `g` and calls tokeninfo, then returns a 302 to the claude callback carrying `code` and the original `state`. `/token` with `grant_type=authorization_code`, the correct `code_verifier`, and the matching `redirect_uri` returns `access_token`, `refresh_token`, `token_type: "Bearer"`, and `expires_in: 3600`. `b.Verify(access)` returns the email.
  - `TestTokenRejects` covers a wrong `code_verifier`, a mismatched `redirect_uri`, a reused code, a code older than 60 s, and a refresh token presented as a code. Each gets 400 with `{"error":"invalid_grant"}`.
  - `TestCallbackNotAllowlisted`: tokeninfo returns a non-allowlisted email. The response is 403 and does not redirect to Claude.
  - `TestRefreshRechecksAllowlist`: a refresh issued to `a@x` after `a@x` is removed from the allowlist gets 400 `invalid_grant`.
- [ ] **Step 2:** Run `go test ./internal/auth/`. Expected: FAIL.
- [ ] **Step 3:** Implement. Lifetimes: flow 10 min, code 60 s, access 1 h, refresh 30 days. A refresh issues a new pair. Track used code `ID`s in an in-memory map pruned at expiry; `max-instances=1` makes that sufficient. Token client auth follows the Task 3 findings, question 6: either `none` (PKCE only), or a `client_secret_post` check against `OAUTH_CLIENT_SECRET` in constant time. Implement exactly one and advertise it in the metadata.
- [ ] **Step 4:** Run `go test -race ./internal/auth/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(auth): OAuth broker issuing signed tokens after Google sign-in`. Open PR 3.

### Task 5: Recipe model and hash parity

**Files:**
- Create: `internal/paprika/hash.go`, `internal/paprika/testdata/recipe_hashes.json`, `internal/paprika/testdata/recipe_wire.json`, `THIRD_PARTY_NOTICES.md`
- Modify: `internal/paprika/client.go` (`Recipe` struct; replace `updateHash`, `asMap`; delete `DeleteRecipe`)
- Test: `internal/paprika/hash_test.go`

**Interfaces:**
- Produces:
  - `Recipe` fields `ImageURL, Photo, PhotoHash, PhotoLarge, Scale, PhotoURL` become `*string`. New field `Deleted bool` with tag `json:"deleted"`. All other fields keep their names.
  - `func (r Recipe) ComputeHash() (string, error)`
  - `SaveRecipe` sets `recipe.Hash` from `ComputeHash` before posting.

- [ ] **Step 1: Build fixtures.** Convert `src/paprika/__fixtures__/recipe-hashes.ts` from mcp-paprika v1.6.3, all 8 cases, to `testdata/recipe_hashes.json` as `[{"name","recipe":{<snake_case wire keys>},"expected_hash"}]`. Keep `null` as `null`. From `docs/wire-captures/writes.har.json`, take one recipe POST body (decode the gzip) and save it as `testdata/recipe_wire.json`. Write the conversion as a throwaway script in the scratchpad, not in the repo. Add `THIRD_PARTY_NOTICES.md` with mcp-paprika's MIT notice (© 2026 Bojan Rajkovic), naming the ported algorithm and fixtures. Add a one-line header comment in `hash.go` pointing to it.
- [ ] **Step 2: Write failing tests.**
  - `TestComputeHashFixtures`: a table over `recipe_hashes.json`, asserting `ComputeHash() == expected_hash`.
  - `TestComputeHashInvariants`: the result matches `^[0-9A-F]{64}$`. It ignores `Hash`. It is independent of category order. It ignores `InTrash` and `Deleted`. A `nil` `Scale` and a `""` `Scale` hash differently.
  - `TestHashSerialization`: test the unexported `hashInput(r Recipe) ([]byte, error)`. Set `Name` to the Go expression `"a<b>&c/d" + string(rune(0x2028)) + "e😀\bf\tg"`. The output contains the bytes `"a<b>&c\/d`, then the raw UTF-8 encoding of U+2028 (`E2 80 A8`), then `e😀\bf\tg"`. In other words: `<>&` stay raw, `/` becomes `\/`, U+2028 is not escaped, and backspace and tab use the short escapes `\b` and `\t`.
  - `TestRecipeWireRoundTrip`: unmarshal `recipe_wire.json` into `Recipe` and marshal it again. The two JSON objects are equal by key and value, compared via `map[string]any`; `null` stays `null`.
  - Update `TestSaveRecipe` in `unit_test.go`. The uploaded record has `"photo":null,"scale":null` when those fields are nil. Its `hash` equals `ComputeHash()` of the posted record.
- [ ] **Step 3:** Run `go test ./internal/paprika/`. Expected: FAIL.
- [ ] **Step 4:** Implement `hashInput` as the 27-element `[]any` in the spec's order. Encode it with `json.NewEncoder` and `SetEscapeHTML(false)`, trim the trailing newline, and replace `/` with `\/`. Then replace the six-character escapes Go emits for U+2028 and U+2029 (backslash + `u2028`, backslash + `u2029`) with the raw characters. `ComputeHash` is uppercase hex SHA-256 of that output. If `TestRecipeWireRoundTrip` shows extra keys, add those fields to `Recipe` with types matching the capture. Then update callers in `internal/mcpserver` for the pointer fields.
- [ ] **Step 5:** Run `go test -race ./...`. Expected: PASS.
- [ ] **Step 6:** Commit `feat(paprika): port Paprika's recipe hash and preserve nullable fields`.

### Task 6: Lazy login, 401 re-login, Retry-After, global rate limit

**Files:**
- Create: `internal/paprika/transport.go` (move `retryTransport` and `roundTripper` here)
- Modify: `internal/paprika/client.go` (`NewClient`, `login`)
- Test: `internal/paprika/transport_test.go`; update `unit_test.go`'s `newFakeClient`

**Interfaces:**
- Produces:
  - `type Config struct { Username, Password, Version string; Logger *slog.Logger; Transport http.RoundTripper; RequestsPerSecond float64; Burst int }`. A nil `Transport` uses the fork's TLS 1.2 transport. A zero rate defaults to 4/s with burst 3.
  - `func NewClient(cfg Config) (*Client, error)`. It makes no network call, and errors on an empty username or password.
  - `var ErrUnauthorized error`, returned when Paprika answers 401 after a re-login.
  - Dependency: `golang.org/x/time/rate`, maintained by the Go team and needed for a token-bucket limiter.

- [ ] **Step 1: Write failing tests** using a fake `Transport` that records requests.
  - `TestLazyLogin`: `NewClient` makes 0 requests. The first `ListRecipes` makes the login request, then the GET with `Authorization: Bearer t1`.
  - `TestReloginOn401`: the GET answers 401 once and then 200. The sequence is login, GET (401), login, GET (200 with `Bearer t2`). The body of a POST retried after a 401 is identical to the original.
  - `TestReloginOnlyOnce`: the GET always answers 401. Expect exactly 2 logins, an error satisfying `errors.Is(err, ErrUnauthorized)`, and no further retries.
  - `TestConcurrent401SingleLogin`: three concurrent GETs all answer 401 on token `t1`. Expect exactly one re-login.
  - `TestRetryAfter`: a 429 with `Retry-After: 2` waits at least 2 s under a fake sleep, then retries. A `Retry-After` above 30 returns the 429 response without waiting.
  - `TestRateLimit`: with `RequestsPerSecond: 1000, Burst: 1`, 5 requests make 5 limiter waits. Inject the limiter's wait func, or assert elapsed time ≥ 4 ms.
  - `TestLoginErrorOmitsSecrets`: login answers 403 with body `{"error":"bad"}`. The returned error string and all captured log output contain neither the password nor the body.
- [ ] **Step 2:** Run `go test ./internal/paprika/`. Expected: FAIL.
- [ ] **Step 3:** Implement the chain, outermost first:
  1. `authTransport` adds the bearer token and handles 401. It holds the token under a mutex. On 401 it re-logs in unless the token already changed since the request started, then replays the request once via `req.GetBody`. The login request bypasses it.
  1. `retryTransport` is the fork's, plus `Retry-After` as seconds, capped at 30.
  1. A rate limiter calls `limiter.Wait(req.Context())` on every attempt.
  1. The header round tripper.
  1. The base transport.
  Remove the `getResult`/`postV2` error logging that echoes response bodies on login. Keep the bodies on sync errors, which contain no secrets.
- [ ] **Step 4:** Run `go test -race ./internal/paprika/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(paprika): lazy login, single 401 re-login, Retry-After, rate limit`.

### Task 7: Meal removal

**Files:**
- Modify: `internal/paprika/meals.go` (add `Deleted bool` with tag `json:"deleted"` to `Meal`), `internal/mcpserver/meals.go`, `internal/mcpserver/server.go` (tool registration)
- Test: `internal/mcpserver/server_test.go`, `internal/paprika/unit_test.go`

**Interfaces:**
- Consumes: `Client.ListMeals`, `Client.SaveMeal`.
- Produces: tool `remove_meal` with a required string argument `uid` and description `Remove one meal from the Paprika meal planner by its meal uid (see list_meals). The recipe itself is not affected.` `list_meals` output includes `meal uid: <uid>` for every meal.

- [ ] **Step 1: Write failing tests.**
  - `TestRemoveMeal`: the fake account has meal `M1` with every field set. After `remove_meal` with `uid=M1`, `SaveMeal` receives a record equal to `M1` except `Deleted == true`. The tool text contains the meal's name and date.
  - `TestRemoveMealUnknownOrDeleted`: `uid=NOPE`, and a meal already marked `Deleted`. Both return a tool error, and `SaveMeal` is not called.
  - `TestListMealsSkipsDeleted`: a deleted meal on the listed date is absent from the output.
  - `TestListMealsShowsMealUID`.
  - `TestSaveMealWireShape` in `paprika`: the uploaded array element has keys `uid, recipe_uid, name, date, type, type_uid, order_flag, deleted, scale, is_ingredient`. Check those key names against the meal POST in mcp-paprika's `docs/wire-captures/meals.har.json`. If the capture omits `scale` or `is_ingredient` on POST, keep them anyway, because GET returns them and the full-record rule applies.
- [ ] **Step 2:** Run `go test ./...`. Expected: FAIL.
- [ ] **Step 3:** Implement `(*Server).removeMeal`, which calls `ListMeals`, finds the meal by exact UID, sets `Deleted = true`, and calls `SaveMeal` with the full record. Filter `Deleted` in `formatMeals`. Also check the HAR for what the app sends as `type` for a custom meal type (`original_type: null`). Make `MealType.OriginalType` an `*int`, and set `Meal.Type` to match the capture.
- [ ] **Step 4:** Run `go test -race ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: remove_meal tool; skip deleted meals`. Open PR 4 covering Tasks 5–7.

### Task 8: GCS-backed cache

**Files:**
- Create: `internal/cachestore/store.go`, `internal/cachestore/gcs.go`
- Modify: `internal/mcpserver/cache.go` (replace file persistence; cache meal types), `internal/mcpserver/server.go` (remove `Start`, the ticker, `addResources`, `addRecipeResource`), `internal/mcpserver/meals.go` (resolve meal types from the cache)
- Test: `internal/cachestore/gcs_test.go`, `internal/mcpserver/server_test.go`

**Interfaces:**
- Produces:
  - `type Store interface { Load(ctx context.Context) (data []byte, generation int64, err error); Save(ctx context.Context, data []byte, ifGeneration int64) (generation int64, err error) }`
  - `var ErrNotFound, ErrPrecondition error`. On `Save`, `ifGeneration == 0` means the object must not exist yet.
  - `func NewGCS(bucket, object string, hc *http.Client, endpoint string, token func(context.Context) (string, error)) *GCS`, where the endpoint defaults to `https://storage.googleapis.com`
  - `func MetadataToken(hc *http.Client) func(context.Context) (string, error)`, which calls `http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token` with `Metadata-Flavor: Google` and caches until 60 s before expiry
  - `NewServerOptions` becomes `{ Version string; Paprika *paprika.Client; Logger *slog.Logger; Store cachestore.Store }`. A nil `Store` means memory only.
  - Cache object `recipe-cache.json` with the shape `{"version":1,"recipes":{},"hashes":{},"categories":[],"meal_types":[]}`

- [ ] **Step 1: Write failing tests.**
  - `TestGCSLoad`: 200 with `x-goog-generation: 7` returns the data and 7. 404 returns `ErrNotFound`. The request is `GET /storage/v1/b/<bucket>/o/<object>?alt=media` with a bearer token.
  - `TestGCSSave`: the request is `POST /upload/storage/v1/b/<bucket>/o?uploadType=media&name=<object>&ifGenerationMatch=7`. A 200 response body `{"generation":"8"}` returns 8. A 412 returns `ErrPrecondition`.
  - `TestMetadataTokenCaches`: two calls produce one hit, and the `Metadata-Flavor` header is present.
  - `TestCacheLoadsFromStore`: the store holds a v1 blob with 2 recipes and matching hashes. Startup fetches 0 recipes from Paprika when `ListRecipes` reports the same hashes.
  - `TestCacheCorruptOrWrongVersion`: the blob is `{nope` or `"version":2`. Expect a full fetch and no error.
  - `TestCacheSavesOnlyWhenChanged`: a refresh with no hash changes makes 0 saves. One changed recipe makes 1 save with `ifGeneration` equal to the last loaded or saved generation.
  - `TestCachePutSaves`: after `put`, 1 save.
  - `TestCachePreconditionReloads`: `Save` returns `ErrPrecondition`. The next `ensureFresh` calls `Load` before refreshing from Paprika, and nothing overwrites the newer object in the meantime.
  - Delete `TestRecipeCachePersistsToDisk`.
- [ ] **Step 2:** Run `go test ./...`. Expected: FAIL.
- [ ] **Step 3:** Implement. `newRecipeCache` calls `Load` with a 10 s timeout. A loaded cache counts as loaded but stale, as in the fork. `refresh` also fetches meal types. Saves run outside `mu`, serialized by a `saveMu`, and the cache tracks `generation`. `addMeal` and `listMeals` read meal types from the cache via `snapshot`, which now returns them.
- [ ] **Step 4:** Run `go test -race ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: GCS-backed recipe cache; drop resources and background ticker`. Open PR 5.

### Task 9: Server entry point and container

**Files:**
- Modify: `cmd/server/main.go`, `cmd/server/main_test.go`
- Create: `Dockerfile`, `.dockerignore`; add the `docker` ecosystem to `.github/dependabot.yml`
- Modify: `README.md` (rewrite for this deployment; keep the credit to Lucas Stephens and Joost Lensen)

**Interfaces:**
- Consumes: `auth.*` from Task 2, `auth.NewBroker` if Task 4 ran, `paprika.NewClient(Config)`, `cachestore.NewGCS`, `cachestore.MetadataToken`, `mcpserver.NewServer`.
- Produces: `func newHandler(cfg config, deps handlerDeps) http.Handler` and `func loadConfig(getenv func(string) string) (config, error)`.
  - Env vars: `PAPRIKA_USERNAME`, `PAPRIKA_PASSWORD`, `PUBLIC_URL`, `ALLOWED_EMAILS`, `GOOGLE_CLIENT_ID`, `CACHE_BUCKET` (optional; empty means memory only), `PORT` (default 8080).
  - Fallback design only: `GOOGLE_CLIENT_SECRET`, `TOKEN_SIGNING_KEY`, `OAUTH_CLIENT_ID`.

- [ ] **Step 1: Write failing tests.**
  - `TestLoadConfig` covers each required var missing (the error names the var and never its value), a `PUBLIC_URL` that isn't https (rejected unless the host is `localhost`), and an empty `ALLOWED_EMAILS` (rejected).
  - `TestHandlerRoutes` uses a stub `Verifier`. `/mcp` without a token gets 401 with `WWW-Authenticate`. With an allowed token, `initialize` succeeds; reuse `postRPC` from `internal/mcpserver/http_test.go`, moved to a shared test helper if needed. `/.well-known/oauth-protected-resource` and `/.well-known/oauth-protected-resource/mcp` return 200. `/mcp/<anything>` gets 404. Under the fallback design, `/.well-known/oauth-authorization-server` returns 200.
  - Delete `TestSecretPath` and `secretPath`.
- [ ] **Step 2:** Run `go test ./cmd/server/`. Expected: FAIL.
- [ ] **Step 3:** Implement `main` with config, the JSON slog logger, deps, and an `http.Server` with `ReadHeaderTimeout: 10s`. Shut down gracefully on SIGTERM with a 10 s deadline. Under the preferred design the PRM names `https://accounts.google.com`. Under the fallback it names `PUBLIC_URL`.
- [ ] **Step 4:** Write the Dockerfile. The build stage is `golang:<version from go.mod>` and runs `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" ./cmd/server`. The final stage is `gcr.io/distroless/static-debian12:nonroot` with `USER nonroot:nonroot`. Pin both images by digest. Run `docker build .`. Expected: success.
- [ ] **Step 5:** Run `go test -race ./...` and `golangci-lint run`. Expected: PASS, 0 issues.
- [ ] **Step 6:** Commit `feat(server): /mcp behind Google auth; container image`.

### Task 10: Cloud Run infrastructure and deploy

Requires Owner gate A. Owner gate B must be done before the first deploy that sets the Paprika secrets.

**Files:**
- Create: `deploy/setup.sh`, `deploy/deploy.sh`, `deploy/README.md`

**Interfaces:**
- Produces: service `paprika-mcp` in `us-central1`, service account `paprika-mcp@<project>.iam.gserviceaccount.com`, bucket `<project>-paprika-cache`.

- [ ] **Step 1:** Write `setup.sh`. It is idempotent, uses `set -euo pipefail`, and requires `PROJECT` and `--configuration=paprika-mcp` on every call. It:
  - enables `run`, `cloudbuild`, `artifactregistry`, `secretmanager`, and `storage` APIs
  - creates the service account
  - creates the bucket with `--location=us-central1 --uniform-bucket-level-access --public-access-prevention`
  - grants `roles/storage.objectUser` on that bucket only
  - grants `roles/secretmanager.secretAccessor` on each named secret only, never project-wide
- [ ] **Step 2:** Write `deploy.sh`, which runs `gcloud run deploy paprika-mcp --source . --region us-central1 --service-account ... --allow-unauthenticated --min-instances 0 --max-instances 1 --set-env-vars PUBLIC_URL=...,ALLOWED_EMAILS=...,GOOGLE_CLIENT_ID=...,CACHE_BUCKET=... --set-secrets PAPRIKA_USERNAME=paprika-username:latest,PAPRIKA_PASSWORD=paprika-password:latest[,fallback secrets]`. Cloud Run IAM allows unauthenticated invocation because the app enforces auth.
- [ ] **Step 3:** Run `shellcheck deploy/*.sh`. Expected: clean.
- [ ] **Step 4:** Run `setup.sh`, then `deploy.sh`. Check that `curl -s -o /dev/null -w '%{http_code}' <url>/mcp` prints `401` and that `<url>/.well-known/oauth-protected-resource` returns JSON. Neither check touches Paprika.
- [ ] **Step 5:** Commit `chore(deploy): Cloud Run setup and deploy scripts`. Open PR 6 covering Tasks 9–10.

### Task 11: Read-only snapshot and hash-parity tool

Requires Owner gate B. Read-only by construction.

**Files:**
- Create: `cmd/paprika-snapshot/main.go`
- Test: `cmd/paprika-snapshot/main_test.go`

**Interfaces:**
- Consumes: `paprika.NewClient(Config)`, `ListRecipes`, `GetRecipe`, `ListCategories`, `ListMealTypes`, `ListMeals`, `Recipe.ComputeHash`.
- Produces: `paprika-snapshot -out <path>`. It reads `PAPRIKA_USERNAME` and `PAPRIKA_PASSWORD` from the environment and writes `{"taken_at","recipes":[...],"categories","meal_types","meals"}` to `<path>` with mode 0600. It prints `hash parity: <matching>/<total> recipes`, followed by the UID of each mismatch.

- [ ] **Step 1: Write failing tests** with a fake `Transport`. `TestSnapshotIsReadOnly`: every request is a GET, except the one login POST. `TestParityReport`: of 3 recipes, 1 has a wrong stored hash, so the output is `hash parity: 2/3` followed by that UID. `-out` pointing into a missing directory returns an error.
- [ ] **Step 2:** Run `go test ./cmd/paprika-snapshot/`. Expected: FAIL. Implement, then run it again. Expected: PASS.
- [ ] **Step 3:** Ask the owner before invoking `op`, then run `op run --env-file=<owner-provided refs> -- go run ./cmd/paprika-snapshot -out "$HOME/Documents/Paprika Backups/api-snapshot-$(date +%F).json"`. Expected: parity equals the total. If it doesn't, stop. The mismatches may be legacy hashes; report them to the owner before Task 12.
- [ ] **Step 4:** Using the deployed connector from Claude Code, exercise read-only tools only: `search_recipes`, `get_recipe`, `list_categories`, and `list_meals` for the current week. Record the results in the PR.
- [ ] **Step 5:** Commit `feat: read-only snapshot and hash-parity tool`. Open PR 7.

### Task 12: Staged live writes and acceptance

Requires Owner gate C. Run the spec's §9 acceptance list in order. Owner-side checks are noted inline.

- [ ] **Step 0:** The owner signs in with the allowlisted Google account, which succeeds. Signing in with a second Google account gets 403 and no Paprika data.
- [ ] **Step 1:** From Claude web, create `[TEST] Connector Smoke` with an existing category. The owner confirms it reaches the phone within a minute, in that category.
- [ ] **Step 2:** Create `[TEST] Bad Category` with category `NoSuchCategory-zz`. Expect a tool error. Confirm `search_recipes query="[TEST] Bad"` finds nothing.
- [ ] **Step 3:** The owner opens Paprika on the Mac and syncs twice. `[TEST] Connector Smoke` must still be there.
- [ ] **Step 4:** From Claude mobile, update that recipe's notes. Then use `get_recipe` and compare against the snapshot shape: only `notes` and `hash` differ.
- [ ] **Step 5:** Re-run `paprika-snapshot`, after asking before `op`. The test recipe's stored hash must equal the server-computed hash.
- [ ] **Step 6:** On `2030-01-05`, add a Snacks meal named `[TEST] Snack`, and a Dinner meal linked to `[TEST] Connector Smoke`. Remove the Snack. The owner confirms on all devices.
- [ ] **Step 7:** Redeploy, then call `search_recipes`. Logs show `fetched=0` or a small number, not the whole library.
- [ ] **Step 8:** Run every tool once from Claude Code through the claude.ai connector.
- [ ] **Step 9:** Remove the remaining `[TEST]` meal with `remove_meal`. Ask the owner to trash the `[TEST]` recipes in the app.
- [ ] **Step 10:** Record the results in `docs/superpowers/specs/2026-11-01-acceptance-results.md` and open a docs PR.

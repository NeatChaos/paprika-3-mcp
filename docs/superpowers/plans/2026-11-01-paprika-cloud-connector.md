# Paprika Cloud Connector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run the forked `paprika-3-mcp` as a remote MCP server on Cloud Run, reachable only through Cloudflare Access, with hash-correct Paprika writes and a GCS-backed cache that keeps Paprika traffic low.

**Architecture:** Cloudflare Access sits in front of the service and is the OAuth server Claude talks to (Access "Managed OAuth"). It signs the owner in and forwards each request with a signed `Cf-Access-Jwt-Assertion` header. The Go binary (`cmd/server`) checks that header on every request and serves MCP over stateless Streamable HTTP at `/mcp`. `internal/paprika` speaks Paprika's sync API through a transport chain (auth → retry → rate limit). The recipe cache loads once per cold start from GCS and from Paprika, and after that refreshes only on request or when a write finds stale data. Terraform in `infra/` manages every GCP resource.

**Tech Stack:** Go (toolchain per `go.mod`), `mark3labs/mcp-go`, `coreos/go-oidc/v3` (the library Cloudflare's own Go example uses to verify Access JWTs), `golang.org/x/time/rate`, stdlib HTTP for the GCS JSON API, distroless container, Terraform, Cloud Run, Artifact Registry, Secret Manager, GCS, Cloudflare Access.

**Spec:** `docs/superpowers/specs/2026-11-01-paprika-cloud-connector-design.md`. Read it before any task. This plan overrides the spec in three places, each decided by the owner after the spec was approved:

- **Access control.** Cloudflare Access replaces the spec's Google OAuth work item 6. Nothing in this repo issues or brokers OAuth tokens.
- **Refresh policy.** Paprika data loads once per cold start. After that it refreshes only on request, or when a write finds the record changed. There is no background or per-call refresh.
- **Meals.** Meals are held in memory with the same refresh policy, not read live on every call. They are never written to GCS.

Where this plan and the spec disagree anywhere else, the spec wins; stop and report.

## Global Constraints

- **Upstream-friendly diff.** Don't delete or rewrite upstream features that do no harm: the stdio binary, keychain support, MCP resources, `.goreleaser.yaml`, `vercel.json`, and `DeleteRecipe` all stay. Keep the module path `github.com/soggycactus/paprika-3-mcp`. Prefer new files and small edits over restructuring existing ones, so later merges from `joostlensen-web/paprika-3-mcp` conflict as little as possible.
- **Paprika calls.** Automated tests and CI never call `paprikaapp.com`. Tests use `httptest` servers or the existing `roundTripFunc` fakes in `internal/paprika/unit_test.go`.
- **Paprika efficiency.** A session is about 30 tool calls in 20 minutes on one warm instance. Reads are answered from memory with zero Paprika requests. A write costs at most one conflict-check GET, one POST, and one `notify`.
- **Live writes.** None until the owner confirms a Paprika export (Owner gate C). After that, writes only touch `[TEST]`-prefixed recipes and `[TEST]` meals dated `2030-01-05`.
- **Secrets.** Never handle, echo, log, or write the Paprika password, a token, or an Access JWT. The owner adds secret values to Secret Manager. Terraform creates secret containers and IAM only, never `google_secret_manager_secret_version`. Ask before running `op`.
- **GCP access.** Every `gcloud` command passes `--configuration=paprika-mcp`. Terraform authenticates with `GOOGLE_OAUTH_ACCESS_TOKEN="$(gcloud auth print-access-token --configuration=paprika-mcp)"`, set inline on the command. Never use Application Default Credentials; ADC and the default configuration belong to the owner's law practice.
- **Infrastructure.** Every GCP resource except the Terraform state bucket is managed in `infra/`. Cloudflare settings are made by the owner in the dashboard, following the exact values in the Owner Gates.
- **GitHub.** Use CSB (`mcp__csb__*` tools, `csb gh ...`). Ask before bare `gh`. Never push to `main`; one PR per PR group below.
- **Dependencies.** Each new Go module, action, base image, or Terraform provider needs a one-line justification in its PR description. Look up the current stable version when adding one.
- **Lint.** Clean runs of `go test -race ./...`, `golangci-lint run`, `gofmt -l .`, `terraform fmt -check -recursive infra`, `terraform -chdir=infra validate`, and `tflint --chdir=infra`. Zero warnings. Fix lint findings only in code this plan touches, plus whatever CI requires; don't reformat untouched upstream files beyond that.
- **Comments.** Go doc comments only where they add information; no history or ticket references.
- **UIDs.** Never validate input UIDs as UUID4. Generated UIDs are uppercase UUID4.
- **Logging.** `log/slog` on stderr. Never log request headers or Paprika login response bodies.
- **Region and scale.** `us-central1`, `min-instances=0`, `max-instances=1`.

## PR Groups

| PR | Tasks | Branch |
|---|---|---|
| 1 | Design doc, this plan, Tasks 1–3 | `feat/cloud-connector-design` |
| — | Task 4 (spike, never merged) | `spike/cloudflare-access` |
| 2 | Tasks 5–7 | `feat/paprika-client` |
| 3 | Tasks 8–9 | `feat/cache-and-server` |

The owner merges. Each PR branches from `main` after the previous one merges.

## Owner Gates

Batch each gate into one ask, with the exact values filled in.

**Gate A, before Task 3:**

1. Google account for the GCP project: a personal one, not `m@peters.law`. Create project `paprika-mcp-<suffix>` under it, with no law-practice organization, and link billing.
1. Run in this session: `! gcloud config configurations create paprika-mcp && gcloud auth login <account> && gcloud config set project <project-id>`.
1. Tell the builder:
   - which Cloudflare zone and hostname to use, for example `paprika.<your-domain>`
   - the Zero Trust team name, from `<team>.cloudflareaccess.com`
   - the email that Access should allow

**Gate B, after Task 3's first apply:**

1. `gcloud secrets versions add paprika-username --configuration=paprika-mcp --data-file=-` (type the value, then Ctrl-D). Do the same for `paprika-password`.
1. Give the builder the `op://` references for the same two values, for the local snapshot script. The builder asks before each `op run`.

**Gate C, during Task 4.** The builder supplies exact values once the service exists.

1. Zero Trust → Access → Applications → Add → Self-hosted:
   - Domain: the hostname from Gate A.
   - Identity provider: Google, or one-time PIN.
   - Policy: Allow, Include: Emails = the owner's email.
   - Session duration: 24 hours.
1. In the same application's Advanced settings, turn on **Managed OAuth**:
   - Dynamic client registration on. Allowed redirect URIs: `https://claude.ai/api/mcp/auth_callback` and `https://claude.com/api/mcp/auth_callback`. Localhost and loopback off.
   - Access token lifetime: 15 minutes.
   - Grant session duration: 14 days.
1. Send the builder the application's AUD tag. It isn't a secret.
1. Add the Worker route and DNS record the builder specifies; see Task 4.

**Gate D, before Task 10:**

1. Export the full library from the Paprika Mac app and confirm the export completed.
1. Add the connector in claude.ai: Admin settings → Connectors → Add custom connector, URL `https://<hostname>/mcp`, no client ID.

## Review Focus

1. **Requests that bypass Cloudflare.** A request straight to the `run.app` URL with no `Cf-Access-Jwt-Assertion`, with a JWT for another Access application (wrong `aud`), or with an expired JWT must get 401 and no Paprika data. Test in Task 2.
1. **Text that JSON encoders disagree on.** Recipe text containing `<`, `&`, `/`, U+2028, emoji, `\b`, and a tab must hash exactly like Paprika. Test in Task 5.
1. **Null round trip on update.** A recipe fetched with `photo: null, scale: null, image_url: ""` and then updated must post those values back unchanged, and every untouched field must be byte-identical. Test in Task 5.
1. **Stale write.** The recipe was edited on the phone after the server loaded it. `update_paprika_recipe` must refuse with a message saying what to do, write nothing, and leave the cache holding the newer copy. Test in Task 8.
1. **Deleted or unknown meals.** `list_meals` skips meals with `deleted: true`. `remove_meal` on an unknown or already-deleted UID returns a tool error and writes nothing. Test in Task 7.

---

### Task 1: CI and tooling

**Files:**
- Create: `.github/workflows/ci.yaml`, `.github/dependabot.yml`, `.golangci.yml`, `.pre-commit-config.yaml`

- [ ] **Step 1:** Add `.golangci.yml` as `version: "2"` with the default linter set. Run `golangci-lint run`. Fix the findings, keeping each fix minimal. Expected: `0 issues.`
- [ ] **Step 2:** Add `ci.yaml`:
  - Triggers on `push` and `pull_request`, with `permissions: contents: read`.
  - Steps: `actions/checkout` with `persist-credentials: false`, then `actions/setup-go` with `go-version-file: go.mod`, then a `gofmt -l .` check that fails on any output, then `go test -race ./...`, then `golangci/golangci-lint-action`.
  - Pin every action by full commit SHA, with a version comment.
- [ ] **Step 3:** Add `dependabot.yml` for `gomod` and `github-actions`, weekly, with `cooldown: default-days: 3` and one group per ecosystem.
- [ ] **Step 4:** Add `.pre-commit-config.yaml` with local hooks for `gofmt -l` and `golangci-lint run`. Run `prek install`, `actionlint .github/workflows/`, and `zizmor .github/workflows/`. Expected: no findings. The existing `release.yaml` stays; fix only what zizmor flags.
- [ ] **Step 5:** Commit `chore: CI, lint, Dependabot, prek`.

### Task 2: Cloudflare Access verification (`internal/access`)

**Files:**
- Create: `internal/access/access.go`
- Test: `internal/access/access_test.go`

**Interfaces:**
- Produces:
  - `func NewVerifier(teamDomain, aud string, allow []string, keySet oidc.KeySet) *Verifier`. `teamDomain` is `https://<team>.cloudflareaccess.com`, and production uses `oidc.NewRemoteKeySet(ctx, teamDomain+"/cdn-cgi/access/certs")`.
  - `func (v *Verifier) Middleware(next http.Handler) http.Handler`, which returns 401 when the header is missing or the JWT is invalid, and 403 when the email is not in `allow`.
  - `func ParseAllowlist(csv string) ([]string, error)`, which errors on empty input. Comparison is case-insensitive.
  - Dependency: `github.com/coreos/go-oidc/v3`.

- [ ] **Step 1: Write failing tests.** Generate a test RSA key, and use `oidc.StaticKeySet` as the key set.
  - `TestAccessValid`: an RS256 JWT with `iss=teamDomain`, `aud=["AUD1"]`, `email=Owner@Example.com`, and `exp` 5 minutes out. `next` runs.
  - `TestAccessRejects`: each case gets 401 and `next` doesn't run. Cases: no header, a garbage header, the wrong `aud`, the wrong `iss`, an expired token, and a token signed with another key.
  - `TestAccessNotAllowlisted`: a valid JWT for `stranger@example.com` gets 403.
- [ ] **Step 2:** Run `go test ./internal/access/`. Expected: FAIL.
- [ ] **Step 3:** Implement with `oidc.NewVerifier(teamDomain, keySet, &oidc.Config{ClientID: aud})`. Read the token from `Cf-Access-Jwt-Assertion` only. Decode the `email` claim.
- [ ] **Step 4:** Run `go test -race ./internal/access/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(access): verify Cloudflare Access JWT at the origin`.

### Task 3: Terraform infrastructure (`infra/`)

Requires Owner gate A.

**Files:**
- Create: `infra/versions.tf`, `infra/backend.tf`, `infra/variables.tf`, `infra/main.tf`, `infra/outputs.tf`, `infra/terraform.tfvars.example`, `infra/.tflint.hcl`, `infra/README.md`
- Modify:
  - `.gitignore`: add `infra/.terraform/`, `infra/*.tfvars`, and `!infra/terraform.tfvars.example`. Commit `.terraform.lock.hcl`.
  - `ci.yaml`: add a `terraform` job.
  - `dependabot.yml`: add the `terraform` and `docker` ecosystems.
  - `.pre-commit-config.yaml`: add `terraform fmt` and `tflint` hooks.
  - `Makefile`: add an `image` target. The existing targets stay.

**Interfaces:**
- Produces:
  - Variables:
    - `project_id`
    - `region`, default `us-central1`
    - `image`, default `""`; empty means no Cloud Run service yet
    - `allowed_emails`
    - `access_team_domain`
    - `access_aud`, default `""`; the Access application doesn't exist until Task 4
  - Outputs: `run_url`, `artifact_repo`, `cache_bucket`.
  - `make image TAG=<tag>` builds `docker build --platform linux/amd64 -t <artifact_repo>/server:<tag> .`, pushes it, and prints the `@sha256:` reference.

- [ ] **Step 1:** Bootstrap the state bucket:
  - Run `gcloud storage buckets create gs://<project>-tfstate --location=us-central1 --uniform-bucket-level-access --public-access-prevention --configuration=paprika-mcp`.
  - Then run `gcloud storage buckets update gs://<project>-tfstate --versioning --configuration=paprika-mcp`.
  - Document both commands in `infra/README.md` as the only resource outside Terraform.
- [ ] **Step 2:** Write the configuration.
  - Backend: `gcs` with `prefix = "paprika-mcp"`.
  - Provider: current stable `hashicorp/google`.
  - Resources:
    - `google_project_service` for `run`, `artifactregistry`, `secretmanager`, and `storage`, with `disable_on_destroy = false`.
    - `google_artifact_registry_repository` `paprika-mcp`, DOCKER format, with a cleanup policy that keeps the 5 newest versions.
    - `google_service_account` `paprika-mcp`.
    - `google_storage_bucket` `<project>-paprika-cache`: `us-central1`, uniform access, `public_access_prevention = "enforced"`.
    - `google_storage_bucket_iam_member` granting `roles/storage.objectUser` on that bucket only.
    - `google_secret_manager_secret` for `paprika-username` and `paprika-password`, with no versions.
    - `google_secret_manager_secret_iam_member` granting `secretAccessor` on each secret only.
    - `google_cloud_run_v2_service` `paprika-mcp`, with `count = var.image == "" ? 0 : 1`:
      - min 0 and max 1 instances, the service account, and port 8080
      - env `ACCESS_TEAM_DOMAIN`, `ACCESS_AUD`, `ALLOWED_EMAILS`, `CACHE_BUCKET`, plus the two secrets at `latest`
      - `deletion_protection = false`
    - `google_cloud_run_v2_service_iam_member` granting `allUsers` `roles/run.invoker`, with a one-line comment: Cloudflare can't present Google IAM credentials, and the app enforces Access.
- [ ] **Step 3:** Add the `Dockerfile` and `.dockerignore`.
  - Build stage: `golang:<go.mod version>`, running `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" ./cmd/server`.
  - Final stage: `gcr.io/distroless/static-debian12:nonroot`.
  - Pin both images by digest. Run `docker build .`. Expected: success.
- [ ] **Step 4:** Add the CI `terraform` job: `fmt -check`, then `init -backend=false`, then `validate`, then tflint. Pin each action by SHA. Run the same checks locally, plus actionlint and zizmor. Expected: clean.
- [ ] **Step 5:** Write the gitignored `infra/terraform.tfvars`. Run `terraform init`, `plan`, and `apply` with an empty `image`. Expected: no service, and both secrets exist with no versions. Ask the owner for **Gate B**.
- [ ] **Step 6:** Commit `feat(infra): Terraform for Cloud Run, cache bucket, secrets, IAM`. Open PR 1, which carries the design doc, this plan, and Tasks 1–3.

### Task 4: Spike: claude.ai through Cloudflare Access Managed OAuth

Requires Gate B. Gate C happens during this task. Branch `spike/cloudflare-access`, never merged.

This spike decides whether Access Managed OAuth works with claude.ai web and mobile. [anthropics/claude-ai-mcp#410](https://github.com/anthropics/claude-ai-mcp/issues/410) reports that the flow fails in claude.ai web and mobile but works in Claude Code. The suspected cause is a missing RFC 8707 `resource` parameter. Issue #572 reports success through an Access MCP portal.

**Files (spike branch only):** `cmd/accessspike/main.go`, `worker/` (the Cloudflare Worker that forwards requests)

- [ ] **Step 1:** Write `cmd/accessspike/main.go`. It serves `/mcp` behind `access.Verifier.Middleware`, with one tool, `whoami`, which returns the JWT email. It logs method, path, and status only. Deploy it with `make image TAG=spike` and `terraform apply -var image=<digest> -var access_aud=<from Gate C>`.
- [ ] **Step 2:** Route the hostname to Cloud Run.
  - The preferred route is a minimal Worker on `<hostname>/*` that `fetch`es the same path on the `run.app` URL and passes headers through. A Worker sets the right Host header and SNI for Cloud Run on any Cloudflare plan.
  - Give the owner the Worker code and the route. They deploy it, or authorize `wrangler` through `op` after you ask.
  - Access runs before the Worker, so the `Cf-Access-Jwt-Assertion` header reaches the origin.
- [ ] **Step 3:** The owner completes Gate C. Then test, in this order:
  1. claude.ai web, adding the connector.
  1. Claude mobile.
  1. Claude Code, through the claude.ai connector.
  1. A second account that isn't on the allowlist.
  1. `whoami` again 30 minutes later, to check that the silent refresh worked.
  1. A direct `curl` to the `run.app` URL, which must get 401.
- [ ] **Step 4:** Write the results to `docs/superpowers/specs/2026-11-01-access-spike-findings.md` and open a docs PR.
  - If every check passes, Tasks 5–10 proceed with Access.
  - If claude.ai web or mobile fails, stop and report to the owner. Two options are known. One is an Access MCP server portal, the variant reported working in #572. The other is a Worker built on Cloudflare's `workers-oauth-provider` library with Google as the identity provider. Don't build either without the owner's go-ahead.

### Task 5: Recipe model and hash parity

**Files:**
- Create: `internal/paprika/hash.go`, `internal/paprika/hash_test.go`, `internal/paprika/testdata/recipe_hashes.json`, `internal/paprika/testdata/recipe_wire.json`, `THIRD_PARTY_NOTICES.md`
- Modify: `internal/paprika/client.go`. Change the `Recipe` field types, add `Deleted`, and make `SaveRecipe` call `ComputeHash`. Delete only `updateHash` and `asMap`, which `ComputeHash` replaces.

**Interfaces:**
- Produces:
  - `Recipe` fields `ImageURL`, `Photo`, `PhotoHash`, `PhotoLarge`, `Scale`, and `PhotoURL` become `*string`. Add `Deleted bool` with tag `json:"deleted"`.
  - `func (r Recipe) ComputeHash() (string, error)`. `SaveRecipe` sets `Hash` from it before posting.

- [ ] **Step 1: Fixtures.**
  - Convert mcp-paprika v1.6.3's `src/paprika/__fixtures__/recipe-hashes.ts`, all 8 cases, to `testdata/recipe_hashes.json` as `[{"name","recipe":{<snake_case wire keys>},"expected_hash"}]`.
  - Take one recipe POST body from `docs/wire-captures/writes.har.json`, gunzip it, and save it as `testdata/recipe_wire.json`.
  - Do the conversion with a throwaway script in the scratchpad.
  - Add `THIRD_PARTY_NOTICES.md` with mcp-paprika's MIT notice (© 2026 Bojan Rajkovic), naming the ported algorithm and fixtures.
- [ ] **Step 2: Write failing tests.**
  - `TestComputeHashFixtures`: a table over `recipe_hashes.json`.
  - `TestComputeHashInvariants`:
    - The result matches `^[0-9A-F]{64}$`.
    - It ignores `Hash`, the order of categories, `InTrash`, and `Deleted`.
    - A nil `Scale` and an empty-string `Scale` give different hashes.
  - `TestHashSerialization`: test the unexported `hashInput(r Recipe) ([]byte, error)` with `Name` set to the Go expression `"a<b>&c/d" + string(rune(0x2028)) + "e😀\bf\tg"`. The output contains the bytes `"a<b>&c\/d`, then the raw UTF-8 bytes `E2 80 A8`, then `e😀\bf\tg"`.
  - `TestRecipeWireRoundTrip`: unmarshal `recipe_wire.json` into `Recipe`, then marshal it again. The result equals the input as a `map[string]any`, and `null` stays `null`.
  - Update `TestSaveRecipe`: nil pointer fields upload as `null`, and the uploaded `hash` equals `ComputeHash()` of the uploaded record.
- [ ] **Step 3:** Run `go test ./internal/paprika/`. Expected: FAIL.
- [ ] **Step 4:** Implement `hashInput`.
  - Build the 27-element `[]any` in the spec's order.
  - Encode it with `json.NewEncoder` and `SetEscapeHTML(false)`, then trim the trailing newline.
  - Replace `/` with `\/`.
  - Replace the six-character escapes Go emits for U+2028 and U+2029 (backslash + `u2028`, backslash + `u2029`) with the raw characters.
  - `ComputeHash` returns the uppercase hex SHA-256 of the result.
  - Add any field the round-trip test shows is missing. Fix compile errors in callers.
- [ ] **Step 5:** Run `go test -race ./...`. Expected: PASS.
- [ ] **Step 6:** Commit `feat(paprika): port Paprika's recipe hash; preserve nullable fields`.

### Task 6: 401 re-login, Retry-After, global rate limit

**Files:**
- Create: `internal/paprika/transport.go`, `internal/paprika/transport_test.go`
- Modify: `internal/paprika/client.go`. Keep the change minimal: `NewClient` wraps its transport with the new layers.

**Interfaces:**
- Produces:
  - The `NewClient(username, password, version string, logger *slog.Logger)` signature stays the same, so upstream callers keep compiling.
  - An unexported `newClientWithTransport(..., base http.RoundTripper)` for tests.
  - `var ErrUnauthorized error`.
  - Dependency: `golang.org/x/time/rate`.

- [ ] **Step 1: Write failing tests.**
  - `TestReloginOn401`:
    - The GET returns 401 once, then 200.
    - The request sequence is login, then GET (401), then login, then GET (200) with the new bearer token.
    - A POST that is retried after a 401 sends a body identical to the original.
  - `TestReloginOnlyOnce`: a GET that always returns 401 produces exactly 2 logins in total and `errors.Is(err, ErrUnauthorized)`.
  - `TestConcurrent401SingleLogin`: three concurrent 401s on the same token trigger one re-login.
  - `TestRetryAfter`:
    - A 429 with `Retry-After: 2` waits at least 2 s under a fake sleep, then retries.
    - A `Retry-After` value above 30 returns the 429 without waiting.
  - `TestRateLimit`: with a rate of 1000/s and burst 1, 5 requests take at least 4 ms.
  - `TestLoginErrorOmitsSecrets`: when login answers 403, neither the error nor the captured logs contain the password or the response body.
- [ ] **Step 2:** Run `go test ./internal/paprika/`. Expected: FAIL.
- [ ] **Step 3:** Implement the chain, outermost first:
  1. `authTransport` adds the token. On a 401 it logs in again under a mutex, unless the token already changed since this request started. Then it replays the request once using `req.GetBody`.
  1. The fork's `retryTransport`, with `Retry-After` honored as seconds and capped at 30.
  1. A limiter at 4 requests per second, burst 3, with `Wait` on every attempt.
  1. The headers.
  1. The base transport.
  The login request bypasses `authTransport`. Remove the response body from login error messages.
- [ ] **Step 4:** Run `go test -race ./internal/paprika/`. Expected: PASS.
- [ ] **Step 5:** Commit `feat(paprika): 401 re-login, Retry-After, rate limit`.

### Task 7: Meal removal

**Files:**
- Modify: `internal/paprika/meals.go` (add `Deleted bool` with tag `json:"deleted"`), `internal/mcpserver/meals.go`, `internal/mcpserver/server.go` (register the tool)
- Test: `internal/mcpserver/server_test.go`, `internal/paprika/unit_test.go`

**Interfaces:**
- Produces:
  - Tool `remove_meal`, with required string `uid`. Description: `Remove one meal from the Paprika meal planner by its meal uid (see list_meals). The recipe itself is not affected.`
  - `list_meals` lines include `meal uid: <uid>`.
  - `(*Server).removeMeal`, built on the fetch-then-flag rule. Task 8 changes where it reads meals from.

- [ ] **Step 1: Write failing tests.**
  - `TestRemoveMeal`: `SaveMeal` receives `M1` with all of its fields unchanged and only `Deleted` set to true.
  - `TestRemoveMealUnknownOrDeleted`: both cases return a tool error and make no `SaveMeal` call.
  - `TestListMealsSkipsDeleted`.
  - `TestListMealsShowsMealUID`.
  - `TestSaveMealWireShape`: the uploaded element has the keys `uid`, `recipe_uid`, `name`, `date`, `type`, `type_uid`, `order_flag`, `deleted`, `scale`, and `is_ingredient`. Check these against the meal POST in mcp-paprika's `meals.har.json`.
- [ ] **Step 2:** Run `go test ./...`. Expected: FAIL.
- [ ] **Step 3:** Implement the tool and the deleted-meal filter.
  - Check `meals.har.json` for the `type` the app sends with a custom meal type (`original_type: null`).
  - Make `MealType.OriginalType` an `*int` and match the capture.
- [ ] **Step 4:** Run `go test -race ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: remove_meal tool; skip deleted meals`. Open PR 2, covering Tasks 5–7.

### Task 8: Cold-start load, on-demand refresh, conflict-checked writes, GCS persistence

**Files:**
- Create: `internal/cachestore/store.go`, `internal/cachestore/gcs.go`, `internal/cachestore/gcs_test.go`
- Modify:
  - `internal/mcpserver/cache.go`: add the store, meal types, meals, and a `refreshMode`.
  - `internal/mcpserver/server.go`: add option fields and the `refresh_paprika` tool.
  - The write handlers in `server.go` and `meals.go`.
- Test: `internal/mcpserver/server_test.go`

**Interfaces:**
- Consumes: `paprika.Recipe.ComputeHash`, `(*Server).removeMeal`.
- Produces:
  - `type Store interface { Load(ctx context.Context) ([]byte, int64, error); Save(ctx context.Context, data []byte, ifGeneration int64) (int64, error) }`.
  - `var ErrNotFound, ErrPrecondition error`. An `ifGeneration` of 0 means the object must not exist yet.
  - `func NewGCS(bucket, object string, hc *http.Client, endpoint string, token func(context.Context) (string, error)) *GCS`.
  - `func MetadataToken(hc *http.Client) func(context.Context) (string, error)`, which caches the token until 60 s before it expires.
  - New fields on `NewServerOptions`:
    - `Store cachestore.Store`. Nil keeps upstream's file or memory behavior.
    - `OnDemandRefresh bool`. When true, there is no ticker, no per-call `ensureFresh` refresh, and reads come from memory after the first load. When false, upstream behavior is unchanged, so stdio users get exactly what upstream gives them.
  - Tool `refresh_paprika`, no arguments. It re-syncs categories, meal types, meals, and changed recipes, and reports counts. Description: `Reload recipes, categories and the meal plan from Paprika. Use when the user says they changed something in the Paprika app during this conversation.`
  - Write conflict message: `This recipe was changed in Paprika since it was loaded. The latest version is now loaded; read it with get_recipe and retry the update.`
  - Cache object `recipe-cache.json`: `{"version":1,"recipes":{},"hashes":{},"categories":[],"meal_types":[]}`. Meals stay in memory and are never written to GCS.

- [ ] **Step 1: Write failing tests.**
  - `TestGCSLoad`:
    - 200 with `x-goog-generation: 7` returns the data and 7.
    - 404 returns `ErrNotFound`.
    - The request is `GET /storage/v1/b/<b>/o/<o>?alt=media` with a bearer token.
  - `TestGCSSave`:
    - The request is `POST /upload/storage/v1/b/<b>/o?uploadType=media&name=<o>&ifGenerationMatch=7`.
    - A response body `{"generation":"8"}` returns 8.
    - 412 returns `ErrPrecondition`.
  - `TestMetadataTokenCaches`: two calls hit the metadata server once, with the `Metadata-Flavor: Google` header.
  - `TestColdStartLoadsOnce`, with `OnDemandRefresh` on and a store holding 2 recipes whose hashes match Paprika's list:
    - The first `search_recipes` makes exactly one call each to categories, meal types, meals, and the recipe list, and fetches 0 recipes.
    - The next 10 calls to `search_recipes`, `get_recipe`, `list_categories`, and `list_meals` make 0 Paprika requests.
  - `TestRefreshTool`: after the fake account changes one recipe's hash, `refresh_paprika` fetches exactly that recipe and saves the store once.
  - `TestUpdateRecipeConflict`: the cache holds hash `H1` and Paprika's `GetRecipe` returns `H2`.
    - The tool returns the conflict message.
    - `SaveRecipe` isn't called.
    - `get_recipe` now returns the `H2` version.
  - `TestUpdateRecipeNoConflict`: the hashes match. Exactly one `GetRecipe`, one `SaveRecipe`, and one store save.
  - `TestRemoveMealConflict`: the cached meal differs from the meal in a fresh `ListMeals`, or is missing from it. The tool returns an error, and `SaveMeal` isn't called.
  - `TestCreateRecipeUnknownCategory`: the error text names the category and suggests `refresh_paprika` if the category was just created in the app. No write happens.
  - `TestCacheCorruptOrWrongVersion`: `{nope` and `"version":2` both trigger a full fetch with no error.
  - `TestCachePreconditionSkipsSave`: when `Save` returns `ErrPrecondition`, the server logs a warning, makes no retry and no overwrite, and the tool call still succeeds.
  - `TestUpstreamModeUnchanged`: with `OnDemandRefresh` off, the existing cache tests pass unmodified.
- [ ] **Step 2:** Run `go test ./...`. Expected: FAIL.
- [ ] **Step 3:** Implement.
  - **Cold start:** the first tool call loads the store, which has a 10 s timeout, then runs one diff refresh. Concurrent first calls wait on the same load.
  - **`update_paprika_recipe`:** run `GetRecipe(uid)`. If the hash differs from the cached one, put the fresh copy in the cache and return the conflict message. Otherwise apply the edits, call `SaveRecipe` and `put`.
  - **`remove_meal`:** run one `ListMeals`. If the meal differs from or is missing in the cached copy, refresh the meals cache and return an error. Otherwise flag it as deleted.
  - **`add_meal` and `create_paprika_recipe`:** these create new UIDs, so they skip the conflict check.
  - **Saves:** after each write, update memory and save the store once. Saves run outside `mu`, serialized by `saveMu`, and track `generation`.
- [ ] **Step 4:** Run `go test -race ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `feat: cold-start cache with on-demand refresh and conflict-checked writes`.

### Task 9: Remote server wiring and rollout

**Files:**
- Modify: `cmd/server/main.go`, `cmd/server/main_test.go`, `README.md` (add a "Cloud Run + Cloudflare Access" section; keep the rest of upstream's README)

**Interfaces:**
- Consumes:
  - `access.NewVerifier`, `access.ParseAllowlist`
  - `cachestore.NewGCS`, `cachestore.MetadataToken`
  - `mcpserver.NewServerOptions{Store, OnDemandRefresh: true}`
- Produces:
  - `func loadConfig(getenv func(string) string) (config, error)`.
  - Env vars, matching Task 3's Terraform exactly: `PAPRIKA_USERNAME`, `PAPRIKA_PASSWORD`, `ACCESS_TEAM_DOMAIN`, `ACCESS_AUD`, `ALLOWED_EMAILS`, `CACHE_BUCKET`, and `PORT` (default 8080).
  - When `ACCESS_AUD` is set, the Access middleware guards `/mcp` and `MCP_SECRET` is ignored.
  - When it's unset, upstream's `MCP_SECRET` path check still works, so the Vercel deployment keeps working.

- [ ] **Step 1: Write failing tests.**
  - `TestLoadConfig`:
    - A missing Paprika var returns an error that names the var, never its value.
    - Setting `ACCESS_AUD` makes `ACCESS_TEAM_DOMAIN` and `ALLOWED_EMAILS` required.
    - With neither `ACCESS_AUD` nor `MCP_SECRET` set, startup fails.
  - `TestAccessModeRoutes`, with a static key set: `/mcp` without the header gets 401, and with a valid JWT, `initialize` succeeds.
  - The existing `TestSecretPath` stays and passes.
- [ ] **Step 2:** Run `go test ./cmd/server/`. Expected: FAIL. Implement, then run again. Expected: PASS.
- [ ] **Step 3:** Run `make image TAG=$(git rev-parse --short HEAD)`, then `terraform apply -var image=<digest>`. Check the following; none of these calls touches Paprika:
  - `curl` to the `run.app` URL `/mcp` gets 401.
  - `curl` to `https://<hostname>/mcp` gets Access's 401 with `WWW-Authenticate`.
- [ ] **Step 4:** Commit `feat(server): Cloudflare Access mode for Cloud Run`. Open PR 3, covering Tasks 8–9.

### Task 10: Live verification and acceptance

Requires Gate D. The snapshot and parity check run as a throwaway script in the scratchpad, not as a command in the repo.

- [ ] **Step 1:** Write a script in the scratchpad that uses `paprika.NewClient` and only GET endpoints. It writes all recipes, categories, meal types, and meals as JSON with mode 0600 to `~/Documents/Paprika Backups/api-snapshot-<date>.json`, then prints `hash parity: <matching>/<total>`. Ask before running it under `op run`. If parity is below the total, stop and report.
- [ ] **Step 2:** Run read-only checks from Claude: `search_recipes`, `get_recipe`, `list_categories`, and `list_meals` for this week. Logs must show one cold-start load and no Paprika calls on the later reads.
- [ ] **Step 3:** Work through the spec's §9 acceptance list with `[TEST]` data:
  1. Sign-in: the allowlisted account succeeds, and any other account is refused.
  1. Create `[TEST] Connector Smoke` in an existing category. It appears on the phone within a minute.
  1. Creating a recipe in an unknown category is rejected, and nothing is written.
  1. After two syncs on the Mac, the test recipe is still there.
  1. A notes update from Claude mobile changes only `notes` and `hash`.
  1. The stored hash equals the server's: re-run the script, after asking before `op`.
  1. On `2030-01-05`, add a Snacks meal and a linked Dinner meal, then remove the Snack.
  1. A redeploy is followed by a cold start that loads from GCS, and the logs show only changed recipes being fetched.
  1. Every tool works from Claude Code through the connector.
  1. Cleanup: run `remove_meal` on the remaining test meal. The owner trashes the `[TEST]` recipes in the app.
- [ ] **Step 4:** Edit a `[TEST]` recipe on the phone, then update it from Claude without refreshing first. Expect the conflict message. After `get_recipe`, retry the update. Expect success.
- [ ] **Step 5:** Record the results in `docs/superpowers/specs/2026-11-01-acceptance-results.md` and open a docs PR.

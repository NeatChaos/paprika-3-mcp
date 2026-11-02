# Infrastructure

Terraform for the Paprika MCP server on Cloud Run: Artifact Registry, the runtime service account, the recipe cache bucket, Secret Manager containers, IAM, and the Cloud Run service. `bootstrap/` holds the identities GitHub Actions deploys with. Cloudflare (Access) is configured by hand in the dashboard; the Worker proxy in `worker/` is deployed by the owner.

Every push to `main` deploys: `.github/workflows/deploy.yaml` builds the image, pushes it, and applies `infra/` with it. Every pull request gets the `infra/` plan as a comment, so review the plan before merging.

Run every command from the repository root. Every `gcloud` command uses `--configuration=paprika-mcp`. Never use Application Default Credentials; local Terraform authenticates with an access token from that configuration, set inline on each command.

## 1. State bucket (the only resource outside Terraform)

```sh
gcloud storage buckets create gs://<project-id>-tfstate --location=us-central1 --uniform-bucket-level-access --public-access-prevention --project=<project-id> --configuration=paprika-mcp
gcloud storage buckets update gs://<project-id>-tfstate --versioning --configuration=paprika-mcp
```

## 2. Bootstrap (owner, once)

Creates the Workload Identity Federation provider and two service accounts. Only a workflow running `main` in the `cloud` environment can use `github-deployer`; pull requests in `cloud-plan` get the read-only `github-planner`.

```sh
GOOGLE_OAUTH_ACCESS_TOKEN="$(gcloud auth print-access-token --configuration=paprika-mcp)" terraform -chdir=infra/bootstrap init -backend-config="bucket=<project-id>-tfstate"
GOOGLE_OAUTH_ACCESS_TOKEN="$(gcloud auth print-access-token --configuration=paprika-mcp)" terraform -chdir=infra/bootstrap apply -var=project_id=<project-id>
```

## 3. GitHub settings (owner, once)

- Environments: `cloud`, with deployment branches limited to `main`, and `cloud-plan`.
- Repository variables (not secrets; none of these is sensitive, and the repository is public so they stay out of the code):

| Variable | Value |
|---|---|
| `GCP_PROJECT` | project ID |
| `WIF_PROVIDER` | `wif_provider` output of the bootstrap |
| `DEPLOYER_EMAIL` | `deployer_email` output |
| `PLANNER_EMAIL` | `planner_email` output |
| `ALLOWED_EMAILS` | JSON list, e.g. `["you@example.com"]` |
| `ACCESS_TEAM_DOMAIN` | `https://<team>.cloudflareaccess.com` |
| `ACCESS_AUD` | AUD tag of the Cloudflare Access application |

## 4. Secret versions (owner)

Terraform never creates secret versions. Add them yourself (e.g. `op read -n ... | gcloud secrets versions add <name> --data-file=- --configuration=paprika-mcp`) before the first deploy; Cloud Run reads `latest`.

## 5. Cloudflare Worker proxy and Access

`worker/worker.js` forwards each request, with the same path, query, headers and body, to the `run.app` origin named by `ORIGIN` and streams the response back unbuffered (MCP Streamable HTTP may use SSE).

```sh
cp infra/worker/wrangler.toml.example infra/worker/wrangler.toml   # set ORIGIN; the run.app URL is deterministic, so the Worker can be deployed before the service
npx wrangler deploy --config infra/worker/wrangler.toml
```

The Worker serves on `paprika-mcp-proxy.<subdomain>.workers.dev`; no custom domain is needed. Protect it with a Zero Trust self-hosted application using the **Workers** destination, with Managed OAuth on, and put its AUD tag in `ACCESS_AUD`. Access adds the `Cf-Access-Jwt-Assertion` header before the request reaches the origin; the server verifies that JWT. The connector URL is `https://paprika-mcp-proxy.<subdomain>.workers.dev/mcp`.

The server exits at startup without `ACCESS_AUD`, so Terraform creates the Cloud Run service only once it is set. The first deploy after that creates it.

## Local plans

With `infra/terraform.tfvars` (gitignored; see `terraform.tfvars.example`), a local plan or apply leaves `image` empty, which keeps the live image.

```sh
GOOGLE_OAUTH_ACCESS_TOKEN="$(gcloud auth print-access-token --configuration=paprika-mcp)" terraform -chdir=infra init -backend-config="bucket=<project-id>-tfstate"
GOOGLE_OAUTH_ACCESS_TOKEN="$(gcloud auth print-access-token --configuration=paprika-mcp)" terraform -chdir=infra plan
```

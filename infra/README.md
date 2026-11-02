# Infrastructure

Terraform for the Paprika MCP server on Cloud Run: Artifact Registry, the runtime service account, the recipe cache bucket, Secret Manager containers, IAM, and the Cloud Run service. Cloudflare (Access, DNS) is configured by hand in the dashboard; the Worker proxy in `worker/` is deployed by the owner.

Every `gcloud` command uses `--configuration=paprika-mcp`. Never use Application Default Credentials; Terraform authenticates with an access token from that configuration, set inline on each command.

## 1. Bootstrap the state bucket (the only resource outside Terraform)

```sh
gcloud storage buckets create gs://<project-id>-tfstate --location=us-central1 --uniform-bucket-level-access --public-access-prevention --project=<project-id> --configuration=paprika-mcp
gcloud storage buckets update gs://<project-id>-tfstate --versioning --configuration=paprika-mcp
```

## 2. Configure and apply

```sh
cp terraform.tfvars.example terraform.tfvars   # gitignored; fill in
GOOGLE_OAUTH_ACCESS_TOKEN="$(gcloud auth print-access-token --configuration=paprika-mcp)" terraform -chdir=infra init -backend-config="bucket=<project-id>-tfstate"
GOOGLE_OAUTH_ACCESS_TOKEN="$(gcloud auth print-access-token --configuration=paprika-mcp)" terraform -chdir=infra plan
GOOGLE_OAUTH_ACCESS_TOKEN="$(gcloud auth print-access-token --configuration=paprika-mcp)" terraform -chdir=infra apply
```

With `image` or `access_aud` empty (the default) no Cloud Run service is created. Expected result: the repository, bucket, service account, and the two secrets (`paprika-username`, `paprika-password`) with no versions.

## 3. Add secret versions (owner)

Terraform never creates secret versions. Add them yourself (e.g. via `op` piped to `gcloud secrets versions add <name> --data-file=- --configuration=paprika-mcp`) before deploying; Cloud Run reads `latest`.

## 4. Build, push, deploy

```sh
make image TAG=<tag>                 # registry comes from `terraform -chdir=infra output -raw artifact_repo`
make image TAG=<tag> REGISTRY=<repo> # or pass the registry explicitly
```

`make image` builds for linux/amd64, pushes, and prints the `repo/server@sha256:...` reference. The server exits at startup without `ACCESS_AUD`, so Terraform creates the service only when both `image` and `access_aud` are set. In order:

1. In the Cloudflare dashboard, create the Access application for the hostname and copy its AUD tag.
1. Set `access_aud` to that tag and `image` to the reference above in `terraform.tfvars`. `access_team_domain` is the full issuer URL, `https://<team>.cloudflareaccess.com`.
1. Apply. This creates the Cloud Run service.

## 5. Cloudflare Worker proxy

`worker/worker.js` forwards each request, with the same path, query, headers and body, to the `run.app` origin named by `ORIGIN` and streams the response back unbuffered (MCP Streamable HTTP may use SSE).

```sh
cp worker/wrangler.toml.example worker/wrangler.toml   # set ORIGIN to `terraform output -raw run_url`, route and zone
npx wrangler deploy --config worker/wrangler.toml
```

Cloudflare Access must be in front of the Worker's hostname so the `Cf-Access-Jwt-Assertion` header is added before the request reaches the origin; the server verifies that JWT. A hostname without Access would reach Cloud Run unauthenticated by Cloudflare and be rejected by the app, but do not rely on that.

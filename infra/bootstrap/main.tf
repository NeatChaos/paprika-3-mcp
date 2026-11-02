resource "google_project_service" "apis" {
  for_each = toset(["iam", "iamcredentials", "sts", "cloudresourcemanager"])

  service            = "${each.key}.googleapis.com"
  disable_on_destroy = false
}

resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "github"

  depends_on = [google_project_service.apis]
}

# Only main under the `cloud` environment can reach the deployer; PR runs get the read-only planner.
resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github-oidc"
  attribute_mapping = {
    "google.subject"        = "assertion.sub"
    "attribute.repository"  = "assertion.repository"
    "attribute.environment" = "assertion.environment"
  }
  attribute_condition = "assertion.repository == \"${var.github_repo}\" && (assertion.environment == \"cloud-plan\" || (assertion.environment == \"cloud\" && assertion.ref == \"refs/heads/main\"))"
  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

resource "google_service_account" "deployer" {
  account_id   = "github-deployer"
  display_name = "GitHub Actions deployer (cloud environment)"
}

resource "google_service_account_iam_member" "deployer_wif" {
  service_account_id = google_service_account.deployer.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.environment/cloud"
}

# Enough for the main stack and nothing more; it cannot change project IAM, so it cannot widen itself.
resource "google_project_iam_member" "deployer" {
  for_each = toset([
    "roles/artifactregistry.admin",
    "roles/iam.serviceAccountAdmin",
    "roles/iam.serviceAccountUser",
    "roles/run.admin",
    "roles/secretmanager.admin",
    "roles/serviceusage.serviceUsageAdmin",
    "roles/storage.admin",
  ])

  project = var.project_id
  role    = each.value
  member  = "serviceAccount:${google_service_account.deployer.email}"
}

resource "google_service_account" "planner" {
  account_id   = "github-planner"
  display_name = "GitHub Actions planner (cloud-plan environment, read-only)"
}

resource "google_service_account_iam_member" "planner_wif" {
  service_account_id = google_service_account.planner.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.environment/cloud-plan"
}

# Metadata and IAM-policy reads only; no secret payloads or objects outside the state bucket.
resource "google_project_iam_custom_role" "planner" {
  role_id = "plannerRead"
  title   = "GitHub planner read"
  permissions = [
    "artifactregistry.repositories.get",
    "iam.serviceAccounts.get",
    "resourcemanager.projects.get",
    "run.services.get",
    "run.services.getIamPolicy",
    "secretmanager.secrets.get",
    "secretmanager.secrets.getIamPolicy",
    "serviceusage.services.get",
    "serviceusage.services.list",
    "storage.buckets.get",
    "storage.buckets.getIamPolicy",
  ]
}

resource "google_project_iam_member" "planner" {
  project = var.project_id
  role    = google_project_iam_custom_role.planner.name
  member  = "serviceAccount:${google_service_account.planner.email}"
}

resource "google_storage_bucket_iam_member" "planner_state" {
  bucket = "${var.project_id}-tfstate"
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.planner.email}"
}

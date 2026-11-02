locals {
  secrets = toset(["paprika-username", "paprika-password"])
}

resource "google_project_service" "apis" {
  for_each = toset(["run", "artifactregistry", "secretmanager", "storage"])

  service            = "${each.key}.googleapis.com"
  disable_on_destroy = false
}

resource "google_artifact_registry_repository" "images" {
  repository_id = "paprika-mcp"
  location      = var.region
  format        = "DOCKER"

  cleanup_policy_dry_run = false

  cleanup_policies {
    id     = "delete-old"
    action = "DELETE"
    condition {
      tag_state = "ANY"
    }
  }

  cleanup_policies {
    id     = "keep-newest-5"
    action = "KEEP"
    most_recent_versions {
      keep_count = 5
    }
  }

  depends_on = [google_project_service.apis]
}

resource "google_service_account" "server" {
  account_id   = "paprika-mcp"
  display_name = "paprika-mcp Cloud Run service"
}

resource "google_storage_bucket" "cache" {
  name                        = "${var.project_id}-paprika-cache"
  location                    = var.region
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"

  depends_on = [google_project_service.apis]
}

resource "google_storage_bucket_iam_member" "cache_access" {
  bucket = google_storage_bucket.cache.name
  role   = "roles/storage.objectUser"
  member = "serviceAccount:${google_service_account.server.email}"
}

resource "google_secret_manager_secret" "paprika" {
  for_each = local.secrets

  secret_id = each.key

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_iam_member" "accessor" {
  for_each = local.secrets

  secret_id = google_secret_manager_secret.paprika[each.key].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.server.email}"
}

locals {
  deploy_service = var.access_aud != ""
  image          = var.image != "" ? var.image : one(data.google_cloud_run_v2_service.live[*].template[0].containers[0].image)
}

# Empty var.image keeps the live image so plans show no image churn; the deploy workflow always passes one
data "google_cloud_run_v2_service" "live" {
  count = local.deploy_service && var.image == "" ? 1 : 0

  name     = "paprika-mcp"
  location = var.region
}

resource "google_cloud_run_v2_service" "server" {
  count = local.deploy_service ? 1 : 0

  name                = "paprika-mcp"
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false

  template {
    service_account = google_service_account.server.email

    scaling {
      min_instance_count = 0
      max_instance_count = 1
    }

    containers {
      image = local.image

      # The first cache load runs detached from the request, so CPU must stay allocated after the response
      resources {
        cpu_idle = false
      }

      ports {
        container_port = 8080
      }

      env {
        name  = "ACCESS_TEAM_DOMAIN"
        value = var.access_team_domain
      }
      env {
        name  = "ACCESS_AUD"
        value = var.access_aud
      }
      env {
        name  = "ALLOWED_EMAILS"
        value = join(",", var.allowed_emails)
      }
      env {
        name  = "CACHE_BUCKET"
        value = google_storage_bucket.cache.name
      }
      env {
        name = "PAPRIKA_USERNAME"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.paprika["paprika-username"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "PAPRIKA_PASSWORD"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.paprika["paprika-password"].secret_id
            version = "latest"
          }
        }
      }
    }
  }

  depends_on = [
    google_secret_manager_secret_iam_member.accessor,
    google_storage_bucket_iam_member.cache_access,
  ]
}

# Cloudflare can't present Google IAM credentials, and the app enforces Access itself.
resource "google_cloud_run_v2_service_iam_member" "public_invoker" {
  count = local.deploy_service ? 1 : 0

  name     = google_cloud_run_v2_service.server[0].name
  location = google_cloud_run_v2_service.server[0].location
  role     = "roles/run.invoker"
  member   = "allUsers"
}

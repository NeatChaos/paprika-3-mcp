output "run_url" {
  description = "run.app URL of the service; null until the service is deployed (image and access_aud set)."
  value       = one(google_cloud_run_v2_service.server[*].uri)
}

output "artifact_repo" {
  description = "Docker repository path for pushing images."
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}"
}

output "cache_bucket" {
  value = google_storage_bucket.cache.name
}

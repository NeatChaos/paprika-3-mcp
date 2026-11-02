variable "project_id" {
  description = "GCP project that hosts every resource."
  type        = string
}

variable "region" {
  description = "Region for Cloud Run, Artifact Registry and the cache bucket."
  type        = string
  default     = "us-central1"
}

variable "image" {
  description = "Image reference (repo/server@sha256:...) to deploy. Empty keeps the live image; the first create must pass one."
  type        = string
  default     = ""
}

variable "allowed_emails" {
  description = "Email addresses the server accepts in the Cloudflare Access JWT."
  type        = list(string)
}

variable "access_team_domain" {
  description = "Cloudflare Access issuer URL, https://<team>.cloudflareaccess.com."
  type        = string

  validation {
    condition     = can(regex("^https://[^/]+$", var.access_team_domain))
    error_message = "access_team_domain must look like https://<team>.cloudflareaccess.com (no trailing slash)."
  }
}

variable "access_aud" {
  description = "Audience tag of the Cloudflare Access application. Empty until the application exists; the Cloud Run service is not created while empty."
  type        = string
  default     = ""
}

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
  description = "Image reference (preferably @sha256:) to deploy. Empty creates no Cloud Run service."
  type        = string
  default     = ""
}

variable "allowed_emails" {
  description = "Email addresses the server accepts in the Cloudflare Access JWT."
  type        = list(string)
}

variable "access_team_domain" {
  description = "Cloudflare Access team domain, e.g. myteam.cloudflareaccess.com."
  type        = string
}

variable "access_aud" {
  description = "Audience tag of the Cloudflare Access application. Empty until the application exists."
  type        = string
  default     = ""
}

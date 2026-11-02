variable "project_id" {
  description = "GCP project that hosts every resource."
  type        = string
}

variable "github_repo" {
  description = "owner/name of the repository whose workflows may deploy."
  type        = string
  default     = "NeatChaos/paprika-3-mcp"
}

terraform {
  required_version = ">= 1.17"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 8.9"
    }
  }

  backend "gcs" {
    prefix = "bootstrap"
  }
}

provider "google" {
  project = var.project_id
}

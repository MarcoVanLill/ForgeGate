terraform {
  required_version = ">= 1.7.0"
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

provider "aws" {
  region                      = var.aws_region
  skip_credentials_validation = !var.enable_deployment
  skip_requesting_account_id  = !var.enable_deployment
  skip_metadata_api_check     = !var.enable_deployment
}

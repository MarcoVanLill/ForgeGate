variable "enable_deployment" {
  description = "Explicit safety switch. The default plan creates no AWS resources."
  type        = bool
  default     = false
}
variable "aws_region" {
  type    = string
  default = "us-east-1"
}
variable "name" {
  type    = string
  default = "forgegate"
}
variable "availability_zones" {
  type    = list(string)
  default = ["us-east-1a", "us-east-1b"]
}
variable "vpc_cidr" {
  type    = string
  default = "10.42.0.0/16"
}
variable "db_instance_class" {
  type    = string
  default = "db.t4g.micro"
}
variable "db_password" {
  description = "RDS master password. Supply through TF_VAR_db_password only when enable_deployment=true."
  type        = string
  sensitive   = true
  default     = null
  validation {
    condition     = !var.enable_deployment || length(var.db_password == null ? "" : var.db_password) >= 16
    error_message = "db_password must contain at least 16 characters when deployment is enabled."
  }
}
variable "tags" {
  type    = map(string)
  default = {}
}

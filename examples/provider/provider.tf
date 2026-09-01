terraform {
  required_providers {
    sylve = {
      source = "ivomarino/sylve"
    }
  }
}

provider "sylve" {
  endpoint  = "https://sylve.example.com:8181" # or SYLVE_ENDPOINT
  username  = "admin"                          # or SYLVE_USERNAME -- the users.username value, NOT an email
  password  = var.sylve_password               # or SYLVE_PASSWORD -- never hardcode this
  auth_type = "sylve"                          # or SYLVE_AUTH_TYPE -- "sylve" (default) or "pam"

  # insecure_tls = true # only for a self-signed-cert dev/test instance
}

variable "sylve_password" {
  type      = string
  sensitive = true
}

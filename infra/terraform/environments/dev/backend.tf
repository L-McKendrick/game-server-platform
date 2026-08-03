terraform {
  backend "s3" {
    key          = "environments/dev/terraform.tfstate"
    encrypt      = true
    use_lockfile = true
  }
}
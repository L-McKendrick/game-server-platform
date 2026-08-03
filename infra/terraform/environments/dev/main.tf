terraform {
  required_version = ">= 1.15.0, < 2.0.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

variable "aws_region" {
  description = "AWS region used by the development environment."
  type        = string
  default     = "us-west-2"
}

variable "project_name" {
  description = "Platform project name."
  type        = string
  default     = "game-server-platform"
}

variable "environment" {
  description = "Deployment environment."
  type        = string
  default     = "dev"
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project     = var.project_name
      Environment = var.environment
      ManagedBy   = "Terraform"
    }
  }
}

data "aws_caller_identity" "current" {}

locals {
  name_prefix = "${var.project_name}-${var.environment}"
}

resource "aws_s3_bucket" "session_assets" {
  bucket = "${local.name_prefix}-assets-${data.aws_caller_identity.current.account_id}-${var.aws_region}"
}

resource "aws_s3_bucket_versioning" "session_assets" {
  bucket = aws_s3_bucket.session_assets.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "session_assets" {
  bucket = aws_s3_bucket.session_assets.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "session_assets" {
  bucket = aws_s3_bucket.session_assets.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_dynamodb_table" "metadata" {
  name         = "${local.name_prefix}-metadata"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "pk"
  range_key    = "sk"

  attribute {
    name = "pk"
    type = "S"
  }

  attribute {
    name = "sk"
    type = "S"
  }

  point_in_time_recovery {
    enabled = true
  }

  server_side_encryption {
    enabled = true
  }
}

resource "aws_secretsmanager_secret" "discord_bot_token" {
  name                    = "/${var.project_name}/${var.environment}/discord-bot-token"
  description             = "Discord bot token for the game server platform."
  recovery_window_in_days = 7
}

resource "aws_secretsmanager_secret" "steam_credentials" {
  name                    = "/${var.project_name}/${var.environment}/steam-credentials"
  description             = "Steam credentials used during game server bootstrap."
  recovery_window_in_days = 7
}

output "session_assets_bucket_name" {
  description = "S3 bucket used for session files and archives."
  value       = aws_s3_bucket.session_assets.id
}

output "metadata_table_name" {
  description = "DynamoDB table used for platform metadata."
  value       = aws_dynamodb_table.metadata.name
}

output "discord_secret_name" {
  description = "Secrets Manager name for the Discord bot token."
  value       = aws_secretsmanager_secret.discord_bot_token.name
}

output "steam_secret_name" {
  description = "Secrets Manager name for Steam credentials."
  value       = aws_secretsmanager_secret.steam_credentials.name
}
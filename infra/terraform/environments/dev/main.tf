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

variable "discord_public_key" {
  description = "Discord Ed25519 application verification public key (hex encoded)."
  type        = string
  sensitive   = true

  validation {
    condition     = can(regex("^[0-9a-fA-F]{64}$", var.discord_public_key))
    error_message = "discord_public_key must be a 64-character hexadecimal Ed25519 public key."
  }
}

variable "discord_application_id" {
  description = "Discord application ID accepted by the interaction handler."
  type        = string
}

variable "discord_allowed_guild_ids" {
  description = "Development Discord guild IDs allowed to invoke commands."
  type        = set(string)

  validation {
    condition     = length(var.discord_allowed_guild_ids) > 0
    error_message = "At least one development Discord guild ID is required."
  }
}

variable "discord_allowed_role_ids" {
  description = "Optional bootstrap fallback role IDs; /admin access replaces these at runtime."
  type        = set(string)
  default     = []
}

variable "discord_allowed_channel_ids" {
  description = "Optional bootstrap fallback channel IDs; /admin access replaces these at runtime."
  type        = set(string)
  default     = []
}

variable "discord_interaction_lambda_package_path" {
  description = "Optional path to the packaged custom-runtime Lambda zip."
  type        = string
  default     = null
  nullable    = true
}

variable "discord_interaction_lambda_source_hash" {
  description = "Optional base64 SHA-256 supplied by CI for an immutable Lambda package."
  type        = string
  default     = null
  nullable    = true
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
  name_prefix         = "${var.project_name}-${var.environment}"
  lambda_package_path = var.discord_interaction_lambda_package_path != null ? var.discord_interaction_lambda_package_path : abspath("${path.module}/../../../../dist/discord-interactions.zip")
  discord_component_tags = {
    Component          = "discord-interactions"
    CostCenter         = "personal-project"
    DataClassification = "internal"
    Owner              = "personal-project"
  }
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

  attribute {
    name = "gsi1pk"
    type = "S"
  }

  attribute {
    name = "gsi1sk"
    type = "S"
  }

  ttl {
    attribute_name = "expires_at_epoch"
    enabled        = true
  }

  global_secondary_index {
    name = "gsi1"

    key_schema {
      attribute_name = "gsi1pk"
      key_type       = "HASH"
    }

    key_schema {
      attribute_name = "gsi1sk"
      key_type       = "RANGE"
    }

    projection_type = "ALL"
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

resource "aws_sqs_queue" "artifact_ingest_dlq" {
  name                      = "${local.name_prefix}-artifact-ingest-dlq.fifo"
  fifo_queue                = true
  message_retention_seconds = 1209600
  sqs_managed_sse_enabled   = true
}

resource "aws_sqs_queue" "artifact_ingest" {
  name                       = "${local.name_prefix}-artifact-ingest.fifo"
  fifo_queue                 = true
  visibility_timeout_seconds = 300
  message_retention_seconds  = 1209600
  sqs_managed_sse_enabled    = true
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.artifact_ingest_dlq.arn
    maxReceiveCount     = 5
  })
}

data "aws_iam_policy_document" "discord_lambda_assume_role" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "discord_lambda" {
  name               = "${local.name_prefix}-discord-interactions-execution"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
  tags               = local.discord_component_tags
}

data "aws_iam_policy_document" "discord_lambda" {
  statement {
    sid = "MetadataAccess"
    actions = [
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:Query",
      "dynamodb:TransactWriteItems",
    ]
    resources = [
      aws_dynamodb_table.metadata.arn,
      "${aws_dynamodb_table.metadata.arn}/index/gsi1",
    ]
  }

  statement {
    sid       = "ArtifactQueueSend"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.artifact_ingest.arn]
  }

  statement {
    sid       = "CommandQueueSend"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.commands.arn]
  }


  statement {
    sid = "RuntimeLogDelivery"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["${aws_cloudwatch_log_group.discord_lambda.arn}:*"]
  }
}

resource "aws_iam_role_policy" "discord_lambda" {
  name   = "${local.name_prefix}-discord-interactions-runtime"
  role   = aws_iam_role.discord_lambda.id
  policy = data.aws_iam_policy_document.discord_lambda.json
}

resource "aws_cloudwatch_log_group" "discord_lambda" {
  name              = "/aws/lambda/${local.name_prefix}-discord-interactions"
  retention_in_days = 30
  tags              = local.discord_component_tags
}

resource "aws_lambda_function" "discord_interactions" {
  function_name    = "${local.name_prefix}-discord-interactions"
  description      = "Verifies and handles Discord interactions for the game server platform."
  role             = aws_iam_role.discord_lambda.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.lambda_package_path
  source_code_hash = var.discord_interaction_lambda_source_hash != null ? var.discord_interaction_lambda_source_hash : try(filebase64sha256(local.lambda_package_path), null)
  timeout          = 5
  memory_size      = 256
  tags             = local.discord_component_tags

  environment {
    variables = {
      APP_ENV                           = var.environment
      LOG_LEVEL                         = "info"
      METADATA_TABLE_NAME               = aws_dynamodb_table.metadata.name
      ARTIFACT_QUEUE_URL                = aws_sqs_queue.artifact_ingest.url
      COMMAND_QUEUE_URL                 = aws_sqs_queue.commands.url
      PROVISIONING_ENABLED              = tostring(var.provisioning_enabled)
      DISCORD_PUBLIC_KEY                = var.discord_public_key
      DISCORD_APPLICATION_ID            = var.discord_application_id
      DISCORD_ALLOWED_GUILD_IDS         = join(",", sort(tolist(var.discord_allowed_guild_ids)))
      DISCORD_ALLOWED_ROLE_IDS          = join(",", sort(tolist(var.discord_allowed_role_ids)))
      DISCORD_ALLOWED_CHANNEL_IDS       = join(",", sort(tolist(var.discord_allowed_channel_ids)))
      DISCORD_MAX_REQUEST_BYTES         = "65536"
      DISCORD_SIGNATURE_MAX_AGE_SECONDS = "300"
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.discord_lambda,
    aws_iam_role_policy.discord_lambda,
  ]

}

resource "aws_apigatewayv2_api" "discord" {
  name          = "${local.name_prefix}-discord-interactions"
  description   = "Public Discord interaction endpoint for the development environment."
  protocol_type = "HTTP"
  tags          = local.discord_component_tags
}

resource "aws_apigatewayv2_integration" "discord_lambda" {
  api_id                 = aws_apigatewayv2_api.discord.id
  integration_type       = "AWS_PROXY"
  integration_uri        = aws_lambda_function.discord_interactions.invoke_arn
  payload_format_version = "2.0"
  timeout_milliseconds   = 5000
}

resource "aws_apigatewayv2_route" "discord_interactions" {
  api_id    = aws_apigatewayv2_api.discord.id
  route_key = "POST /discord/interactions"
  target    = "integrations/${aws_apigatewayv2_integration.discord_lambda.id}"
}

resource "aws_cloudwatch_log_group" "discord_api" {
  name              = "/aws/apigateway/${local.name_prefix}-discord-interactions"
  retention_in_days = 30
  tags              = local.discord_component_tags
}

resource "aws_apigatewayv2_stage" "discord" {
  api_id      = aws_apigatewayv2_api.discord.id
  name        = "$default"
  auto_deploy = true

  default_route_settings {
    throttling_burst_limit = 20
    throttling_rate_limit  = 10
  }

  access_log_settings {
    destination_arn = aws_cloudwatch_log_group.discord_api.arn
    format = jsonencode({
      requestId        = "$context.requestId"
      routeKey         = "$context.routeKey"
      status           = "$context.status"
      responseLength   = "$context.responseLength"
      integrationError = "$context.integrationErrorMessage"
      sourceIp         = "$context.identity.sourceIp"
      userAgent        = "$context.identity.userAgent"
    })
  }

  tags = local.discord_component_tags
}

resource "aws_lambda_permission" "discord_api" {
  statement_id  = "AllowDiscordHTTPAPIInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.discord_interactions.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.discord.execution_arn}/*/POST/discord/interactions"
}

resource "aws_cloudwatch_metric_alarm" "discord_interactions_errors" {
  alarm_name          = "${local.name_prefix}-discord-interactions-errors"
  alarm_description   = "Discord interaction Lambda reported one or more errors."
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  datapoints_to_alarm = 1
  metric_name         = "Errors"
  namespace           = "AWS/Lambda"
  period              = 300
  statistic           = "Sum"
  threshold           = 1
  treat_missing_data  = "notBreaching"

  dimensions = {
    FunctionName = aws_lambda_function.discord_interactions.function_name
  }

  tags = local.discord_component_tags
}

resource "aws_cloudwatch_metric_alarm" "discord_interactions_duration" {
  alarm_name          = "${local.name_prefix}-discord-interactions-duration-p95"
  alarm_description   = "Discord interaction Lambda p95 duration is approaching the response deadline."
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  datapoints_to_alarm = 1
  metric_name         = "Duration"
  namespace           = "AWS/Lambda"
  period              = 300
  extended_statistic  = "p95"
  threshold           = 2500
  treat_missing_data  = "notBreaching"

  dimensions = {
    FunctionName = aws_lambda_function.discord_interactions.function_name
  }

  tags = local.discord_component_tags
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

output "discord_interactions_endpoint" {
  description = "Public Discord interaction endpoint to configure in the developer portal."
  value       = "${aws_apigatewayv2_api.discord.api_endpoint}/discord/interactions"
}

output "discord_interactions_endpoint_url" {
  description = "Compatibility alias for the public Discord interaction endpoint."
  value       = "${aws_apigatewayv2_api.discord.api_endpoint}/discord/interactions"
}

output "discord_interactions_function_name" {
  description = "Discord interaction Lambda function name."
  value       = aws_lambda_function.discord_interactions.function_name
}

output "artifact_ingest_queue_url" {
  description = "FIFO queue containing validated attachment-ingest requests for Phase 4 workers."
  value       = aws_sqs_queue.artifact_ingest.url
}

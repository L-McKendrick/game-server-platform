variable "discord_interaction_lambda_package_path" {
  description = "Local path to the packaged Discord interaction Lambda ZIP file."
  type        = string

  validation {
    condition     = trimspace(var.discord_interaction_lambda_package_path) != ""
    error_message = "discord_interaction_lambda_package_path cannot be empty."
  }
}

variable "discord_public_key" {
  description = "Discord application public verification key as 64 hexadecimal characters."
  type        = string
  sensitive   = true

  validation {
    condition     = can(regex("^[0-9A-Fa-f]{64}$", var.discord_public_key))
    error_message = "discord_public_key must contain exactly 64 hexadecimal characters."
  }
}

variable "discord_application_id" {
  description = "Discord application ID accepted by the interaction handler."
  type        = string

  validation {
    condition     = can(regex("^[0-9]+$", var.discord_application_id))
    error_message = "discord_application_id must contain only decimal digits."
  }
}

variable "discord_allowed_guild_ids" {
  description = "Development Discord guild IDs allowed to execute commands."
  type        = list(string)

  validation {
    condition = (
      length(var.discord_allowed_guild_ids) > 0 &&
      alltrue([
        for guild_id in var.discord_allowed_guild_ids :
        can(regex("^[0-9]+$", guild_id))
      ])
    )
    error_message = "discord_allowed_guild_ids must contain at least one numeric guild ID."
  }
}

variable "discord_interaction_log_retention_days" {
  description = "CloudWatch Logs retention for Discord interaction Lambda and API logs."
  type        = number
  default     = 14
}

locals {
  discord_interactions_function_name = "${local.name_prefix}-discord-interactions"
  discord_interactions_package_path  = abspath(var.discord_interaction_lambda_package_path)
  discord_interactions_tags = {
    Component          = "discord-interactions"
    Owner              = "personal-project"
    CostCenter         = "personal-project"
    DataClassification = "internal"
  }
}

resource "aws_cloudwatch_log_group" "discord_interactions_lambda" {
  name              = "/aws/lambda/${local.discord_interactions_function_name}"
  retention_in_days = var.discord_interaction_log_retention_days
  tags              = local.discord_interactions_tags
}

resource "aws_cloudwatch_log_group" "discord_interactions_api" {
  name              = "/aws/apigateway/${local.discord_interactions_function_name}"
  retention_in_days = var.discord_interaction_log_retention_days
  tags              = local.discord_interactions_tags
}

resource "aws_iam_role" "discord_interactions_lambda" {
  name = "${local.discord_interactions_function_name}-execution"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "lambda.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      }
    ]
  })

  tags = local.discord_interactions_tags
}

resource "aws_iam_role_policy" "discord_interactions_lambda" {
  name = "${local.discord_interactions_function_name}-runtime"
  role = aws_iam_role.discord_interactions_lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "WriteFunctionLogs"
        Effect = "Allow"
        Action = [
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = "${aws_cloudwatch_log_group.discord_interactions_lambda.arn}:*"
      },
      {
        Sid    = "ReadAndWriteSessionMetadata"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem"
        ]
        Resource = aws_dynamodb_table.metadata.arn
      },
      {
        Sid      = "ListSessionsByOwner"
        Effect   = "Allow"
        Action   = "dynamodb:Query"
        Resource = "${aws_dynamodb_table.metadata.arn}/index/gsi1"
      }
    ]
  })
}

resource "aws_lambda_function" "discord_interactions" {
  filename         = local.discord_interactions_package_path
  source_code_hash = filebase64sha256(local.discord_interactions_package_path)

  function_name = local.discord_interactions_function_name
  description   = "Verifies and handles Discord game-server session commands."
  role          = aws_iam_role.discord_interactions_lambda.arn
  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["arm64"]

  memory_size = 256
  timeout     = 5

  environment {
    variables = {
      APP_ENV                           = var.environment
      LOG_LEVEL                         = "info"
      METADATA_TABLE_NAME               = aws_dynamodb_table.metadata.name
      IDEMPOTENCY_RETENTION_HOURS       = "168"
      DISCORD_PUBLIC_KEY                = var.discord_public_key
      DISCORD_APPLICATION_ID            = var.discord_application_id
      DISCORD_ALLOWED_GUILD_IDS         = join(",", var.discord_allowed_guild_ids)
      DISCORD_MAX_REQUEST_BYTES         = "65536"
      DISCORD_SIGNATURE_MAX_AGE_SECONDS = "300"
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.discord_interactions_lambda,
    aws_iam_role_policy.discord_interactions_lambda,
  ]

  tags = local.discord_interactions_tags
}

resource "aws_apigatewayv2_api" "discord_interactions" {
  name          = local.discord_interactions_function_name
  protocol_type = "HTTP"
  description   = "Public Discord interaction endpoint for the development environment."

  tags = local.discord_interactions_tags
}

resource "aws_apigatewayv2_integration" "discord_interactions" {
  api_id = aws_apigatewayv2_api.discord_interactions.id

  integration_type   = "AWS_PROXY"
  integration_method = "POST"
  integration_uri    = aws_lambda_function.discord_interactions.invoke_arn

  payload_format_version = "2.0"
  timeout_milliseconds   = 5000
}

resource "aws_apigatewayv2_route" "discord_interactions" {
  api_id = aws_apigatewayv2_api.discord_interactions.id

  route_key = "POST /discord/interactions"
  target    = "integrations/${aws_apigatewayv2_integration.discord_interactions.id}"
}

resource "aws_apigatewayv2_stage" "discord_interactions" {
  api_id = aws_apigatewayv2_api.discord_interactions.id

  name        = "$default"
  auto_deploy = true

  default_route_settings {
    throttling_burst_limit = 20
    throttling_rate_limit  = 10
  }

  access_log_settings {
    destination_arn = aws_cloudwatch_log_group.discord_interactions_api.arn
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

  tags = local.discord_interactions_tags
}

resource "aws_lambda_permission" "discord_interactions_api" {
  statement_id  = "AllowDiscordHTTPAPIInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.discord_interactions.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.discord_interactions.execution_arn}/*/POST/discord/interactions"
}

resource "aws_cloudwatch_metric_alarm" "discord_interactions_errors" {
  alarm_name          = "${local.discord_interactions_function_name}-errors"
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

  tags = local.discord_interactions_tags
}

resource "aws_cloudwatch_metric_alarm" "discord_interactions_duration" {
  alarm_name          = "${local.discord_interactions_function_name}-duration-p95"
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

  tags = local.discord_interactions_tags
}

output "discord_interactions_endpoint_url" {
  description = "HTTPS endpoint to configure in the Discord developer portal."
  value       = "${aws_apigatewayv2_api.discord_interactions.api_endpoint}/discord/interactions"
}

output "discord_interactions_function_name" {
  description = "Lambda function that handles Discord interactions."
  value       = aws_lambda_function.discord_interactions.function_name
}

variable "artifact_worker_lambda_package_path" {
  description = "Optional path to the packaged artifact worker Lambda zip."
  type        = string
  default     = null
  nullable    = true
}

variable "artifact_worker_lambda_source_hash" {
  description = "Optional base64 SHA-256 supplied by CI for the artifact worker package."
  type        = string
  default     = null
  nullable    = true
}

variable "notification_worker_lambda_package_path" {
  description = "Optional path to the packaged notification worker Lambda zip."
  type        = string
  default     = null
  nullable    = true
}

variable "notification_worker_lambda_source_hash" {
  description = "Optional base64 SHA-256 supplied by CI for the notification worker package."
  type        = string
  default     = null
  nullable    = true
}

locals {
  artifact_worker_package_path     = var.artifact_worker_lambda_package_path != null ? var.artifact_worker_lambda_package_path : abspath("${path.module}/../../../../dist/artifact-worker.zip")
  notification_worker_package_path = var.notification_worker_lambda_package_path != null ? var.notification_worker_lambda_package_path : abspath("${path.module}/../../../../dist/notification-worker.zip")
  workflow_types = toset([
    "BootstrapGameServer",
    "SleepSession",
    "WakeSession",
    "ArchiveSession",
    "RestoreSession",
    "DestroySession",
    "ReconcileSession",
  ])
}

resource "aws_sqs_queue" "command_dlq" {
  name                      = "${local.name_prefix}-command-dlq.fifo"
  fifo_queue                = true
  message_retention_seconds = 1209600
  sqs_managed_sse_enabled   = true
}

resource "aws_sqs_queue" "commands" {
  name                       = "${local.name_prefix}-commands.fifo"
  fifo_queue                 = true
  visibility_timeout_seconds = 300
  message_retention_seconds  = 1209600
  sqs_managed_sse_enabled    = true
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.command_dlq.arn
    maxReceiveCount     = 5
  })
}

resource "aws_sqs_queue" "notification_dlq" {
  name                      = "${local.name_prefix}-notification-dlq.fifo"
  fifo_queue                = true
  message_retention_seconds = 1209600
  sqs_managed_sse_enabled   = true
}

resource "aws_sqs_queue" "notifications" {
  name                       = "${local.name_prefix}-notifications.fifo"
  fifo_queue                 = true
  visibility_timeout_seconds = 90
  message_retention_seconds  = 1209600
  sqs_managed_sse_enabled    = true
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.notification_dlq.arn
    maxReceiveCount     = 5
  })
}

data "aws_iam_policy_document" "artifact_worker" {
  statement {
    sid = "MetadataAccess"
    actions = [
      "dynamodb:GetItem",
      "dynamodb:TransactWriteItems",
    ]
    resources = [aws_dynamodb_table.metadata.arn]
  }

  statement {
    sid       = "ValidatedArtifactWrite"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.session_assets.arn}/sessions/*/input/*"]
  }

  statement {
    sid       = "NotificationSend"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.notifications.arn]
  }

  statement {
    sid = "ArtifactQueueConsume"
    actions = [
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:GetQueueAttributes",
    ]
    resources = [aws_sqs_queue.artifact_ingest.arn]
  }

  statement {
    sid = "RuntimeLogDelivery"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["${aws_cloudwatch_log_group.artifact_worker.arn}:*"]
  }
}

resource "aws_iam_role" "artifact_worker" {
  name               = "${local.name_prefix}-artifact-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}

resource "aws_iam_role_policy" "artifact_worker" {
  name   = "runtime"
  role   = aws_iam_role.artifact_worker.id
  policy = data.aws_iam_policy_document.artifact_worker.json
}

resource "aws_cloudwatch_log_group" "artifact_worker" {
  name              = "/aws/lambda/${local.name_prefix}-artifact-worker"
  retention_in_days = 30
}

resource "aws_lambda_function" "artifact_worker" {
  function_name    = "${local.name_prefix}-artifact-worker"
  description      = "Downloads, validates, hashes, and stores Discord session artifacts."
  role             = aws_iam_role.artifact_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.artifact_worker_package_path
  source_code_hash = var.artifact_worker_lambda_source_hash != null ? var.artifact_worker_lambda_source_hash : try(filebase64sha256(local.artifact_worker_package_path), null)
  timeout          = 45
  memory_size      = 512

  environment {
    variables = {
      APP_ENV                     = var.environment
      LOG_LEVEL                   = "info"
      METADATA_TABLE_NAME         = aws_dynamodb_table.metadata.name
      SESSION_ASSETS_BUCKET       = aws_s3_bucket.session_assets.id
      NOTIFICATION_QUEUE_URL      = aws_sqs_queue.notifications.url
      IDEMPOTENCY_RETENTION_HOURS = "168"
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.artifact_worker,
    aws_iam_role_policy.artifact_worker,
  ]
}

resource "aws_lambda_event_source_mapping" "artifact_worker" {
  event_source_arn        = aws_sqs_queue.artifact_ingest.arn
  function_name           = aws_lambda_function.artifact_worker.arn
  batch_size              = 1
  function_response_types = ["ReportBatchItemFailures"]
}

data "aws_iam_policy_document" "notification_worker" {
  statement {
    sid       = "DiscordTokenRead"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.discord_bot_token.arn]
  }

  statement {
    sid = "NotificationQueueConsume"
    actions = [
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:GetQueueAttributes",
    ]
    resources = [aws_sqs_queue.notifications.arn]
  }

  statement {
    sid = "RuntimeLogDelivery"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["${aws_cloudwatch_log_group.notification_worker.arn}:*"]
  }
}

resource "aws_iam_role" "notification_worker" {
  name               = "${local.name_prefix}-notification-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}

resource "aws_iam_role_policy" "notification_worker" {
  name   = "runtime"
  role   = aws_iam_role.notification_worker.id
  policy = data.aws_iam_policy_document.notification_worker.json
}

resource "aws_cloudwatch_log_group" "notification_worker" {
  name              = "/aws/lambda/${local.name_prefix}-notification-worker"
  retention_in_days = 30
}

resource "aws_lambda_function" "notification_worker" {
  function_name    = "${local.name_prefix}-notification-worker"
  description      = "Delivers bounded asynchronous Discord notifications."
  role             = aws_iam_role.notification_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.notification_worker_package_path
  source_code_hash = var.notification_worker_lambda_source_hash != null ? var.notification_worker_lambda_source_hash : try(filebase64sha256(local.notification_worker_package_path), null)
  timeout          = 15
  memory_size      = 256

  environment {
    variables = {
      APP_ENV             = var.environment
      LOG_LEVEL           = "info"
      METADATA_TABLE_NAME = aws_dynamodb_table.metadata.name
      DISCORD_SECRET_NAME = aws_secretsmanager_secret.discord_bot_token.name
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.notification_worker,
    aws_iam_role_policy.notification_worker,
  ]
}

resource "aws_lambda_event_source_mapping" "notification_worker" {
  event_source_arn        = aws_sqs_queue.notifications.arn
  function_name           = aws_lambda_function.notification_worker.arn
  batch_size              = 5
  function_response_types = ["ReportBatchItemFailures"]
}

data "aws_iam_policy_document" "workflow_assume_role" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["states.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "workflow" {
  name               = "${local.name_prefix}-workflows"
  assume_role_policy = data.aws_iam_policy_document.workflow_assume_role.json
}

resource "aws_sfn_state_machine" "workflow" {
  for_each = local.workflow_types

  name     = "${local.name_prefix}-${each.value}"
  role_arn = aws_iam_role.workflow.arn
  type     = "STANDARD"
  definition = jsonencode({
    Comment = "Phase 4 contract boundary; implementation arrives in its lifecycle phase."
    StartAt = "NotImplemented"
    States = {
      NotImplemented = {
        Type  = "Fail"
        Error = "PhaseNotImplemented"
        Cause = "The workflow contract exists, but its infrastructure implementation is not enabled yet."
      }
    }
  })
}

output "command_queue_url" {
  description = "FIFO queue for normalized asynchronous lifecycle commands."
  value       = aws_sqs_queue.commands.url
}

output "notification_queue_url" {
  description = "FIFO queue consumed by the Discord notification worker."
  value       = aws_sqs_queue.notifications.url
}

output "workflow_state_machine_arns" {
  description = "Canonical Step Functions Standard workflow ARNs."
  value = merge(
    { for name, machine in aws_sfn_state_machine.workflow : name => machine.arn },
    { ProvisionSession = aws_sfn_state_machine.provision_session.arn },
  )
}

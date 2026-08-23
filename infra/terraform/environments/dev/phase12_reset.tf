variable "reset_enabled" {
  description = "Enables the Administrator-only full runtime reset. Keep false until deployment review is complete."
  type        = bool
  default     = false
}

variable "reset_worker_lambda_package_path" {
  description = "Optional path to the packaged reset worker Lambda zip."
  type        = string
  default     = null
  nullable    = true
}

variable "reset_worker_lambda_source_hash" {
  description = "Optional base64 SHA-256 supplied by CI for the immutable reset worker package."
  type        = string
  default     = null
  nullable    = true
}

locals {
  reset_worker_package_path = var.reset_worker_lambda_package_path != null ? var.reset_worker_lambda_package_path : abspath("${path.module}/../../../../dist/reset-worker.zip")
  reset_runtime_queues = [
    aws_sqs_queue.artifact_ingest,
    aws_sqs_queue.artifact_ingest_dlq,
    aws_sqs_queue.commands,
    aws_sqs_queue.command_dlq,
    aws_sqs_queue.notifications,
    aws_sqs_queue.notification_dlq,
  ]
  reset_state_machines = [
    aws_sfn_state_machine.workflow,
    aws_sfn_state_machine.provision_session,
    aws_sfn_state_machine.bootstrap_game_server,
  ]
  reset_application_log_groups = [
    aws_cloudwatch_log_group.discord_lambda,
    aws_cloudwatch_log_group.artifact_worker,
    aws_cloudwatch_log_group.notification_worker,
    aws_cloudwatch_log_group.command_worker,
    aws_cloudwatch_log_group.provisioning_worker,
    aws_cloudwatch_log_group.bootstrap_worker,
    aws_cloudwatch_log_group.monitor_worker,
    aws_cloudwatch_log_group.sleepwake_worker,
    aws_cloudwatch_log_group.archive_worker,
    aws_cloudwatch_log_group.restore_worker,
    aws_cloudwatch_log_group.termination_worker,
    aws_cloudwatch_log_group.reliability_worker,
  ]
}

resource "aws_sqs_queue" "reset_dlq" {
  name                      = "${local.name_prefix}-reset-dlq.fifo"
  fifo_queue                = true
  message_retention_seconds = 1209600
  sqs_managed_sse_enabled   = true
}

resource "aws_sqs_queue" "reset" {
  name                       = "${local.name_prefix}-reset.fifo"
  fifo_queue                 = true
  visibility_timeout_seconds = 5400
  message_retention_seconds  = 1209600
  sqs_managed_sse_enabled    = true
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.reset_dlq.arn
    maxReceiveCount     = 3
  })
}

resource "aws_sqs_queue_redrive_allow_policy" "reset" {
  queue_url = aws_sqs_queue.reset_dlq.url
  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.reset.arn]
  })
}

resource "aws_cloudwatch_log_group" "reset_worker" {
  name              = "/aws/lambda/${local.name_prefix}-reset-worker"
  retention_in_days = 30
}

data "aws_iam_policy_document" "reset_worker" {
  statement {
    sid = "ResetMetadata"
    actions = [
      "dynamodb:DeleteItem",
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:Scan",
      "dynamodb:TransactWriteItems",
    ]
    resources = [aws_dynamodb_table.metadata.arn]
  }

  statement {
    sid       = "ListSessionArtifacts"
    actions   = ["s3:ListBucketVersions"]
    resources = [aws_s3_bucket.session_assets.arn]
    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["sessions/*"]
    }
  }

  statement {
    sid       = "DeleteSessionArtifacts"
    actions   = ["s3:DeleteObject", "s3:DeleteObjectVersion"]
    resources = ["${aws_s3_bucket.session_assets.arn}/sessions/*"]
  }

  statement {
    sid       = "DescribeOwnedCompute"
    actions   = ["ec2:DescribeInstances", "ec2:DescribeVolumes"]
    resources = ["*"]
  }

  statement {
    sid       = "TerminateOwnedInstances"
    actions   = ["ec2:TerminateInstances"]
    resources = ["arn:aws:ec2:${var.aws_region}:${data.aws_caller_identity.current.account_id}:instance/*"]
    condition {
      test     = "StringEquals"
      variable = "ec2:ResourceTag/Project"
      values   = [var.project_name]
    }
    condition {
      test     = "StringEquals"
      variable = "ec2:ResourceTag/Environment"
      values   = [var.environment]
    }
  }

  statement {
    sid       = "DeleteOwnedVolumes"
    actions   = ["ec2:DeleteVolume"]
    resources = ["arn:aws:ec2:${var.aws_region}:${data.aws_caller_identity.current.account_id}:volume/*"]
    condition {
      test     = "StringEquals"
      variable = "ec2:ResourceTag/Project"
      values   = [var.project_name]
    }
    condition {
      test     = "StringEquals"
      variable = "ec2:ResourceTag/Environment"
      values   = [var.environment]
    }
  }

  statement {
    sid       = "PurgeRuntimeQueues"
    actions   = ["sqs:PurgeQueue"]
    resources = [for queue in local.reset_runtime_queues : queue.arn]
  }

  statement {
    sid       = "ConsumeResetQueue"
    actions   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"]
    resources = [aws_sqs_queue.reset.arn]
  }

  statement {
    sid       = "InspectWorkflows"
    actions   = ["states:ListExecutions"]
    resources = [for machine in local.reset_state_machines : machine.arn]
  }

  statement {
    sid       = "StopWorkflowExecutions"
    actions   = ["states:StopExecution"]
    resources = [for machine in local.reset_state_machines : "arn:aws:states:${var.aws_region}:${data.aws_caller_identity.current.account_id}:execution:${split(":", machine.arn)[6]}:*"]
  }

  statement {
    sid       = "DeleteEligibleLogStreams"
    actions   = ["logs:DescribeLogStreams", "logs:DeleteLogStream"]
    resources = [for group in local.reset_application_log_groups : "${group.arn}:*"]
  }

  statement {
    sid       = "DiscordTokenRead"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.discord_bot_token.arn]
  }

  statement {
    sid       = "RuntimeLogDelivery"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.reset_worker.arn}:*"]
  }
}

resource "aws_iam_role" "reset_worker" {
  name               = "${local.name_prefix}-reset-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}

resource "aws_iam_role_policy" "reset_worker" {
  name   = "runtime"
  role   = aws_iam_role.reset_worker.id
  policy = data.aws_iam_policy_document.reset_worker.json
}

resource "aws_lambda_function" "reset_worker" {
  function_name    = "${local.name_prefix}-reset-worker"
  description      = "Performs an explicitly confirmed reset of platform-owned runtime state."
  role             = aws_iam_role.reset_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.reset_worker_package_path
  source_code_hash = var.reset_worker_lambda_source_hash != null ? var.reset_worker_lambda_source_hash : try(filebase64sha256(local.reset_worker_package_path), null)
  timeout          = 900
  memory_size      = 512

  environment {
    variables = {
      APP_ENV                  = var.environment
      AWS_REGION               = var.aws_region
      LOG_LEVEL                = "info"
      PROJECT_NAME             = var.project_name
      METADATA_TABLE_NAME      = aws_dynamodb_table.metadata.name
      SESSION_ASSETS_BUCKET    = aws_s3_bucket.session_assets.id
      DISCORD_SECRET_NAME      = aws_secretsmanager_secret.discord_bot_token.name
      RESET_RUNTIME_QUEUE_URLS = join(",", [for queue in local.reset_runtime_queues : queue.url])
      RESET_STATE_MACHINE_ARNS = join(",", [for machine in local.reset_state_machines : machine.arn])
      RESET_LOG_GROUPS         = join(",", [for group in local.reset_application_log_groups : group.name])
    }
  }

  depends_on = [aws_cloudwatch_log_group.reset_worker, aws_iam_role_policy.reset_worker]
}

resource "aws_lambda_event_source_mapping" "reset_worker" {
  enabled                 = var.reset_enabled
  event_source_arn        = aws_sqs_queue.reset.arn
  function_name           = aws_lambda_function.reset_worker.arn
  batch_size              = 1
  function_response_types = ["ReportBatchItemFailures"]
}

output "reset_enabled" {
  description = "Whether the Administrator-only reset is enabled."
  value       = var.reset_enabled
}

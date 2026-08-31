variable "reliability_worker_lambda_package_path" {
  description = "Optional path to the packaged Phase 10 reliability worker Lambda zip."
  type        = string
  default     = null
  nullable    = true
}

variable "reliability_worker_lambda_source_hash" {
  description = "Optional base64 SHA-256 supplied by CI for an immutable reliability worker package."
  type        = string
  default     = null
  nullable    = true
}

locals {
  reliability_worker_package_path = var.reliability_worker_lambda_package_path != null ? var.reliability_worker_lambda_package_path : abspath("${path.module}/../../../../dist/reliability-worker.zip")

  # Retry only invocation-service failures. Application errors remain terminal and
  # are handled by each workflow's existing catch/rollback path.
  lambda_transient_retry = {
    ErrorEquals = [
      "Lambda.ServiceException",
      "Lambda.AWSLambdaException",
      "Lambda.SdkClientException",
      "Lambda.TooManyRequestsException",
    ]
    IntervalSeconds = 2
    BackoffRate     = 2
    MaxAttempts     = 3
    MaxDelaySeconds = 30
    JitterStrategy  = "FULL"
  }

  reliability_dlqs = {
    command      = aws_sqs_queue.command_dlq
    notification = aws_sqs_queue.notification_dlq
    artifact     = aws_sqs_queue.artifact_ingest_dlq
  }
}

resource "aws_sqs_queue_redrive_allow_policy" "command" {
  queue_url = aws_sqs_queue.command_dlq.id
  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.commands.arn]
  })
}

resource "aws_sqs_queue_redrive_allow_policy" "notification" {
  queue_url = aws_sqs_queue.notification_dlq.id
  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.notifications.arn]
  })
}

resource "aws_sqs_queue_redrive_allow_policy" "artifact" {
  queue_url = aws_sqs_queue.artifact_ingest_dlq.id
  redrive_allow_policy = jsonencode({
    redrivePermission = "byQueue"
    sourceQueueArns   = [aws_sqs_queue.artifact_ingest.arn]
  })
}

resource "aws_cloudwatch_log_group" "reliability_worker" {
  name              = "/aws/lambda/${local.name_prefix}-reliability-worker"
  retention_in_days = 30
}

data "aws_iam_policy_document" "reliability_worker" {
  statement {
    actions = [
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:Query",
      "dynamodb:Scan",
      "dynamodb:TransactWriteItems",
    ]
    resources = [aws_dynamodb_table.metadata.arn]
  }

  statement {
    actions   = ["states:DescribeExecution"]
    resources = ["arn:aws:states:${var.aws_region}:${data.aws_caller_identity.current.account_id}:execution:${local.name_prefix}-*:*"]
  }

  statement {
    actions   = ["ssm:GetCommandInvocation", "ssm:ListCommands", "ssm:CancelCommand"]
    resources = ["*"]
  }

  statement {
    actions = [
      "ec2:DescribeInstances",
      "ec2:DescribeSecurityGroups",
      "ec2:DescribeVolumes",
    ]
    resources = ["*"]
  }

  statement {
    actions = [
      "ec2:CreateTags",
      "ec2:DeleteVolume",
      "ec2:TerminateInstances",
    ]
    resources = [
      "arn:aws:ec2:${var.aws_region}:${data.aws_caller_identity.current.account_id}:instance/*",
      "arn:aws:ec2:${var.aws_region}:${data.aws_caller_identity.current.account_id}:volume/*",
    ]
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
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.session_assets.arn]
    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["sessions/*"]
    }
  }

  statement {
    actions = [
      "sqs:DeleteMessage",
      "sqs:GetQueueAttributes",
      "sqs:ReceiveMessage",
      "sqs:StartMessageMoveTask",
    ]
    resources = [for queue in values(local.reliability_dlqs) : queue.arn]
  }

  statement {
    actions = [
      "sqs:GetQueueAttributes",
      "sqs:SendMessage",
    ]
    resources = [
      aws_sqs_queue.commands.arn,
      aws_sqs_queue.notifications.arn,
      aws_sqs_queue.artifact_ingest.arn,
    ]
  }

  statement {
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.reliability_worker.arn}:*"]
  }
}

resource "aws_iam_role" "reliability_worker" {
  name               = "${local.name_prefix}-reliability-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}

resource "aws_iam_role_policy" "reliability_worker" {
  name   = "runtime"
  role   = aws_iam_role.reliability_worker.id
  policy = data.aws_iam_policy_document.reliability_worker.json
}

resource "aws_lambda_function" "reliability_worker" {
  function_name    = "${local.name_prefix}-reliability-worker"
  role             = aws_iam_role.reliability_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.reliability_worker_package_path
  source_code_hash = var.reliability_worker_lambda_source_hash != null ? var.reliability_worker_lambda_source_hash : try(filebase64sha256(local.reliability_worker_package_path), null)
  timeout          = 300
  memory_size      = 256

  environment {
    variables = {
      APP_ENV                  = var.environment
      LOG_LEVEL                = "info"
      PROJECT_NAME             = var.project_name
      METADATA_TABLE_NAME      = aws_dynamodb_table.metadata.name
      SESSION_ASSETS_BUCKET    = aws_s3_bucket.session_assets.bucket
      COMMAND_DLQ_URL          = aws_sqs_queue.command_dlq.url
      COMMAND_DLQ_ARN          = aws_sqs_queue.command_dlq.arn
      COMMAND_QUEUE_ARN        = aws_sqs_queue.commands.arn
      NOTIFICATION_DLQ_URL     = aws_sqs_queue.notification_dlq.url
      NOTIFICATION_DLQ_ARN     = aws_sqs_queue.notification_dlq.arn
      NOTIFICATION_QUEUE_ARN   = aws_sqs_queue.notifications.arn
      ARTIFACT_DLQ_URL         = aws_sqs_queue.artifact_ingest_dlq.url
      ARTIFACT_DLQ_ARN         = aws_sqs_queue.artifact_ingest_dlq.arn
      ARTIFACT_QUEUE_ARN       = aws_sqs_queue.artifact_ingest.arn
      ORPHAN_MINIMUM_AGE_HOURS = "24"
      ORPHAN_QUARANTINE_HOURS  = "24"
      BOOTSTRAP_SCRIPT_KEY     = aws_s3_object.bootstrap_script.key
      STEAM_AUTH_SECRET_ID     = aws_secretsmanager_secret.steam_authorization_cache.name
      TEAMSPEAK_VERSION        = var.teamspeak_version
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.reliability_worker,
    aws_iam_role_policy.reliability_worker,
  ]
}

resource "aws_cloudwatch_event_rule" "reliability_scan" {
  name                = "${local.name_prefix}-reliability-scan"
  description         = "Reconcile stale workflows and record conservative orphan findings every fifteen minutes."
  schedule_expression = "rate(15 minutes)"
}

resource "aws_cloudwatch_event_target" "reliability_scan" {
  rule      = aws_cloudwatch_event_rule.reliability_scan.name
  target_id = "reliability-worker"
  arn       = aws_lambda_function.reliability_worker.arn
  input     = jsonencode({ action = "scheduled", limit = 100 })
}

resource "aws_lambda_permission" "eventbridge_reliability_worker" {
  statement_id  = "AllowEventBridgeReliabilityScan"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.reliability_worker.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.reliability_scan.arn
}

resource "aws_cloudwatch_metric_alarm" "reliability_dlq_messages" {
  for_each = local.reliability_dlqs

  alarm_name          = "${local.name_prefix}-${each.key}-dlq-messages"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Maximum"
  threshold           = 1
  treat_missing_data  = "notBreaching"
  dimensions          = { QueueName = each.value.name }
}

resource "aws_cloudwatch_metric_alarm" "reliability_worker_errors" {
  alarm_name          = "${local.name_prefix}-reliability-worker-errors"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "Errors"
  namespace           = "AWS/Lambda"
  period              = 300
  statistic           = "Sum"
  threshold           = 1
  treat_missing_data  = "notBreaching"
  dimensions          = { FunctionName = aws_lambda_function.reliability_worker.function_name }
}

output "reliability_worker_function_name" {
  description = "Lambda function used for scheduled reliability scans and explicit operator actions."
  value       = aws_lambda_function.reliability_worker.function_name
}

data "aws_iam_policy_document" "steam_auth_enrollment_assume_role" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "AWS"
      identifiers = ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"]
    }
    condition {
      test     = "Bool"
      variable = "aws:MultiFactorAuthPresent"
      values   = ["true"]
    }
  }
}

resource "aws_iam_role" "steam_auth_enrollment" {
  name                 = "${local.name_prefix}-steam-auth-enrollment"
  description          = "MFA-gated operator role for local SteamCMD config.vdf enrollment and rollback."
  assume_role_policy   = data.aws_iam_policy_document.steam_auth_enrollment_assume_role.json
  max_session_duration = 3600
}

data "aws_iam_policy_document" "steam_auth_enrollment" {
  statement {
    sid       = "VersionSteamAuthorizationCache"
    actions   = ["secretsmanager:DescribeSecret", "secretsmanager:PutSecretValue", "secretsmanager:UpdateSecretVersionStage"]
    resources = [aws_secretsmanager_secret.steam_authorization_cache.arn]
  }

  statement {
    sid       = "SerializeSteamAuthorizationEnrollment"
    actions   = ["dynamodb:GetItem", "dynamodb:UpdateItem"]
    resources = [aws_dynamodb_table.metadata.arn]

    condition {
      test     = "ForAllValues:StringEquals"
      variable = "dynamodb:LeadingKeys"
      values   = ["STEAM_AUTH#CACHE"]
    }
  }

}

resource "aws_iam_role_policy" "steam_auth_enrollment" {
  name   = "steam-auth-cache"
  role   = aws_iam_role.steam_auth_enrollment.id
  policy = data.aws_iam_policy_document.steam_auth_enrollment.json
}

output "steam_auth_enrollment_role_arn" {
  description = "MFA-gated role to assume for a 15-minute local Steam authorization enrollment session."
  value       = aws_iam_role.steam_auth_enrollment.arn
}

variable "monitor_worker_lambda_package_path" {
  type     = string
  default  = null
  nullable = true
}
variable "monitor_worker_lambda_source_hash" {
  type     = string
  default  = null
  nullable = true
}
locals {
  monitor_worker_package_path = var.monitor_worker_lambda_package_path != null ? var.monitor_worker_lambda_package_path : abspath("${path.module}/../../../../dist/monitor-worker.zip")
}
resource "aws_cloudwatch_log_group" "monitor_worker" {
  name              = "/aws/lambda/${local.name_prefix}-monitor-worker"
  retention_in_days = 30
}
data "aws_iam_policy_document" "monitor_worker" {
  statement {
    actions   = ["dynamodb:Scan", "dynamodb:PutItem", "dynamodb:TransactWriteItems"]
    resources = [aws_dynamodb_table.metadata.arn]
  }
  statement {
    actions   = ["ssm:SendCommand"]
    resources = ["arn:aws:ssm:${var.aws_region}::document/AWS-RunShellScript"]
  }
  statement {
    actions   = ["ssm:SendCommand"]
    resources = ["arn:aws:ec2:${var.aws_region}:${data.aws_caller_identity.current.account_id}:instance/*"]
    condition {
      test     = "StringEquals"
      variable = "ssm:resourceTag/Project"
      values   = [var.project_name]
    }
    condition {
      test     = "StringEquals"
      variable = "ssm:resourceTag/Environment"
      values   = [var.environment]
    }
  }
  statement {
    actions   = ["ssm:GetCommandInvocation"]
    resources = ["*"]
  }
  statement {
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.notifications.arn]
  }
  statement {
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.monitor_worker.arn}:*"]
  }
}
resource "aws_iam_role" "monitor_worker" {
  name               = "${local.name_prefix}-monitor-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}
resource "aws_iam_role_policy" "monitor_worker" {
  name   = "runtime"
  role   = aws_iam_role.monitor_worker.id
  policy = data.aws_iam_policy_document.monitor_worker.json
}
resource "aws_lambda_function" "monitor_worker" {
  function_name    = "${local.name_prefix}-monitor-worker"
  description      = "Phase 7 read-only game server health monitor."
  role             = aws_iam_role.monitor_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.monitor_worker_package_path
  source_code_hash = var.monitor_worker_lambda_source_hash != null ? var.monitor_worker_lambda_source_hash : try(filebase64sha256(local.monitor_worker_package_path), null)
  timeout          = 60
  memory_size      = 256
  environment { variables = { APP_ENV = var.environment, LOG_LEVEL = "info", METADATA_TABLE_NAME = aws_dynamodb_table.metadata.name, NOTIFICATION_QUEUE_URL = aws_sqs_queue.notifications.url, MONITOR_SCHEMA_VERSION = "1" } }
  depends_on = [aws_cloudwatch_log_group.monitor_worker, aws_iam_role_policy.monitor_worker]
}
resource "aws_cloudwatch_event_rule" "monitor_game_servers" {
  name                = "${local.name_prefix}-monitor-game-servers"
  description         = "Run Phase 7 read-only health probes every five minutes."
  schedule_expression = "rate(5 minutes)"
}
resource "aws_cloudwatch_event_target" "monitor_game_servers" {
  rule      = aws_cloudwatch_event_rule.monitor_game_servers.name
  target_id = "monitor-worker"
  arn       = aws_lambda_function.monitor_worker.arn
}
resource "aws_lambda_permission" "eventbridge_monitor_worker" {
  statement_id  = "AllowEventBridgeMonitoring"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.monitor_worker.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.monitor_game_servers.arn
}
resource "aws_cloudwatch_metric_alarm" "monitor_worker_errors" {
  alarm_name          = "${local.name_prefix}-monitor-worker-errors"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  metric_name         = "Errors"
  namespace           = "AWS/Lambda"
  period              = 300
  statistic           = "Sum"
  threshold           = 1
  treat_missing_data  = "notBreaching"
  dimensions          = { FunctionName = aws_lambda_function.monitor_worker.function_name }
}

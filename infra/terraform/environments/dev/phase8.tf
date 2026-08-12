variable "sleepwake_worker_lambda_package_path" {
  type     = string
  default  = null
  nullable = true
}
variable "sleepwake_worker_lambda_source_hash" {
  type     = string
  default  = null
  nullable = true
}

locals {
  sleepwake_worker_package_path = var.sleepwake_worker_lambda_package_path != null ? var.sleepwake_worker_lambda_package_path : abspath("${path.module}/../../../../dist/sleepwake-worker.zip")
  sleep_wake_definitions = {
    SleepSession = jsonencode({ StartAt = "Dispatch", States = {
      Dispatch = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "dispatch", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultPath = "$.stage", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "Wait" }
      Wait     = { Type = "Wait", Seconds = 15, Next = "Observe" }
      Observe  = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "observe", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.stage", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "Stopped" }
      Stopped  = { Type = "Choice", Choices = [{ Variable = "$.stage.result.succeeded", BooleanEquals = true, Next = "Complete" }], Default = "Wait" }
      Complete = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "complete", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, End = true }
      Fail     = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "fail", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "error_code.$" = "$.failure.Error", "error_message.$" = "$.failure.Cause" } }, End = true }
    } })
    WakeSession = jsonencode({ StartAt = "Dispatch", States = {
      Dispatch       = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "dispatch", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultPath = "$.stage", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "WaitForEC2" }
      WaitForEC2     = { Type = "Wait", Seconds = 15, Next = "ObserveEC2" }
      ObserveEC2     = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "observe", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.stage", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "EC2Ready" }
      EC2Ready       = { Type = "Choice", Choices = [{ Variable = "$.stage.result.succeeded", BooleanEquals = true, Next = "DispatchHealth" }], Default = "WaitForEC2" }
      DispatchHealth = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "dispatch_health", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.health", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "WaitForHealth" }
      WaitForHealth  = { Type = "Wait", Seconds = 20, Next = "ObserveHealth" }
      ObserveHealth  = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "observe_health", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "command_id.$" = "$.health.result.command_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.health", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "HealthResult" }
      HealthResult   = { Type = "Choice", Choices = [{ Variable = "$.health.result.succeeded", BooleanEquals = true, Next = "Complete" }, { Variable = "$.health.result.done", BooleanEquals = true, Next = "HealthFailed" }], Default = "WaitForHealth" }
      HealthFailed   = { Type = "Pass", Parameters = { "Error.$" = "$.health.result.error_code", "Cause.$" = "$.health.result.error_message" }, ResultPath = "$.failure", Next = "Fail" }
      Complete       = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "complete", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, End = true }
      Fail           = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "fail", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "error_code.$" = "$.failure.Error", "error_message.$" = "$.failure.Cause" } }, End = true }
    } })
  }
}
resource "aws_cloudwatch_log_group" "sleepwake_worker" {
  name              = "/aws/lambda/${local.name_prefix}-sleepwake-worker"
  retention_in_days = 30
}
data "aws_iam_policy_document" "sleepwake_worker" {
  statement {
    actions   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:TransactWriteItems"]
    resources = [aws_dynamodb_table.metadata.arn]
  }
  statement {
    actions   = ["ec2:DescribeInstances", "ec2:StartInstances", "ec2:StopInstances"]
    resources = ["*"]
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
    resources = ["${aws_cloudwatch_log_group.sleepwake_worker.arn}:*"]
  }
}
resource "aws_iam_role" "sleepwake_worker" {
  name               = "${local.name_prefix}-sleepwake-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}
resource "aws_iam_role_policy" "sleepwake_worker" {
  name   = "runtime"
  role   = aws_iam_role.sleepwake_worker.id
  policy = data.aws_iam_policy_document.sleepwake_worker.json
}
resource "aws_lambda_function" "sleepwake_worker" {
  function_name    = "${local.name_prefix}-sleepwake-worker"
  role             = aws_iam_role.sleepwake_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.sleepwake_worker_package_path
  source_code_hash = var.sleepwake_worker_lambda_source_hash != null ? var.sleepwake_worker_lambda_source_hash : try(filebase64sha256(local.sleepwake_worker_package_path), null)
  timeout          = 60
  memory_size      = 256
  environment { variables = { APP_ENV = var.environment, LOG_LEVEL = "info", METADATA_TABLE_NAME = aws_dynamodb_table.metadata.name, NOTIFICATION_QUEUE_URL = aws_sqs_queue.notifications.url } }
  depends_on = [aws_cloudwatch_log_group.sleepwake_worker, aws_iam_role_policy.sleepwake_worker]
}
data "aws_iam_policy_document" "sleepwake_workflow" {
  statement {
    actions   = ["lambda:InvokeFunction"]
    resources = [aws_lambda_function.sleepwake_worker.arn]
  }
}
resource "aws_iam_role_policy" "sleepwake_workflow" {
  name   = "sleep-wake"
  role   = aws_iam_role.workflow.id
  policy = data.aws_iam_policy_document.sleepwake_workflow.json
}

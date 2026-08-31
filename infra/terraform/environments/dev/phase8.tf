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
      Dispatch = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "dispatch", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultPath = "$.stage", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "Wait" }
      Wait     = { Type = "Wait", Seconds = 15, Next = "Observe" }
      Observe  = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "observe", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.stage", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "Stopped" }
      Stopped  = { Type = "Choice", Choices = [{ Variable = "$.stage.result.succeeded", BooleanEquals = true, Next = "Complete" }], Default = "Wait" }
      Complete = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "complete", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, End = true }
      Fail     = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "fail", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "error_code.$" = "$.failure.Error", "error_message.$" = "$.failure.Cause" } }, End = true }
    } })
    WakeSession = jsonencode({ StartAt = "Dispatch", States = {
      Dispatch                  = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "dispatch", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultPath = "$.stage", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }], Next = "WaitForEC2" }
      WaitForEC2                = { Type = "Wait", Seconds = 15, Next = "ObserveEC2" }
      ObserveEC2                = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "observe", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.stage", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }], Next = "EC2Ready" }
      EC2Ready                  = { Type = "Choice", Choices = [{ Variable = "$.stage.result.succeeded", BooleanEquals = true, Next = "InitializeManagedAttempts" }], Default = "WaitForEC2" }
      InitializeManagedAttempts = { Type = "Pass", Result = 0, ResultPath = "$.attempt", Next = "WaitForManagedNode" }
      WaitForManagedNode        = { Type = "Wait", Seconds = 15, Next = "CheckManagedNode" }
      CheckManagedNode = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "check_managed", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.managed", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }], Next = "ManagedNodeReady"
      }
      ManagedNodeReady           = { Type = "Choice", Choices = [{ Variable = "$.managed.result.managed", BooleanEquals = true, Next = "DispatchContent" }], Default = "IncrementManagedAttempts" }
      IncrementManagedAttempts   = { Type = "Pass", Parameters = { "value.$" = "States.MathAdd($.attempt, 1)" }, ResultPath = "$.counter", Next = "CopyManagedAttempts" }
      CopyManagedAttempts        = { Type = "Pass", InputPath = "$.counter.value", ResultPath = "$.attempt", Next = "ManagedAttemptsAvailable" }
      ManagedAttemptsAvailable   = { Type = "Choice", Choices = [{ Variable = "$.attempt", NumericGreaterThanEquals = 40, Next = "ManagedTimeout" }], Default = "WaitForManagedNode" }
      ManagedTimeout             = { Type = "Pass", Result = { Error = "ERR_SSM_TIMEOUT", Cause = "Woken EC2 instance did not register online with Systems Manager within the bounded wait." }, ResultPath = "$.failure", Next = "DispatchRollback" }
      DispatchContent            = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "dispatch_content", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.mods", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }], Next = "ContentDispatchResult" }
      ContentDispatchResult      = { Type = "Choice", Choices = [{ Variable = "$.mods.result.succeeded", BooleanEquals = true, Next = "DispatchHealth" }], Default = "InitializeModsAttempts" }
      InitializeModsAttempts     = { Type = "Pass", Result = 0, ResultPath = "$.mods_attempt", Next = "WaitForMods" }
      WaitForMods                = { Type = "Wait", Seconds = 30, Next = "ObserveMods" }
      ObserveMods                = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "observe_content", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "command_id.$" = "$.mods.result.command_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.mods", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }], Next = "ModsResult" }
      ModsResult                 = { Type = "Choice", Choices = [{ Variable = "$.mods.result.succeeded", BooleanEquals = true, Next = "DispatchHealth" }, { Variable = "$.mods.result.done", BooleanEquals = true, Next = "ModsFailed" }], Default = "IncrementModsAttempts" }
      IncrementModsAttempts      = { Type = "Pass", Parameters = { "value.$" = "States.MathAdd($.mods_attempt, 1)" }, ResultPath = "$.mods_counter", Next = "CopyModsAttempts" }
      CopyModsAttempts           = { Type = "Pass", InputPath = "$.mods_counter.value", ResultPath = "$.mods_attempt", Next = "ModsAttemptsAvailable" }
      ModsAttemptsAvailable      = { Type = "Choice", Choices = [{ Variable = "$.mods_attempt", NumericGreaterThanEquals = local.bootstrap_poll_limit, Next = "ModsTimeout" }], Default = "WaitForMods" }
      ModsFailed                 = { Type = "Pass", Parameters = { "Error.$" = "$.mods.result.error_code", "Cause.$" = "$.mods.result.error_message" }, ResultPath = "$.failure", Next = "DispatchRollback" }
      ModsTimeout                = { Type = "Pass", Result = { Error = "ERR_MOD_REVISION_TIMEOUT", Cause = "Pending mod revision application exceeded its bounded runtime." }, ResultPath = "$.failure", Next = "DispatchRollback" }
      DispatchHealth             = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "dispatch_health", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.health", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }], Next = "WaitForHealth" }
      WaitForHealth              = { Type = "Wait", Seconds = 20, Next = "ObserveHealth" }
      ObserveHealth              = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "observe_health", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "command_id.$" = "$.health.result.command_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.health", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }], Next = "HealthResult" }
      HealthResult               = { Type = "Choice", Choices = [{ Variable = "$.health.result.succeeded", BooleanEquals = true, Next = "Complete" }, { Variable = "$.health.result.done", BooleanEquals = true, Next = "HealthFailed" }], Default = "WaitForHealth" }
      HealthFailed               = { Type = "Pass", Parameters = { "Error.$" = "$.health.result.error_code", "Cause.$" = "$.health.result.error_message" }, ResultPath = "$.failure", Next = "DispatchRollback" }
      Complete                   = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "complete", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, End = true }
      DispatchRollback           = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "dispatch_rollback", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.rollback", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.rollback_failure", Next = "Fail" }], Next = "RollbackDispatched" }
      RollbackDispatched         = { Type = "Choice", Choices = [{ Variable = "$.rollback.result.succeeded", BooleanEquals = true, Next = "Fail" }], Default = "InitializeRollbackAttempts" }
      InitializeRollbackAttempts = { Type = "Pass", Result = 0, ResultPath = "$.rollback_attempt", Next = "WaitForRollback" }
      WaitForRollback            = { Type = "Wait", Seconds = 30, Next = "ObserveRollback" }
      ObserveRollback            = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "observe_rollback", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "command_id.$" = "$.rollback.result.command_id" } }, ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.rollback", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.rollback_failure", Next = "Fail" }], Next = "RollbackComplete" }
      RollbackComplete           = { Type = "Choice", Choices = [{ Variable = "$.rollback.result.done", BooleanEquals = true, Next = "Fail" }], Default = "IncrementRollbackAttempts" }
      IncrementRollbackAttempts  = { Type = "Pass", Parameters = { "value.$" = "States.MathAdd($.rollback_attempt, 1)" }, ResultPath = "$.rollback_counter", Next = "CopyRollbackAttempts" }
      CopyRollbackAttempts       = { Type = "Pass", InputPath = "$.rollback_counter.value", ResultPath = "$.rollback_attempt", Next = "RollbackAttemptsAvailable" }
      RollbackAttemptsAvailable  = { Type = "Choice", Choices = [{ Variable = "$.rollback_attempt", NumericGreaterThanEquals = local.bootstrap_poll_limit, Next = "Fail" }], Default = "WaitForRollback" }
      Fail                       = { Type = "Task", Resource = "arn:aws:states:::lambda:invoke", Parameters = { FunctionName = aws_lambda_function.sleepwake_worker.function_name, Payload = { action = "fail", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "error_code.$" = "$.failure.Error", "error_message.$" = "$.failure.Cause" } }, End = true }
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
    actions   = ["ssm:DescribeInstanceInformation"]
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
  environment {
    variables = {
      APP_ENV                           = var.environment
      LOG_LEVEL                         = "info"
      METADATA_TABLE_NAME               = aws_dynamodb_table.metadata.name
      NOTIFICATION_QUEUE_URL            = aws_sqs_queue.notifications.url
      SESSION_ASSETS_BUCKET             = aws_s3_bucket.session_assets.bucket
      BOOTSTRAP_SCRIPT_KEY              = aws_s3_object.bootstrap_script.key
      STEAM_AUTH_SECRET_ID              = aws_secretsmanager_secret.steam_authorization_cache.name
      TEAMSPEAK_VERSION                 = var.teamspeak_version
      BOOTSTRAP_COMMAND_TIMEOUT_SECONDS = tostring(var.bootstrap_command_timeout_seconds)
    }
  }
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

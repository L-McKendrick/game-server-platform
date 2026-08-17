variable "bootstrap_worker_lambda_package_path" {
  description = "Optional path to the packaged Phase 6 bootstrap worker Lambda zip."
  type        = string
  default     = null
  nullable    = true
}

variable "bootstrap_worker_lambda_source_hash" {
  description = "Optional base64 SHA-256 supplied by CI for the bootstrap worker package."
  type        = string
  default     = null
  nullable    = true
}

variable "bootstrap_command_timeout_seconds" {
  description = "Maximum duration of the resumable Systems Manager bootstrap command."
  type        = number
  default     = 21600

  validation {
    condition     = var.bootstrap_command_timeout_seconds >= 900 && var.bootstrap_command_timeout_seconds <= 172800
    error_message = "bootstrap_command_timeout_seconds must be between 900 and 172800 seconds."
  }
}

variable "teamspeak_version" {
  description = "Pinned TeamSpeak 3 server version used only when voice is enabled for a session."
  type        = string
  default     = "3.13.8"

  validation {
    condition     = can(regex("^[0-9]+\\.[0-9]+\\.[0-9]+$", var.teamspeak_version))
    error_message = "teamspeak_version must be a dotted numeric release."
  }
}

locals {
  bootstrap_worker_package_path = var.bootstrap_worker_lambda_package_path != null ? var.bootstrap_worker_lambda_package_path : abspath("${path.module}/../../../../dist/bootstrap-worker.zip")
  bootstrap_script_source_path  = abspath("${path.module}/../../../../deploy/bootstrap/arma3-bootstrap.sh")
  bootstrap_script_content      = replace(file(local.bootstrap_script_source_path), "\r\n", "\n")
  bootstrap_script_hash         = sha256(local.bootstrap_script_content)
  bootstrap_script_object_key   = "platform/bootstrap/arma3-${substr(local.bootstrap_script_hash, 0, 16)}.sh"
  bootstrap_poll_limit          = ceil(var.bootstrap_command_timeout_seconds / 30)
}

resource "aws_s3_object" "bootstrap_script" {
  bucket       = aws_s3_bucket.session_assets.id
  key          = local.bootstrap_script_object_key
  content      = local.bootstrap_script_content
  source_hash  = local.bootstrap_script_hash
  content_type = "text/x-shellscript"

  lifecycle {
    create_before_destroy = true
  }

  depends_on = [aws_s3_bucket_server_side_encryption_configuration.session_assets]
}

data "aws_iam_policy_document" "game_instance_bootstrap" {
  statement {
    sid       = "ReadBootstrapArtifact"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.session_assets.arn}/platform/bootstrap/*"]
  }

  statement {
    sid       = "ReadSteamCredentials"
    actions   = ["secretsmanager:GetSecretValue"]
    resources = [aws_secretsmanager_secret.steam_credentials.arn]
  }
}

resource "aws_iam_role_policy" "game_instance_bootstrap" {
  name   = "bootstrap-secrets"
  role   = aws_iam_role.game_instance.id
  policy = data.aws_iam_policy_document.game_instance_bootstrap.json
}

data "aws_iam_policy_document" "bootstrap_worker" {
  statement {
    sid = "MetadataWorkflowAccess"
    actions = [
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:TransactWriteItems",
    ]
    resources = [aws_dynamodb_table.metadata.arn]
  }

  statement {
    sid       = "UseRunShellScriptDocument"
    actions   = ["ssm:SendCommand"]
    resources = ["arn:aws:ssm:${var.aws_region}::document/AWS-RunShellScript"]
  }

  statement {
    sid       = "BootstrapTaggedGameInstances"
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
    sid       = "ObserveBootstrapCommands"
    actions   = ["ssm:GetCommandInvocation"]
    resources = ["*"]
  }

  statement {
    sid       = "NotificationSend"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.notifications.arn]
  }

  statement {
    sid = "RuntimeLogDelivery"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["${aws_cloudwatch_log_group.bootstrap_worker.arn}:*"]
  }
}

resource "aws_iam_role" "bootstrap_worker" {
  name               = "${local.name_prefix}-bootstrap-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}

resource "aws_iam_role_policy" "bootstrap_worker" {
  name   = "runtime"
  role   = aws_iam_role.bootstrap_worker.id
  policy = data.aws_iam_policy_document.bootstrap_worker.json
}

resource "aws_cloudwatch_log_group" "bootstrap_worker" {
  name              = "/aws/lambda/${local.name_prefix}-bootstrap-worker"
  retention_in_days = 30
}

resource "aws_lambda_function" "bootstrap_worker" {
  function_name    = "${local.name_prefix}-bootstrap-worker"
  description      = "Dispatches and observes the resumable Phase 6 Systems Manager bootstrap."
  role             = aws_iam_role.bootstrap_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.bootstrap_worker_package_path
  source_code_hash = var.bootstrap_worker_lambda_source_hash != null ? var.bootstrap_worker_lambda_source_hash : try(filebase64sha256(local.bootstrap_worker_package_path), null)
  timeout          = 60
  memory_size      = 256

  environment {
    variables = {
      APP_ENV                           = var.environment
      LOG_LEVEL                         = "info"
      METADATA_TABLE_NAME               = aws_dynamodb_table.metadata.name
      NOTIFICATION_QUEUE_URL            = aws_sqs_queue.notifications.url
      SESSION_ASSETS_BUCKET             = aws_s3_bucket.session_assets.id
      BOOTSTRAP_SCRIPT_KEY              = aws_s3_object.bootstrap_script.key
      STEAM_SECRET_ID                   = aws_secretsmanager_secret.steam_credentials.name
      TEAMSPEAK_VERSION                 = var.teamspeak_version
      BOOTSTRAP_COMMAND_TIMEOUT_SECONDS = tostring(var.bootstrap_command_timeout_seconds)
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.bootstrap_worker,
    aws_iam_role_policy.bootstrap_worker,
  ]
}

data "aws_iam_policy_document" "bootstrap_workflow" {
  statement {
    sid       = "InvokeBootstrapWorker"
    actions   = ["lambda:InvokeFunction"]
    resources = [aws_lambda_function.bootstrap_worker.arn]
  }

  statement {
    sid = "WorkflowLogDelivery"
    actions = [
      "logs:CreateLogDelivery",
      "logs:GetLogDelivery",
      "logs:UpdateLogDelivery",
      "logs:DeleteLogDelivery",
      "logs:ListLogDeliveries",
      "logs:PutResourcePolicy",
      "logs:DescribeResourcePolicies",
      "logs:DescribeLogGroups",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "bootstrap_workflow" {
  name   = "bootstrap-game-server"
  role   = aws_iam_role.workflow.id
  policy = data.aws_iam_policy_document.bootstrap_workflow.json
}

resource "aws_cloudwatch_log_group" "bootstrap_workflow" {
  name              = "/aws/states/${local.name_prefix}-BootstrapGameServer"
  retention_in_days = 30
}

resource "aws_sfn_state_machine" "bootstrap_game_server" {
  name     = "${local.name_prefix}-BootstrapGameServer"
  role_arn = aws_iam_role.workflow.arn
  type     = "STANDARD"

  logging_configuration {
    include_execution_data = false
    level                  = "ERROR"
    log_destination        = "${aws_cloudwatch_log_group.bootstrap_workflow.arn}:*"
  }

  definition = jsonencode({
    Comment = "Phase 6 resumable Arma 3 installation and health boundary."
    StartAt = "InitializeAttempts"
    States = {
      InitializeAttempts = {
        Type       = "Pass"
        Result     = { value = 0 }
        ResultPath = "$.attempts"
        Next       = "Prepare"
      }
      Prepare = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.bootstrap_worker.function_name
          Payload = {
            action             = "prepare"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
          }
        }
        ResultPath = "$.preparation"
        Retry      = [{ ErrorEquals = ["States.TaskFailed"], IntervalSeconds = 2, BackoffRate = 2, MaxAttempts = 3 }]
        Catch      = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }]
        Next       = "Dispatch"
      }
      Dispatch = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.bootstrap_worker.function_name
          Payload = {
            action             = "dispatch"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
          }
        }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.command"
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }]
        Next           = "WaitForCommand"
      }
      WaitForCommand = {
        Type    = "Wait"
        Seconds = 30
        Next    = "ObserveCommand"
      }
      ObserveCommand = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.bootstrap_worker.function_name
          Payload = {
            action             = "observe"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
            "command_id.$"     = "$.command.result.command_id"
          }
        }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.observation"
        Retry          = [{ ErrorEquals = ["States.TaskFailed"], IntervalSeconds = 5, BackoffRate = 2, MaxAttempts = 3 }]
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }]
        Next           = "CommandComplete"
      }
      CommandComplete = {
        Type = "Choice"
        Choices = [
          { Variable = "$.observation.result.succeeded", BooleanEquals = true, Next = "Complete" },
          { Variable = "$.observation.result.done", BooleanEquals = true, Next = "CaptureCommandFailure" },
        ]
        Default = "IncrementAttempts"
      }
      IncrementAttempts = {
        Type = "Pass"
        Parameters = {
          "value.$" = "States.MathAdd($.attempts.value, 1)"
        }
        ResultPath = "$.attempts"
        Next       = "AttemptsAvailable"
      }
      AttemptsAvailable = {
        Type    = "Choice"
        Choices = [{ Variable = "$.attempts.value", NumericGreaterThanEquals = local.bootstrap_poll_limit, Next = "CommandTimeout" }]
        Default = "WaitForCommand"
      }
      CommandTimeout = {
        Type       = "Pass"
        Result     = { Error = "ERR_BOOTSTRAP_TIMEOUT", Cause = "Managed bootstrap command exceeded its bounded runtime." }
        ResultPath = "$.failure"
        Next       = "DispatchRollback"
      }
      CaptureCommandFailure = {
        Type = "Pass"
        Parameters = {
          "Error.$" = "$.observation.result.error_code"
          "Cause.$" = "$.observation.result.error_message"
        }
        ResultPath = "$.failure"
        Next       = "DispatchRollback"
      }
      Complete = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.bootstrap_worker.function_name
          Payload = {
            action             = "complete"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
          }
        }
        ResultPath = "$.completion"
        Retry      = [{ ErrorEquals = ["States.TaskFailed"], IntervalSeconds = 2, BackoffRate = 2, MaxAttempts = 3 }]
        Catch      = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }]
        End        = true
      }
      DispatchRollback = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.bootstrap_worker.function_name
          Payload = {
            action             = "dispatch_rollback"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
          }
        }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.rollback"
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.rollback_failure", Next = "MarkFailed" }]
        Next           = "RollbackDispatched"
      }
      RollbackDispatched = {
        Type    = "Choice"
        Choices = [{ Variable = "$.rollback.result.succeeded", BooleanEquals = true, Next = "MarkFailed" }]
        Default = "InitializeRollbackAttempts"
      }
      InitializeRollbackAttempts = {
        Type       = "Pass"
        Result     = 0
        ResultPath = "$.rollback_attempt"
        Next       = "WaitForRollback"
      }
      WaitForRollback = {
        Type    = "Wait"
        Seconds = 30
        Next    = "ObserveRollback"
      }
      ObserveRollback = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.bootstrap_worker.function_name
          Payload = {
            action             = "observe_rollback"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
            "command_id.$"     = "$.rollback.result.command_id"
          }
        }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.rollback"
        Retry          = [{ ErrorEquals = ["States.TaskFailed"], IntervalSeconds = 5, BackoffRate = 2, MaxAttempts = 3 }]
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.rollback_failure", Next = "MarkFailed" }]
        Next           = "RollbackComplete"
      }
      RollbackComplete = {
        Type    = "Choice"
        Choices = [{ Variable = "$.rollback.result.done", BooleanEquals = true, Next = "MarkFailed" }]
        Default = "IncrementRollbackAttempts"
      }
      IncrementRollbackAttempts = {
        Type = "Pass"
        Parameters = {
          "value.$" = "States.MathAdd($.rollback_attempt, 1)"
        }
        ResultPath = "$.rollback_counter"
        Next       = "CopyRollbackAttempts"
      }
      CopyRollbackAttempts = {
        Type       = "Pass"
        InputPath  = "$.rollback_counter.value"
        ResultPath = "$.rollback_attempt"
        Next       = "RollbackAttemptsAvailable"
      }
      RollbackAttemptsAvailable = {
        Type    = "Choice"
        Choices = [{ Variable = "$.rollback_attempt", NumericGreaterThanEquals = local.bootstrap_poll_limit, Next = "MarkFailed" }]
        Default = "WaitForRollback"
      }
      MarkFailed = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.bootstrap_worker.function_name
          Payload = {
            action             = "fail"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
            "error_code.$"     = "$.failure.Error"
            "error_message.$"  = "$.failure.Cause"
          }
        }
        ResultPath = "$.failure_record"
        Retry      = [{ ErrorEquals = ["States.TaskFailed"], IntervalSeconds = 2, BackoffRate = 2, MaxAttempts = 3 }]
        Next       = "BootstrapFailed"
      }
      BootstrapFailed = {
        Type  = "Fail"
        Error = "GameServerBootstrapFailed"
        Cause = "Phase 6 bootstrap failed; durable stage markers and infrastructure were retained for retry."
      }
    }
  })

  depends_on = [
    aws_iam_role_policy.bootstrap_workflow,
    aws_cloudwatch_log_group.bootstrap_workflow,
  ]
}

variable "command_worker_lambda_package_path" {
  description = "Optional path to the packaged command worker Lambda zip."
  type        = string
  default     = null
  nullable    = true
}

variable "command_worker_lambda_source_hash" {
  description = "Optional base64 SHA-256 supplied by CI for the command worker package."
  type        = string
  default     = null
  nullable    = true
}

variable "provisioning_worker_lambda_package_path" {
  description = "Optional path to the packaged provisioning worker Lambda zip."
  type        = string
  default     = null
  nullable    = true
}

variable "provisioning_worker_lambda_source_hash" {
  description = "Optional base64 SHA-256 supplied by CI for the provisioning worker package."
  type        = string
  default     = null
  nullable    = true
}

variable "provisioning_enabled" {
  description = "Expose cost-bearing infrastructure provisioning to Discord. Requires a budget alert recipient."
  type        = bool
  default     = false
}

variable "monthly_budget_limit_usd" {
  description = "Monthly account budget used as an alerting guardrail."
  type        = number
  default     = 75

  validation {
    condition     = var.monthly_budget_limit_usd >= 5
    error_message = "monthly_budget_limit_usd must be at least 5."
  }
}

variable "budget_alert_email" {
  description = "Optional budget-alert email. Required when provisioning_enabled is true."
  type        = string
  default     = null
  nullable    = true
}

variable "vpc_cidr" {
  description = "Development game-server VPC CIDR."
  type        = string
  default     = "10.40.0.0/16"
}

variable "public_subnet_cidrs" {
  description = "Two public game-server subnet CIDRs."
  type        = list(string)
  default     = ["10.40.0.0/24", "10.40.1.0/24"]

  validation {
    condition     = length(var.public_subnet_cidrs) == 2
    error_message = "Exactly two public subnet CIDRs are required."
  }
}

variable "provisioning_instance_type" {
  description = "Operator-controlled Phase 5 compute profile instance type."
  type        = string
  default     = "c7i-flex.large"

  validation {
    condition     = contains(["c7i-flex.large", "c7i.large", "c7i.xlarge"], var.provisioning_instance_type)
    error_message = "provisioning_instance_type must be an approved Arma 3 profile instance type."
  }
}

variable "provisioning_root_volume_gib" {
  description = "Encrypted root volume size."
  type        = number
  default     = 30
}

variable "provisioning_data_volume_gib" {
  description = "Encrypted persistent session-data volume size."
  type        = number
  default     = 100

  validation {
    condition     = var.provisioning_data_volume_gib >= 20 && var.provisioning_data_volume_gib <= 500
    error_message = "provisioning_data_volume_gib must be between 20 and 500 GiB."
  }
}

variable "max_provisioned_sessions" {
  description = "Hard DynamoDB-backed capacity-slot limit for provisioned sessions."
  type        = number
  default     = 1

  validation {
    condition     = var.max_provisioned_sessions >= 1 && var.max_provisioned_sessions <= 3
    error_message = "max_provisioned_sessions must be between 1 and 3 in development."
  }
}

data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_ssm_parameter" "game_host_ami" {
  name = "/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id"
}

locals {
  command_worker_package_path      = var.command_worker_lambda_package_path != null ? var.command_worker_lambda_package_path : abspath("${path.module}/../../../../dist/command-worker.zip")
  provisioning_worker_package_path = var.provisioning_worker_lambda_package_path != null ? var.provisioning_worker_lambda_package_path : abspath("${path.module}/../../../../dist/provisioning-worker.zip")
}

resource "aws_budgets_budget" "monthly" {
  name         = "${local.name_prefix}-monthly-cost"
  budget_type  = "COST"
  limit_amount = tostring(var.monthly_budget_limit_usd)
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  dynamic "notification" {
    for_each = var.budget_alert_email == null ? [] : [var.budget_alert_email]
    content {
      comparison_operator        = "GREATER_THAN"
      threshold                  = 80
      threshold_type             = "PERCENTAGE"
      notification_type          = "FORECASTED"
      subscriber_email_addresses = [notification.value]
    }
  }

  dynamic "notification" {
    for_each = var.budget_alert_email == null ? [] : [var.budget_alert_email]
    content {
      comparison_operator        = "GREATER_THAN"
      threshold                  = 100
      threshold_type             = "PERCENTAGE"
      notification_type          = "ACTUAL"
      subscriber_email_addresses = [notification.value]
    }
  }
}

resource "aws_vpc" "game" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = {
    Name      = "${local.name_prefix}-game"
    Component = "compute-network"
  }
}

resource "aws_internet_gateway" "game" {
  vpc_id = aws_vpc.game.id
  tags = {
    Name      = "${local.name_prefix}-game"
    Component = "compute-network"
  }
}

resource "aws_subnet" "game_public" {
  count = 2

  vpc_id                  = aws_vpc.game.id
  cidr_block              = var.public_subnet_cidrs[count.index]
  availability_zone       = data.aws_availability_zones.available.names[count.index]
  map_public_ip_on_launch = true

  tags = {
    Name      = "${local.name_prefix}-game-public-${count.index + 1}"
    Component = "compute-network"
  }
}

resource "aws_route_table" "game_public" {
  vpc_id = aws_vpc.game.id
  tags = {
    Name      = "${local.name_prefix}-game-public"
    Component = "compute-network"
  }
}

resource "aws_route" "game_public_ipv4" {
  route_table_id         = aws_route_table.game_public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.game.id
}

resource "aws_route_table_association" "game_public" {
  count = 2

  subnet_id      = aws_subnet.game_public[count.index].id
  route_table_id = aws_route_table.game_public.id
}

resource "aws_security_group" "arma" {
  name_prefix = "${local.name_prefix}-arma-"
  description = "Arma 3 player traffic; no administrative ingress."
  vpc_id      = aws_vpc.game.id

  tags = {
    Name      = "${local.name_prefix}-arma"
    Component = "game-server"
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_ingress_rule" "arma_udp" {
  security_group_id = aws_security_group.arma.id
  description       = "Arma 3 game and query ports"
  ip_protocol       = "udp"
  from_port         = 2302
  to_port           = 2306
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "arma_ipv4" {
  security_group_id = aws_security_group.arma.id
  description       = "Steam, package, SSM, and telemetry egress"
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_security_group" "teamspeak" {
  name_prefix = "${local.name_prefix}-teamspeak-"
  description = "Optional TeamSpeak voice traffic; no ServerQuery ingress."
  vpc_id      = aws_vpc.game.id

  tags = {
    Name      = "${local.name_prefix}-teamspeak"
    Component = "game-server"
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_vpc_security_group_ingress_rule" "teamspeak_voice" {
  security_group_id = aws_security_group.teamspeak.id
  description       = "TeamSpeak voice"
  ip_protocol       = "udp"
  from_port         = 9987
  to_port           = 9987
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_iam_role" "game_instance" {
  name = "${local.name_prefix}-game-instance"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "game_instance_ssm" {
  role       = aws_iam_role.game_instance.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

data "aws_iam_policy_document" "game_instance" {
  statement {
    sid     = "ReadSessionInputs"
    actions = ["s3:GetObject"]
    resources = [
      "${aws_s3_bucket.session_assets.arn}/sessions/*/input/*",
    ]
  }

  statement {
    sid     = "WriteSessionLogs"
    actions = ["s3:PutObject"]
    resources = [
      "${aws_s3_bucket.session_assets.arn}/sessions/*/logs/*",
    ]
  }

  statement {
    sid     = "WriteSessionArchives"
    actions = ["s3:PutObject"]
    resources = [
      "${aws_s3_bucket.session_assets.arn}/sessions/*/archives/*/session.tar.gz",
    ]
  }

  statement {
    sid     = "ReadSessionArchiveMetadata"
    actions = ["s3:GetObject"]
    resources = [
      "${aws_s3_bucket.session_assets.arn}/sessions/*/archives/*/session.tar.gz",
    ]
  }
}

resource "aws_iam_role_policy" "game_instance" {
  name   = "session-assets"
  role   = aws_iam_role.game_instance.id
  policy = data.aws_iam_policy_document.game_instance.json
}

resource "aws_iam_instance_profile" "game" {
  name = "${local.name_prefix}-game-instance"
  role = aws_iam_role.game_instance.name
}

data "aws_iam_policy_document" "command_worker" {
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
    sid     = "StartImplementedWorkflows"
    actions = ["states:StartExecution"]
    resources = [
      aws_sfn_state_machine.provision_session.arn,
      aws_sfn_state_machine.bootstrap_game_server.arn,
      aws_sfn_state_machine.workflow["SleepSession"].arn,
      aws_sfn_state_machine.workflow["WakeSession"].arn,
      aws_sfn_state_machine.workflow["ArchiveSession"].arn,
      aws_sfn_state_machine.workflow["RestoreSession"].arn,
      aws_sfn_state_machine.workflow["DestroySession"].arn,
    ]
  }

  statement {
    sid = "ConsumeCommands"
    actions = [
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:GetQueueAttributes",
    ]
    resources = [aws_sqs_queue.commands.arn]
  }

  statement {
    sid = "RuntimeLogDelivery"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = ["${aws_cloudwatch_log_group.command_worker.arn}:*"]
  }
}

resource "aws_iam_role" "command_worker" {
  name               = "${local.name_prefix}-command-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}

resource "aws_iam_role_policy" "command_worker" {
  name   = "runtime"
  role   = aws_iam_role.command_worker.id
  policy = data.aws_iam_policy_document.command_worker.json
}

resource "aws_cloudwatch_log_group" "command_worker" {
  name              = "/aws/lambda/${local.name_prefix}-command-worker"
  retention_in_days = 30
}

resource "aws_lambda_function" "command_worker" {
  function_name    = "${local.name_prefix}-command-worker"
  description      = "Revalidates normalized lifecycle commands and starts durable workflows."
  role             = aws_iam_role.command_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.command_worker_package_path
  source_code_hash = var.command_worker_lambda_source_hash != null ? var.command_worker_lambda_source_hash : try(filebase64sha256(local.command_worker_package_path), null)
  timeout          = 30
  memory_size      = 256

  environment {
    variables = {
      APP_ENV                     = var.environment
      LOG_LEVEL                   = "info"
      METADATA_TABLE_NAME         = aws_dynamodb_table.metadata.name
      PROVISION_STATE_MACHINE_ARN = aws_sfn_state_machine.provision_session.arn
      BOOTSTRAP_STATE_MACHINE_ARN = aws_sfn_state_machine.bootstrap_game_server.arn
      SLEEP_STATE_MACHINE_ARN     = aws_sfn_state_machine.workflow["SleepSession"].arn
      WAKE_STATE_MACHINE_ARN      = aws_sfn_state_machine.workflow["WakeSession"].arn
      ARCHIVE_STATE_MACHINE_ARN   = aws_sfn_state_machine.workflow["ArchiveSession"].arn
      RESTORE_STATE_MACHINE_ARN   = aws_sfn_state_machine.workflow["RestoreSession"].arn
      TERMINATE_STATE_MACHINE_ARN = aws_sfn_state_machine.workflow["DestroySession"].arn
      DISCORD_PUBLIC_KEY          = var.discord_public_key
      DISCORD_APPLICATION_ID      = var.discord_application_id
      DISCORD_ALLOWED_GUILD_IDS   = join(",", sort(tolist(var.discord_allowed_guild_ids)))
      DISCORD_ALLOWED_ROLE_IDS    = join(",", sort(tolist(var.discord_allowed_role_ids)))
      DISCORD_ALLOWED_CHANNEL_IDS = join(",", sort(tolist(var.discord_allowed_channel_ids)))
    }
  }

  depends_on = [aws_cloudwatch_log_group.command_worker, aws_iam_role_policy.command_worker]
}

resource "aws_lambda_event_source_mapping" "command_worker" {
  event_source_arn        = aws_sqs_queue.commands.arn
  function_name           = aws_lambda_function.command_worker.arn
  batch_size              = 1
  function_response_types = ["ReportBatchItemFailures"]
}

locals {
  provisioning_instance_resource = "arn:aws:ec2:${var.aws_region}:${data.aws_caller_identity.current.account_id}:instance/*"
  provisioning_volume_resource   = "arn:aws:ec2:${var.aws_region}:${data.aws_caller_identity.current.account_id}:volume/*"
  provisioning_launch_resources = [
    "arn:aws:ec2:${var.aws_region}::image/${data.aws_ssm_parameter.game_host_ami.value}",
    aws_subnet.game_public[0].arn,
    aws_security_group.arma.arn,
    aws_security_group.teamspeak.arn,
    "arn:aws:ec2:${var.aws_region}:${data.aws_caller_identity.current.account_id}:network-interface/*",
  ]
}

check "provisioning_run_instances_resource_scope" {
  assert {
    condition = (
      local.provisioning_instance_resource != "*" &&
      local.provisioning_volume_resource != "*" &&
      !contains(local.provisioning_launch_resources, "*") &&
      contains(local.provisioning_launch_resources, aws_subnet.game_public[0].arn)
    )
    error_message = "RunInstances must use separately scoped instance, volume, and approved launch resources."
  }
}

data "aws_iam_policy_document" "provisioning_worker" {
  statement {
    sid = "MetadataAccess"
    actions = [
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:DeleteItem",
      "dynamodb:TransactWriteItems",
    ]
    resources = [aws_dynamodb_table.metadata.arn]
  }

  statement {
    sid       = "LaunchTaggedApprovedInstance"
    actions   = ["ec2:RunInstances"]
    resources = [local.provisioning_instance_resource]

    condition {
      test     = "StringEquals"
      variable = "ec2:Region"
      values   = [var.aws_region]
    }

    condition {
      test     = "StringEquals"
      variable = "ec2:InstanceType"
      values   = [var.provisioning_instance_type]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/Project"
      values   = [var.project_name]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/Environment"
      values   = [var.environment]
    }
  }

  statement {
    sid       = "LaunchEncryptedGP3Volumes"
    actions   = ["ec2:RunInstances"]
    resources = [local.provisioning_volume_resource]

    condition {
      test     = "Bool"
      variable = "ec2:Encrypted"
      values   = ["true"]
    }

    condition {
      test     = "StringEquals"
      variable = "ec2:VolumeType"
      values   = ["gp3"]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/Project"
      values   = [var.project_name]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/Environment"
      values   = [var.environment]
    }
  }

  # RunInstances evaluates an AMI-backed root volume against the encryption
  # state of its parent snapshot. The approved Ubuntu AMI currently uses
  # an unencrypted public snapshot even though the block-device mapping asks
  # EC2 to create the resulting root volume encrypted.
  statement {
    sid       = "LaunchAMIBackedGP3RootVolume"
    actions   = ["ec2:RunInstances"]
    resources = [local.provisioning_volume_resource]

    condition {
      test     = "StringEquals"
      variable = "ec2:VolumeType"
      values   = ["gp3"]
    }

    condition {
      test     = "Null"
      variable = "ec2:ParentSnapshot"
      values   = ["false"]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/Project"
      values   = [var.project_name]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/Environment"
      values   = [var.environment]
    }
  }

  statement {
    sid       = "UseApprovedLaunchResources"
    actions   = ["ec2:RunInstances"]
    resources = local.provisioning_launch_resources
  }

  statement {
    sid     = "TagCreatedCompute"
    actions = ["ec2:CreateTags"]
    resources = [
      "arn:aws:ec2:${var.aws_region}:${data.aws_caller_identity.current.account_id}:instance/*",
      "arn:aws:ec2:${var.aws_region}:${data.aws_caller_identity.current.account_id}:volume/*",
    ]

    condition {
      test     = "StringEquals"
      variable = "ec2:CreateAction"
      values   = ["RunInstances"]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:RequestTag/Project"
      values   = [var.project_name]
    }
  }

  statement {
    sid       = "ObserveCompute"
    actions   = ["ec2:DescribeInstances"]
    resources = ["*"]
  }

  statement {
    sid       = "PassGameInstanceRole"
    actions   = ["iam:PassRole"]
    resources = [aws_iam_role.game_instance.arn]

    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ec2.amazonaws.com"]
    }
  }

  statement {
    sid       = "ObserveManagedNode"
    actions   = ["ssm:DescribeInstanceInformation"]
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
    resources = ["${aws_cloudwatch_log_group.provisioning_worker.arn}:*"]
  }
}

resource "aws_iam_role" "provisioning_worker" {
  name               = "${local.name_prefix}-provisioning-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}

resource "aws_iam_role_policy" "provisioning_worker" {
  name   = "runtime"
  role   = aws_iam_role.provisioning_worker.id
  policy = data.aws_iam_policy_document.provisioning_worker.json
}

resource "aws_cloudwatch_log_group" "provisioning_worker" {
  name              = "/aws/lambda/${local.name_prefix}-provisioning-worker"
  retention_in_days = 30
}

resource "aws_lambda_function" "provisioning_worker" {
  function_name    = "${local.name_prefix}-provisioning-worker"
  description      = "Idempotently creates and observes Phase 5 EC2/EBS infrastructure."
  role             = aws_iam_role.provisioning_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.provisioning_worker_package_path
  source_code_hash = var.provisioning_worker_lambda_source_hash != null ? var.provisioning_worker_lambda_source_hash : try(filebase64sha256(local.provisioning_worker_package_path), null)
  timeout          = 60
  memory_size      = 256

  environment {
    variables = {
      APP_ENV                              = var.environment
      PROJECT_NAME                         = var.project_name
      LOG_LEVEL                            = "info"
      METADATA_TABLE_NAME                  = aws_dynamodb_table.metadata.name
      NOTIFICATION_QUEUE_URL               = aws_sqs_queue.notifications.url
      PROVISIONING_AMI_ID                  = data.aws_ssm_parameter.game_host_ami.value
      PROVISIONING_INSTANCE_TYPE           = var.provisioning_instance_type
      PROVISIONING_SUBNET_ID               = aws_subnet.game_public[0].id
      PROVISIONING_GAME_SECURITY_GROUP_ID  = aws_security_group.arma.id
      PROVISIONING_VOICE_SECURITY_GROUP_ID = aws_security_group.teamspeak.id
      PROVISIONING_INSTANCE_PROFILE        = aws_iam_instance_profile.game.name
      PROVISIONING_ROOT_VOLUME_GIB         = tostring(var.provisioning_root_volume_gib)
      PROVISIONING_DATA_VOLUME_GIB         = tostring(var.provisioning_data_volume_gib)
      MAX_PROVISIONED_SESSIONS             = tostring(var.max_provisioned_sessions)
    }
  }

  lifecycle {
    precondition {
      condition     = !var.provisioning_enabled || var.budget_alert_email != null
      error_message = "budget_alert_email must be set before provisioning_enabled can be true."
    }
  }

  depends_on = [aws_cloudwatch_log_group.provisioning_worker, aws_iam_role_policy.provisioning_worker]
}

data "aws_iam_policy_document" "provision_workflow" {
  statement {
    sid       = "InvokeProvisioningWorker"
    actions   = ["lambda:InvokeFunction"]
    resources = [aws_lambda_function.provisioning_worker.arn]
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

resource "aws_iam_role_policy" "provision_workflow" {
  name   = "provision-session"
  role   = aws_iam_role.workflow.id
  policy = data.aws_iam_policy_document.provision_workflow.json
}

resource "aws_cloudwatch_log_group" "provision_workflow" {
  name              = "/aws/states/${local.name_prefix}-ProvisionSession"
  retention_in_days = 30
}

resource "aws_sfn_state_machine" "provision_session" {
  name     = "${local.name_prefix}-ProvisionSession"
  role_arn = aws_iam_role.workflow.arn
  type     = "STANDARD"

  logging_configuration {
    include_execution_data = false
    level                  = "ERROR"
    log_destination        = "${aws_cloudwatch_log_group.provision_workflow.arn}:*"
  }

  definition = jsonencode({
    Comment = "Phase 5 idempotent EC2/EBS provisioning boundary."
    StartAt = "InitializeAttempts"
    States = {
      InitializeAttempts = {
        Type       = "Pass"
        Result     = { instance = 0, ssm = 0 }
        ResultPath = "$.attempts"
        Next       = "Prepare"
      }
      Prepare = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.provisioning_worker.function_name
          Payload = {
            action             = "prepare"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
          }
        }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.stage"
        Retry          = [{ ErrorEquals = ["States.TaskFailed"], IntervalSeconds = 2, BackoffRate = 2, MaxAttempts = 3 }]
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "MarkFailed" }]
        Next           = "EnsureInstance"
      }
      EnsureInstance = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.provisioning_worker.function_name
          Payload = {
            action             = "ensure_instance"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
          }
        }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.stage"
        Retry          = [{ ErrorEquals = ["States.TaskFailed"], IntervalSeconds = 5, BackoffRate = 2, MaxAttempts = 3 }]
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "MarkFailed" }]
        Next           = "WaitForInstance"
      }
      WaitForInstance = { Type = "Wait", Seconds = 15, Next = "ObserveInstance" }
      ObserveInstance = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.provisioning_worker.function_name
          Payload = {
            action             = "observe_instance"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
          }
        }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.stage"
        Retry          = [{ ErrorEquals = ["States.TaskFailed"], IntervalSeconds = 5, BackoffRate = 2, MaxAttempts = 3 }]
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "MarkFailed" }]
        Next           = "InstanceReady"
      }
      InstanceReady = {
        Type    = "Choice"
        Choices = [{ Variable = "$.stage.result.ready", BooleanEquals = true, Next = "WaitForManagedNode" }]
        Default = "IncrementInstanceAttempts"
      }
      IncrementInstanceAttempts = {
        Type = "Pass"
        Parameters = {
          "instance.$" = "States.MathAdd($.attempts.instance, 1)"
          "ssm.$"      = "$.attempts.ssm"
        }
        ResultPath = "$.attempts"
        Next       = "InstanceAttemptsAvailable"
      }
      InstanceAttemptsAvailable = {
        Type    = "Choice"
        Choices = [{ Variable = "$.attempts.instance", NumericGreaterThanEquals = 40, Next = "InstanceTimeout" }]
        Default = "WaitForInstance"
      }
      InstanceTimeout = {
        Type       = "Pass"
        Result     = { Error = "ERR_INSTANCE_TIMEOUT", Cause = "EC2 instance did not become ready within the bounded wait." }
        ResultPath = "$.failure"
        Next       = "MarkFailed"
      }
      WaitForManagedNode = { Type = "Wait", Seconds = 15, Next = "CheckManagedNode" }
      CheckManagedNode = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.provisioning_worker.function_name
          Payload = {
            action             = "check_managed"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
          }
        }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.stage"
        Retry          = [{ ErrorEquals = ["States.TaskFailed"], IntervalSeconds = 5, BackoffRate = 2, MaxAttempts = 3 }]
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "MarkFailed" }]
        Next           = "ManagedNodeReady"
      }
      ManagedNodeReady = {
        Type    = "Choice"
        Choices = [{ Variable = "$.stage.result.managed", BooleanEquals = true, Next = "Complete" }]
        Default = "IncrementSSMAttempts"
      }
      IncrementSSMAttempts = {
        Type = "Pass"
        Parameters = {
          "instance.$" = "$.attempts.instance"
          "ssm.$"      = "States.MathAdd($.attempts.ssm, 1)"
        }
        ResultPath = "$.attempts"
        Next       = "SSMAttemptsAvailable"
      }
      SSMAttemptsAvailable = {
        Type    = "Choice"
        Choices = [{ Variable = "$.attempts.ssm", NumericGreaterThanEquals = 40, Next = "SSMTimeout" }]
        Default = "WaitForManagedNode"
      }
      SSMTimeout = {
        Type       = "Pass"
        Result     = { Error = "ERR_SSM_TIMEOUT", Cause = "EC2 instance did not register with Systems Manager within the bounded wait." }
        ResultPath = "$.failure"
        Next       = "MarkFailed"
      }
      Complete = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.provisioning_worker.function_name
          Payload = {
            action             = "complete"
            "session_id.$"     = "$.session_id"
            "workflow_id.$"    = "$.workflow_id"
            "correlation_id.$" = "$.correlation_id"
          }
        }
        ResultPath = "$.completion"
        Retry      = [{ ErrorEquals = ["States.TaskFailed"], IntervalSeconds = 2, BackoffRate = 2, MaxAttempts = 3 }]
        Catch      = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "MarkFailed" }]
        End        = true
      }
      MarkFailed = {
        Type     = "Task"
        Resource = "arn:aws:states:::lambda:invoke"
        Parameters = {
          FunctionName = aws_lambda_function.provisioning_worker.function_name
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
        Next       = "ProvisioningFailed"
      }
      ProvisioningFailed = {
        Type  = "Fail"
        Error = "InfrastructureProvisioningFailed"
        Cause = "Phase 5 provisioning failed; metadata retains discovered resources for reconciliation."
      }
    }
  })

  depends_on = [aws_iam_role_policy.provision_workflow, aws_cloudwatch_log_group.provision_workflow]
}

output "game_vpc_id" {
  description = "Development game-server VPC."
  value       = aws_vpc.game.id
}

output "game_public_subnet_ids" {
  description = "Public subnets available to session provisioning."
  value       = aws_subnet.game_public[*].id
}

output "provisioning_enabled" {
  description = "Whether Discord start requests are accepted."
  value       = var.provisioning_enabled
}

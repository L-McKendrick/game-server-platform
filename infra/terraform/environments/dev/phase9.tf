variable "archive_worker_lambda_package_path" {
  description = "Optional path to the packaged archive worker Lambda zip."
  type        = string
  default     = null
  nullable    = true
}

variable "archive_worker_lambda_source_hash" {
  description = "Optional base64 SHA-256 supplied by CI for the archive worker package."
  type        = string
  default     = null
  nullable    = true
}

variable "restore_worker_lambda_package_path" {
  description = "Optional path to the packaged restore worker Lambda zip."
  type        = string
  default     = null
  nullable    = true
}

variable "restore_worker_lambda_source_hash" {
  description = "Optional base64 SHA-256 supplied by CI for the restore worker package."
  type        = string
  default     = null
  nullable    = true
}

variable "termination_worker_lambda_package_path" {
  description = "Optional path to the packaged termination worker Lambda zip."
  type        = string
  default     = null
  nullable    = true
}

variable "termination_worker_lambda_source_hash" {
  description = "Optional base64 SHA-256 supplied by CI for the termination worker package."
  type        = string
  default     = null
  nullable    = true
}

locals {
  archive_worker_package_path     = var.archive_worker_lambda_package_path != null ? var.archive_worker_lambda_package_path : abspath("${path.module}/../../../../dist/archive-worker.zip")
  restore_worker_package_path     = var.restore_worker_lambda_package_path != null ? var.restore_worker_lambda_package_path : abspath("${path.module}/../../../../dist/restore-worker.zip")
  termination_worker_package_path = var.termination_worker_lambda_package_path != null ? var.termination_worker_lambda_package_path : abspath("${path.module}/../../../../dist/termination-worker.zip")
  archive_definition = jsonencode({
    Comment        = "Phase 9 verifies a portable archive before deleting tagged disposable infrastructure."
    TimeoutSeconds = 18000
    StartAt        = "InitializeAttempts"
    States = {
      InitializeAttempts = { Type = "Pass", Result = { host = 0, termination = 0, volume = 0 }, ResultPath = "$.attempts", Next = "PrepareHost" }
      PrepareHost = {
        Type           = "Task"
        Resource       = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "prepare_host", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.host"
        Retry          = [local.lambda_transient_retry]
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }]
        Next           = "HostPrepared"
      }
      HostPrepared = { Type = "Choice", Choices = [{ Variable = "$.host.result.ready", BooleanEquals = true, Next = "Dispatch" }], Default = "WaitForHost" }
      WaitForHost  = { Type = "Wait", Seconds = 15, Next = "ObserveHost" }
      ObserveHost = {
        Type           = "Task"
        Resource       = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "observe_host", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.host"
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }]
        Next           = "HostReady"
      }
      HostReady             = { Type = "Choice", Choices = [{ Variable = "$.host.result.ready", BooleanEquals = true, Next = "Dispatch" }], Default = "IncrementHost" }
      IncrementHost         = { Type = "Pass", Parameters = { "host.$" = "States.MathAdd($.attempts.host, 1)", "termination.$" = "$.attempts.termination", "volume.$" = "$.attempts.volume" }, ResultPath = "$.attempts", Next = "HostAttemptsAvailable" }
      HostAttemptsAvailable = { Type = "Choice", Choices = [{ Variable = "$.attempts.host", NumericGreaterThanEquals = 40, Next = "HostTimeout" }], Default = "WaitForHost" }
      HostTimeout           = { Type = "Pass", Result = { Error = "ERR_ARCHIVE_HOST_TIMEOUT", Cause = "Sleeping archive host did not become managed within the bounded wait." }, ResultPath = "$.failure", Next = "Fail" }
      Dispatch = {
        Type           = "Task"
        Resource       = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "dispatch", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.archive"
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }]
        Next           = "WaitForArchive"
      }
      WaitForArchive = { Type = "Wait", Seconds = 30, Next = "Observe" }
      Observe = {
        Type           = "Task"
        Resource       = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "observe", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "command_id.$" = "$.archive.result.command_id" } }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.archive"
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }]
        Next           = "ArchiveResult"
      }
      ArchiveResult = {
        Type = "Choice"
        Choices = [
          { Variable = "$.archive.result.succeeded", BooleanEquals = true, Next = "Verify" },
          { Variable = "$.archive.result.done", BooleanEquals = true, Next = "ArchiveFailed" },
        ]
        Default = "WaitForArchive"
      }
      ArchiveFailed = { Type = "Pass", Parameters = { "Error.$" = "$.archive.result.error_code", "Cause.$" = "$.archive.result.error_message" }, ResultPath = "$.failure", Next = "Fail" }
      Verify = {
        Type           = "Task"
        Resource       = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "verify", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "object_key.$" = "$.archive.result.object_key", "sha256.$" = "$.archive.result.sha256", "size_bytes.$" = "$.archive.result.size_bytes" } }
        ResultSelector = { "result.$" = "$.Payload" }
        ResultPath     = "$.verified"
        Catch          = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }]
        Next           = "RecordVerified"
      }
      RecordVerified = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "record_verified", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "object_key.$" = "$.verified.result.object_key", "sha256.$" = "$.verified.result.sha256", "size_bytes.$" = "$.verified.result.size_bytes", "manifest_object_key.$" = "$.verified.result.manifest_object_key", "manifest_sha256.$" = "$.verified.result.manifest_sha256", "manifest_size_bytes.$" = "$.verified.result.manifest_size_bytes" } }
        ResultPath = "$.recorded", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "TerminateInstance"
      }
      TerminateInstance = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "terminate_instance", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultPath = "$.termination", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "WaitForTermination"
      }
      WaitForTermination = { Type = "Wait", Seconds = 15, Next = "ObserveTermination" }
      ObserveTermination = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "observe_termination", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.termination", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "TerminationComplete"
      }
      TerminationComplete          = { Type = "Choice", Choices = [{ Variable = "$.termination.result.done", BooleanEquals = true, Next = "DeleteVolume" }], Default = "IncrementTermination" }
      IncrementTermination         = { Type = "Pass", Parameters = { "termination.$" = "States.MathAdd($.attempts.termination, 1)", "volume.$" = "$.attempts.volume" }, ResultPath = "$.attempts", Next = "TerminationAttemptsAvailable" }
      TerminationAttemptsAvailable = { Type = "Choice", Choices = [{ Variable = "$.attempts.termination", NumericGreaterThanEquals = 40, Next = "TerminationTimeout" }], Default = "WaitForTermination" }
      TerminationTimeout           = { Type = "Pass", Result = { Error = "ERR_TERMINATION_TIMEOUT", Cause = "Tagged instance did not terminate within the bounded wait." }, ResultPath = "$.failure", Next = "Fail" }
      DeleteVolume = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "delete_volume", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultPath = "$.volume", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "WaitForVolumeDeletion"
      }
      WaitForVolumeDeletion = { Type = "Wait", Seconds = 10, Next = "ObserveVolumeDeletion" }
      ObserveVolumeDeletion = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "observe_volume", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.volume", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "VolumeDeletionComplete"
      }
      VolumeDeletionComplete  = { Type = "Choice", Choices = [{ Variable = "$.volume.result.done", BooleanEquals = true, Next = "Complete" }], Default = "IncrementVolume" }
      IncrementVolume         = { Type = "Pass", Parameters = { "termination.$" = "$.attempts.termination", "volume.$" = "States.MathAdd($.attempts.volume, 1)" }, ResultPath = "$.attempts", Next = "VolumeAttemptsAvailable" }
      VolumeAttemptsAvailable = { Type = "Choice", Choices = [{ Variable = "$.attempts.volume", NumericGreaterThanEquals = 40, Next = "VolumeTimeout" }], Default = "WaitForVolumeDeletion" }
      VolumeTimeout           = { Type = "Pass", Result = { Error = "ERR_VOLUME_DELETE_TIMEOUT", Cause = "Tagged data volume did not delete within the bounded wait." }, ResultPath = "$.failure", Next = "Fail" }
      Complete = {
        Type       = "Task"
        Resource   = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "complete", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        Next       = "ArchiveWorkflowFailed"
      }
      ArchiveWorkflowFailed = { Type = "Fail", Error = "ArchiveWorkflowFailed", Cause = "Archive or guarded infrastructure destruction failed." }
      Fail = {
        Type       = "Task"
        Resource   = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.archive_worker.function_name, Payload = { action = "fail", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "error_code.$" = "$.failure.Error", "error_message.$" = "$.failure.Cause" } }
        End        = true
      }
    }
  })
  restore_definition = jsonencode({
    Comment        = "Phase 9 recreates disposable infrastructure and restores a checksum-verified portable archive."
    TimeoutSeconds = 64800
    StartAt        = "VerifyArchive"
    States = {
      VerifyArchive = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "verify_archive", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultPath = "$.verification", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "Prepare"
      }
      Prepare = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "prepare", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultPath = "$.prepared", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "EnsureInstance"
      }
      EnsureInstance = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "ensure_instance", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultPath = "$.instance", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "InitializeInstanceAttempts"
      }
      InitializeInstanceAttempts = { Type = "Pass", Result = 0, ResultPath = "$.attempt", Next = "WaitForInstance" }
      WaitForInstance            = { Type = "Wait", Seconds = 15, Next = "ObserveInstance" }
      ObserveInstance = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "observe_instance", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.instance", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "InstanceReady"
      }
      InstanceReady             = { Type = "Choice", Choices = [{ Variable = "$.instance.result.ready", BooleanEquals = true, Next = "InitializeManagedAttempts" }], Default = "IncrementInstanceAttempts" }
      IncrementInstanceAttempts = { Type = "Pass", Parameters = { "value.$" = "States.MathAdd($.attempt, 1)" }, ResultPath = "$.counter", Next = "CopyInstanceAttempts" }
      CopyInstanceAttempts      = { Type = "Pass", InputPath = "$.counter.value", ResultPath = "$.attempt", Next = "InstanceAttemptsAvailable" }
      InstanceAttemptsAvailable = { Type = "Choice", Choices = [{ Variable = "$.attempt", NumericGreaterThanEquals = 40, Next = "InstanceTimeout" }], Default = "WaitForInstance" }
      InstanceTimeout           = { Type = "Pass", Result = { Error = "ERR_INSTANCE_TIMEOUT", Cause = "Restored EC2 instance did not become ready within the bounded wait." }, ResultPath = "$.failure", Next = "Fail" }
      InitializeManagedAttempts = { Type = "Pass", Result = 0, ResultPath = "$.attempt", Next = "WaitForManagedNode" }
      WaitForManagedNode        = { Type = "Wait", Seconds = 15, Next = "CheckManagedNode" }
      CheckManagedNode = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "check_managed", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.managed", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "ManagedNodeReady"
      }
      ManagedNodeReady         = { Type = "Choice", Choices = [{ Variable = "$.managed.result.managed", BooleanEquals = true, Next = "DispatchRestore" }], Default = "IncrementManagedAttempts" }
      IncrementManagedAttempts = { Type = "Pass", Parameters = { "value.$" = "States.MathAdd($.attempt, 1)" }, ResultPath = "$.counter", Next = "CopyManagedAttempts" }
      CopyManagedAttempts      = { Type = "Pass", InputPath = "$.counter.value", ResultPath = "$.attempt", Next = "ManagedAttemptsAvailable" }
      ManagedAttemptsAvailable = { Type = "Choice", Choices = [{ Variable = "$.attempt", NumericGreaterThanEquals = 40, Next = "ManagedTimeout" }], Default = "WaitForManagedNode" }
      ManagedTimeout           = { Type = "Pass", Result = { Error = "ERR_SSM_TIMEOUT", Cause = "Restored EC2 instance did not register with Systems Manager within the bounded wait." }, ResultPath = "$.failure", Next = "Fail" }
      DispatchBootstrap = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "dispatch_bootstrap", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.bootstrap", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }], Next = "InitializeBootstrapAttempts"
      }
      InitializeBootstrapAttempts = { Type = "Pass", Result = 0, ResultPath = "$.attempt", Next = "WaitForBootstrap" }
      WaitForBootstrap            = { Type = "Wait", Seconds = 30, Next = "ObserveBootstrap" }
      ObserveBootstrap = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "observe_bootstrap", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "command_id.$" = "$.bootstrap.result.command_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.bootstrap", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }], Next = "BootstrapResult"
      }
      BootstrapResult            = { Type = "Choice", Choices = [{ Variable = "$.bootstrap.result.succeeded", BooleanEquals = true, Next = "Complete" }, { Variable = "$.bootstrap.result.done", BooleanEquals = true, Next = "BootstrapFailed" }], Default = "IncrementBootstrapAttempts" }
      IncrementBootstrapAttempts = { Type = "Pass", Parameters = { "value.$" = "States.MathAdd($.attempt, 1)" }, ResultPath = "$.counter", Next = "CopyBootstrapAttempts" }
      CopyBootstrapAttempts      = { Type = "Pass", InputPath = "$.counter.value", ResultPath = "$.attempt", Next = "BootstrapAttemptsAvailable" }
      BootstrapAttemptsAvailable = { Type = "Choice", Choices = [{ Variable = "$.attempt", NumericGreaterThanEquals = 1440, Next = "BootstrapTimeout" }], Default = "WaitForBootstrap" }
      BootstrapFailed            = { Type = "Pass", Parameters = { "Error.$" = "$.bootstrap.result.error_code", "Cause.$" = "$.bootstrap.result.error_message" }, ResultPath = "$.failure", Next = "DispatchRollback" }
      BootstrapTimeout           = { Type = "Pass", Result = { Error = "ERR_BOOTSTRAP_TIMEOUT", Cause = "Bootstrap did not complete within the bounded wait." }, ResultPath = "$.failure", Next = "DispatchRollback" }
      DispatchRestore = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "dispatch_restore", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.restore", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "InitializeRestoreAttempts"
      }
      InitializeRestoreAttempts = { Type = "Pass", Result = 0, ResultPath = "$.attempt", Next = "WaitForRestore" }
      WaitForRestore            = { Type = "Wait", Seconds = 30, Next = "ObserveRestore" }
      ObserveRestore = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "observe_restore", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "command_id.$" = "$.restore.result.command_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.restore", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "RestoreResult"
      }
      RestoreResult            = { Type = "Choice", Choices = [{ Variable = "$.restore.result.succeeded", BooleanEquals = true, Next = "DispatchBootstrap" }, { Variable = "$.restore.result.done", BooleanEquals = true, Next = "RestoreFailed" }], Default = "IncrementRestoreAttempts" }
      IncrementRestoreAttempts = { Type = "Pass", Parameters = { "value.$" = "States.MathAdd($.attempt, 1)" }, ResultPath = "$.counter", Next = "CopyRestoreAttempts" }
      CopyRestoreAttempts      = { Type = "Pass", InputPath = "$.counter.value", ResultPath = "$.attempt", Next = "RestoreAttemptsAvailable" }
      RestoreAttemptsAvailable = { Type = "Choice", Choices = [{ Variable = "$.attempt", NumericGreaterThanEquals = 480, Next = "RestoreTimeout" }], Default = "WaitForRestore" }
      RestoreFailed            = { Type = "Pass", Parameters = { "Error.$" = "$.restore.result.error_code", "Cause.$" = "$.restore.result.error_message" }, ResultPath = "$.failure", Next = "Fail" }
      RestoreTimeout           = { Type = "Pass", Result = { Error = "ERR_RESTORE_TIMEOUT", Cause = "Archive restore did not complete within the bounded wait." }, ResultPath = "$.failure", Next = "Fail" }
      Complete = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "complete", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        Retry      = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "DispatchRollback" }], End = true
      }
      DispatchRollback = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "dispatch_rollback", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.rollback", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.rollback_failure", Next = "Fail" }], Next = "RollbackDispatched"
      }
      RollbackDispatched         = { Type = "Choice", Choices = [{ Variable = "$.rollback.result.succeeded", BooleanEquals = true, Next = "Fail" }], Default = "InitializeRollbackAttempts" }
      InitializeRollbackAttempts = { Type = "Pass", Result = 0, ResultPath = "$.rollback_attempt", Next = "WaitForRollback" }
      WaitForRollback            = { Type = "Wait", Seconds = 30, Next = "ObserveRollback" }
      ObserveRollback = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "observe_rollback", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "command_id.$" = "$.rollback.result.command_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.rollback", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.rollback_failure", Next = "Fail" }], Next = "RollbackComplete"
      }
      RollbackComplete          = { Type = "Choice", Choices = [{ Variable = "$.rollback.result.done", BooleanEquals = true, Next = "Fail" }], Default = "IncrementRollbackAttempts" }
      IncrementRollbackAttempts = { Type = "Pass", Parameters = { "value.$" = "States.MathAdd($.rollback_attempt, 1)" }, ResultPath = "$.rollback_counter", Next = "CopyRollbackAttempts" }
      CopyRollbackAttempts      = { Type = "Pass", InputPath = "$.rollback_counter.value", ResultPath = "$.rollback_attempt", Next = "RollbackAttemptsAvailable" }
      RollbackAttemptsAvailable = { Type = "Choice", Choices = [{ Variable = "$.rollback_attempt", NumericGreaterThanEquals = local.bootstrap_poll_limit, Next = "Fail" }], Default = "WaitForRollback" }
      Fail = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.restore_worker.function_name, Payload = { action = "fail", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "error_code.$" = "$.failure.Error", "error_message.$" = "$.failure.Cause" } }
        Next       = "RestoreWorkflowFailed"
      }
      RestoreWorkflowFailed = { Type = "Fail", Error = "RestoreWorkflowFailed", Cause = "Archive verification, infrastructure recreation, bootstrap, or restore failed." }
    }
  })
  termination_definition = jsonencode({
    Comment        = "Phase 9.3 permanently deletes one session's tagged infrastructure and all versioned stored objects."
    TimeoutSeconds = 3600
    StartAt        = "InitializeAttempts"
    States = {
      InitializeAttempts = { Type = "Pass", Result = { termination = 0, volume = 0 }, ResultPath = "$.attempts", Next = "TerminateInstance" }
      TerminateInstance = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.termination_worker.function_name, Payload = { action = "terminate_instance", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultPath = "$.stage", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "WaitForTermination"
      }
      WaitForTermination = { Type = "Wait", Seconds = 15, Next = "ObserveTermination" }
      ObserveTermination = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.termination_worker.function_name, Payload = { action = "observe_termination", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.stage", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "InstanceTerminated"
      }
      InstanceTerminated           = { Type = "Choice", Choices = [{ Variable = "$.stage.result.succeeded", BooleanEquals = true, Next = "DeleteVolume" }], Default = "IncrementTermination" }
      IncrementTermination         = { Type = "Pass", Parameters = { "termination.$" = "States.MathAdd($.attempts.termination, 1)", "volume.$" = "$.attempts.volume" }, ResultPath = "$.attempts", Next = "TerminationAttemptsAvailable" }
      TerminationAttemptsAvailable = { Type = "Choice", Choices = [{ Variable = "$.attempts.termination", NumericGreaterThanEquals = 40, Next = "TerminationTimeout" }], Default = "WaitForTermination" }
      TerminationTimeout           = { Type = "Pass", Result = { Error = "ERR_TERMINATION_TIMEOUT", Cause = "Tagged session instance did not terminate within the bounded wait." }, ResultPath = "$.failure", Next = "Fail" }
      DeleteVolume = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.termination_worker.function_name, Payload = { action = "delete_volume", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultPath = "$.stage", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "WaitForVolumeDeletion"
      }
      WaitForVolumeDeletion = { Type = "Wait", Seconds = 15, Next = "ObserveVolume" }
      ObserveVolume = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.termination_worker.function_name, Payload = { action = "observe_volume", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.stage", Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "VolumeDeleted"
      }
      VolumeDeleted           = { Type = "Choice", Choices = [{ Variable = "$.stage.result.succeeded", BooleanEquals = true, Next = "DeleteObjects" }], Default = "IncrementVolume" }
      IncrementVolume         = { Type = "Pass", Parameters = { "termination.$" = "$.attempts.termination", "volume.$" = "States.MathAdd($.attempts.volume, 1)" }, ResultPath = "$.attempts", Next = "VolumeAttemptsAvailable" }
      VolumeAttemptsAvailable = { Type = "Choice", Choices = [{ Variable = "$.attempts.volume", NumericGreaterThanEquals = 40, Next = "VolumeTimeout" }], Default = "WaitForVolumeDeletion" }
      VolumeTimeout           = { Type = "Pass", Result = { Error = "ERR_VOLUME_TIMEOUT", Cause = "Tagged session data volume did not delete within the bounded wait." }, ResultPath = "$.failure", Next = "Fail" }
      DeleteObjects = {
        Type           = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters     = { FunctionName = aws_lambda_function.termination_worker.function_name, Payload = { action = "delete_objects", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id" } }
        ResultSelector = { "result.$" = "$.Payload" }, ResultPath = "$.objects", Retry = [local.lambda_transient_retry], Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }], Next = "Complete"
      }
      Complete = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.termination_worker.function_name, Payload = { action = "complete", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "objects_deleted.$" = "$.objects.result.objects_deleted" } }
        End        = true, Catch = [{ ErrorEquals = ["States.ALL"], ResultPath = "$.failure", Next = "Fail" }]
      }
      Fail = {
        Type       = "Task", Resource = "arn:aws:states:::lambda:invoke"
        Parameters = { FunctionName = aws_lambda_function.termination_worker.function_name, Payload = { action = "fail", "session_id.$" = "$.session_id", "workflow_id.$" = "$.workflow_id", "correlation_id.$" = "$.correlation_id", "error_code.$" = "$.failure.Error", "error_message.$" = "$.failure.Cause" } }
        End        = true
      }
    }
  })
}

resource "aws_cloudwatch_log_group" "archive_worker" {
  name              = "/aws/lambda/${local.name_prefix}-archive-worker"
  retention_in_days = 30
}

data "aws_iam_policy_document" "archive_worker" {
  statement {
    sid       = "MetadataAccess"
    actions   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem", "dynamodb:TransactWriteItems"]
    resources = [aws_dynamodb_table.metadata.arn]
  }
  statement {
    sid       = "ArchiveObjectVerification"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.session_assets.arn}/sessions/*/archives/*"]
  }
  statement {
    sid       = "ArchiveManifestWrite"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.session_assets.arn}/sessions/*/archives/*/manifest.v1.json"]
  }
  statement {
    sid       = "ArchiveCommandDispatch"
    actions   = ["ssm:SendCommand"]
    resources = ["arn:aws:ssm:${var.aws_region}::document/AWS-RunShellScript"]
  }
  statement {
    sid       = "ArchiveManagedInstanceDispatch"
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
    sid       = "ArchiveCommandObserve"
    actions   = ["ssm:GetCommandInvocation"]
    resources = ["*"]
  }
  statement {
    sid       = "ObserveArchiveInfrastructure"
    actions   = ["ec2:DescribeInstances", "ec2:DescribeVolumes"]
    resources = ["*"]
  }
  statement {
    sid       = "StartSleepingArchiveInstance"
    actions   = ["ec2:StartInstances"]
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
    sid       = "ObserveSleepingArchiveManagedNode"
    actions   = ["ssm:DescribeInstanceInformation"]
    resources = ["*"]
  }
  statement {
    sid       = "TerminateTaggedArchiveInstance"
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
    sid       = "DeleteTaggedArchiveVolume"
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
    sid       = "NotificationSend"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.notifications.arn]
  }
  statement {
    sid       = "RuntimeLogDelivery"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.archive_worker.arn}:*"]
  }
}

resource "aws_iam_role" "archive_worker" {
  name               = "${local.name_prefix}-archive-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}

resource "aws_iam_role_policy" "archive_worker" {
  name   = "runtime"
  role   = aws_iam_role.archive_worker.id
  policy = data.aws_iam_policy_document.archive_worker.json
}

resource "aws_lambda_function" "archive_worker" {
  function_name    = "${local.name_prefix}-archive-worker"
  description      = "Coordinates portable session archive creation and checksum verification."
  role             = aws_iam_role.archive_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.archive_worker_package_path
  source_code_hash = var.archive_worker_lambda_source_hash != null ? var.archive_worker_lambda_source_hash : try(filebase64sha256(local.archive_worker_package_path), null)
  timeout          = 60
  memory_size      = 256
  environment {
    variables = {
      APP_ENV                = var.environment
      PROJECT_NAME           = var.project_name
      LOG_LEVEL              = "info"
      METADATA_TABLE_NAME    = aws_dynamodb_table.metadata.name
      NOTIFICATION_QUEUE_URL = aws_sqs_queue.notifications.url
      SESSION_ASSETS_BUCKET  = aws_s3_bucket.session_assets.id
    }
  }
  depends_on = [aws_cloudwatch_log_group.archive_worker, aws_iam_role_policy.archive_worker]
}

data "aws_iam_policy_document" "archive_workflow" {
  statement {
    actions   = ["lambda:InvokeFunction"]
    resources = [aws_lambda_function.archive_worker.arn, aws_lambda_function.restore_worker.arn, aws_lambda_function.termination_worker.arn]
  }
}

resource "aws_cloudwatch_log_group" "termination_worker" {
  name              = "/aws/lambda/${local.name_prefix}-termination-worker"
  retention_in_days = 30
}

data "aws_iam_policy_document" "termination_worker" {
  statement {
    sid       = "MetadataAndCapacityAccess"
    actions   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem", "dynamodb:TransactWriteItems"]
    resources = [aws_dynamodb_table.metadata.arn]
  }
  statement {
    sid       = "ListSessionObjectVersions"
    actions   = ["s3:ListBucketVersions"]
    resources = [aws_s3_bucket.session_assets.arn]
    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["sessions/*"]
    }
  }
  statement {
    sid       = "DeleteSessionObjectVersions"
    actions   = ["s3:DeleteObject", "s3:DeleteObjectVersion"]
    resources = ["${aws_s3_bucket.session_assets.arn}/sessions/*"]
  }
  statement {
    sid       = "ObserveTerminationInfrastructure"
    actions   = ["ec2:DescribeInstances", "ec2:DescribeVolumes"]
    resources = ["*"]
  }
  statement {
    sid       = "TerminateTaggedSessionInstance"
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
    sid       = "DeleteTaggedSessionVolume"
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
    sid       = "NotificationSend"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.notifications.arn]
  }
  statement {
    sid       = "RuntimeLogDelivery"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.termination_worker.arn}:*"]
  }
}

resource "aws_iam_role" "termination_worker" {
  name               = "${local.name_prefix}-termination-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}

resource "aws_iam_role_policy" "termination_worker" {
  name   = "runtime"
  role   = aws_iam_role.termination_worker.id
  policy = data.aws_iam_policy_document.termination_worker.json
}

resource "aws_lambda_function" "termination_worker" {
  function_name    = "${local.name_prefix}-termination-worker"
  description      = "Permanently deletes one session's tagged infrastructure and stored object versions."
  role             = aws_iam_role.termination_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.termination_worker_package_path
  source_code_hash = var.termination_worker_lambda_source_hash != null ? var.termination_worker_lambda_source_hash : try(filebase64sha256(local.termination_worker_package_path), null)
  timeout          = 300
  memory_size      = 256
  environment {
    variables = {
      APP_ENV                = var.environment
      PROJECT_NAME           = var.project_name
      LOG_LEVEL              = "info"
      METADATA_TABLE_NAME    = aws_dynamodb_table.metadata.name
      NOTIFICATION_QUEUE_URL = aws_sqs_queue.notifications.url
      SESSION_ASSETS_BUCKET  = aws_s3_bucket.session_assets.id
    }
  }
  depends_on = [aws_cloudwatch_log_group.termination_worker, aws_iam_role_policy.termination_worker]
}

resource "aws_iam_role_policy" "archive_workflow" {
  name   = "archive"
  role   = aws_iam_role.workflow.id
  policy = data.aws_iam_policy_document.archive_workflow.json
}

resource "aws_cloudwatch_log_group" "restore_worker" {
  name              = "/aws/lambda/${local.name_prefix}-restore-worker"
  retention_in_days = 30
}

data "aws_iam_policy_document" "restore_worker" {
  statement {
    sid       = "MetadataAndCapacityAccess"
    actions   = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DeleteItem", "dynamodb:TransactWriteItems"]
    resources = [aws_dynamodb_table.metadata.arn]
  }
  statement {
    sid       = "ReadVerifiedArchive"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.session_assets.arn}/sessions/*/archives/*"]
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
    sid     = "TagCreatedRestoreCompute"
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
    sid       = "ObserveRestoreCompute"
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
    sid       = "RestoreCommandDispatch"
    actions   = ["ssm:SendCommand"]
    resources = ["arn:aws:ssm:${var.aws_region}::document/AWS-RunShellScript"]
  }
  statement {
    sid       = "RestoreManagedInstanceDispatch"
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
    sid       = "RestoreCommandObserve"
    actions   = ["ssm:GetCommandInvocation"]
    resources = ["*"]
  }
  statement {
    sid       = "NotificationSend"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.notifications.arn]
  }
  statement {
    sid       = "RuntimeLogDelivery"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.restore_worker.arn}:*"]
  }
}

resource "aws_iam_role" "restore_worker" {
  name               = "${local.name_prefix}-restore-worker"
  assume_role_policy = data.aws_iam_policy_document.discord_lambda_assume_role.json
}

resource "aws_iam_role_policy" "restore_worker" {
  name   = "runtime"
  role   = aws_iam_role.restore_worker.id
  policy = data.aws_iam_policy_document.restore_worker.json
}

resource "aws_lambda_function" "restore_worker" {
  function_name    = "${local.name_prefix}-restore-worker"
  description      = "Recreates infrastructure and restores a checksum-verified portable session archive."
  role             = aws_iam_role.restore_worker.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["x86_64"]
  filename         = local.restore_worker_package_path
  source_code_hash = var.restore_worker_lambda_source_hash != null ? var.restore_worker_lambda_source_hash : try(filebase64sha256(local.restore_worker_package_path), null)
  timeout          = 60
  memory_size      = 256
  environment {
    variables = {
      APP_ENV                              = var.environment
      PROJECT_NAME                         = var.project_name
      LOG_LEVEL                            = "info"
      METADATA_TABLE_NAME                  = aws_dynamodb_table.metadata.name
      NOTIFICATION_QUEUE_URL               = aws_sqs_queue.notifications.url
      SESSION_ASSETS_BUCKET                = aws_s3_bucket.session_assets.id
      BOOTSTRAP_SCRIPT_KEY                 = aws_s3_object.bootstrap_script.key
      STEAM_AUTH_SECRET_ID                 = aws_secretsmanager_secret.steam_authorization_cache.name
      TEAMSPEAK_VERSION                    = var.teamspeak_version
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
  depends_on = [aws_cloudwatch_log_group.restore_worker, aws_iam_role_policy.restore_worker]
}

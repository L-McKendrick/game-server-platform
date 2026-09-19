locals {
  host_access_issuers = {
    bootstrap   = { role = aws_iam_role.bootstrap_worker.id, lifecycle = true, archive_write = false, archive_read = false }
    sleepwake   = { role = aws_iam_role.sleepwake_worker.id, lifecycle = true, archive_write = false, archive_read = false }
    artifact    = { role = aws_iam_role.artifact_worker.id, lifecycle = true, archive_write = false, archive_read = false }
    reliability = { role = aws_iam_role.reliability_worker.id, lifecycle = true, archive_write = false, archive_read = false }
    restore     = { role = aws_iam_role.restore_worker.id, lifecycle = true, archive_write = false, archive_read = true }
    archive     = { role = aws_iam_role.archive_worker.id, lifecycle = false, archive_write = true, archive_read = false }
  }
}

data "aws_iam_policy_document" "host_access_issuer" {
  for_each = local.host_access_issuers

  statement {
    sid       = "ReadPinnedAttemptObjects"
    actions   = ["s3:GetObject", "s3:GetObjectVersion"]
    resources = ["${aws_s3_bucket.session_assets.arn}/sessions/*/runtime/host-access/*"]
  }
  statement {
    sid       = "WriteTaggedAttemptObjects"
    actions   = ["s3:PutObject", "s3:PutObjectTagging"]
    resources = ["${aws_s3_bucket.session_assets.arn}/sessions/*/runtime/host-access/*"]
    condition {
      test     = "StringEquals"
      variable = "s3:RequestObjectTag/gsp-retention"
      values   = ["host-access"]
    }
  }
  statement {
    sid       = "ValidateOwnedInstanceAndCommands"
    actions   = ["ec2:DescribeInstances", "ssm:ListCommands"]
    resources = ["*"]
  }
  statement {
    sid       = "ReadAndConditionallyPersistAttempts"
    actions   = ["dynamodb:GetItem", "dynamodb:Query", "dynamodb:PutItem", "dynamodb:ConditionCheckItem"]
    resources = [aws_dynamodb_table.metadata.arn]
    condition {
      test     = "ForAllValues:StringLike"
      variable = "dynamodb:LeadingKeys"
      values   = ["SESSION#*"]
    }
  }
  dynamic "statement" {
    for_each = each.value.lifecycle ? [true] : []
    content {
      sid     = "ReadVerifiedWorkshopObjects"
      actions = ["s3:GetObject", "s3:GetObjectVersion"]
      resources = [
        "${aws_s3_bucket.session_assets.arn}/sessions/*/workshop-resolutions/*.tsv",
        "${aws_s3_bucket.session_assets.arn}/sessions/*/workshop-sync/*.json",
      ]
    }
  }
  dynamic "statement" {
    for_each = each.value.lifecycle ? [true] : []
    content {
      sid     = "ReadAuthorizedLifecycleInputs"
      actions = ["s3:GetObject", "s3:GetObjectVersion"]
      resources = [
        "${aws_s3_bucket.session_assets.arn}/platform/bootstrap/*",
        "${aws_s3_bucket.session_assets.arn}/sessions/*/input/*",
        "${aws_s3_bucket.session_assets.arn}/guilds/*/server-config/revisions/*/server.cfg",
      ]
    }
  }
  dynamic "statement" {
    for_each = each.value.lifecycle ? [true] : []
    content {
      sid     = "PublishVerifiedWorkshopObjects"
      actions = ["s3:PutObject"]
      resources = [
        "${aws_s3_bucket.session_assets.arn}/sessions/*/input/missions/*",
        "${aws_s3_bucket.session_assets.arn}/sessions/*/workshop-resolutions/*.tsv",
        "${aws_s3_bucket.session_assets.arn}/sessions/*/workshop-sync/*.json",
      ]
    }
  }
  dynamic "statement" {
    for_each = each.value.archive_write ? [true] : []
    content {
      sid       = "CreateAndInspectWorkflowArchives"
      actions   = ["s3:PutObject", "s3:GetObject", "s3:GetObjectVersion"]
      resources = ["${aws_s3_bucket.session_assets.arn}/sessions/*/archives/*/session.tar.gz"]
    }
  }
  dynamic "statement" {
    for_each = each.value.archive_read ? [true] : []
    content {
      sid       = "ReadVerifiedRestoreArchives"
      actions   = ["s3:GetObject", "s3:GetObjectVersion"]
      resources = ["${aws_s3_bucket.session_assets.arn}/sessions/*/archives/*/session.tar.gz"]
    }
  }
  dynamic "statement" {
    for_each = each.key == "reliability" ? [true] : []
    content {
      sid       = "ListExpiredAttemptVersions"
      actions   = ["s3:ListBucketVersions"]
      resources = [aws_s3_bucket.session_assets.arn]
      condition {
        test     = "StringLike"
        variable = "s3:prefix"
        values   = ["sessions/*/runtime/host-access/*"]
      }
    }
  }
  dynamic "statement" {
    for_each = each.key == "reliability" ? [true] : []
    content {
      sid       = "DeleteExpiredAttemptVersions"
      actions   = ["s3:DeleteObject", "s3:DeleteObjectVersion"]
      resources = ["${aws_s3_bucket.session_assets.arn}/sessions/*/runtime/host-access/*"]
    }
  }
}

resource "aws_iam_role_policy" "host_access_issuer" {
  for_each = local.host_access_issuers
  name     = "scoped-host-access"
  role     = each.value.role
  policy   = data.aws_iam_policy_document.host_access_issuer[each.key].json
}

data "aws_iam_policy_document" "host_access_termination_query" {
  statement {
    actions   = ["dynamodb:GetItem", "dynamodb:Query"]
    resources = [aws_dynamodb_table.metadata.arn]
    condition {
      test     = "ForAllValues:StringLike"
      variable = "dynamodb:LeadingKeys"
      values   = ["SESSION#*"]
    }
  }
}

resource "aws_iam_role_policy" "host_access_termination_query" {
  name   = "host-access-maintenance"
  role   = aws_iam_role.termination_worker.id
  policy = data.aws_iam_policy_document.host_access_termination_query.json
}

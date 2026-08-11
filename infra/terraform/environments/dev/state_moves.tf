moved {
  from = aws_apigatewayv2_api.discord_interactions
  to   = aws_apigatewayv2_api.discord
}

moved {
  from = aws_sfn_state_machine.workflow["ProvisionSession"]
  to   = aws_sfn_state_machine.provision_session
}

moved {
  from = aws_apigatewayv2_integration.discord_interactions
  to   = aws_apigatewayv2_integration.discord_lambda
}

moved {
  from = aws_apigatewayv2_stage.discord_interactions
  to   = aws_apigatewayv2_stage.discord
}

moved {
  from = aws_cloudwatch_log_group.discord_interactions_api
  to   = aws_cloudwatch_log_group.discord_api
}

moved {
  from = aws_cloudwatch_log_group.discord_interactions_lambda
  to   = aws_cloudwatch_log_group.discord_lambda
}

moved {
  from = aws_iam_role.discord_interactions_lambda
  to   = aws_iam_role.discord_lambda
}

moved {
  from = aws_iam_role_policy.discord_interactions_lambda
  to   = aws_iam_role_policy.discord_lambda
}

moved {
  from = aws_lambda_permission.discord_interactions_api
  to   = aws_lambda_permission.discord_api
}

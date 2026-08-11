param(
    [Parameter(Mandatory = $true)]
    [string]$PlanPath,

    [string]$AwsProfile = "game-server-dev"
)

$ErrorActionPreference = "Stop"

$resolvedPlanPath = (Resolve-Path -LiteralPath $PlanPath).Path
$temporaryDefinitionPath = Join-Path ([System.IO.Path]::GetTempPath()) ("phase5-provision-session-{0}.json" -f [guid]::NewGuid())

try {
    $plan = terraform show -json $resolvedPlanPath | ConvertFrom-Json
    $stateMachine = $plan.resource_changes | Where-Object {
        $_.address -eq "aws_sfn_state_machine.provision_session"
    }

    if ($null -eq $stateMachine) {
        throw "ProvisionSession state machine was not found in the Terraform plan."
    }

    $definition = [string]$stateMachine.change.after.definition
    if ([string]::IsNullOrWhiteSpace($definition)) {
        throw "ProvisionSession definition is not known in the Terraform plan."
    }

    $null = $definition | ConvertFrom-Json
    [System.IO.File]::WriteAllText(
        $temporaryDefinitionPath,
        $definition,
        [System.Text.UTF8Encoding]::new($false)
    )

    aws stepfunctions validate-state-machine-definition `
        --profile $AwsProfile `
        --definition ("file://{0}" -f $temporaryDefinitionPath) `
        --type STANDARD `
        --severity ERROR `
        --output json

    if ($LASTEXITCODE -ne 0) {
        throw "AWS Step Functions definition validation failed."
    }
}
finally {
    if (Test-Path -LiteralPath $temporaryDefinitionPath) {
        Remove-Item -LiteralPath $temporaryDefinitionPath -Force
    }
}

[CmdletBinding()]
param(
    [string]$FunctionName = "game-server-platform-dev-bootstrap-worker",
    [string]$ArchivePath = "",
    [string]$Profile = "game-server-dev",
    [string]$Region = "us-west-2"
)

$ErrorActionPreference = "Stop"
$repositoryRoot = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($ArchivePath)) {
    $ArchivePath = Join-Path $repositoryRoot "dist/bootstrap-worker.zip"
}
$resolvedArchive = (Resolve-Path -LiteralPath $ArchivePath).Path

$sha256 = [Security.Cryptography.SHA256]::Create()
try {
    $stream = [IO.File]::OpenRead($resolvedArchive)
    try {
        $localHash = [Convert]::ToBase64String($sha256.ComputeHash($stream))
    }
    finally {
        $stream.Dispose()
    }
}
finally {
    $sha256.Dispose()
}

$configurationJSON = aws lambda get-function-configuration `
    --function-name $FunctionName `
    --profile $Profile `
    --region $Region `
    --output json
if ($LASTEXITCODE -ne 0) {
    throw "Unable to read Lambda configuration for $FunctionName"
}
$configuration = $configurationJSON | ConvertFrom-Json

if ($configuration.LastUpdateStatus -ne "Successful" -or $configuration.State -ne "Active") {
    throw "$FunctionName is not Active/Successful (State=$($configuration.State), LastUpdateStatus=$($configuration.LastUpdateStatus))"
}
if ($configuration.CodeSha256 -ne $localHash) {
    throw "$FunctionName code hash does not match $resolvedArchive. Package and apply a fresh reviewed Terraform plan."
}

$requiredEnvironment = @(
    "APP_ENV",
    "METADATA_TABLE_NAME",
    "NOTIFICATION_QUEUE_URL",
    "SESSION_ASSETS_BUCKET",
    "BOOTSTRAP_SCRIPT_KEY",
    "STEAM_AUTH_SECRET_ID",
    "TEAMSPEAK_VERSION",
    "BOOTSTRAP_COMMAND_TIMEOUT_SECONDS",
    "BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION"
)
$environment = $configuration.Environment.Variables
foreach ($name in $requiredEnvironment) {
    $property = $environment.PSObject.Properties[$name]
    if ($null -eq $property -or [string]::IsNullOrWhiteSpace([string]$property.Value)) {
        throw "$FunctionName is missing required environment variable $name"
    }
}
if ($environment.BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION -ne "steam-auth-cache-v1") {
    throw "$FunctionName has incompatible BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION=$($environment.BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION)"
}
if ($null -ne $environment.PSObject.Properties["STEAM_SECRET_ID"]) {
    throw "$FunctionName still exposes retired STEAM_SECRET_ID configuration"
}

Write-Output "$FunctionName package and runtime configuration match the local bootstrap worker release."

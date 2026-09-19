[CmdletBinding()]
param(
    [string]$FunctionName = "",
    [ValidateSet("bootstrap", "artifact", "sleepwake", "archive", "restore", "reliability", "termination")]
    [string]$Worker = "bootstrap",
    [string]$ArchivePath = "",
    [string]$Profile = "game-server-dev",
    [string]$Region = "us-west-2"
)

$ErrorActionPreference = "Stop"
$repositoryRoot = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($FunctionName)) {
    $FunctionName = "game-server-platform-dev-$Worker-worker"
}
if ([string]::IsNullOrWhiteSpace($ArchivePath)) {
    $ArchivePath = Join-Path $repositoryRoot "dist/$Worker-worker.zip"
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
    "SESSION_ASSETS_BUCKET",
    "PROJECT_NAME"
)
if ($Worker -ne "reliability") {
    $requiredEnvironment += "NOTIFICATION_QUEUE_URL"
}
if ($Worker -ne "termination") {
    $requiredEnvironment += @("BOOTSTRAP_SCRIPT_KEY", "BOOTSTRAP_SCRIPT_SHA256", "BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION")
}
if ($Worker -eq "bootstrap") {
    $requiredEnvironment += @("STEAM_AUTH_SECRET_ID", "TEAMSPEAK_VERSION", "BOOTSTRAP_COMMAND_TIMEOUT_SECONDS")
}
$environment = $configuration.Environment.Variables
foreach ($name in $requiredEnvironment) {
    $property = $environment.PSObject.Properties[$name]
    if ($null -eq $property -or [string]::IsNullOrWhiteSpace([string]$property.Value)) {
        throw "$FunctionName is missing required environment variable $name"
    }
}
if ($Worker -ne "termination" -and $environment.BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION -ne "scoped-host-access-v1") {
    throw "$FunctionName has incompatible BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION=$($environment.BOOTSTRAP_RUNTIME_CONFIGURATION_VERSION)"
}
if ($Worker -ne "termination") {
    $scriptText = [IO.File]::ReadAllText((Join-Path $repositoryRoot "deploy/bootstrap/arma3-bootstrap.sh")).Replace("`r`n", "`n")
    $scriptHasher = [Security.Cryptography.SHA256]::Create()
    try {
        $scriptHash = ([BitConverter]::ToString($scriptHasher.ComputeHash([Text.Encoding]::UTF8.GetBytes($scriptText)))).Replace("-", "").ToLowerInvariant()
    }
    finally { $scriptHasher.Dispose() }
    if ($environment.BOOTSTRAP_SCRIPT_SHA256 -ne $scriptHash -or $environment.BOOTSTRAP_SCRIPT_KEY -ne "platform/bootstrap/arma3-$($scriptHash.Substring(0, 16)).sh") {
        throw "$FunctionName bootstrap script identity does not match the normalized local release."
    }
}
if ($null -ne $environment.PSObject.Properties["STEAM_SECRET_ID"]) {
    throw "$FunctionName still exposes retired STEAM_SECRET_ID configuration"
}

Write-Output "$FunctionName package and runtime configuration match the local $Worker worker release."

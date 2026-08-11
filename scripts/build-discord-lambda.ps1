[CmdletBinding()]
param(
    [string]$OutputPath = "dist/discord-interactions.zip"
)

$ErrorActionPreference = "Stop"

$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
if ([System.IO.Path]::IsPathRooted($OutputPath)) {
    $PackagePath = [System.IO.Path]::GetFullPath($OutputPath)
}
else {
    $PackagePath = [System.IO.Path]::GetFullPath(
        (Join-Path $RepositoryRoot $OutputPath)
    )
}

$PackageDirectory = Split-Path -Parent $PackagePath
$BootstrapPath = Join-Path $PackageDirectory "bootstrap"
New-Item -ItemType Directory -Force -Path $PackageDirectory | Out-Null

$GoPath = (& go env GOPATH).Trim()
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($GoPath)) {
    throw "Unable to determine GOPATH."
}

$ZipTool = Join-Path $GoPath "bin/build-lambda-zip.exe"
if (-not (Test-Path $ZipTool)) {
    Write-Host "Installing build-lambda-zip v1.54.0..."
    & go install github.com/aws/aws-lambda-go/cmd/build-lambda-zip@v1.54.0
    if ($LASTEXITCODE -ne 0) {
        throw "Installing build-lambda-zip failed."
    }
}

$PreviousEnvironment = @{
    GOOS        = $env:GOOS
    GOARCH      = $env:GOARCH
    CGO_ENABLED = $env:CGO_ENABLED
}

Push-Location $RepositoryRoot
try {
    $env:GOOS = "linux"
    $env:GOARCH = "arm64"
    $env:CGO_ENABLED = "0"

    Remove-Item -Force -ErrorAction SilentlyContinue $BootstrapPath
    Remove-Item -Force -ErrorAction SilentlyContinue $PackagePath

    & go build `
        -trimpath `
        -tags lambda.norpc `
        -ldflags "-s -w" `
        -o $BootstrapPath `
        ./cmd/discord-lambda
    if ($LASTEXITCODE -ne 0) {
        throw "Building the Discord interaction Lambda failed."
    }

    & $ZipTool -o $PackagePath $BootstrapPath
    if ($LASTEXITCODE -ne 0) {
        throw "Packaging the Discord interaction Lambda failed."
    }

    $Package = Get-Item $PackagePath
    Write-Host "Created $($Package.FullName) ($($Package.Length) bytes)."
}
finally {
    Remove-Item -Force -ErrorAction SilentlyContinue $BootstrapPath

    foreach ($Name in $PreviousEnvironment.Keys) {
        $Value = $PreviousEnvironment[$Name]
        if ($null -eq $Value) {
            Remove-Item "Env:$Name" -ErrorAction SilentlyContinue
        }
        else {
            Set-Item "Env:$Name" $Value
        }
    }

    Pop-Location
}

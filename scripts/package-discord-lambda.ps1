$ErrorActionPreference = "Stop"

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$distDirectory = Join-Path $repositoryRoot "dist"
$cacheDirectory = Join-Path $repositoryRoot ".cache"
$bootstrapPath = Join-Path $distDirectory "bootstrap"
$packagerFilename = if ([System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT) { "package-lambda.exe" } else { "package-lambda" }
$packagerPath = Join-Path $cacheDirectory $packagerFilename
$packages = @(
    @{ Command = "./cmd/discord-lambda"; Archive = "discord-interactions.zip" },
    @{ Command = "./cmd/artifact-worker"; Archive = "artifact-worker.zip" },
    @{ Command = "./cmd/notification-worker"; Archive = "notification-worker.zip" },
    @{ Command = "./cmd/command-worker"; Archive = "command-worker.zip" },
    @{ Command = "./cmd/provisioning-worker"; Archive = "provisioning-worker.zip" },
    @{ Command = "./cmd/bootstrap-worker"; Archive = "bootstrap-worker.zip" },
    @{ Command = "./cmd/monitor-worker"; Archive = "monitor-worker.zip" },
    @{ Command = "./cmd/sleepwake-worker"; Archive = "sleepwake-worker.zip" },
    @{ Command = "./cmd/archive-worker"; Archive = "archive-worker.zip" }
    @{ Command = "./cmd/restore-worker"; Archive = "restore-worker.zip" }
    @{ Command = "./cmd/termination-worker"; Archive = "termination-worker.zip" }
    @{ Command = "./cmd/reliability-worker"; Archive = "reliability-worker.zip" }
)

New-Item -ItemType Directory -Force -Path $distDirectory | Out-Null
New-Item -ItemType Directory -Force -Path $cacheDirectory | Out-Null

$previousGOOS = $env:GOOS
$previousGOARCH = $env:GOARCH
$previousCGOEnabled = $env:CGO_ENABLED
$previousGOCACHE = $env:GOCACHE

try {
    $env:GOCACHE = Join-Path $cacheDirectory "go-build"
    go build -buildvcs=false -trimpath -o $packagerPath ./cmd/package-lambda
    if ($LASTEXITCODE -ne 0) { throw "packaging tool build failed" }

    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    $env:CGO_ENABLED = "0"
    foreach ($package in $packages) {
        $archivePath = Join-Path $distDirectory $package.Archive
        go build -buildvcs=false -tags lambda.norpc -trimpath -ldflags "-s -w" -o $bootstrapPath $package.Command
        if ($LASTEXITCODE -ne 0) { throw "Go build failed for $($package.Command)" }

        & $packagerPath -source $bootstrapPath -output $archivePath
        if ($LASTEXITCODE -ne 0) { throw "Lambda packaging failed for $($package.Command)" }
        Write-Output $archivePath
    }
}
finally {
    $env:GOOS = $previousGOOS
    $env:GOARCH = $previousGOARCH
    $env:CGO_ENABLED = $previousCGOEnabled
    $env:GOCACHE = $previousGOCACHE
    Remove-Item -LiteralPath $bootstrapPath -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $packagerPath -Force -ErrorAction SilentlyContinue
}

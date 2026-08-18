[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('Enroll', 'Status', 'Rollback', 'Invalidate')]
    [string]$Action,

    [Parameter(Mandatory = $true)]
    [string]$SecretId,

    [Parameter(Mandatory = $true)]
    [string]$MetadataTableName,

    [Parameter(Mandatory = $true)]
    [string]$Region,

    [string]$Profile,
    [string]$Username,
    [string]$ConfigVdfPath
)

$ErrorActionPreference = 'Stop'
$script:LockOwner = "operator:$([Environment]::UserName):$([guid]::NewGuid().ToString('N'))"
$script:LockHeld = $false

function Invoke-AwsJson {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)
    $base = @('--region', $Region, '--output', 'json', '--no-cli-pager')
    if ($Profile) { $base += @('--profile', $Profile) }
    $output = & aws @base @Arguments
    if ($LASTEXITCODE -ne 0) { throw "AWS CLI operation failed with exit code $LASTEXITCODE." }
    if ([string]::IsNullOrWhiteSpace(($output -join ''))) { return $null }
    return (($output -join "`n") | ConvertFrom-Json)
}

function ConvertTo-CompactJson {
    param([Parameter(Mandatory = $true)]$Value)
    return ($Value | ConvertTo-Json -Compress -Depth 10)
}

function Get-SteamAuthKey {
    return (ConvertTo-CompactJson @{ pk = @{ S = 'STEAM_AUTH#CACHE' }; sk = @{ S = 'STATE' } })
}

function Assert-EnrollmentRole {
    $identity = Invoke-AwsJson @('sts', 'get-caller-identity')
    if ($identity.Arn -notmatch ':assumed-role/.+-steam-auth-enrollment/') {
        throw 'Refusing Steam authorization mutation: assume the MFA-gated steam-auth-enrollment role for a 900-second session first.'
    }
}

function Acquire-SteamAuthLock {
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $values = ConvertTo-CompactJson @{
        ':owner'   = @{ S = $script:LockOwner }
        ':now'     = @{ N = [string]$now }
        ':expires' = @{ N = [string]($now + 900) }
    }
    [void](Invoke-AwsJson @(
        'dynamodb', 'update-item', '--table-name', $MetadataTableName,
        '--key', (Get-SteamAuthKey),
        '--update-expression', 'SET lease_owner = :owner, lease_expires_at = :expires',
        '--condition-expression', 'attribute_not_exists(lease_owner) OR lease_expires_at < :now OR lease_owner = :owner',
        '--expression-attribute-values', $values
    ))
    $script:LockHeld = $true
}

function Release-SteamAuthLock {
    if (-not $script:LockHeld) { return }
    $values = ConvertTo-CompactJson @{ ':owner' = @{ S = $script:LockOwner } }
    try {
        [void](Invoke-AwsJson @(
            'dynamodb', 'update-item', '--table-name', $MetadataTableName,
            '--key', (Get-SteamAuthKey),
            '--update-expression', 'REMOVE lease_owner, lease_expires_at',
            '--condition-expression', 'lease_owner = :owner',
            '--expression-attribute-values', $values
        ))
    } catch {
        Write-Warning 'The enrollment lease could not be released; it expires automatically after 15 minutes.'
    } finally {
        $script:LockHeld = $false
    }
}

function Set-SteamAuthState {
    param(
        [Parameter(Mandatory = $true)][ValidateSet('ACTIVE', 'REAUTH_REQUIRED')][string]$Status,
        [string]$VersionId,
        [string]$SHA256
    )
    $now = [DateTimeOffset]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    $values = @{
        ':owner'  = @{ S = $script:LockOwner }
        ':status' = @{ S = $Status }
        ':now'    = @{ S = $now }
    }
    $expression = 'SET #status = :status, updated_at = :now'
    if ($VersionId) { $values[':version'] = @{ S = $VersionId }; $expression += ', current_version_id = :version' }
    if ($SHA256) { $values[':sha'] = @{ S = $SHA256 }; $expression += ', config_sha256 = :sha' }
    if ($Status -eq 'REAUTH_REQUIRED') {
        $values[':code'] = @{ S = 'ERR_STEAM_REAUTH_REQUIRED' }
        $expression += ', last_error_code = :code'
    } else {
        $remove = @('last_error_code')
        if (-not $SHA256) { $remove += 'config_sha256' }
        $expression += ' REMOVE ' + ($remove -join ', ')
    }
    [void](Invoke-AwsJson @(
        'dynamodb', 'update-item', '--table-name', $MetadataTableName,
        '--key', (Get-SteamAuthKey), '--update-expression', $expression,
        '--condition-expression', 'lease_owner = :owner',
        '--expression-attribute-names', '{"#status":"status"}',
        '--expression-attribute-values', (ConvertTo-CompactJson $values)
    ))
}

function Invoke-Enroll {
    if ([string]::IsNullOrWhiteSpace($Username) -or [string]::IsNullOrWhiteSpace($ConfigVdfPath)) {
        throw 'Enroll requires -Username and -ConfigVdfPath.'
    }
    if ($Username.Length -gt 64 -or $Username -match '[\x00-\x20"\\]') {
        throw 'The Steam account name contains unsupported characters.'
    }
    $resolved = (Resolve-Path -LiteralPath $ConfigVdfPath).Path
    $bytes = [IO.File]::ReadAllBytes($resolved)
    if ($bytes.Length -lt 1 -or $bytes.Length -gt 524288) { throw 'config.vdf must be between 1 byte and 512 KiB.' }
    $sha = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($bytes)).ToLowerInvariant()
    $now = [DateTimeOffset]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    $payload = [ordered]@{
        schema_version    = 1
        cache_format      = 'steamcmd-config-vdf'
        status            = 'ACTIVE'
        username          = $Username
        config_vdf_base64 = [Convert]::ToBase64String($bytes)
        config_sha256     = $sha
        enrolled_at       = $now
        updated_at        = $now
        source_version_id = ''
    }
    [Array]::Clear($bytes, 0, $bytes.Length)
    $payloadPath = Join-Path ([IO.Path]::GetTempPath()) ("gsp-steam-auth-$([guid]::NewGuid().ToString('N')).json")
    try {
        [IO.File]::WriteAllText($payloadPath, (ConvertTo-CompactJson $payload), [Text.UTF8Encoding]::new($false))
        Acquire-SteamAuthLock
        $fileUri = 'file://' + ($payloadPath -replace '\\', '/')
        $result = Invoke-AwsJson @(
            'secretsmanager', 'put-secret-value', '--secret-id', $SecretId,
            '--client-request-token', ([guid]::NewGuid().ToString()), '--secret-string', $fileUri
        )
        Set-SteamAuthState -Status ACTIVE -VersionId $result.VersionId -SHA256 $sha
        [PSCustomObject]@{ Status = 'ACTIVE'; VersionId = $result.VersionId; ConfigSHA256 = $sha; EnrolledAt = $now }
    } finally {
        Release-SteamAuthLock
        if (Test-Path -LiteralPath $payloadPath) { Remove-Item -LiteralPath $payloadPath -Force }
        $payload.config_vdf_base64 = ''
    }
}

function Invoke-Status {
    $state = Invoke-AwsJson @('dynamodb', 'get-item', '--table-name', $MetadataTableName, '--key', (Get-SteamAuthKey), '--consistent-read')
    $secret = Invoke-AwsJson @('secretsmanager', 'describe-secret', '--secret-id', $SecretId)
    $current = $null
    foreach ($property in $secret.VersionIdsToStages.PSObject.Properties) {
        if ($property.Value -contains 'AWSCURRENT') { $current = $property.Name; break }
    }
    [PSCustomObject]@{
        Status           = $state.Item.status.S
        CurrentVersionId = $current
        StateVersionId   = $state.Item.current_version_id.S
        UpdatedAt        = $state.Item.updated_at.S
        LastErrorCode    = $state.Item.last_error_code.S
        LeaseExpiresAt   = $state.Item.lease_expires_at.N
    }
}

function Invoke-Rollback {
    Acquire-SteamAuthLock
    try {
        $secret = Invoke-AwsJson @('secretsmanager', 'describe-secret', '--secret-id', $SecretId)
        $current = $null; $previous = $null
        foreach ($property in $secret.VersionIdsToStages.PSObject.Properties) {
            if ($property.Value -contains 'AWSCURRENT') { $current = $property.Name }
            if ($property.Value -contains 'AWSPREVIOUS') { $previous = $property.Name }
        }
        if (-not $current -or -not $previous) { throw 'Both AWSCURRENT and AWSPREVIOUS are required for rollback.' }
        [void](Invoke-AwsJson @(
            'secretsmanager', 'update-secret-version-stage', '--secret-id', $SecretId,
            '--version-stage', 'AWSCURRENT', '--move-to-version-id', $previous, '--remove-from-version-id', $current
        ))
        Set-SteamAuthState -Status ACTIVE -VersionId $previous
        [PSCustomObject]@{ Status = 'ACTIVE'; VersionId = $previous; RolledBackFrom = $current }
    } finally {
        Release-SteamAuthLock
    }
}

function Invoke-Invalidate {
    Acquire-SteamAuthLock
    try {
        Set-SteamAuthState -Status REAUTH_REQUIRED
        [PSCustomObject]@{ Status = 'REAUTH_REQUIRED' }
    } finally {
        Release-SteamAuthLock
    }
}

Assert-EnrollmentRole
switch ($Action) {
    'Enroll'     { Invoke-Enroll }
    'Status'     { Invoke-Status }
    'Rollback'   { Invoke-Rollback }
    'Invalidate' { Invoke-Invalidate }
}

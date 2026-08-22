[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$ApplicationId,

    [Parameter(Mandatory)]
    [string]$GuildId,

    [string]$CommandFile = "deploy/discord/rb-command.json"
)

$ErrorActionPreference = "Stop"
$RepositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path

if (-not [System.IO.Path]::IsPathRooted($CommandFile)) {
    $CommandFile = Join-Path $RepositoryRoot $CommandFile
}
$CommandFile = (Resolve-Path $CommandFile).Path

$Token = $env:DISCORD_BOT_TOKEN
$TokenPointer = [IntPtr]::Zero
try {
    if ([string]::IsNullOrWhiteSpace($Token)) {
        $SecureToken = Read-Host "Discord bot token" -AsSecureString
        $TokenPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR(
            $SecureToken
        )
        $Token = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($TokenPointer)
    }

    if ([string]::IsNullOrWhiteSpace($Token)) {
        throw "A Discord bot token is required."
    }

	$Command = Get-Content -Raw -Path $CommandFile | ConvertFrom-Json
	[object[]]$Commands = @($Command)
	$Body = ConvertTo-Json -InputObject $Commands -Depth 100 -Compress

    $Uri = "https://discord.com/api/v10/applications/$ApplicationId/guilds/$GuildId/commands"
    $UserAgent = "DiscordBot (https://github.com/L-McKendrick/game-server-platform, 0.1.0)"

	$Response = Invoke-RestMethod `
		-Method Put `
        -Uri $Uri `
        -Headers @{ Authorization = "Bot $Token" } `
        -UserAgent $UserAgent `
        -ContentType "application/json" `
        -Body $Body

	$Response | ForEach-Object {
		[pscustomobject]@{
			Id          = $_.id
			Name        = $_.name
			Description = $_.description
			Version     = $_.version
		}
	} | Format-List
}
finally {
    $Token = $null
    if ($TokenPointer -ne [IntPtr]::Zero) {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($TokenPointer)
    }
}

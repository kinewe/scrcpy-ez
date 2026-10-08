# Windows release packaging: fresh profile, complete runtime and hash manifests.
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v?[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?$')]
    [string]$Version,
    [Parameter(Mandatory = $true)][string]$Dist,
    [Parameter(Mandatory = $true)][string]$OutputDir
)
$ErrorActionPreference = 'Stop'
$Dist = (Resolve-Path -LiteralPath $Dist).Path
$OutputDir = (Resolve-Path -LiteralPath $OutputDir).Path
$ZipName = "scrcpy-ez-$Version.zip"
$ZipPath = Join-Path $OutputDir $ZipName
if (Test-Path -LiteralPath $ZipPath) { throw 'Preserve the existing release archive.' }
$tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\', '/')
$Stage = Join-Path $tempRoot ('scrcpy-ez-stage-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $Stage | Out-Null

function Test-Excluded([string]$Name) {
    if ($Name -in @('config.txt', 'config.identity.txt', 'profiles.json', 'settings.json', 'root-repair.json', 'scrcpy_frame_log.txt', 'SHA256SUMS.txt', 'FILE_SHA256SUMS.txt')) { return $true }
    if ($Name -match '\.bak' -or $Name -match '\.(zip|old|log|lnk|url|ico|vbs|ps1|py|pem|key|env|pdb)$') { return $true }
    if ($Name -like 'README-ez*' -or $Name -like 'open_a_terminal_here*' -or $Name -like 'TSF*' -or $Name -like 'scrcpy-ez-gui*') { return $true }
    return $false
}

try {
    Get-ChildItem -LiteralPath $Dist -Force -File |
        Where-Object { -not (Test-Excluded $_.Name) } |
        ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination $Stage }
    [IO.File]::WriteAllText((Join-Path $Stage 'profiles.json'),
        '{"devices": {}, "deviceOrder": []}', [Text.UTF8Encoding]::new($false))
    $rows = Get-ChildItem -LiteralPath $Stage -File | Sort-Object Name | ForEach-Object {
        '{0}  {1}' -f (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash, $_.Name
    }
    [IO.File]::WriteAllText((Join-Path $Stage 'FILE_SHA256SUMS.txt'),
        (($rows -join "`n") + "`n"), [Text.UTF8Encoding]::new($false))
    Push-Location -LiteralPath $Stage
    try {
        # Use *, never *.*, so extensionless Android servers are included.
        Compress-Archive -Path * -DestinationPath $ZipPath
    } finally { Pop-Location }
    $zipHash = (Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256).Hash
    [IO.File]::WriteAllText((Join-Path $OutputDir 'SHA256SUMS.txt'),
        "$zipHash  $ZipName`n", [Text.UTF8Encoding]::new($false))
    "Created $ZipName with a fresh profile and file/archive SHA256 manifests."
} finally {
    # Validate the absolute generated path before any recursive cleanup.
    $resolvedStage = [IO.Path]::GetFullPath($Stage)
    $parent = [IO.Path]::GetDirectoryName($resolvedStage).TrimEnd('\', '/')
    if ($parent -ne $tempRoot -or [IO.Path]::GetFileName($resolvedStage) -notmatch '^scrcpy-ez-stage-[a-f0-9]{32}$') {
        throw 'Refusing cleanup outside the generated staging directory.'
    }
    if (Test-Path -LiteralPath $resolvedStage) { Remove-Item -LiteralPath $resolvedStage -Recurse -Force }
}

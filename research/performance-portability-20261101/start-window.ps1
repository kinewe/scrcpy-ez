param([Parameter(Mandatory=$true)][string]$ScopePackage,[Parameter(Mandatory=$true)][string]$ScopeSerial,[Parameter(Mandatory=$true)][string]$ScopeLabel,[switch]$PerspectiveMode)
if ($ScopePackage -notmatch '^[a-zA-Z0-9_.]+$' -or $ScopeLabel -notmatch '^[a-zA-Z0-9-]+$') { throw 'Invalid research label/package' }
if (-not $env:ADB) { $env:ADB=(Get-Command adb -ErrorAction Stop).Source }
if (-not $env:RC25_RESEARCH_ADB_PORT) { $env:RC25_RESEARCH_ADB_PORT='5037' }
if ($env:RC25_RESEARCH_ADB_PORT -notmatch '^\d+$' -or [int]$env:RC25_RESEARCH_ADB_PORT -lt 1 -or [int]$env:RC25_RESEARCH_ADB_PORT -gt 65535) { throw 'Invalid ADB port' }
$env:ADB_SERVER_SOCKET="tcp:localhost:$env:RC25_RESEARCH_ADB_PORT"
if (-not $env:SCRCPY_EZ_RUNTIME) { throw 'Set SCRCPY_EZ_RUNTIME' }
$runtime=(Resolve-Path -LiteralPath $env:SCRCPY_EZ_RUNTIME).Path
$extra=@()
if ($PerspectiveMode) { $extra=@('--perspective-camera','--perspective-input-fps=1') }
& "$runtime\scrcpy.exe" -s $ScopeSerial --new-display=1440x3200/600 "--start-app=$ScopePackage" --no-audio --max-fps=30 --video-bit-rate=12M --window-width=520 --window-height=1156 "--window-title=RC25 Perf - $ScopeLabel" @extra 2>&1 | Tee-Object -LiteralPath "$PSScriptRoot\out\$ScopeLabel-window.log"

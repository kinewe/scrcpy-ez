param([Parameter(Mandatory=$true)][string]$Package,[Parameter(Mandatory=$true)][string]$Serial,[Parameter(Mandatory=$true)][string]$Label)
if ($Package -notmatch '^[a-zA-Z0-9_.]+$' -or $Label -notmatch '^[a-zA-Z0-9-]+$') { throw 'Invalid package/label' }
if (-not $env:SCRCPY_EZ_BUILD_DIR) { throw 'Set SCRCPY_EZ_BUILD_DIR' }
$build=(Resolve-Path -LiteralPath $env:SCRCPY_EZ_BUILD_DIR).Path
if (-not $env:SCRCPY_EZ_RUNTIME) { throw 'Set SCRCPY_EZ_RUNTIME' }
$runtime=(Resolve-Path -LiteralPath $env:SCRCPY_EZ_RUNTIME).Path
$env:ADB=Join-Path $runtime 'adb.exe'
if (-not $env:RC25_RESEARCH_ADB_PORT) { $env:RC25_RESEARCH_ADB_PORT='5037' }
if ($env:RC25_RESEARCH_ADB_PORT -notmatch '^\d+$' -or [int]$env:RC25_RESEARCH_ADB_PORT -lt 1 -or [int]$env:RC25_RESEARCH_ADB_PORT -gt 65535) { throw 'Invalid ADB port' }
$env:ADB_SERVER_SOCKET="tcp:localhost:$env:RC25_RESEARCH_ADB_PORT"
$env:SCRCPY_SERVER_PATH=Join-Path $build 'scrcpy-server'
$env:PATH="$runtime;$env:PATH"
& "$build\scrcpy.exe" -s $Serial --new-display=1440x3200/600 "--start-app=$Package" --no-audio --max-fps=30 --video-bit-rate=12M --window-width=520 --window-height=1156 "--window-title=RC25 Fix - $Label" --perspective-camera --perspective-input-fps=1 2>&1 | Tee-Object -LiteralPath "$PSScriptRoot\out\$Label.log"

@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"
rem Parent: one event supervisor owns this casting session.
if not "%SCEZ_EVENT_CHILD%"=="1" (
    if not defined SCEZ_EVENT_HELPER set "SCEZ_EVENT_HELPER=%~dp0scrcpy-ez.exe"
    if not exist "!SCEZ_EVENT_HELPER!" (
        echo [错误] 缺少事件监督器 scrcpy-ez.exe，请使用完整发行包。
        exit /b 1
    )
    "!SCEZ_EVENT_HELPER!" --event-cast "%~f0" %*
    exit /b !ERRORLEVEL!
)
rem Child: exactly one already-verified transport, exactly one scrcpy run.
if not defined SCEZ_EVENT_ROUTE exit /b 1
set "WATCH_TAG=%SCEZ_WATCH_TAG%"
set "PICK=%SCEZ_EVENT_ROUTE%"
set "USB_DEV=%SCEZ_EVENT_ROUTE%"
rem The supervisor knows the actual transport even when ADB uses an mDNS name.
set "ROUTE_WIRELESS=0"
if "%SCEZ_EVENT_KIND%"=="wifi" set "ROUTE_WIRELESS=1"
if not defined SCEZ_EVENT_KIND (
    echo !PICK! | findstr /i /c:":" /c:"._adb-tls-connect." /c:"._adb._tcp" >nul 2>&1
    if not errorlevel 1 set "ROUTE_WIRELESS=1"
)
set "SCRCPY_SERVER_PATH=%~dp0scrcpy-server"
set "ADB=adb"
if exist "%~dp0adb.exe" set "ADB=%~dp0adb.exe"
set "SCRIPT_DIR=%~dp0"

rem ----- 串流参数：有线 USB 带宽充足用高规格；无线带宽有限保持低延迟 -----
rem 键盘模式默认 uhid；:detect_keyboard 会按 Android SDK 覆盖，读不到时保持 uhid
set "KEYBOARD=uhid"
set "LEGACY_DEVICE="
rem 有线规格：动态分配（:detect_display_spec 读取设备分辨率/刷新率后覆盖），此值为读取失败时的保底默认
rem v2.1.91 编码格式（两套；默认 h264 与 opus；GUI 注入 SCEZ_VCODEC_*/SCEZ_ACODEC_* 覆盖）
set "VCODEC_USB=h264"
set "VCODEC_WIFI=h264"
set "ACODEC_USB=opus"
set "ACODEC_WIFI=opus"
if defined SCEZ_VCODEC_USB set "VCODEC_USB=!SCEZ_VCODEC_USB!"
if defined SCEZ_ACODEC_USB set "ACODEC_USB=!SCEZ_ACODEC_USB!"
if defined SCEZ_VCODEC_WIFI set "VCODEC_WIFI=!SCEZ_VCODEC_WIFI!"
if defined SCEZ_ACODEC_WIFI set "ACODEC_WIFI=!SCEZ_ACODEC_WIFI!"
set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_USB! --audio-codec=!ACODEC_USB! --video-bit-rate=50M --max-size 2560 --max-fps 120 --video-codec-options="max-b-frames:int=0,bitrate-mode:int=1" --video-buffer=0"
set "WIFI_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_WIFI! --audio-codec=!ACODEC_WIFI! --video-bit-rate=15M --max-size 1920 --max-fps 60"


goto :do_cast

:detect_keyboard
set "KEYBOARD=uhid"
set "LEGACY_DEVICE="
set "SDK_VER="
set "SDK_IS_NUM="
rem 命令必须把 !ADB! 用引号包住：路径含空格，未加引号时 FOR /F 只会执行到第一个空格
for /f "tokens=1" %%v in ('"!ADB!" -s !PICK! shell getprop ro.build.version.sdk 2^>nul') do if not defined SDK_VER set "SDK_VER=%%v"
rem 只信任纯数字版本号：getprop 失败/异常输出时保持默认 uhid，高版本零污染
if defined SDK_VER for /f "delims=0123456789" %%d in ("!SDK_VER!") do if not "%%d"=="" set "SDK_IS_NUM=1"
if not defined SDK_IS_NUM if defined SDK_VER (
    if !SDK_VER! lss 33 set "KEYBOARD=sdk"
    if !SDK_VER! lss 29 set "LEGACY_DEVICE=1"
)
rem SDK 键盘走 adb 注入，USB/无线全版本可用，是 Windows 下老设备的唯一通用回退
echo [键盘模式] Android SDK=!SDK_VER! -^> !KEYBOARD! legacy=!LEGACY_DEVICE!
exit /b

rem ============================================
rem   设备市场名检测：优先 ro.product.marketname，
rem   空/失败回退 厂商+型号；再失败 DEV_LABEL 保持序列号。
rem   同一设备只查询一次（serial 缓存），投屏循环/重连不重复 getprop
rem ============================================
:detect_market_name
if defined MARKET_SERIAL if "!MARKET_SERIAL!"=="!PICK!" exit /b 0
set "MARKET_SERIAL=!PICK!"
set "MARKET_NAME="
set "MKT_MOD="
set "DEV_LABEL=!PICK!"
for /f "delims=" %%m in ('"!ADB!" -s !PICK! shell getprop ro.product.marketname 2^>nul') do set "MARKET_NAME=%%m"
rem 型号一并查询（防抢比对 PIN_MOD 用：市场名读不到时仍有稳定身份）
for /f "delims=" %%m in ('"!ADB!" -s !PICK! shell getprop ro.product.model 2^>nul') do set "MKT_MOD=%%m"
if defined MARKET_NAME goto :market_name_ok
set "MKT_MAN="
for /f "delims=" %%m in ('"!ADB!" -s !PICK! shell getprop ro.product.manufacturer 2^>nul') do set "MKT_MAN=%%m"
if defined MKT_MAN if defined MKT_MOD set "MARKET_NAME=!MKT_MAN! !MKT_MOD!"
:market_name_ok
if defined MARKET_NAME set "DEV_LABEL=!MARKET_NAME!（!PICK!）"
exit /b 0

:detect_display_spec
set "DEV_RES="
set "DEV_W="
set "DEV_H="
for /f "tokens=3 delims=: " %%a in ('"!ADB!" -s !USB_DEV! shell wm size 2^>nul') do set "DEV_RES=%%a"
if defined DEV_RES (
    for /f "tokens=1,2 delims=x" %%a in ("!DEV_RES!") do (
        set "DEV_W=%%a"
        set "DEV_H=%%b"
    )
)
set "DEV_FPS="
for /f "delims=" %%a in ('"!ADB!" -s !USB_DEV! shell settings get system peak_refresh_rate 2^>nul') do set "DEV_FPS=%%a"
if "!DEV_FPS!"=="null" set "DEV_FPS="
if not defined DEV_FPS set "DEV_FPS=60"
for /f "tokens=1 delims=." %%a in ("!DEV_FPS!") do set "DEV_FPS=%%a"
if not defined DEV_H (
    rem 读取/解析失败：回退默认有线规格；老设备用兼容档
    if defined LEGACY_DEVICE (
        set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_USB! --audio-codec=!ACODEC_USB! --video-bit-rate=6M --max-size 720 --max-fps 24 --video-buffer=0"
        set "SPEC_INFO=兼容模式 !VCODEC_USB!/6M/720/24fps（老设备）"
    ) else (
        set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_USB! --audio-codec=!ACODEC_USB! --video-bit-rate=50M --max-size 2560 --max-fps 120 --video-codec-options="max-b-frames:int=0,bitrate-mode:int=1" --video-buffer=0"
        set "SPEC_INFO=设备规格读取失败，使用默认规格 !VCODEC_USB!/50M/2560/120fps"
    )
    exit /b 0
)
rem 计算：长边 -> max-size（上限 2560）；fps 上限 120；bit-rate 按 分辨率×刷新率 分级估算（clamp 15..80M）
if !DEV_W! GEQ !DEV_H! (set "DEV_LONG=!DEV_W!") else (set "DEV_LONG=!DEV_H!")
set "MAX_SIZE=!DEV_LONG!"
if !DEV_LONG! GTR 2560 set "MAX_SIZE=2560"
if !DEV_FPS! GTR 120 set "DEV_FPS=120"
set /a "BR=!DEV_W!*!DEV_H!/2073600"
if !BR! LSS 1 set "BR=1"
set /a "BR=!BR!*!DEV_FPS!/60"
if !BR! LSS 1 set "BR=1"
set /a "BR=!BR!*15"
if !BR! LSS 15 set "BR=15"
if !BR! GTR 80 set "BR=80"
set "USB_BITRATE=!BR!M"
rem 键盘模式已在 do_cast 开头统一检测，这里复用结果（避免重复输出 [键盘模式]）
if defined LEGACY_DEVICE (
    if !SDK_VER! lss 29 (
        set "MAX_SIZE=720"
        set "DEV_FPS=24"
        set "USB_BITRATE=6M"
        set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_USB! --audio-codec=!ACODEC_USB! --video-bit-rate=!USB_BITRATE! --max-size !MAX_SIZE! --max-fps !DEV_FPS! --video-buffer=0"
        set "SPEC_INFO=兼容模式 !VCODEC_USB!/6M/720/24fps（Android 8 及更早设备）"
    ) else (
        if !DEV_FPS! GTR 30 set "DEV_FPS=30"
        set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_USB! --audio-codec=!ACODEC_USB! --video-bit-rate=!USB_BITRATE! --max-size !MAX_SIZE! --max-fps !DEV_FPS! --video-codec-options="max-b-frames:int=0,bitrate-mode:int=1" --video-buffer=0"
        set "SPEC_INFO=检测到设备 !DEV_W!x!DEV_H!@!DEV_FPS!Hz，有线规格 !VCODEC_USB!/!USB_BITRATE!/!MAX_SIZE!/!DEV_FPS!fps（老设备限 30fps）"
    )
) else (
    set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_USB! --audio-codec=!ACODEC_USB! --video-bit-rate=!USB_BITRATE! --max-size !MAX_SIZE! --max-fps !DEV_FPS! --video-codec-options="max-b-frames:int=0,bitrate-mode:int=1" --video-buffer=0"
    set "SPEC_INFO=检测到设备 !DEV_W!x!DEV_H!@!DEV_FPS!Hz，有线规格 !VCODEC_USB!/!USB_BITRATE!/!MAX_SIZE!/!DEV_FPS!fps"
)
exit /b 0

rem ============================================
rem   投屏循环（自动切换投屏模式）
rem   scrcpy 退出后自动重新检测设备：
rem   拔线 -> 自动切无线；插线 -> 自动切有线
rem ============================================
:do_cast
rem clear orphan watcher-switch flag left by a previous dying cast (stale-flag misread fix)
rem ---- resurrect guard: user closed this session -> exit, never reconnect ----
if exist "%TEMP%\scrcpy_closed_!WATCH_TAG!.flag" exit /b 0
rem 首次投屏推送当前电脑剪贴板（启动推送）；模式切换/重连（再次进入 do_cast）
rem 时传 --no-clipboard-push-on-start，避免手机剪贴板被相同内容反复占满
if not defined FIRST_CAST_DONE (
    set "FIRST_CAST_DONE=1"
    set "CLIP_START_PUSH="
) else (
    set "CLIP_START_PUSH=--no-clipboard-push-on-start"
)
echo.
call :detect_market_name
echo ===== 开始投屏：!DEV_LABEL! =====
echo   提示：关闭窗口即断开；Alt+f 切换全屏
echo.
call :detect_keyboard
if defined LEGACY_DEVICE (
    if !SDK_VER! lss 29 (
        set "WIFI_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_WIFI! --audio-codec=!ACODEC_WIFI! --video-bit-rate=4M --max-size 720 --max-fps 24"
    ) else (
        set "WIFI_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_WIFI! --audio-codec=!ACODEC_WIFI! --video-bit-rate=8M --max-size 1080 --max-fps 30"
    )
) else (
    set "WIFI_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_WIFI! --audio-codec=!ACODEC_WIFI! --video-bit-rate=15M --max-size 1920 --max-fps 60"
)
set "AUDIO_MODE="
set "CAST_ARGS=!WIFI_ARGS!"
if "!ROUTE_WIRELESS!"=="1" (
    echo [自动切换] 无线投屏中：会话监督器正在订阅插拔线事件
    echo [流畅] 无线模式：带宽有限，已启用低延迟串流（!VCODEC_WIFI!/15M/1920/60fps，剪贴板自动同步（电脑复制即达手机））
    rem scrcpy-ez 参数浮窗覆盖（无线 SCEZ_*_WIFI）：未设置=原逻辑
    if defined SCEZ_RES_WIFI set "CAST_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_WIFI! --audio-codec=!ACODEC_WIFI! --video-bit-rate=!SCEZ_BITRATE_WIFI!M --max-size !SCEZ_RES_WIFI! --max-fps !SCEZ_FPS_WIFI!"
    if defined SCEZ_RES_WIFI echo [custom] wireless res=!SCEZ_RES_WIFI! fps=!SCEZ_FPS_WIFI! bitrate=!SCEZ_BITRATE_WIFI! ^(wifi^) codec=!VCODEC_WIFI! acodec=!ACODEC_WIFI!
    set "AUDIO_MODE=!SCEZ_AUDIO_WIFI!"
rem ABR 锁定（GUI 参数浮窗「锁定」）：锁定维度不被自适应调整
if defined SCEZ_LOCK_FPS_WIFI set "CAST_ARGS=!CAST_ARGS! --abr-lock-fps"
if defined SCEZ_LOCK_BITRATE_WIFI set "CAST_ARGS=!CAST_ARGS! --abr-lock-bitrate"
) else (
    rem 有线 USB 连接：动态读取设备分辨率/刷新率，按设备能力分配规格（读取失败回退默认）
    call :detect_display_spec
    set "CAST_ARGS=!USB_ARGS!"
    echo [高清] 有线模式：!SPEC_INFO!（低延迟优化，剪贴板自动同步（电脑复制即达手机））
    rem scrcpy-ez 参数浮窗覆盖（有线 SCEZ_*_USB）：未设置=原逻辑
    if defined SCEZ_RES_USB set "CAST_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC_USB! --audio-codec=!ACODEC_USB! --video-bit-rate=!SCEZ_BITRATE_USB!M --max-size !SCEZ_RES_USB! --max-fps !SCEZ_FPS_USB! --video-codec-options="max-b-frames:int=0,bitrate-mode:int=1" --video-buffer=0"
    if defined SCEZ_RES_USB echo [custom] wired res=!SCEZ_RES_USB! fps=!SCEZ_FPS_USB! bitrate=!SCEZ_BITRATE_USB! ^(usb^) codec=!VCODEC_USB! acodec=!ACODEC_USB!
    set "AUDIO_MODE=!SCEZ_AUDIO_USB!"
rem ABR 锁定（GUI 参数浮窗「锁定」）：锁定维度不被自适应调整
if defined SCEZ_LOCK_FPS_USB set "CAST_ARGS=!CAST_ARGS! --abr-lock-fps"
if defined SCEZ_LOCK_BITRATE_USB set "CAST_ARGS=!CAST_ARGS! --abr-lock-bitrate"
)
rem ----- gui57 虚拟屏（窗口 / 单应用工作台）参数：SCEZ_VD_* 未设置 = 完全不参与（零回归） -----
rem 虚拟屏会话不注入 --max-size（与 flex 语义冲突，实测会锁死窗口跟随）。
rem 两套参数（v2.1.47）：SCEZ_VD_*_USB / SCEZ_VD_*_WIFI 按当前连接形态各取一套（PICK 含":"=无线）；
rem 旧单套变量（SCEZ_VD_SIZE/DPI/FPS/BIT/FLEX/AUDIO）作为回退——新旧组合兼容。
set "VD_ARGS="
set "VD_ON=0"
if defined SCEZ_VD_SIZE set "VD_ON=1"
if defined SCEZ_VD_SIZE_USB set "VD_ON=1"
if defined SCEZ_VD_SIZE_WIFI set "VD_ON=1"
if "!VD_ON!"=="1" (
    set "VD_WIRE=0"
    if "!ROUTE_WIRELESS!"=="1" set "VD_WIRE=1"
    set "VD_SIZE="
    set "VD_DPI="
    set "VD_FPS="
    set "VD_BIT="
    set "VD_FLEX="
    set "VD_AUTO_DPI="
    set "VD_MATCH_PHONE="
    set "VD_MAX_SIZE="
    set "VD_AUDIO="
    if "!VD_WIRE!"=="0" (
        set "VD_SIZE=!SCEZ_VD_SIZE_USB!"
        set "VD_DPI=!SCEZ_VD_DPI_USB!"
        set "VD_FPS=!SCEZ_VD_FPS_USB!"
        set "VD_BIT=!SCEZ_VD_BIT_USB!"
        set "VD_FLEX=!SCEZ_VD_FLEX_USB!"
        set "VD_AUTO_DPI=!SCEZ_VD_AUTO_DPI_USB!"
        set "VD_MATCH_PHONE=!SCEZ_VD_MATCH_PHONE_USB!"
        set "VD_MAX_SIZE=!SCEZ_VD_MAX_SIZE_USB!"
        set "VD_AUDIO=!SCEZ_VD_AUDIO_USB!"
    ) else (
        set "VD_SIZE=!SCEZ_VD_SIZE_WIFI!"
        set "VD_DPI=!SCEZ_VD_DPI_WIFI!"
        set "VD_FPS=!SCEZ_VD_FPS_WIFI!"
        set "VD_BIT=!SCEZ_VD_BIT_WIFI!"
        set "VD_FLEX=!SCEZ_VD_FLEX_WIFI!"
        set "VD_AUTO_DPI=!SCEZ_VD_AUTO_DPI_WIFI!"
        set "VD_MATCH_PHONE=!SCEZ_VD_MATCH_PHONE_WIFI!"
        set "VD_MAX_SIZE=!SCEZ_VD_MAX_SIZE_WIFI!"
        set "VD_AUDIO=!SCEZ_VD_AUDIO_WIFI!"
    )
    rem 回退：对应形态无新变量时用旧单套变量（旧 GUI 组合）
    if not defined VD_SIZE if defined SCEZ_VD_SIZE set "VD_SIZE=!SCEZ_VD_SIZE!"
    if not defined VD_DPI if defined SCEZ_VD_DPI set "VD_DPI=!SCEZ_VD_DPI!"
    if not defined VD_FPS if defined SCEZ_VD_FPS set "VD_FPS=!SCEZ_VD_FPS!"
    if not defined VD_BIT if defined SCEZ_VD_BIT set "VD_BIT=!SCEZ_VD_BIT!"
    if not defined VD_FLEX if defined SCEZ_VD_FLEX set "VD_FLEX=!SCEZ_VD_FLEX!"
    if not defined VD_AUTO_DPI if defined SCEZ_VD_AUTO_DPI set "VD_AUTO_DPI=!SCEZ_VD_AUTO_DPI!"
    if not defined VD_AUTO_DPI set "VD_AUTO_DPI=0"
    set "SCEZ_VD_AUTO_DPI=!VD_AUTO_DPI!"
    if not defined VD_MATCH_PHONE if defined SCEZ_VD_MATCH_PHONE set "VD_MATCH_PHONE=!SCEZ_VD_MATCH_PHONE!"
    if not defined VD_MAX_SIZE if defined SCEZ_VD_MAX_SIZE set "VD_MAX_SIZE=!SCEZ_VD_MAX_SIZE!"
    if not defined VD_MAX_SIZE set "VD_MAX_SIZE=0"
    if not defined VD_AUDIO if defined SCEZ_VD_AUDIO set "VD_AUDIO=!SCEZ_VD_AUDIO!"
    if defined VD_SIZE (
        if not defined VD_BIT set "VD_BIT=8"
        if not defined VD_FPS set "VD_FPS=60"
        rem v2.1.91：编码格式按连接形态（虚拟屏）
        if "!VD_WIRE!"=="0" (set "VCODEC=!VCODEC_USB!") else (set "VCODEC=!VCODEC_WIFI!")
        if "!VD_WIRE!"=="0" (set "ACODEC=!ACODEC_USB!") else (set "ACODEC=!ACODEC_WIFI!")
        set "CAST_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC! --audio-codec=!ACODEC! --video-bit-rate=!VD_BIT!M --max-fps !VD_FPS!"
        if "!VD_WIRE!"=="0" set "CAST_ARGS=!CAST_ARGS! --video-codec-options="max-b-frames:int=0,bitrate-mode:int=1" --video-buffer=0"
        if defined LEGACY_DEVICE set "CAST_ARGS=--keyboard=!KEYBOARD! --video-codec=!VCODEC! --audio-codec=!ACODEC! --video-bit-rate=4M --max-fps 24"
        if defined VD_DPI (
            set "VD_ARGS=--new-display=!VD_SIZE!/!VD_DPI!"
        ) else (
            set "VD_ARGS=--new-display=!VD_SIZE!"
        )
        if "!VD_MATCH_PHONE!"=="1" (
            set "VD_ARGS=--new-display"
            set "SCEZ_VD_AUTO_DPI=0"
        ) else (
            if "!VD_FLEX!"=="1" set "VD_ARGS=!VD_ARGS! --flex-display"
        )
        if defined VD_MAX_SIZE if !VD_MAX_SIZE! GTR 0 set "CAST_ARGS=!CAST_ARGS! --max-size !VD_MAX_SIZE!"
        if defined SCEZ_VD_IME set "VD_ARGS=!VD_ARGS! --display-ime-policy=!SCEZ_VD_IME!"
        if "%SCEZ_VD_NO_DECOR%"=="1" set "VD_ARGS=!VD_ARGS! --no-vd-system-decorations"
        if "%SCEZ_VD_KEEP_CONTENT%"=="1" set "VD_ARGS=!VD_ARGS! --no-vd-destroy-content"
        if "!VD_AUDIO!"=="phone" set "VD_ARGS=!VD_ARGS! --no-audio"
        if "!VD_AUDIO!"=="both" set "VD_ARGS=!VD_ARGS! --audio-dup"
        if defined SCEZ_START_APP set "VD_ARGS=!VD_ARGS! --start-app=!SCEZ_START_APP!"
        if defined SCEZ_WIN_TITLE set "VD_ARGS=!VD_ARGS! --window-title=!SCEZ_WIN_TITLE!"
        rem ABR 锁定（GUI 窗口设置「锁定」）：锁定维度不被自适应调整
        if "!VD_WIRE!"=="0" (
            if defined SCEZ_VD_LOCK_FPS_USB set "VD_ARGS=!VD_ARGS! --abr-lock-fps"
            if defined SCEZ_VD_LOCK_BITRATE_USB set "VD_ARGS=!VD_ARGS! --abr-lock-bitrate"
        ) else (
            if defined SCEZ_VD_LOCK_FPS_WIFI set "VD_ARGS=!VD_ARGS! --abr-lock-fps"
            if defined SCEZ_VD_LOCK_BITRATE_WIFI set "VD_ARGS=!VD_ARGS! --abr-lock-bitrate"
        )
        echo [窗口] 虚拟屏 !VD_SIZE! dpi=!VD_DPI! flex=!VD_FLEX! 声音=!VD_AUDIO! 应用=!SCEZ_START_APP!
    ) else (
        echo [窗口] 虚拟屏参数缺失（SCEZ_VD_SIZE[_USB/_WIFI] 均未定义，跳过虚拟屏参数）
    )
)
rem 默认使用当前版本定制 server，旧 Android 的功能保护由 server 处理。
rem scrcpy-server-legacy 保留为旧版备份，其协议与 5.0.1 客户端不同。
rem 回退请恢复完整旧包，勿只替换服务端。
set "SCRCPY_SERVER_PATH=%~dp0scrcpy-server"
if defined LEGACY_DEVICE (
    echo [兼容] 老设备使用定制 server（已自动关闭 ABR，保留图片剪贴板）
)
rem ----- 保存当前代码页（scrcpy 可能改成 UTF-8），退出后恢复避免中文乱码 -----
for /f "tokens=2 delims=:" %%c in ('chcp') do set "OLD_CP=%%c"
set "OLD_CP=!OLD_CP: =!"
if not defined OLD_CP set "OLD_CP=936"
rem ----- 声音档位（v2.1.78）：phone=--no-audio / both=--audio-dup（pc=不加参数；虚拟屏走上方 VD 段映射）-----
if not "!VD_ON!"=="1" (
    if "!AUDIO_MODE!"=="phone" set "CAST_ARGS=!CAST_ARGS! --no-audio"
    if "!AUDIO_MODE!"=="both" set "CAST_ARGS=!CAST_ARGS! --audio-dup"
)
rem Global ez idle-sleep policy applies after both main and virtual-display arguments.
if "%SCEZ_KEEP_ACTIVE%"=="1" set "CAST_ARGS=!CAST_ARGS! --keep-active"
if "!SCEZ_REUSE_APP_TASK!"=="1" if "!SCEZ_START_APP:~0,1!"=="+" set "SCEZ_START_APP=!SCEZ_START_APP:~1!"
"%~dp0scrcpy.exe" --serial !PICK! !CAST_ARGS! !VD_ARGS! !CLIP_START_PUSH! %*
set "CAST_RC=!ERRORLEVEL!"
chcp !OLD_CP! >nul 2>&1

exit /b !CAST_RC!

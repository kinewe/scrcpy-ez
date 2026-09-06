@echo off
setlocal enabledelayedexpansion
rem 多会话 watcher 隔离（根治串会话）：每个 bat 会话一个唯一标记（GUI 注入
rem SCEZ_WATCH_TAG；未注入=旧单会话默认），切换 flag 文件按标记区分——
rem 互不清理、互不误杀、互不误读，杜绝"甲会话的插线切换被乙会话领走"
if not defined WATCH_TAG if defined SCEZ_WATCH_TAG set "WATCH_TAG=!SCEZ_WATCH_TAG!"
if not defined WATCH_TAG set "WATCH_TAG=SCRCPY_LAUNCH_USB_WATCH"
rem 会话锁定设备身份（GUI 注入：档案 marketname/model）——无线回退防抢与
rem watcher 比对的"会话本尊"判据；未注入（旧 GUI/无档案）时退回运行时检测
if defined SCEZ_MARKET set "PIN_MKT=!SCEZ_MARKET!"
if defined SCEZ_MODEL set "PIN_MOD=!SCEZ_MODEL!"
rem 清理本会话上次 watcher 切换标记（防残留误判"插线切换"）
del /q "%TEMP%\scrcpy_watch_switch_!WATCH_TAG!.flag" >nul 2>&1
rem clear this-session user-close marker (resurrect guard)
del /q "%TEMP%\scrcpy_closed_!WATCH_TAG!.flag" >nul 2>&1
cd /d "%~dp0"

rem ============================================
rem   scrcpy-ez 投屏启动脚本
rem
rem   首次连接：
rem     1. 手机/平板开启开发者模式，打开 USB 调试
rem        （小米设备还需打开「USB 调试（安全设置）」）
rem     2. 用 USB 线连接电脑后双击本脚本
rem     3. 插线 = USB 有线投屏（高规格）；拔线 = 无线投屏
rem
rem   后续连接：
rem     - 相同局域网下直接双击本脚本即可无线连接上次设备
rem     - 更换设备/网络：用 USB 线重新连接一次即可
rem
rem   快捷键：
rem     Ctrl+F  投屏参数控件开关（控件可按住 Alt 拖动位置）
rem     Ctrl+G  将电脑复制的图片保存至手机（平板）相册
rem     Ctrl+H  投屏期间黑屏省电（再次按下恢复亮屏）
rem     Ctrl+T  投屏窗口置顶开关（或按住 Alt 点击参数控件左侧指示灯）
rem     - 电脑复制文本/图片自动同步到手机剪贴板，长按粘贴即可
rem     - 关窗断开；拔线/异常自动重连（USB 优先，无线兜底）
rem


rem 使用本目录内的 server（自定义构建必须，不依赖环境变量）
set "SCRCPY_SERVER_PATH=%~dp0scrcpy-server"

rem 优先使用本目录内的 adb.exe，否则用 PATH 中的 adb
set "ADB=adb"
if exist "%~dp0adb.exe" set "ADB=%~dp0adb.exe"

set "SCRIPT_DIR=%~dp0"

rem ----- 串流参数：有线 USB 带宽充足用高规格；无线带宽有限保持低延迟 -----
rem 键盘模式默认 uhid；:detect_keyboard 会按 Android SDK 覆盖，读不到时保持 uhid
set "KEYBOARD=uhid"
set "LEGACY_DEVICE="
rem 有线规格：动态分配（:detect_display_spec 读取设备分辨率/刷新率后覆盖），此值为读取失败时的保底默认
set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=50M --max-size 2560 --max-fps 120 --video-codec-options="max-b-frames:int=0,bitrate-mode:int=1" --render-driver=direct3d --video-buffer=0"
set "WIFI_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=15M --max-size 1920 --max-fps 60"

rem ===== 自动切换投屏模式参数（与 手机投屏.bat 同步）=====
rem 注：WATCH_TAG 会话唯一（顶部按 SCEZ_WATCH_TAG 注入），避免多会话互相误杀
set "WATCH_INTERVAL=2"
set "AUTO_RETRY_SEC=2"

rem ============================================
rem   主流程：重置 adb -> 检测 USB -> 有线/无线
rem ============================================
rem 启动后直接进入主流程识别设备；首次连接向导仅在菜单中选择 [2] 时进入
goto :main
:pair_wizard
echo.
echo ===== 首次连接向导 =====
echo.
echo 第一步：手机/平板开放权限
echo   1. 进入设置 - 开发者模式（连点版本号 7 次开启）
echo   2. 打开 USB 调试开关
echo      小米设备还需打开「USB 调试（安全设置）」
echo.
echo 第二步：连接电脑
echo   1. 用 USB 线将电脑与手机（平板）连接
echo   2. 完成后脚本将自动检测设备并投屏（等待 3 秒）
echo.
rem 无控制台自愈：pause 依赖键盘输入，替换为纯延时（ping 不依赖 stdin/控制台）
ping -n 4 127.0.0.1 >nul
goto :main

:main
rem ---- resurrect guard: user closed this session -> exit, never reconnect ----
if exist "%TEMP%\scrcpy_closed_!WATCH_TAG!.flag" exit /b 0

rem ----- 1. 重置 adb 服务，清理僵死状态 -----
echo.
echo [键位] Ctrl+F 参数控件开关（按住 Alt 可拖动控件位置）^| Ctrl+G 复制图片存相册
echo          Ctrl+H 黑屏省电 ^| Ctrl+T 窗口置顶 ^| Alt+F 切换全屏
echo.
if defined SCEZ_NO_ADB_RESET (
    echo [提示] GUI 已接管 adb：跳过重置
    goto :adb_skip_reset
)
echo [1] 重置 adb 服务...
rem 多会话防抢：还有别的 scrcpy 在投屏时只 start-server 自愈，不 kill-server——
rem 共享 adb server 被杀会打断别的会话（scrcpy 断连退出），两个 bat 各自重连又互相
rem 杀服，形成"抢线循环"（bridge 日志 16:52-16:54 的互抢根源之一）。无其他投屏时
rem 保持原语义完整重置（清僵死状态/陈旧无线条目）。
set "OTHER_SCR=0"
for /f %%t in ('powershell -NoProfile -WindowStyle Hidden -Command "(Get-Process scrcpy -ErrorAction SilentlyContinue).Count" 2^>nul') do set "OTHER_SCR=%%t"
if not defined OTHER_SCR set "OTHER_SCR=0"
if "!OTHER_SCR!"=="0" (
    "!ADB!" kill-server >nul 2>&1
) else (
    echo [多会话] 检测到 !OTHER_SCR! 个其他投屏在运行，跳过 kill-server（保留共享 adb 服务）
)
:adb_skip_reset
"!ADB!" start-server >nul 2>&1
if errorlevel 1 (
    echo [失败] adb 启动失败，请确认 adb 可用（3 秒后自动退出）
    rem 无控制台自愈：pause 替换为纯延时
    ping -n 4 127.0.0.1 >nul
    call :adb_cleanup
    exit /b 1
)

rem ----- 2. 检测 USB 设备（serial 不含冒号且不含 _adb-tls 即为 USB）-----
rem GUI 多会话参数（优先于下方 USB 自由检测）：
rem   SCEZ_SERIAL 定义了目标就只认它（仅 USB 形态）——在线→锁定；不在线→转无线（跳过自由检测）
rem   SCEZ_ADDR 定义了无线锁定目标→跳过 USB 检测直接无线（无线分支用 ADDR 直连或原记忆/扫描）；
rem   两者都定义时 SCEZ_SERIAL 匹配在前（GUI USB 场景），匹配失败才走 ADDR 无线
set "USB_DEV="
set "USB_HINT="
set "SKIP_FREE_USB="
if defined SCEZ_SERIAL (
    echo !SCEZ_SERIAL! | findstr /i ":" >nul 2>&1
    if errorlevel 1 (
        set "USB_DEV="
        for /f "skip=1 tokens=1,2" %%a in ('"!ADB!" devices 2^>nul') do if "%%a"=="!SCEZ_SERIAL!" if "%%b"=="device" set "USB_DEV=!SCEZ_SERIAL!"
        if defined USB_DEV (
            rem 防抢兜底：锁定 serial 在线，但身份（model）与锁定本尊不一致（如陈旧
            rem watcher flag 锁到别的设备）→ 放弃该 serial，转入无线再找本尊
            set "PICK=!USB_DEV!"
            call :detect_market_name
            if defined PIN_MOD if defined MKT_MOD if not "!MKT_MOD!"=="!PIN_MOD!" (
                echo [防抢] 指定设备 !SCEZ_SERIAL! 在线但是别的设备（!DEV_LABEL!），不采用
                set "USB_DEV="
            )
        )
        if defined USB_DEV goto :usb_selected
        echo [提示] 指定设备 !SCEZ_SERIAL! 不可用，转入无线连接
        set "SKIP_FREE_USB=1"
    )
)
if defined SCEZ_ADDR (
    if not defined PINNED_ADDR (
        echo !SCEZ_ADDR! | findstr /r "^[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*:" >nul 2>&1 && set "PINNED_ADDR=!SCEZ_ADDR!"
    )
)
if defined SCEZ_ADDR2 (
    echo !SCEZ_ADDR2! | findstr /r "^[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*:" >nul 2>&1 && set "ALT_ADDR=!SCEZ_ADDR2!"
)
if defined SCEZ_ADDR goto :wireless_mode
if defined SKIP_FREE_USB goto :wireless_mode
echo [2] 检测 USB 设备...
for /f "skip=1 tokens=1,2" %%a in ('"!ADB!" devices 2^>nul') do (
    if "%%b"=="device" (
        rem 无线设备 serial 为 IP:端口，含冒号，排除
        echo %%a | findstr /i ":" >nul 2>&1
        if errorlevel 1 (
            rem 排除 mDNS 自动发现条目（serial 含 _adb-tls）
            echo %%a | findstr /i "_adb-tls" >nul 2>&1
            if errorlevel 1 (
                rem 非 IP 且非 mDNS = USB 设备，取第一个
                if not defined USB_DEV set "USB_DEV=%%a"
            )
        )
    ) else if "%%b"=="unauthorized" (
        rem 插线但未授权（手机弹窗未点"允许"）：不参与投屏选择，但记录以便提示
        echo %%a | findstr /i ":" >nul 2>&1
        if errorlevel 1 (
            echo %%a | findstr /i "_adb-tls" >nul 2>&1
            if errorlevel 1 if not defined USB_HINT set "USB_HINT=%%a"
        )
    ) else if "%%b"=="offline" (
        rem 插线但离线（驱动/连接问题）：同样记录提示
        echo %%a | findstr /i ":" >nul 2>&1
        if errorlevel 1 (
            echo %%a | findstr /i "_adb-tls" >nul 2>&1
            if errorlevel 1 if not defined USB_HINT set "USB_HINT=%%a"
        )
    )
)

:usb_selected
if defined USB_DEV (
    set "PICK=!USB_DEV!"
    call :detect_market_name
    rem 本尊身份记录：锁定会话（SCEZ_SERIAL 定义）恒定不变（PIN_MOD/PIN_MKT 只补空）；
    rem 自由会话跟随当前选中的设备（换设备=换本尊，允许正常换机）
    if defined SCEZ_SERIAL (
        if not defined PIN_MOD if defined MKT_MOD set "PIN_MOD=!MKT_MOD!"
        if not defined PIN_MKT if defined MARKET_NAME set "PIN_MKT=!MARKET_NAME!"
    ) else (
        if defined MKT_MOD set "PIN_MOD=!MKT_MOD!"
        if defined MARKET_NAME set "PIN_MKT=!MARKET_NAME!"
    )
    echo [OK] 检测到 USB 设备：!DEV_LABEL!
    goto :do_cast
)

rem 插线但未授权/未就绪：提示用户操作手机（不阻止无线投屏继续）
if defined USB_HINT (
    echo [提示] 检测到 USB 设备（!USB_HINT!）但未授权或未就绪，
    echo       请解锁手机并点击"允许 USB 调试"，插线监测将自动切换有线投屏
    echo.
)
echo [提示] 未检测到 USB 设备，进入无线模式
echo.
goto :wireless_mode

rem ============================================
rem   无线模式：GUI 注入地址连接（SCEZ_ADDR/SCEZ_ADDR2）+ 扫描 + 配对
rem ============================================
:wireless_mode
rem ---- resurrect guard: user closed this session -> exit, never reconnect ----
if exist "%TEMP%\scrcpy_closed_!WATCH_TAG!.flag" exit /b 0
rem 连接阶段 USB 插线监测（v2）：无线连接全程（连接/扫描/重试）监测 USB——
rem 检测到在线 USB（非冒号/mDNS/emulator）即中断无线连接转 USB 有线。
rem 目标优先级：锁定会话（SCEZ_SERIAL/SCEZ_ADDR）只认本尊（serial 直配或
rem 身份对上——平板无线+平板 USB 直接切，K80 插线不切）；自由会话取第一台
rem （原逻辑）。多台 USB 时 SCEZ_SERIAL/会话目标优先。
call :check_usb_plug
rem root fix: jump to :plug_go HERE (caller context) so no abandoned CALL frame
if exist "%TEMP%\scrcpy_closed_!WATCH_TAG!.flag" exit /b 0
if defined PLUG_PICK goto :plug_go
echo [3] 无线模式：尝试连接上次保存的地址...
set "WIRE_DEV="
set "LAST_DEVICE="

rem ----- 3a. 连接主地址（GUI 投递 SCEZ_ADDR）；主地址失效时降级尝试备用地址（SCEZ_ADDR2）-----
rem     gui42：config.txt 记忆退役——地址只来自 GUI 启动注入，bat 不再读写记忆文件
set "LAST_DEVICE="
if defined PINNED_ADDR set "LAST_DEVICE=!PINNED_ADDR!"
if defined LAST_DEVICE (
    echo [提示] 尝试连接：!LAST_DEVICE!
    rem adb connect 的失败详情是 UTF-8 中文且走 stdout，在 GBK 代码页(936)下显示为乱码，
    rem 这里吞掉原始输出，成功/失败统一由下面的 adb devices 验证，并显示本脚本自己的提示
    "!ADB!" connect !LAST_DEVICE! >nul 2>&1
    echo [提示] 已尝试连接，正在验证设备状态...
    rem 验证连接成功（adb devices 中有该设备且状态为 device）
    for /f "skip=1 tokens=1,2" %%a in ('"!ADB!" devices 2^>nul') do (
        if "%%a"=="!LAST_DEVICE!" if "%%b"=="device" set "WIRE_DEV=!LAST_DEVICE!"
    )
    if defined WIRE_DEV (
        set "PICK=!WIRE_DEV!"
        call :detect_market_name
        rem 防抢（锁定会话、非 GUI 钉定地址）：连接的设备与 PIN_MOD 对不上 → 丢弃，转扫描再找本尊
        if not defined PINNED_ADDR if defined PIN_MOD (
            if not defined MKT_MOD (
                echo [防抢] !LAST_DEVICE! 身份读不到，跳过（保守不采用）
                set "WIRE_DEV="
            ) else if not "!MKT_MOD!"=="!PIN_MOD!" (
                echo [防抢] !LAST_DEVICE! 是 !DEV_LABEL!，与锁定设备（!PIN_MOD!）不一致，跳过
                set "WIRE_DEV="
            )
        )
        if defined WIRE_DEV (
            if not defined PIN_MKT if defined MARKET_NAME set "PIN_MKT=!MARKET_NAME!"
            if not defined PIN_MOD if defined MKT_MOD set "PIN_MOD=!MKT_MOD!"
            echo [OK] 连接成功：!DEV_LABEL!
        )
    ) else (
        echo [提示] 连接失败或设备未就绪：!LAST_DEVICE!（请确认同一局域网且无线调试端口已开启）
    )
    rem ---- gui42 单次降级：主地址失败/身份不符/未就绪 → 尝试备用地址（SCEZ_ADDR2 投递的 ALT_ADDR） ----
    if not defined WIRE_DEV if defined ALT_ADDR (
        echo [切换] 主地址不可用，尝试备用地址：!ALT_ADDR!
        set "LAST_DEVICE=!ALT_ADDR!"
        set "WIRE_DEV="
        "!ADB!" connect !ALT_ADDR! >nul 2>&1
        echo [提示] 已尝试连接，正在验证设备状态...
        for /f "skip=1 tokens=1,2" %%a in ('"!ADB!" devices 2^>nul') do (
            if "%%a"=="!ALT_ADDR!" if "%%b"=="device" set "WIRE_DEV=!ALT_ADDR!"
        )
        if defined WIRE_DEV (
            set "PICK=!WIRE_DEV!"
            call :detect_market_name
            if defined WIRE_DEV (
                if not defined PIN_MKT if defined MARKET_NAME set "PIN_MKT=!MARKET_NAME!"
                if not defined PIN_MOD if defined MKT_MOD set "PIN_MOD=!MKT_MOD!"
                rem 降级成功：固化记忆——本会话后续重连直接用备用地址，不再试失效的主地址
                set "PINNED_ADDR=!ALT_ADDR!"
                set "ALT_ADDR="
                echo [OK] 备用地址连接成功：!DEV_LABEL!（已降级，后续重连使用 !LAST_DEVICE!）
            )
        ) else (
            echo [提示] 备用地址连接失败或设备未就绪：!ALT_ADDR!
        )
    )
) else (
    rem 无主地址（GUI 未投递；独立运行场景）→ 直接转扫描
    echo [提示] 未注入主地址（无 GUI 投递），转扫描
)

rem 3a 连接尝试完成后复查一次（插线发生在 connect/验证期间 → 中断无线转有线）
call :check_usb_plug
rem root fix: jump to :plug_go HERE (caller context) so no abandoned CALL frame
if exist "%TEMP%\scrcpy_closed_!WATCH_TAG!.flag" exit /b 0
if defined PLUG_PICK goto :plug_go

rem ----- 3b. 若 config 地址无效，扫描已有无线设备（IP 条目优先 + 去重）-----
if not defined WIRE_DEV if not defined PINNED_ADDR (
    echo [3] 扫描已有无线设备（IP 条目优先，自动去重）...
    set "IP_COUNT=0"
    for /f "skip=1 tokens=1,2" %%a in ('"!ADB!" devices 2^>nul') do (
        if "%%b"=="device" (
            set "SERIAL=%%a"
            rem 只收 IP:端口 格式条目
            echo !SERIAL! | findstr /r "^[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*:" >nul 2>&1
            if not errorlevel 1 (
                rem 同一手机 IP 条目 + mDNS 条目并存时，只保留第一个（按 IP 前缀去重）
                for /f "tokens=1 delims=:" %%k in ("!SERIAL!") do set "IPKEY=%%k"
                set "MARK=SEEN_!IPKEY!"
                if not defined !MARK! (
                    set "!MARK!=1"
                    set /a IP_COUNT+=1
                    set "IP_!IP_COUNT!=!SERIAL!"
                )
            )
        )
    )
    rem 逐台比对身份：锁定会话只接"模型对上 PIN_MOD"的一台（防扫描扫到别的设备）；
    rem 未锁定（无 PIN_MOD）保持原逻辑取第一台
    if !IP_COUNT! GTR 0 (
        set "SCAN_PICK="
        for /l %%i in (1,1,!IP_COUNT!) do (
            if not defined SCAN_PICK (
                set "PICK=!IP_%%i!"
                call :detect_market_name
                set "IDENT_OK="
                if defined PIN_MOD (
                    if defined MKT_MOD if "!MKT_MOD!"=="!PIN_MOD!" set "IDENT_OK=1"
                ) else (
                    set "IDENT_OK=1"
                )
                if defined IDENT_OK (
                    set "SCAN_PICK=!PICK!"
                    if not defined PIN_MKT if defined MARKET_NAME set "PIN_MKT=!MARKET_NAME!"
                    if not defined PIN_MOD if defined MKT_MOD set "PIN_MOD=!MKT_MOD!"
                )
            )
        )
        if defined SCAN_PICK set "WIRE_DEV=!SCAN_PICK!"
    )
)

rem 3b 扫描完成后复查一次（插线发生在扫描/身份比对期间 → 中断无线转有线）
call :check_usb_plug
rem root fix: jump to :plug_go HERE (caller context) so no abandoned CALL frame
if exist "%TEMP%\scrcpy_closed_!WATCH_TAG!.flag" exit /b 0
if defined PLUG_PICK goto :plug_go

rem ----- 3c. 仍无可用设备 -> 引导用户配对 -----
if not defined WIRE_DEV (
    echo.
    echo [提示] 未检测到已连接的无线设备！
    echo.
    echo   请确认：
    echo     1. 首次连接请用 USB 线连接电脑
    echo     2. 无线连接需同一 WiFi，且之前已用 USB 连接过
    echo     3. 配对新设备：手机打开无线调试的配对码-IP 模式，插 USB 后重新运行本脚本
    echo.
    echo [提示] 未找到可用无线设备，5 秒后自动重新检测...
    rem 无控制台自愈：choice 依赖控制台键盘，替换为纯延时 + 自动重新循环检测
    ping -n 6 127.0.0.1 >nul
    goto :main
)

set "PICK=!WIRE_DEV!"
rem gui42：config.txt 记忆退役——无线地址由 GUI 启动时注入（SCEZ_ADDR/SCEZ_ADDR2），
rem bat 不再读写任何地址记忆文件
goto :do_cast

rem ============================================
rem   动态规格分配：读取设备分辨率（wm size）与峰值刷新率（peak_refresh_rate），
rem   按 分辨率×刷新率 估算有线 bit-rate；失败回退默认 50M/2560/120fps
rem ============================================
rem ============================================
rem   键盘模式自适应：Android 13+ 用 uhid，Android 12 及以下回退 sdk
rem   注意：不使用 aoa —— Windows 下 adb 已独占 USB 设备，
rem   scrcpy 会拒绝 --keyboard=aoa（仅 OTG 模式可用）
rem   兼容视频档/legacy server 只针对 Android 10 以下（SDK<29），
rem   避免把 Android 11/12 这类较新设备误判成老设备
rem ============================================
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
        set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=6M --max-size 720 --max-fps 24 --render-driver=direct3d --video-buffer=0"
        set "SPEC_INFO=兼容模式 h264/6M/720/24fps（老设备）"
    ) else (
        set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=50M --max-size 2560 --max-fps 120 --video-codec-options="max-b-frames:int=0,bitrate-mode:int=1" --render-driver=direct3d --video-buffer=0"
        set "SPEC_INFO=设备规格读取失败，使用默认规格 h264/50M/2560/120fps"
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
        set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=!USB_BITRATE! --max-size !MAX_SIZE! --max-fps !DEV_FPS! --render-driver=direct3d --video-buffer=0"
        set "SPEC_INFO=兼容模式 h264/6M/720/24fps（Android 8 及更早设备）"
    ) else (
        if !DEV_FPS! GTR 30 set "DEV_FPS=30"
        set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=!USB_BITRATE! --max-size !MAX_SIZE! --max-fps !DEV_FPS! --video-codec-options="max-b-frames:int=0,bitrate-mode:int=1" --render-driver=direct3d --video-buffer=0"
        set "SPEC_INFO=检测到设备 !DEV_W!x!DEV_H!@!DEV_FPS!Hz，有线规格 h264/!USB_BITRATE!/!MAX_SIZE!/!DEV_FPS!fps（老设备限 30fps）"
    )
) else (
    set "USB_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=!USB_BITRATE! --max-size !MAX_SIZE! --max-fps !DEV_FPS! --video-codec-options="max-b-frames:int=0,bitrate-mode:int=1" --render-driver=direct3d --video-buffer=0"
    set "SPEC_INFO=检测到设备 !DEV_W!x!DEV_H!@!DEV_FPS!Hz，有线规格 h264/!USB_BITRATE!/!MAX_SIZE!/!DEV_FPS!fps"
)
exit /b 0

rem ============================================
rem   投屏循环（自动切换投屏模式）
rem   scrcpy 退出后自动重新检测设备：
rem   拔线 -> 自动切无线；插线 -> 自动切有线
rem ============================================
:do_cast
rem clear orphan watcher-switch flag left by a previous dying cast (stale-flag misread fix)
del /q "%TEMP%\scrcpy_watch_switch_!WATCH_TAG!.flag" >nul 2>&1
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
call :stop_usb_watch
call :detect_keyboard
if defined LEGACY_DEVICE (
    if !SDK_VER! lss 29 (
        set "WIFI_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=4M --max-size 720 --max-fps 24"
    ) else (
        set "WIFI_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=8M --max-size 1080 --max-fps 30"
    )
) else (
    set "WIFI_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=15M --max-size 1920 --max-fps 60"
)
set "CAST_ARGS=!WIFI_ARGS!"
echo !PICK! | findstr /i ":" >nul 2>&1
if not errorlevel 1 (
    if "%SCEZ_NO_WATCH%"=="1" (echo [自动切换] 无线投屏中（SCEZ_NO_WATCH：USB 插线监测已禁用）) else (echo [自动切换] 无线投屏中，已开启 USB 插线监测（每 !WATCH_INTERVAL! 秒检测一次）)
    echo [流畅] 无线模式：带宽有限，已启用低延迟串流（h264/15M/1920/60fps，剪贴板自动同步（电脑复制即达手机））
    rem scrcpy-ez 参数浮窗覆盖（无线 SCEZ_*_WIFI）：未设置=原逻辑
    if defined SCEZ_RES_WIFI set "CAST_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=!SCEZ_BITRATE_WIFI!M --max-size !SCEZ_RES_WIFI! --max-fps !SCEZ_FPS_WIFI!"
    if defined SCEZ_RES_WIFI echo [custom] wireless res=!SCEZ_RES_WIFI! fps=!SCEZ_FPS_WIFI! bitrate=!SCEZ_BITRATE_WIFI! ^(wifi^)
    rem 无线目标身份（watcher"对上才切"比对用）：市场名 → 厂商+型号 → 序列号回退链；
    rem 市场名读不到时优先用锁定会话的 PIN_MKT（GUI 注入的本尊市场名）
    set "WIRE_MKT=!MARKET_NAME!"
    if not defined WIRE_MKT if defined PIN_MKT set "WIRE_MKT=!PIN_MKT!"
    if not defined WIRE_MKT set "WIRE_MKT=!PICK!"
    rem SCEZ_NO_WATCH=1（GUI 并行会话）：跳过 USB 插线监测（避免串会话）
    if not "%SCEZ_NO_WATCH%"=="1" call :start_usb_watch
) else (
    rem 有线 USB 连接：动态读取设备分辨率/刷新率，按设备能力分配规格（读取失败回退默认）
    call :detect_display_spec
    set "CAST_ARGS=!USB_ARGS!"
    echo [高清] 有线模式：!SPEC_INFO!（低延迟优化，剪贴板自动同步（电脑复制即达手机））
    rem scrcpy-ez 参数浮窗覆盖（有线 SCEZ_*_USB）：未设置=原逻辑
    if defined SCEZ_RES_USB set "CAST_ARGS=--keyboard=!KEYBOARD! --video-codec=h264 --video-bit-rate=!SCEZ_BITRATE_USB!M --max-size !SCEZ_RES_USB! --max-fps !SCEZ_FPS_USB! --video-codec-options="max-b-frames:int=0,bitrate-mode:int=1" --render-driver=direct3d --video-buffer=0"
    if defined SCEZ_RES_USB echo [custom] wired res=!SCEZ_RES_USB! fps=!SCEZ_FPS_USB! bitrate=!SCEZ_BITRATE_USB! ^(usb^)
)
rem 所有设备统一使用定制 scrcpy-server：server 内对 SDK<29（Android 10 以下）
rem 已自动关闭 ABR，保留图片剪贴板等定制功能。若某台老设备仍异常，可手动
rem 切回最后兜底：把下一行改成 set "SCRCPY_SERVER_PATH=%~dp0scrcpy-server-legacy"
set "SCRCPY_SERVER_PATH=%~dp0scrcpy-server"
if defined LEGACY_DEVICE (
    echo [兼容] 老设备使用定制 server（已自动关闭 ABR，保留图片剪贴板）
)
rem ----- 保存当前代码页（scrcpy 可能改成 UTF-8），退出后恢复避免中文乱码 -----
for /f "tokens=2 delims=:" %%c in ('chcp') do set "OLD_CP=%%c"
set "OLD_CP=!OLD_CP: =!"
if not defined OLD_CP set "OLD_CP=936"
"%~dp0scrcpy.exe" --serial !PICK! !CAST_ARGS! !CLIP_START_PUSH! %*
set "CAST_RC=!ERRORLEVEL!"
chcp !OLD_CP! >nul 2>&1
call :stop_usb_watch
echo.
rem ----- 按退出码区分：0=正常关闭窗口（投屏结束，退出循环）；非0=异常断开（自动重连）-----
if "!CAST_RC!"=="0" (
    rem 区分"用户关窗"与"watcher 切换（插线）"：watcher 温和关闭 scrcpy 前会写
    rem 本会话专属标记文件（按 WATCH_TAG 区分，防别的会话读走/清掉）
    if exist "%TEMP%\scrcpy_watch_switch_!WATCH_TAG!.flag" (
        set "WATCH_SERIAL="
        set /p WATCH_SERIAL=<"%TEMP%\scrcpy_watch_switch_!WATCH_TAG!.flag"
        del /q "%TEMP%\scrcpy_watch_switch_!WATCH_TAG!.flag" >nul 2>&1
        echo [自动切换] 检测到 USB 插线，切换至有线投屏...
        rem watcher 写入的是"对上"的 USB serial：锁定它走有线（防止自由检测抓别的设备）；
        rem 旧版 watcher 写的是 "1"（无 serial）→ 不锁定，走原检测流程；
        rem 只在命中台仍在线时锁定（防陈旧 flag 锁到已拔线/被抢的设备）
        if defined WATCH_SERIAL if not "!WATCH_SERIAL!"=="1" (
            set "WATCH_OK="
            for /f "skip=1 tokens=1,2" %%a in ('"!ADB!" devices 2^>nul') do if "%%a"=="!WATCH_SERIAL!" if "%%b"=="device" set "WATCH_OK=1"
            if defined WATCH_OK set "SCEZ_SERIAL=!WATCH_SERIAL!"
        )
        goto :main
    )
    > "%TEMP%\scrcpy_closed_!WATCH_TAG!.flag" echo 1
    rem user-close marker written above: every re-entry gate now exits (resurrect guard)
    echo [提示] 已检测到窗口关闭（退出码 0），投屏已结束，退出投屏循环
    call :adb_cleanup
    rem 无控制台自愈：timeout 在输入重定向下会立即返回，改为 ping 纯延时（1 秒）
    ping -n 2 127.0.0.1 >nul
    exit /b 0
)
echo [提示] 检测到连接断开（退出码 !CAST_RC!），!AUTO_RETRY_SEC! 秒后自动重连...
rem 无线场景：退出码非 0 时先确认设备是否仍在线（adb 状态为 device）。
rem 若设备已离线，说明投屏连接已结束（如用户关窗时无线连接已断开、设备掉线），
rem 直接退出投屏循环，避免无限自动重连导致"关窗后 bat 不退出"
echo !PICK! | findstr /i ":" >nul 2>&1
if not errorlevel 1 (
    set "STILL_THERE="
    set "USB_BACK="
    for /f "skip=1 tokens=1,2" %%a in ('"!ADB!" devices 2^>nul') do (
        if "%%a"=="!PICK!" if "%%b"=="device" set "STILL_THERE=1"
        rem 插线切换场景：无线可能暂时离线，若 USB 已接入则继续重连走有线
        if not defined USB_BACK if not "%%b"=="" (
            echo %%a | findstr /i ":" >nul 2>&1
            if errorlevel 1 (
                echo %%a | findstr /i "_adb-tls" >nul 2>&1
                if errorlevel 1 set "USB_BACK=%%a"
            )
        )
    )
    if not defined STILL_THERE (
        if not defined USB_BACK (
            echo [提示] 无线设备 !PICK! 已离线，投屏连接已断开，退出投屏循环
            call :adb_cleanup
            rem 无控制台自愈：timeout 在输入重定向下会立即返回，改为 ping 纯延时（1 秒）
            ping -n 2 127.0.0.1 >nul
            exit /b 0
        )
        echo [提示] 无线设备已离线，检测到 USB 设备（!USB_BACK!），继续重连（锁定会话仍只认锁定目标，不会改投别的设备）
    ) else (
        echo [提示] 无线设备 !PICK! 仍在线，尝试自动重连...
    )
)
echo.
rem 自动重连（无控制台自愈）：choice 依赖控制台键盘，替换为纯延时 + 无条件重投；
rem 退出循环由 GUI 会话级操作（停止投屏）处理
echo [自动切换] !AUTO_RETRY_SEC! 秒后自动重新检测并投屏（如需退出请关闭 GUI 投屏会话）...
set /a "WAIT_N=!AUTO_RETRY_SEC!+1"
ping -n !WAIT_N! 127.0.0.1 >nul
goto :main
rem ============================================
rem   投屏结束菜单
rem ============================================
:menu
echo.
echo ============================================
echo   投屏已结束，3 秒后自动退出
echo ============================================
rem 无控制台自愈：choice 已移除；重新投屏/返回列表由 GUI 会话级操作接管
ping -n 4 127.0.0.1 >nul
call :adb_cleanup
exit /b 0


rem ============================================
rem   连接阶段 USB 插线监测（无线模式全程：连接阶段 + 投屏中）
rem   投屏中的监测由 watcher 负责（"对上才切"）；本子程序覆盖连接阶段
rem   （3a connect/3b 扫描/3c 重试——watcher 启动前的空窗）。
rem   检测到在线 USB（非冒号/非 mDNS/非 emulator）→ 中断无线连接，
rem   直接转 USB 有线（:usb_selected 走原检测/学习流程）。
rem   多台 USB 优先级：SCEZ_SERIAL（锁定目标，身份防抢复核）>
rem   身份对上（PIN_MOD/PIN_MKT——平板无线+平板 USB 直接切，K80 插线不切）
rem   > 自由会话第一台；锁定会话绝不切到别的设备（防抢闸）。
rem ============================================
:check_usb_plug
set "PLUG_PICK="
rem 优先级 1：锁定目标（SCEZ_SERIAL）USB 在线 → 直接切（身份防抢复核）
if defined SCEZ_SERIAL (
    for /f "skip=1 tokens=1,2" %%a in ('"!ADB!" devices 2^>nul') do if "%%a"=="!SCEZ_SERIAL!" if "%%b"=="device" set "PLUG_PICK=!SCEZ_SERIAL!"
    if defined PLUG_PICK if defined PIN_MOD (
        call :plug_ident !SCEZ_SERIAL!
        if not defined PLUG_OK (
            echo [防抢] 指定设备 !SCEZ_SERIAL! 在线但身份不匹配，不采用
            set "PLUG_PICK="
        )
    )
)
rem 优先级 2/3：锁定会话按身份对上（平板无线+平板 USB 直接切；K80 插线不切），
rem 自由会话（无 SCEZ_SERIAL/SCEZ_ADDR）取第一台 USB（原逻辑）
for /f "skip=1 tokens=1,2" %%a in ('"!ADB!" devices 2^>nul') do (
    if "%%b"=="device" if not defined PLUG_PICK (
        echo %%a | findstr /i ":" >nul 2>&1
        if errorlevel 1 (
            echo %%a | findstr /i "_adb-tls" >nul 2>&1
            if errorlevel 1 (
                echo %%a | findstr /i "emulator" >nul 2>&1
                if errorlevel 1 (
                    if not defined SCEZ_SERIAL if not defined SCEZ_ADDR (
                        set "PLUG_PICK=%%a"
                    ) else (
                        call :plug_ident %%a
                        if defined PLUG_OK set "PLUG_PICK=%%a"
                    )
                )
            )
        )
    )
)
exit /b 0
:plug_go
rem reached only by the caller (top-level) jump: goto :usb_selected below runs in caller context
rem 文案保持"检测到 USB 插线，切换至有线投屏"连续短语（GUI classify 据此显示
rem "切换有线投屏"阶段）
echo [自动切换] 连接阶段检测到 USB 插线，切换至有线投屏（!PLUG_PICK!）...
set "USB_DEV=!PLUG_PICK!"
goto :usb_selected

rem 身份比对辅助（连接阶段插线用）：按市场名→厂商+型号回退链与锁定本尊
rem （PIN_MKT/PIN_MOD）比对；命中置 PLUG_OK=1。PICK 用后恢复（不污染无线
rem 连接流程）；MARKET_SERIAL/MARKET_NAME/MKT_MOD 缓存留给 :usb_selected 复用。
:plug_ident
set "PLUG_OK="
set "PLUG_ID_PICK=%~1"
set "PLUG_SAVE_PICK=!PICK!"
set "PICK=!PLUG_ID_PICK!"
call :detect_market_name
if defined PIN_MOD if defined MKT_MOD if "!MKT_MOD!"=="!PIN_MOD!" set "PLUG_OK=1"
if not defined PLUG_OK if defined PIN_MKT if defined MARKET_NAME if "!MARKET_NAME!"=="!PIN_MKT!" set "PLUG_OK=1"
set "PICK=!PLUG_SAVE_PICK!"
exit /b

:start_usb_watch
call :stop_usb_watch
rem 多会话 watcher 脚本落盘执行（每会话独立 ps1，按 WATCH_TAG 命名）：
rem 复杂比对逻辑写成文件，避免 cmd 命令行引号/管道转义损坏（CIM 查询含引号）。
rem 会话参数全走环境变量（WATCH_TAG/ADB/WIRE_MKT/SCEZ_SERIAL/WATCH_INTERVAL，
rem bat 的 set 即环境变量，watcher 子进程继承）。块内 cmd 元字符一律 ^ 转义。
(
echo $ErrorActionPreference='SilentlyContinue'
echo $tag=$env:WATCH_TAG
echo $adb=$env:ADB
echo $wmt=$env:WIRE_MKT
echo $wser=$env:SCEZ_SERIAL
echo $interval=[int]$env:WATCH_INTERVAL
echo if^(-not $interval^){$interval=2}
echo $flag=Join-Path $env:TEMP ^('scrcpy_watch_switch_'+$tag+'.flag'^)
echo while^($true^){
echo   if^(-not^(Get-Process scrcpy -ErrorAction SilentlyContinue^)^){break}
echo   $u=^& $adb devices 2^>$null ^| Select-Object -Skip 1 ^| Where-Object{$_ -match '\S+\s+\S+\s*$' -and $_ -notmatch ':' -and $_ -notmatch '_adb-tls' -and $_ -notmatch 'emulator'}
echo   if^($u^){
echo     $hit=''
echo     foreach^($ln in $u^){
echo       $s=^($ln -split '\s+'^)[0]
echo       $st=^($ln -split '\s+'^)[1]
echo       if^(-not ^($st -eq 'device' -or $st -eq 'unauthorized' -or $st -eq 'offline'^)^){continue}
echo       if^($wser -and ^($s -eq $wser^)^){$hit=$s;break}
echo       $mkt=''
echo       $v=^& $adb -s $s shell getprop ro.product.marketname 2^>$null
echo       if^($v^){$mkt=^($v ^| Select-Object -First 1^).Trim^(^)}
echo       if^(-not $mkt^){
echo         $man='';$mv=^& $adb -s $s shell getprop ro.product.manufacturer 2^>$null
echo         if^($mv^){$man=^($mv ^| Select-Object -First 1^).Trim^(^)}
echo         $mod='';$dv=^& $adb -s $s shell getprop ro.product.model 2^>$null
echo         if^($dv^){$mod=^($dv ^| Select-Object -First 1^).Trim^(^)}
echo         if^($man -and $mod^){$mkt=$man+' '+$mod}
echo       }
echo       if^($wmt -and $mkt -and ^($mkt -eq $wmt^)^){$hit=$s;break}
echo     }
echo     if^($hit^){
echo       Set-Content -Path $flag -Value $hit
echo       $pp=^(Get-CimInstance Win32_Process -Filter "ProcessId=$PID"^).ParentProcessId
echo       if^($pp^){
echo         $wps=@^(Get-CimInstance Win32_Process -Filter "Name='scrcpy.exe'" ^| Where-Object{$_.ParentProcessId -eq $pp} ^| Select-Object -ExpandProperty ProcessId^)
echo       } else {
echo         $wps=@^(Get-Process scrcpy -ErrorAction SilentlyContinue ^| Select-Object -ExpandProperty Id^)
echo       }
echo       foreach^($wid in $wps^){
echo         $h=Get-Process -Id $wid -ErrorAction SilentlyContinue
echo         if^($h -and $h.MainWindowHandle -ne 0^){[void]$h.CloseMainWindow^(^)}
echo       }
echo       $dl=^(Get-Date^).AddSeconds^(5^)
echo       if^($pp^){
echo         while^(@^(Get-CimInstance Win32_Process -Filter "Name='scrcpy.exe'" ^| Where-Object{$_.ParentProcessId -eq $pp}^).Count -gt 0 -and ^(Get-Date^)-lt $dl^){Start-Sleep -Milliseconds 200}
echo         Get-CimInstance Win32_Process -Filter "Name='scrcpy.exe'" ^| Where-Object{$_.ParentProcessId -eq $pp} ^| ForEach-Object{Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue}
echo       } else {
echo         while^(^(Get-Process scrcpy -ErrorAction SilentlyContinue^) -and ^(Get-Date^)-lt $dl^){Start-Sleep -Milliseconds 200}
echo         Get-Process scrcpy -ErrorAction SilentlyContinue ^| Stop-Process -Force -ErrorAction SilentlyContinue
echo       }
echo       break
echo     }
echo   }
echo   Start-Sleep -Seconds $interval
echo }
) > "%TEMP%\scrcpy_watch_!WATCH_TAG!.ps1"
start "!WATCH_TAG!" /b powershell -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "%TEMP%\scrcpy_watch_!WATCH_TAG!.ps1"
exit /b

rem ============================================
rem   停止 USB 插线监测（按命令行标记精确清理，
rem   排除自身 PID，避免误杀其他 powershell）
rem ============================================
:stop_usb_watch
rem -WindowStyle Hidden：GUI 无控制台启动时防 powershell 闪黑框
powershell -NoProfile -WindowStyle Hidden -Command "Get-WmiObject Win32_Process | Where-Object { $_.CommandLine -match '!WATCH_TAG!' -and $_.ProcessId -ne $PID } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }" >nul 2>&1
exit /b

rem ============================================
rem   退出前清理 adb server（确保后台干净）
rem ============================================
:adb_cleanup
if defined SCEZ_NO_ADB_RESET exit /b
rem 多会话：还有别的投屏在运行时不再杀共享 adb server（与 :main 同判据），
rem 退出一个会话不打断另一个会话；最后一个会话退出才完整清理
set "OTHER_SCR=0"
for /f %%t in ('powershell -NoProfile -WindowStyle Hidden -Command "(Get-Process scrcpy -ErrorAction SilentlyContinue).Count" 2^>nul') do set "OTHER_SCR=%%t"
if not defined OTHER_SCR set "OTHER_SCR=0"
if "!OTHER_SCR!"=="0" "!ADB!" kill-server >nul 2>&1
exit /b

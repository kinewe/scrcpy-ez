# 透视相机研究：复现入口与归档约定

从[研发全记录](../../docs/透视相机研发全记录_rc17至rc25_20261103.md)开始阅读；[历史报告索引](../../docs/perspective-camera/README.md)保留各阶段的成功、失败与后续修正。本目录组织研究资料，不新增产品兼容承诺。

## 目录与状态

|目录|用途|状态|
|---|---|---|
|`../general-rgba-20261101`|厂商图层名称过滤、真实流CRC、自有层语义对照|独立研究源，未接入正式GUI|
|`../layer-filter-generality-20261101`|设备/页面结构、框架能力、性能分段|历史测量，未证明全ROM覆盖|
|`../performance-portability-20261101`|编码ABBA、读回候选、近期OEM材料|编码减复制有效；读回候选已撤回|
|`../shutdown-alipay-20261101`|Windows句柄继承回归及实际会话退出|产品修复已验证；旧部署/清理脚本禁止重放|
|`../alipay-texture-20261102`|真实虚拟投屏、残影、相机配置、共享缓冲/SKP与绘制入口|研究探针；支付宝稳定通用流未通过|
|`../../docs/perspective-camera/history`|脱敏历史报告与少量标量摘要|历史记录，不能把当时的待验声明当作最终状态|

## 环境配置

Python 3.10+；Java探针使用JDK17、Android SDK平台36及build-tools36.0.0，并依赖对应rc25源码构建出的产品类。配套产品进展保存在[音墨rc25研究分支](https://github.com/kinewe/PC-kinewe-yinmo/tree/codex/rc25-progress-20261102)；默认分支收录研究档案，不表示其产品版本已升级到rc25。Windows客户端重链/进程回归还依赖MinGW、原基准构建的compile_commands/build.ninja及配套库，不能仅凭源码档案直接重建历史二进制。这里没有分发SDK、编译器、APK或设备框架。

从仓库根目录执行，按实际安装位置填写环境变量：

```powershell
$env:ADB=(Get-Command adb -ErrorAction Stop).Source
$env:ANDROID_SDK_ROOT='<android-sdk>'
$env:JAVA_HOME='<jdk17>'
$env:SCRCPY_EZ_PRODUCT_ROOT=(Get-Location).Path
$env:SCRCPY_EZ_BUILD_DIR=Join-Path $env:SCRCPY_EZ_PRODUCT_ROOT 'build-rc25/out/rc25'
$env:RC25_RESEARCH_SERIAL='<device-serial>'
$env:RC25_RESEARCH_ADB_PORT='5037'
```

工具和设备配置统一由[research_common.py](../research_common.py)读取。没有默认手机地址，也不会因为只连一台设备便自动选择它。设备序列号和当前显示/任务ID必须来自本次实验；不要使用历史报告中的编号。

|变量|用途|
|---|---|
|`ADB`|adb可执行文件；未设置时从PATH寻找|
|`RC25_RESEARCH_SERIAL`|单设备实验的显式目标|
|`RC25_RESEARCH_DEVICES`|多设备标签映射JSON，例如`{"phone":"<phone-serial>","tablet":"<tablet-serial>"}`；不写入仓库|
|`RC25_RESEARCH_ADB_PORT`|已有ADB服务端口，默认5037；脚本不主动停止ADB服务|
|`ANDROID_SDK_ROOT`／`ANDROID_HOME`、`JAVA_HOME`|SDK/JDK路径|
|`SCRCPY_EZ_PRODUCT_ROOT`、`SCRCPY_EZ_BUILD_DIR`|配套产品源码与已编译产物；默认当前仓库及其rc25构建目录|
|`SCRCPY_EZ_RUNTIME`、`MINGW_ROOT`|完整Windows运行目录、MinGW根目录，仅相关工具需要|
|`SCRCPY_EZ_REFERENCE_BUILD`、`SCRCPY_EZ_CODEC_PREFIX`|历史重链工具的基准构建与编解码器安装前缀，不包含在公开档案|
|`RC25_RESEARCH_ARCHIVE_DIR`|历史打包代码的输出位置；旧会话打包脚本已禁止直接执行|

原有脚本的位置参数保持原含义。除了明确说明的只读入口，其余是工程脚本，运行前需要阅读对应报告及源码，确定它要求的目标窗口和恢复范围。

## 最小只读设备实验

先构建探针，产品类必须与所用源码版本配套：

```powershell
python research/alipay-texture-20261102/build-probe.py --sdk-root "$env:ANDROID_SDK_ROOT" --java-home "$env:JAVA_HOME" --product-classes "$env:SCRCPY_EZ_BUILD_DIR/classes" --product-classpath "$env:SCRCPY_EZ_BUILD_DIR/classes.jar"
python research/alipay-texture-20261102/run-graphics-capabilities.py --help
python research/alipay-texture-20261102/run-graphics-capabilities.py --serial "$env:RC25_RESEARCH_SERIAL" --adb "$env:ADB" --adb-port "$env:RC25_RESEARCH_ADB_PORT"
```

这个入口只读取指定应用的debuggable及图形元数据，不启动目标、不改设置、不注入。它上传随机独占名称的临时探针，结束后删除并核实；断连时明确报告清理未确认，不把连接错误算作文件已删除。结果写在被忽略的`out/`，可能含设备标识，不直接提交。

多设备可重复`--serial`，也可设置`RC25_RESEARCH_DEVICES`。探针结果表示声明条件，不等于实际GPU层注入测试。

## 其他实验的前提

- 名称过滤：先运行自有层语义对照，再读取实际独立相机子树；需要设备真实存在相应厂商扩展。参见[原型说明](../general-rgba-20261101/README.md)。
- 自有HardwareBuffer/SKP：Java/DEX及ARM64小库需先准备并按源码中的专属路径推送。JNI仅在自有shell进程加载；编译依赖原报告中的Zig版本和本机设备库，未分发这些二进制。参见[实验边界](../alipay-texture-20261102/RENDER-BRIDGE-20261103.md)。
- 支付宝窗口适配、自由窗口、相机配置、双背景：会创建实验资源或暂改目标窗口，必须使用当前有效的独占虚拟显示、相机、任务ID和恢复流程。`run-dsh-probe.py`保留动态请求准入，`start-dsh-trial.py`保留时限。没有用旧成功截图跳过这些条件。
- 性能ABBA：需要原始/候选配套server和同一动态场景；既有日志分析器读取的是本地历史输入，公开仓库没有这些原始数据。

这些入口没有因本次参数整理重新做手机实测。历史性能数字及其条件保持原记录，不将配置测试说成新设备验证。

## 禁止直接重放的历史会话脚本

下列文件保留了历史实现，但在执行任何其他代码前停止，避免重用旧PID、转发端口或部署清单。需要新会话流程时，应基于明确的当前资源归属重新编写，而非删除保护后直接运行。

|目录|文件|
|---|---|
|general-rgba|`finalize-research.py`|
|layer-filter-generality|`cleanup-scope.py`、`finalize-scope.py`、`make-baseline-runner.py`|
|performance-portability|`cleanup-performance.py`、`package-performance.py`|
|shutdown-alipay|`cleanup.py`、`deploy.py`|

## 公开资料的校验

```powershell
python -m unittest discover -s research/perspective-camera/tests -v
python research/perspective-camera/check-public-archive.py
```

单元验证使用模拟ADB结果，不连接设备。公开清单仅列入说明、研究源码、必要源码对照/补丁和脱敏标量摘要；`out/`、原始日志、截图、APK/框架/运行产物、个人profiles/settings均不在清单中。

`PUBLIC_MANIFEST.json`校验本次整理后的文件，按清单声明将CRLF统一为LF后计算SHA256，避免Git检出换行差异。各实验目录旧MANIFEST中的哈希保留为当轮历史快照，不能拿它们断言整理后源码未变化。历史报告脱敏前后摘要见[HISTORY_MANIFEST.json](../../docs/perspective-camera/HISTORY_MANIFEST.json)。哈希用于关联版本，不代表独立复现了所有实验。

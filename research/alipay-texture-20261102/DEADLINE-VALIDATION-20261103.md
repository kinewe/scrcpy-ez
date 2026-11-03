# 2026-11-03 午间限时验证：支付宝动态场景与透明重绘

用户允许继续测试，要求2:00前收尾。现场北京时间13:30，按当日下午14:00截止安排，13:55为停止采样的最晚时间。本轮设备实验实际于13:42停止，13:51完成独立清理核对。没有启动后续设备实验。

## 结论

支付宝真实虚拟相机预览本轮恢复了持续请求，排除了此前“摄像头启动失败”静态页对这轮数据的污染。但三个重绘候选仍未提供稳定透明流，扫描线堆叠与手电筒黑底在原始窗口图中继续出现。不能宣布根因修复，也不能把间歇阳性作为通用高速RGBA已接通。

## 条件与有效性

- K80，Android16/HyperOS3，支付宝12.12.30.8000，PID32591。
- 独立ADB5053，连接既有5555入口；没有停止5037/5038。
- 真实PC客户端建立显示253、虚拟设备201、相机v201_1003；本轮新任务844，扫码直接在253启动。
- 临时freeform mode5，边界(8,8)-(1432,3192)，捕获原始窗口1424×3184。
- 进入变更实验前，主机观察到了新的正Perspective请求速率，约25–28fps。各组RGBA CRC均逐样本变化。请求速率与UI变化仅用于证明实验有动态活动，不等于透明UI流的吞吐、逐帧相机内容校验或扫码业务验收。
- 研究客户端仍不把外部探针RGBA送回PC，PC显示不能作为本轮RGBA端到端验收。

## 结果

“非全不透明”指存在A<255像素；“像素校验通过”复用现有alphaExact、spatialAlpha、相机区域coverage>=0.6三个条件。它不包含连续帧稳定性、完整UI、几何配准和时间恢复验收。

|顺序|条件|样本数|不同RGBA CRC|非全不透明|像素校验通过|
|---|---|---:|---:|---:|---:|
|1|产品背景/相机Alpha适配后的原始窗口基线|12|12|2|2|
|2|整窗INVALIDATE一次|24|24|5|5|
|3|根Alpha临时0.999|24|24|7|0|
|4|黑色资源→透明资源重设，再layout/invalidate|24|24|0|0|
|5|恢复后再次适配，独立短测|60|60|0|0|

这是固定顺序的小样本实验，存在缓冲历史、动画阶段、时间和前项操作影响。5/24不能视为INVALIDATE提升成功率的因果证据；根Alpha0.999的7张非全不透明帧也不能当作有效流。三项均不足以进入产品修复。

各候选另保存3张同一HardwareBuffer的原始窗口PNG，共9张。CPU/GPU逐像素Alpha比较均为0处差异；INVALIDATE组前2张含透明像素，其余7张全A255。因此本轮有透明样本的A8读回一致性证据，未做这9张的完整RGB逐字节核验。最后60帧未出现透明阳性，没有触发positive-audit分支，不能声称完成了60张透明帧核验。

三组第3张原始窗口图都能看到密集累积扫描线；前两组手电筒区域有黑底。背景重设后图像外观有所变化，但仍保留密集扫描线，不能宣布清除修复。PNG仅是慢速诊断，不计入流性能。候选组capture+read均值约18.8–19.4ms，也不是端到端帧率。

产品Session捕获路径与原始窗口路径仍需区分：本轮Session基线12帧全不透明；后续表面opaque=false与根elevation+16的Session短测也未出现透明帧。原始窗口的少量阳性不自动代表正式产品已接受或输出。

## 探针修正与社区接口核对

PrimitiveRefreshProbe会rebind并释放旧SurfaceControl，而WindowAdaptationProbe后续辅助opaque检查仍引用旧句柄，日志出现layer=-1。本轮三候选自身在每次rebind后取得当前句柄，不受这个后续辅助检查问题影响；该轮辅助opaque检查应排除。已修正为辅助检查前重新取source，Java/DEX编译通过，未再次上机验证修正后的分支。第二轮60帧没有执行primitive rebind，取得有效layer86422，表面原opaque=false，设置false后亦未出现Session透明帧。

再次核对[AOSP ViewDebug参数反序列化](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/core/java/android/view/ViewDebug.java)：支持基本值、String和byte[]，按这些类型查找View的public方法，没有通用Paint对象/typed-null句柄协议。`View.setLayerType(int, Paint)`不能因Paint可为null就直接用当前通道调用；不能把进程内渲染器示例算成第三方应用跨进程方案。设备内实际可调用接口的结论沿用COMMUNITY-OPACITY-FOLLOWUP.md。

本轮加强的是“源窗口残留、渲染状态和透明帧稳定性需分别验证”的证据，没有新增现代非小米设备实测。未证明现有名称过滤API为跨厂商标准，也未发现可直接替代为通用高速RGBA流的社区实现。

## 限时保护与清理

start-dsh-trial.py新增默认300秒上限，允许1–600秒，并可用RC25_TRIAL_DEADLINE_UTC设置绝对截止。本轮使用360秒、05:55 UTC截止，实际360秒先到，13:42研究客户端被主机terminate，退出1。Android服务随后记录close-camera/device/association。**这是限时终止清理，不是正常关闭回归通过。**

系统保留了新任务844并移到默认显示；在核实它完全由本轮创建后，仅移除该任务，没有force-stop整个支付宝。Alpha和背景由Session恢复，根elevation读回0.0、freeform恢复mode1和原边界。背景恢复仍是产品现有theme-class-match机制，不声称原Drawable内部状态逐字节恢复。

北京时间13:50:56（05:50:56 UTC），out/deadline-cleanup.json独立核对全部通过：无活动相机、无显示253、无任务844、无研究服务、无本轮远端诊断文件，原Companion关联58保留、无新增活动关联、支付宝MILLET临时条目撤销、原deviceidle白名单保留。随后仅停止本轮5053服务器，保留5037/5038。

原始记录保存在忽略的out目录：dsh-deadline-alipay-cast.log、dsh-deadline-refresh.log、deadline-refresh-summary.json、dsh-deadline-audit.log、deadline-cleanup.json及少量原始窗口图。日志、截图、设备框架和构建产物不加入Git。正式运行目录、UI1和目标APK未改，本轮无Release或远端推送。

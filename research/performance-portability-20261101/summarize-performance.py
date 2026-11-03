from pathlib import Path as _ResearchPath
import sys as _research_sys
_research_sys.path.insert(0, str(_ResearchPath(__file__).resolve().parents[1]))
import research_common as research_config
from pathlib import Path
import hashlib,json,re,statistics
folder=Path(__file__).resolve().parent;root=folder.parents[1];out=folder/'out'
owned=json.loads((out/'owned-ab.json').read_text());real=json.loads((out/'real-ab.json').read_text())
before=[r for r in real if r['label'].startswith('real-a')];after=[r for r in real if r['label'].startswith('real-b')]
base_fps=statistics.mean(r['hostReceiveFps'] for r in before);opt_fps=statistics.mean(r['hostReceiveFps'] for r in after)
alloc=lambda r:int(r['memory'][-1]['gc']['art.gc.bytes-allocated'])-int(r['memory'][1]['gc']['art.gc.bytes-allocated'])
rows=[]
for r in real:
    rows.append(f'| {r["label"]} | {r["hostReceiveFps"]:.2f} | {r["stages"]["encode"]["p50Ms"]:.2f} | {r["stages"]["patchBuild"]["p50Ms"]:.2f} | {alloc(r)/1e6:.2f} | {r["meanChangedRows"]:.1f} |')
codec=owned['codec'];cpu_a=statistics.mean(r['threadCpu']['p50Ms'] for r in codec if r['label'].startswith('codec-a'));cpu_b=statistics.mean(r['threadCpu']['p50Ms'] for r in codec if r['label'].startswith('codec-b'))
product=(out/'Pad-XHS-Product-window.log').read_text(encoding='utf8',errors='replace')
assert 'PERSPECTIVE_ACTIVE=1' in product and 'alphaExact=true' in product and 'ResizableSurfaceView' in product and 'NATIVE_ALPHA_STOPPED restored=true' in product
tree_text=(out/'pad-xhs-product-tree-WindowTreeProbe.log').read_text(encoding='utf8');tree=json.loads(next(l.split('WINDOW_TREE_JSON ',1)[1] for l in tree_text.splitlines() if l.startswith('WINDOW_TREE_JSON ')))
previous=root/'research/layer-filter-generality-20261101/out';physical_text=(previous/'pad-xhs-camera-SceneProbe.log').read_text(encoding='utf8');physical=json.loads(next(l.split('SCENE_JSON ',1)[1] for l in physical_text.splitlines() if l.startswith('SCENE_JSON ')))
assert (physical['versionCode'],physical['fingerprint'])==(tree['versionCode'],tree['fingerprint'])
cam=json.loads((out/'pad-xhs-product-confirm-camera-client.json').read_text())
summary=dict(wireFrames=480,allFrameCRC='PASS',baselineMeanFps=base_fps,optimizedMeanFps=opt_fps,shortTrialGainPercent=(opt_fps/base_fps-1)*100,codecCpuP50GainPercent=(1-cpu_b/cpu_a)*100,realAllocationReductionPercent=(1-statistics.mean(alloc(r)for r in after)/statistics.mean(alloc(r)for r in before))*100,xhs=dict(package=tree['package'],version=tree['versionName'],sameDeviceAndVersion=True,physicalRendering='TextureView',perspectiveRendering='ResizableSurfaceView',virtualCameraOwner=cam['evidence'],productAlphaValidated=True,productExitReportedRestored=True,qrBusinessRevalidated=False),readbackAttempt='byte-exact but no stable speed gain; reverted',optimizedServerSHA256=hashlib.sha256((research_config.build_dir() / 'scrcpy-server').read_bytes()).hexdigest())
(out/'summary.json').write_text(json.dumps(summary,ensure_ascii=False,indent=2),encoding='utf8')
(out/'xhs-context-comparison.json').write_text(json.dumps(dict(physical=physical,perspective=tree,productEvidence=summary['xhs']),ensure_ascii=False,indent=2),encoding='utf8')
report=f'''# RC25 性能、分类规则与近期 ROM 调研

2026-11-01。研究范围：免 root，不修改目标 APK；允许既有策略临时改变并恢复 View 属性。本轮聚焦近期 Android 15/16，未用老华为 Android 8 推断现代厂商支持情况。

## 结果

编码器优化已保留并构建：去掉每帧 `patch.toByteArray()` 的额外复制，直接把自有缓冲的有效区间交给 Deflater。压缩在该缓冲复用前完成，既有协议、关键帧及行补丁格式不变。[源码差异](product-performance.patch)。

企业微信真实 RGBA 实验流 ABBA 短测，优化前 **26.44 / 27.89 fps**，优化后 **29.30 / 29.75 fps**；均为120帧，共480帧完整主机重建CRC通过。本次均值约提高 **{summary['shortTrialGainPercent']:.1f}%**，不是全应用或长时间运行的保证。

| 轮次 | 主机重建CRC fps | 编码p50 ms | 补丁构造p50 ms | 分配增量MB/120帧 | 平均变化行 |
| --- | ---: | ---: | ---: | ---: | ---: |
{chr(10).join(rows)}

[完整ABBA数据](out/real-ab.json)。同一手机、企业微信5.0.11、1440×3200、同一相机页、同一TLS ADB与普通H264监视链路。四轮都用临时唤醒锁，内存样本均interactive=true，结束释放。动态UI各轮内容并不逐帧相同；变化行相近，但温度与调度仍会波动。fps包括捕获、GPU读回、无损编码、传输、主机重建CRC，不含产品GUI显示。

旧算法在本次较好性能状态下本身已回到26–28fps，所以之前10–16fps不能算成代码升级带来的差值。此次代码收益按同场景新旧对照判断。

## 1. 优化验证与局限

自建确定性1440×3200数据、每帧384行改变，编码ABBA的输入总字节、压缩总字节、压缩流CRC完全一致。180帧分配量：旧版398.30–398.32MB，优化版3.68–3.70MB；旧版4–5次GC，优化版0次。编码线程CPU p50平均下降约 **{summary['codecCpuP50GainPercent']:.1f}%**。[确定性对照](out/owned-ab.json)。真实页面分配量下降约 **{summary['realAllocationReductionPercent']:.1f}%**；该下降不等于已经证明所有泄漏都解决。

另有64例全帧、补丁、静态、关键帧、reset、resize、非16整除高度与1×1边界测试，输出与基线逐字节相同。[测试日志](out/codec-parity/result.log)。

Paint复用和连续平面大块读取的尝试在1440/1441两种宽度、每种60帧中RGBA逐字节正确，但p50/长尾没有稳定收益，**已撤回读回修改**。最终NativeAlphaReadback与本轮开始时的源码hash相同，不把未证实提速的尝试纳入候选。

性能候选server SHA256：`{summary['optimizedServerSHA256'].upper()}`。它是独立研究构建，已验收运行目录和发行ZIP未覆盖。下一性能瓶颈仍包括系统捕获、GPU读回、哈希/压缩及系统调度；本轮不宣称达到60fps。

## 2. 小红书为什么曾成功，而普通相机实验不适用

你的记忆正确。历史档案中平板小红书9.49.1不仅有supported标签，还有surface-zalpha的22帧实测、alphaExact=true。当前已使用正式rc25产品模式复测，连续GUI取流日志出现22.4、23.1fps，GPU A8校验通过，P3→sRGB最大RGB差异1。本轮没有重新验收实际二维码业务识别。

**同一设备、同一APK版本和扫码Activity，输入会话不同，实际预览类型发生变化：**

| 条件 | 实际可见预览 | 另一预览 | 已验证路径 |
| --- | --- | --- | --- |
| 普通显示，物理相机0（上一轮现场） | TextureView 1440×3200 | ResizableSurfaceView GONE、0×0 | 无独立相机图层，名称过滤不能分离 |
| 产品透视会话，虚拟相机v110_1109（本轮） | ResizableSurfaceView 1800×3200 | TextureView GONE、0×0 | 既有surface-zalpha，背景/相机属性适配后连续真实A8 |

[同版本上下文对照](out/xhs-context-comparison.json)、[产品原始日志](out/Pad-XHS-Product-window.log)、[虚拟相机归属](out/pad-xhs-product-confirm-camera-client.json)。没有把物理/虚拟相机切换单独证明为唯一因果；准确说法是透视会话环境改变时观察到渲染结构改变。最终准入仍按实际View/Surface结构和输出验证，不能把相机名字变成白名单。

上一轮“不适用”的结论仅针对**普通相机页上的只读图层名称过滤路径**，不能解释成“整套rc25规则不支持小红书”。本次没有用已修改过View的产品页面冒充“名称过滤不改View成功”。产品运行时的自有镜像持续变化，严格图层快照读取曾拒绝；随后只读View审计明确标记未验证图层稳定性、不执行捕获。产品取流证据由独立的正式GUI日志提供。

支付宝、向日葵的既有档案仍是失败，无已验证有效流；它们的TextureView类型说明有可研究的规则族，不等于已经打通。V次元共享Flutter表面整层包含UI，仍未分离成功。Steam的名称过滤成功属于研究捕获源，尚未成为正式GUI自动覆盖的承诺。

## 3. 对更准确、更一般、更泛用的信心

**对结构分类和可验证策略选择的信心增加；对任意应用或任意ROM通吃没有证据。** 本轮增强的是判定依据，不是凭空扩大已支持应用数量。

| 实际结构 | 当前结论 | 规则通用性边界 |
| --- | --- | --- |
| 独立相机Surface + 独立原生UI | 有多种已证实取流路径 | 名称过滤还依赖ROM扩展；旧Z/Alpha策略还依赖恢复能力 |
| 独立TextureView叶节点 + 原生UI | 既有texture-background规则族可尝试 | 需可恢复背景、正确可见区域、真实Alpha及持续帧验证；不能直接按应用名宣布支持 |
| 共享Flutter/GL/Vulkan整页Surface | 不能把整页Surface当相机隐藏 | UI与相机已经混合，本轮没有新增分离能力 |
| 多预览或身份/恢复能力不清楚 | 先保存具体拒绝原因 | 存在SurfaceView、类名像相机、一次透明截图都不是充分证据 |

分类应记录：实际UID/window/display、真实可见预览类型、几何/裁剪、独立UI与相机关系、背景恢复能力、捕获源、相机输入会话、Alpha/业务/性能结果。输入环境属于复测与诊断上下文，支持与否仍取决于现场能力。这样能以少量结构机制覆盖同类布局，并在app/ROM/相机路由变化时重新判定；不靠无止境增加包名分支。当前已有产品策略仍临时修改View后恢复，与完全只读的新捕获源必须区分。

## 4. 小米独有，还是其他安卓厂商也有

需要分清**标准图层捕获/句柄排除**和**本次字符串名称过滤**。前者有AOSP与三星近期材料证据；后者目前只有两台小米实机证实，既不能说全安卓都有，也不能宣布小米独占。

| 样本/材料 | 捕获或排除能力 | 名称过滤结论 | 证据等级 |
| --- | --- | --- | --- |
| 小米OS3.0.309.0 / Android16，两台实机 | CaptureArgs.Builder及captureLayers | 名称参数与mode语义均通过 | 实机方法、像素、应用测试 |
| AOSP Android16 r1 | setExcludeLayers(SurfaceControl[]) | 无该名称参数 | 官方源码 |
| AOSP Android16 QPR2近期分支 | 内部实现迁至ScreenCaptureInternal，仍有句柄排除 | 无该名称参数；公开ScreenCaptureParams的mode=1是REQUIRE_OPTIMIZED | 官方源码 |
| 三星S24 Ultra S928BXXU4ZXKZ / Android15框架导入样本 | setExcludeLayers(SurfaceControl[]) | 该捕获类未发现名称字段/方法 | 社区原始反编译材料，固件导入提交可定位；非本机实测 |
| Oplus Screenshot15.0.0 / Android15 | 项目直接适配OplusScreenCapture.CaptureArgs.Builder及setUid(long) | 未得到同等名称过滤语义证据 | API使用方源代码线索；非OEM官方声明/非实机 |
| 其他近期vivo、荣耀、华为或最新OneUI版本 | 待验证 | 待验证 | 没有以老机或搜索无结果作否定 |

三星样本固定在导入提交[f090fe5](https://github.com/488315/samsung_framework/commit/f090fe5941476ffeb9fa685b311de674f2620491)，对应[捕获类](https://github.com/488315/samsung_framework/blob/f090fe5941476ffeb9fa685b311de674f2620491/framework/sources/android/window/ScreenCapture.java)。它不能代表最新OneUI8/8.5的所有截图通道。Oplus线索来自[API使用方原始源码](https://github.com/LSPosed/DisableFlagSecure/blob/main/app/src/main/java/io/github/lsposed/disableflagsecure/DisableFlagSecure.java)，其类由截图APK的classloader加载；“APK里有类”不等于shell启动的app_process可直接调用。本轮未安装或执行该项目。

[AOSP近期内部捕获源码](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-qpr2-release/core/java/android/window/ScreenCaptureInternal.java)、[公开参数源码](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android16-qpr2-release/core/java/android/window/ScreenCapture.java)、[获取时间对应的URL/哈希索引](out/modern-sources/pinned-index.json)。同名setCaptureMode、同为整数1，不是同一语义证据。

已扩展只读能力探针，覆盖ScreenCapture/ScreenCaptureInternal、标准参数Builder和厂商类名，并明确classloader范围。方法存在性只是第一层，其他厂商仍需同一构建上的语义与真实页面验证。**底层API跨厂商存在，不代表目前无需子层句柄的名称过滤路径已跨厂商打通。** 本次没有新增近期非小米实机，因此跨ROM可用性不能从源码样本升级成真机成功。

## 5. 退出问题与清理

产品小红书窗口关闭时出现一次旧版退出挂起：Alpha日志已报告restored=true，Windows客户端无响应，平板仍持有267号虚拟显示。已核验本次启动的PID37360、完整命令行与窗口标题，定向结束该客户端；随后其Android服务和267号显示消失，全部实验窗口、探针与转发已清理。[退出证据与结果](out/exit-hang.json)、[探针清理](out/cleanup.json)。

这是正式旧构建中观察到的一次退出残留风险，根因与修复尚待定位，不能归咎于本轮编码优化，也未证明它导致此前K80帧率波动。性能改进与会话退出可靠性分别记录。
'''
(folder/'REPORT.md').write_text(report,encoding='utf8')
prefix='../research/'+folder.name+'/'
docs=re.sub(r'\]\((?!https://)([^)]+)\)',lambda m:']('+prefix+m.group(1)+')',report)
(root/'docs/RC25_性能与近期ROM调研_20261101.md').write_text(docs,encoding='utf8')
old=root/'docs/图层过滤泛用性调研报告_20261101.md';prior=old.read_text(encoding='utf8');marker='后续上下文复测补充'
if marker not in prior:
    old.write_text('> **后续上下文复测补充**：下文不适用仅指普通相机页上的名称过滤，不代表整套rc25不支持小红书。同版本小红书在产品虚拟相机会话转为SurfaceView，既有策略已重新取流。见 [性能与近期ROM报告](RC25_性能与近期ROM调研_20261101.md)。\n\n'+prior,encoding='utf8')
(folder/'README.md').write_text('# RC25性能与近期ROM调研\n\n结论见[REPORT.md](REPORT.md)，结果见[out/summary.json](out/summary.json)，产品改动见[product-performance.patch](product-performance.patch)。\n\n仅编码器无额外复制改动保留；读回尝试已撤回。before-product保存研究起点。脚本使用本工作区SDK/JDK与ADB5038；run-probe.py及profile-run.py可用RC25_PERF_SERVER选择基线或候选server。普通与透视相机页面必须分别记录上下文。\n',encoding='utf8')
print(json.dumps(summary,ensure_ascii=False))

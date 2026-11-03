# 透视相机研究档案

从[rc17至rc25研发全记录](../透视相机研发全记录_rc17至rc25_20261103.md)阅读技术路线、成功与失败；从[研究复现入口](../../research/perspective-camera/README.md)了解配置、实验状态及不能直接重放的旧会话脚本。

本轮将24份仓库外历史报告/标量摘要加入`history/`，清理设备标识、本机路径和私人画面引用。每份Markdown保留历史说明；其旧结论须与后来的总记录对照，不修改历史实验数值来迎合新结论。

## 历史资料

|资料组|脱敏副本|
|---|---|
|rc18-temporal|[EXPERIMENT_RESULTS.json](history/rc18-temporal/EXPERIMENT_RESULTS.json)|
|rc18-temporal|[ALPHA_OBSERVATIONS.json](history/rc18-temporal/ALPHA_OBSERVATIONS.json)|
|rc18-temporal|[BACKING_OBSERVATIONS.json](history/rc18-temporal/BACKING_OBSERVATIONS.json)|
|rc18-native|[SMALL_SUMMARY.json](history/rc18-native/SMALL_SUMMARY.json)|
|native-ui|[README.md](history/native-ui/README.md)|
|native-ui|[INVERSE_LIVE_20261028.md](history/native-ui/INVERSE_LIVE_20261028.md)|
|virtual-display|[README.md](history/virtual-display/README.md)|
|rc19|[INVERSE6_DESIGN.md](history/rc19/INVERSE6_DESIGN.md)|
|rc19|[RC20_INTEGRATION_PLAN.md](history/rc19/RC20_INTEGRATION_PLAN.md)|
|rc19|[REPORT.md](history/rc19/inverse6-sim/REPORT.md)|
|srgb-alpha|[内部渲染器研究记录.md](history/srgb-alpha/内部渲染器研究记录.md)|
|srgb-alpha|[初始化假说_延时与主动重绘验证.md](history/srgb-alpha/初始化假说_延时与主动重绘验证.md)|
|srgb-alpha|[原生Alpha吞吐上限实测_20261030.md](history/srgb-alpha/原生Alpha吞吐上限实测_20261030.md)|
|srgb-alpha|[场景过渡原生Alpha实测记录.md](history/srgb-alpha/场景过渡原生Alpha实测记录.md)|
|srgb-alpha|[微信原始Alpha实验记录.md](history/srgb-alpha/微信原始Alpha实验记录.md)|
|srgb-alpha|[调研结论_sRGB与免root实时Alpha.md](history/srgb-alpha/调研结论_sRGB与免root实时Alpha.md)|
|andromeld|[逆向与Windows实测报告_20261028.md](history/andromeld/逆向与Windows实测报告_20261028.md)|
|rc24|[RC24_NATIVE_ALPHA.md](history/rc24/RC24_NATIVE_ALPHA.md)|
|rc24|[RC24_UI_REDRAW_RESEARCH.md](history/rc24/RC24_UI_REDRAW_RESEARCH.md)|
|rc24|[RC24_CAPABILITY_REPORT.md](history/rc24/RC24_CAPABILITY_REPORT.md)|
|rc24|[RC24_AUTO_ALPHA_PROFILES.md](history/rc24/RC24_AUTO_ALPHA_PROFILES.md)|
|rc24|[RC24_RULES_DESIGN.md](history/rc24/RC24_RULES_DESIGN.md)|
|rc24|[RC24_SOURCE_ARCHIVE.md](history/rc24/RC24_SOURCE_ARCHIVE.md)|
|rc24|[分析_原生UI重绘与相机分离通道_20261031.md](history/rc24/handoffs/分析_原生UI重绘与相机分离通道_20261031.md)|

## 来源与可复核范围

[HISTORY_MANIFEST.json](HISTORY_MANIFEST.json)记录原始资料的工作目录相对名称、原文SHA256、脱敏副本位置及SHA256；不记录设备连接地址。原文哈希使用原始字节；公开副本及公开清单的哈希统一将CRLF换行为LF，以适应不同Git检出设置。脱敏副本与原文不是逐字节相同的文件，原文哈希只用于与本地原始归档关联。

公开资料包含研究说明、源码、补丁及少量标量摘要。原始日志、图片、APK、厂商框架、编译器和运行产物未公开；不能仅靠这些报告独立重新计算每一项历史指标。新的复现实验需要自己的设备、相应ROM/应用版本与工具链，不承诺得到相同帧率。

本轮只整理档案、参数化研究工具并做本地验证，没有重跑手机实验，也没有新增应用兼容结论。旧MANIFEST保留其历史含义；当前文件由[公开清单](../../research/perspective-camera/PUBLIC_MANIFEST.json)关联。

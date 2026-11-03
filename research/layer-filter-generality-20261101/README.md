# 图层过滤泛用性调研

详细结论见 [REPORT.md](REPORT.md)，机器可读汇总见 out/results-summary.json（本地历史证据，未公开）。

依次运行 build-probes.py、run-probe.py 或 profile-run.py。ADB 明确使用5038；脚本保留了本工作区 SDK/运行时绝对路径。基线重测使用原始探针 DEX 与 before-product 的已验收 server，未改设备端代码。完整框架及目标 APK 不进入归档。

# RC25性能与近期ROM调研

结论见[REPORT.md](REPORT.md)，结果见out/summary.json（本地历史证据，未公开），产品改动见[product-performance.patch](product-performance.patch)。

仅编码器无额外复制改动保留；读回尝试已撤回。before-product保存研究起点。脚本使用本工作区SDK/JDK与ADB5038；run-probe.py及profile-run.py可用RC25_PERF_SERVER选择基线或候选server。普通与透视相机页面必须分别记录上下文。

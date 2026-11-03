# 本轮已交付

任务来源为 TASK.md。未实现回退/兼容策略。

完成三台设备盘点、两台小米自建离屏语义核验、7个实际设备×相机页组合及上一轮Steam证据复核。覆盖结论见REPORT.md与out/coverage-matrix.json。

本轮5640帧实流完整RGBA CRC通过，含未插桩原始构建对照、TLS/TCP ABBA、2400帧176.9秒长跑、用户清理后台后的120帧余热状态短测。加上上一轮2640帧共8280帧。未把自建图层或全透明/全不透明负例计为成功。

用户反馈手机发热后结束相机与投屏；长跑在退出前已正常完成全部2400帧。长跑interactive状态中途变化，不能当恒定环境实验。用户清理后台后短测16.31fps，随后立即退出相机并关闭窗口；不是冷机数据。原生分配约1MiB增长来源、厂商内部温控/调度/负载贡献、其他ROM与最低版本仍待验证，不作无依据的排除。

产品修改仅为NativeAlphaReadback/NativeAlphaFrameCodec默认关闭的研究计时，server构建通过，40例编码字节与修改前一致。最终研究server SHA256 C96886946D75B149B2DA619AE46D04051186A3B575CC41623D61F2D247E62FDC。正式runtime三文件hash核验未变，未覆盖正式ZIP或UI1。

所有研究窗口已关闭，三台设备无本轮研究进程，固定临时文件与研究forward已清理；见out/cleanup.json。临时屏幕唤醒锁已正常释放，没有改系统电源设置。

源码为最终研究快照，CPU/电源计时在后期增加；不回填早期记录缺失字段。基线设备端DEX与server保持原样，仅主机路径和ADB端口适配；out/baseline-invalid-socket.log是最初主机socket名称不满足旧探针校验的失败记录，后已用原名称格式修正，未计作成功。

完整OEM框架、DEX和目标APK仅留本地，不进入交付ZIP。ZIP由finalize-scope.py生成，MANIFEST.json逐文件SHA256，归档重新读取核验通过。

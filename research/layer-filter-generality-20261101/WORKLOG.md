# 图层过滤泛用性攻关（中期记录）

以下为中途快照，最终状态见 COMPLETION.md 和 REPORT.md。

任务来源：docs/任务书_图层过滤泛用性调研.md。只做接口来源、覆盖实测、吞吐归因，不设计回退/兼容策略。

设备均使用 ADB 5038：K80 TLS serial <device-serial>；Pad8Pro <device-address>:5555；Huawei FLA-TL10 USB <device-serial>。

已完成：
- K80/Pad Android16 OS3.0.309.0 的 CaptureArgs.Builder 名称过滤方法字节码相同；3份 AOSP Android16源码无此字段和方法。OEM扩展而非AOSP标准 flag；确切OEM原生C++实现尚未获得。
- 两台小米已通过自建图层模式/包含排除/非递归/完整子树 vs handle 的 RGBA 逐字节语义测试。老华为 Android8 SDK26 无该接口，本路径不支持；不外推到新款华为。
- K80 小红书9.43.0、支付宝12.12.30.8000、向日葵15.5.3.81450的真实扫码页均为 TextureView；无独立相机Surface，基线A255，排除整窗后A0且UI同时丢失。向日葵 CameraService 活动客户端已核实。
- V次元5.0.3 主界面：自定义混淆类 t6.i 对应独立 SurfaceView；排除完整子树整套UI消失，不能视为相机独立层。正在核实扫码页。
- 研究专用 PipelineProbe/SceneProbe/DeviceCapabilitiesProbe 可运行；NativeAlphaReadback/NativeAlphaFrameCodec 仅增加默认关闭的分段计时。server构建通过，编码40例与修改前字节完全一致；正式runtime/ZIP未覆盖。

待完成：V次元扫码；Pad实际应用（特别是小红书9.49.1在历史数据中为SurfaceView，必须当场复测，不能按应用名定性）；WeCom正例+捕获/编码/传输分段、长跑内存/GC/热状态、TLS vs TCP与主机背压对照；汇总覆盖矩阵/报告/精简证据归档；清理自建窗口/probe/forward和设备临时文件。

详尽原始数据位于out/。不要分发整份OEM framework.jar；可分发其hash、限定类的smali及元数据。

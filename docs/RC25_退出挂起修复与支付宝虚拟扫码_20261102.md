# RC25 退出挂起修复与支付宝虚拟扫码

2026-11-02。接续 UI1 基线的 rc25。按“先修复退出根因，再看虚拟投屏支付宝扫一扫”的顺序完成。UI1 未修改。

## 退出挂起

定位到 `app/src/sys/win/process.c` 的 Windows 子进程句柄继承缺陷：只继承标准输出的普通启动没有设置句柄白名单，`bInheritHandles=TRUE` 会把另一个并发启动正在创建的 Alpha 管道写端一并传给无关子进程。原有 `!!pin || !!pout || !!perr` 还把三个流的数量错误计算为最多 1。

退出先恢复并停止 Alpha，再停止主服务。如果主服务传输子进程持有 Alpha 管道的额外写端，Alpha 启动线程就收不到 EOF；终止 Alpha 自己的传输也不能解除此等待。这解释了此前日志已有 `NATIVE_ALPHA_STOPPED restored=true`，客户端却仍卡住的现象。未采集旧挂起现场线程栈；证据是生产进程实现的确定性复现和修复前后对照，以及修复后的真实会话关闭。

修复后所有需要继承 I/O 的启动都使用 `PROC_THREAD_ATTRIBUTE_HANDLE_LIST`，逐一计算实际句柄数；父进程标准输出/错误输出使用专属、可继承的副本，启动完成或出错时关闭。没有改变设备属性恢复顺序，也没有扩大终止进程范围。

| 使用真实生产 process.c 的测试 | 修复前 | 修复后 |
| --- | --- | --- |
| 无关子进程仍存活时，独立管道应收到 EOF | 失败：EOF 被阻塞 | 通过 |
| stdin/stdout/stderr 三路重定向 | 失败 | 通过 |
| 父进程标准输出不可继承时，子进程仍能正常输出 | 失败 | 通过 |

原始结果：`research/shutdown-alipay-20261101/out/before-process-test.log`、`fixed-process-test.log`。测试链接实际产品 Win32 实现，仅命令序列化、UTF 转换及少量包装函数为测试桩；不覆盖所有进程启动参数组合。

构建 `tools/build-rc25.py native` 成功。真实关闭验证：

| 会话 | 取流/场景 | 正常关闭结果 |
| --- | --- | --- |
| 小米平板，小红书9.49.1，虚拟相机v111_1110，display268 | Alpha 接通，842帧客户端校验通过；alphaExact=true，P3转换最大RGB误差1 | restored=true；客户端退出码0；手机服务、虚拟屏幕和本轮转发端口释放 |
| K80，支付宝12.12.30.8000，虚拟相机v186_1187，display238 | 扫码页实际占用虚拟相机；透明流未通过 | restored=true；客户端退出码0；手机服务、虚拟屏幕和本轮转发端口释放 |

两次都未出现原来的 `remote restore did not finish in time`。支付宝原有系统空闲白名单在启动前就是 true，关闭后仍保持；没有误删用户已有配置。另清理了上一轮已确认属于本实验PID37360的遗留转发和临时jar。当前两台设备均无本轮服务/探针残留。

## 支付宝的真实虚拟扫码结果

此次是产品透视相机模式，不是普通物理相机页。相机服务当前所有者记录确认：支付宝进程27250实际连接 `v186_1187`。扫码 Activity 为 `com.alipay.mobile.scan.as.main.MainCaptureActivity`，测试期间仍在虚拟屏幕238上存活、可见，没有重现旧版本场景切换把扫码页关闭的问题。

可见预览仍为 `com.alipay.mobile.scan.ui.APTextureView`，1440×3200，74个View。相关应用图层只有扫码 Activity 的窗口缓冲层及产品自己的镜像，没有独立相机 SurfaceView 图层。View审计仅采集元数据，不执行捕获，并明确标记未验证图层稳定性；不能拿它冒充未改View的名称过滤成功。

现有通用策略先遇到一次 `unrestorable_ancestor_background` 并重新绑定。随后 `texture-background` 显示镜像和 `window-direct` 窗口缓冲源均完成全帧 A8 验证：4,608,000像素，min=max=255，透明覆盖率0。最终缓存为 unsupported，客户端 **0帧**有效透明流。`sceneFeature=0`，未强行启用目标应用未启用的场景特性。

结论：**这版支付宝在当前虚拟相机场景仍未打通透明流**。实测支持的判断是：所试捕获路径得到不透明合成结果，TextureView相机与UI共享应用窗口，现有 SurfaceFlinger 图层过滤无法单独排除相机。仅凭此结果还不能把具体障碍唯一归因于窗口像素格式、某一个背景或硬件渲染标记。

这进一步支持“按实际预览类型、独立图层及可恢复能力分类”。小红书在虚拟相机下切到独立 SurfaceView，可以获得透明流；支付宝在虚拟相机下仍是 TextureView，需要继续研究这一类合成路径。应用名称或“进入了虚拟相机”本身都不足以判定支持。

证据均在 `research/shutdown-alipay-20261101/out/`：`Alipay-Virtual.log`、`k80-alipay-virtual-camera-client.json`、`k80-alipay-virtual-WindowTreeProbe.log`、`alipay-window-attributes.json` 以及两份 `*-cleanup.json`。未验证支付宝实际二维码业务识别。

## 交付

已验证的构建已部署到现有 `<runtime-root>/scrcpy-ez-2.4.2-rc.25-win64/scrcpy-ez-2.4.2-rc.25`，包括退出修复客户端和前一轮已验证的无额外复制编码器。GUI及版本号仍为rc25。个人配置和旧发行ZIP保留；原运行文件备份到同级 `rc25-pre-shutdownfix-20261102`。

新客户端SHA256：`0692C539D9BF1E19CB169DC863F1032A84EDB3F7FED260274751C423E1C7577A`。

新服务端SHA256：`0AFEF919793866933D0CAFD1E27612134B17C55153D5A41E1C12CA1FE9B114F5`。

部署和补丁包清单位于 `research/shutdown-alipay-20261101/out/deployment.json`；回放测试命令：`python research/shutdown-alipay-20261101/build-process-test.py before`（预期失败）、`python research/shutdown-alipay-20261101/build-process-test.py fixed`（预期成功）。

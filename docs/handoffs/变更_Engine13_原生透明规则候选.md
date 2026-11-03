# Engine 13 原生透明规则候选

用户要求把规则实际加入候选，设备测试由用户进行。本轮仅在电脑修改、构建和离线验证，未替用户操作应用或推送测试 DEX。

## 新加入的行为

- 根背景清除使用透明背景资源，避免原 ColorDrawable 就地改颜色绕过 DecorView 的背景更新流程；颜色恢复时也重新建立背景 Drawable。目标是优先获得会正常清帧的窗口，而非等很久后重建。
- 初始绑定读取系统编码结构，并按当前窗口/View 哈希关联非不透明背景、资源来源和渲染器能力。非不透明复杂背景保持原样，不再一律当作必须清除的未知背景。
- 复杂根背景优先读取原始背景资源 ID；若系统只提供“来自资源”及 Drawable 类，则从当前窗口主题解析 windowBackground 并核对 Drawable 类，使用独立的 themeResource 恢复类型和 theme-resource-class-match 证据。**后一种只验证主题资源与类型相符，不保证复现应用额外运行时着色或 Drawable 状态**。资源 ID、原始颜色、层级和 View 哈希仅保存在当前会话，不写成可跨设备执行的常量。
- SurfaceView 优先通过当前 View 的 getCompositionOrder 返回值获取原始层级，并用 setCompositionOrder 恢复；必须收到正确类型的整数，void/错误回包不当作成功。无能力时保持原来的受限恢复路径。
- 原相机 Surface/Texture 仍需叶节点约束；混淆类可尝试有限的 getter 能力发现。缺乏独立 UI 的整页 Surface 拒绝隐藏，避免 V次元式共用画面把控件一起抹除。
- 相机 Alpha 检查使用当前屏幕位置、累计缩放/旋转与祖先裁剪后的区域；3D 变换保守拒绝。窗口与显示屏允许最多 16px 且不超过 1% 的对齐差，不再因为两像素偏差直接拒绝同一来源。
- 根 Activity 内扫码在具备属性/资源恢复能力时可进入系统场景路径，不再强制要求可以结束扫描子 Activity；仅需 closeScanner 的路径仍保留子 Activity 身份检查，不会为通过验证而关闭根应用。
- Surface 通道在覆盖率从已观察最好值持续下降超过 8 个百分点后可提前尝试窗口来源，不必一直等跌到 60%；仍只尝试一次，保留窗口/UID/任务/图层身份检查。
- 场景转换只在实际改变过服务端透明状态时恢复该状态；持有安全恢复路径时不靠关闭根 Activity 回滚。失败具体错误与回滚能力写入日志，成功方案仍必须通过真实两帧与 Alpha 空间检查后才入档。

## 测试建议

使用同一 auto-rules 目录的新包。先测试支付宝、向日葵、Steam，再看米游社第一次进入是否还需要长时间等待。每个应用分别记录：首次进入有无蓝底、控件是否保留、光带是否累积、返回再进情况、关闭投屏后手机背景是否恢复。V次元共享渲染表面尚无已验证分离方案，本版不保证透明。

关注日志 NATIVE_ALPHA_ROLLBACK / NATIVE_ALPHA_CAPABILITY / NATIVE_ALPHA_DRIFT / NATIVE_ALPHA_SCENE_FAILED，以及档案 engineVersion=13、restore、restoreEvidence。主题资源候选要特别核对退出后的背景；没有创建虚假的 supported 档案。

代码依据：Android ViewDebug 的 INVOKE_METHOD 返回类型化结果、DUMP_THEME 导出资源名/路径；DecorView 的 setBackgroundDrawable 进入窗口背景更新流程。以上机制已在源码中实现，应用兼容效果待用户实测，不把离线通过当成设备适配成功。

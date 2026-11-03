# 透视相机颜色保留：行业方案调研

日期：2026-10-27。实现参照音墨仓库 RC.18 保存点 `018fc0cad93431ee1b7173e72833fbd223a7d618` / `ez-2.4.2-rc.18`。本报告是公开资料与源码分析，不代表已在 EZ 实测这些商业产品；本轮不改变运行版本和校准动画。

## 结论

有成熟产品和成熟方法。值得借鉴的是前景保护遮罩、透明度与颜色分别处理，以及直接传输 Alpha 的合成架构。本轮未查到能直接替换 EZ 现有流程、并保证免 root 的任意 Android 应用动态界面都完整保色的现成组件。

黑白多底色估计透明度有经典双背景抠像依据。当前更需要解决运行期蓝色前景与蓝色底板的歧义，单纯延长初始校准无法可靠恢复之后移动的同色扫描线。[Smith–Blinn，Blue Screen Matting，Microsoft Research](https://www.microsoft.com/en-us/research/publication/blue-screen-matting-2/)

## 产品与可借鉴部分

| 产品 / 技术 | 官方资料确认的能力 | 对 EZ 的意义与边界 |
| --- | --- | --- |
| Blackmagic Ultimatte 12 | 自动采样底色、分区域颜色处理，并有用于保留相近色前景的保护遮罩输入 | 广播实时合成的参考。可靠颜色保留不应只依赖一个全局色差阈值；额外保护遮罩本身也是输入信息，不能理解成单帧任意颜色无条件可还原。[产品与接口说明](https://www.blackmagicdesign.com/products/ultimatte) |
| Nuke / Keylight | 区分主体、内部保护区、外部排除区；透明度偏置和去底色污染可分别控制；提供保留源图并附加遮罩的中间输出 | 很适合借鉴“确定的前景先保护，透明边缘另处理”。官方以蓝底前的同色细节说明保护遮罩的用途。[保护遮罩](https://learn.foundry.com/nuke/16.0/content/comp_environment/keying_with_keylight/mattes.html)、[颜色与透明度控制](https://learn.foundry.com/nuke/content/reference_guide/keyer_nodes/keylight.html)、[中间输出](https://learn.foundry.com/nuke/14.0/content/comp_environment/keying_with_keylight/advanced_keying.html) |
| DaVinci Resolve / Fusion Delta Keyer | 使用色差方法，并可接入干净背景参考、主体保护及背景排除遮罩 | 与 EZ 的静态参考层最接近，可借鉴背景建模和分区处理，不等于直接接入整个后期软件就能自动识别第三方 UI。[官方 Resolve 20 视觉特效指南，绿幕合成章节](https://documents.blackmagicdesign.com/UserManuals/DaVinci-Resolve-20-Fusion-Visual-Effects.pdf) |
| OBS Chroma Key | 自定义底色、相似度、平滑和底色污染抑制 | 可借鉴轻量实时色键。官方也说明阈值过强会删除原本希望保留的内容；换成 OBS 的普通色键不等于解决同色前景。[官方滤镜说明](https://obsproject.com/kb/chroma-key-filter) |
| vMix / NDI 的 Alpha 与 Key/Fill | vMix 可分别输出颜色和透明度信号；NDI 的 BGRA/RGBA 格式携带独立 Alpha | 如果上游能够输出干净的 UI 颜色和透明度，便不必根据颜色猜透明度。传输协议不会自动替 EZ 从已经合成的 Android 画面里恢复 Alpha。[vMix Key/Fill](https://www.vmix.com/help28/KeyFill.html)、[NDI 帧格式](https://docs.ndi.video/all/developing-with-ndi/sdk/frame-types) |

Ultimatte 的产品页还明确区分了独立叠加半透明有色图层与传统色键。不能把“支持半透明有色图层”当成“可以从一幅任意混合画面中精确分离所有有色透明物体”。

## 为什么蓝色扫描线仍然困难

理想合成关系为：

`C = alpha * F + (1 - alpha) * B`

`C` 是观察到的 RGB，`B` 是已知底板，`F` 是未预乘的前景颜色。单帧只有三个颜色观测，而前景三个通道加透明度共有四个未知量。经典论文说明一般问题不唯一，并给出了同一前景在不同背景上观测时的解法。[原始论文说明](https://www.microsoft.com/en-us/research/publication/blue-screen-matting-2/)

对当前实现的分析：

- 多底色初始采样可以估计稳定按钮、固定半透明底板的颜色和透明度；这也是当前静态蓝色控件能得到改善的原因
- 移动扫描线不满足同一像素在多张校准图中保持不变的假设，不能直接把它冻结进静态参考，否则可能出现旧位置残留
- `app/src/gpu_matte.c` 的实时回退仍按蓝色分量估计底板贡献，再扣除该贡献。真正的蓝色动态前景也可能被计入底板，造成偏色或丢失
- 这是基于当前源码和现象的机制推断，尚未通过同帧输入/输出颜色采样独立证明蓝线偏色的全部来源；压缩、色彩矩阵/范围与预乘处理仍应分别排查

提高相机帧率只能增加时间采样；如果每次仍只有同一种底色，不能单靠帧率补足上述颜色歧义。

## 建议的验证顺序

以下属于面向 EZ 的工程建议，尚未实现，也不是产品官方对本项目的效果承诺。

1. **分开检查透明度与颜色**：保存少量同帧的回传 RGB、估计透明度和最终合成结果，用蓝/绿/红/白细线及半透明渐变确认在哪一步变色。RGB 与 Alpha 分开处理不等于简单透传混有底板的 RGB；半透明像素仍需正确扣除底板贡献，并保持预乘约定一致
2. **加入可靠的前景保护**：沿用现有多底色校准中稳定、置信度高的控件区域，保护已确认前景，避免再次全局去蓝；移动特效需要独立判断，不能照搬静态遮罩
3. **优先试运行底色自适应**：企业微信蓝色扫描线存在时，选择与当前前景冲突更小的底色，作为相机会话内部参数，不一刀切把所有应用改成绿底。保留桌面1fps/10fps选择、当前校准时长、定位动画和淡出。更换底色时，需要同步改分类、背景估计和 GPU 分离，不能只换发送颜色
4. **单底色仍不足时，再评估成对底色观测**：用时间靠近、亮度相近的不同底色增加观测信息；须处理帧标记、应用自适应界面、动画位移及置信度。双背景数学成立的条件是前景足够一致，不能直接把相邻两帧当成静止同一前景。这条路线有额外开销和错配风险，需要单独原型验证
5. **直接取得 Alpha 或独立 UI 图层作为长期路线**：Android 16 的系统源码存在按层采集和排除图层的接口，但它们标为隐藏接口。取得第三方应用的目标图层句柄、权限和干净透明度仍是待验证条件；不能因为接口存在，就承诺任意应用可免校准输出 RGBA。[Android 16 ScreenCapture 源码](https://android.googlesource.com/platform/frameworks/base/+/android16-release/core/java/android/window/ScreenCapture.java)

最小影响的下一步，是先做第1项诊断，再验证前景保护与底色自适应。当前用户认可的校准时长和动画无需为此缩短或延长。直接套用商业键控器、单独调高阈值、加强去蓝或者重置所有应用配置，都不是已得到证据支持的修复。

## 验收条件

- 蓝色扫描线保持蓝色，微信绿色扫描线也不被误删；按钮、半透明底板和细字保持可辨
- 白底、灰底、黑底桌面上没有新增固定格子、条带残影或亮度闪烁
- 拖动、缩放仍保持本地预览跟手，既有 flex 和锁屏行为不退回旧路径
- 1fps 与10fps的真实二维码识别继续通过；保留按应用/会话的隔离和现有资源上限
- 记录额外 CPU/GPU 时间和实际延迟，不能用电影后期或人像抠像演示推断第三方应用 UI 的性能及正确性

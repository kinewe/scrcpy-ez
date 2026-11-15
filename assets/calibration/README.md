# 审定的校准遮罩素材

来自用户提供的定位环图案，原图 SHA256：
`3cef41e7e620ef5b2084b2fd935b49e99e28b7b07cb636c6a4fc203ff3c05588`。

`layers/*.webp` 为用户确认并完成边缘修订的五张无损抠图，不能重新切分或重绘。`motion.json` 记录共同中心、旋转动作及最后确认的粗像素字模。运行 `tools/generate_calibration_art.py` 生成原生 PNG 资源与布局常量。

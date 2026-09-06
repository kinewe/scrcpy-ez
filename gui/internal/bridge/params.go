package bridge

// ModeParams 是单模式（usb/wifi）的参数浮窗覆盖。
// Set=false 表示该模式自动档：不注入该模式环境变量，bat 该模式走自身检测逻辑。
// Set=true 时 GUI 注入该模式的三个环境变量（如 usb → SCEZ_RES_USB/SCEZ_FPS_USB/
// SCEZ_BITRATE_USB），bat 在 scrcpy 启动前用这三个值覆盖该模式检测值
// （长边/fps/码率 Mbps）。
type ModeParams struct {
	Res     int
	FPS     int
	Bitrate int
	Set     bool
}

// CastParams 投屏参数覆盖（多设备 Phase 1 轮 A 起扩展为四类注入）：
//   - Usb.Set=true → 注入 SCEZ_RES_USB/SCEZ_FPS_USB/SCEZ_BITRATE_USB（bat 有线分支覆盖）；
//   - Wifi.Set=true → 注入 SCEZ_RES_WIFI/SCEZ_FPS_WIFI/SCEZ_BITRATE_WIFI（bat 无线分支覆盖）；
//   - Serial 非空 → 注入 SCEZ_SERIAL（锁定目标设备：USB serial 或 IP:port）；
//   - Addr 非空 → 注入 SCEZ_ADDR（锁定主无线地址，跳过 config 记忆/扫描）；
//   - Addr2 非空 → 注入 SCEZ_ADDR2（备用无线地址，与主地址不同形态；bat 单次降级用）；
//   - NoWatch=true → 注入 SCEZ_NO_WATCH=1（已废弃：watcher 会话化后不再需要，
//     字段保留仅用于 bat 兼容——GUI 不再生成该值，恒 false）；
//   - Market/Model 非空 → 注入 SCEZ_MARKET/SCEZ_MODEL（档案身份：bat 无线回退防抢
//     比对与 watcher 比对本尊用；仅锁定会话注入）。
//
// 未设置（空/零值）的字段不注入，bat 走原逻辑（回归兼容）。
type CastParams struct {
	Usb     ModeParams
	Wifi    ModeParams
	Serial  string
	Addr    string
	Addr2   string // SCEZ_ADDR2（备用无线地址；空=无备用）
	NoWatch bool
	Market  string // SCEZ_MARKET（档案 marketname，仅锁定会话）
	Model   string // SCEZ_MODEL（档案 model，仅锁定会话）
}

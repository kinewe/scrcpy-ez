# 🖥️📱 scrcpy-ez — Easy Android Screen Mirroring, Enhanced

> **English | [中文](README.md)**

> An enhanced build based on [scrcpy 5.0.1](https://github.com/Genymobile/scrcpy) for PC ↔ Android mirroring. **Plug and play, zero hassle.**

> ✨ **Entirely developed by AI** (code and docs written by AI)
> ⚠️ **Tested on**: Windows 11 only (other versions unverified)

---

## 🚀 Quick Start

### Step 1: Enable permissions

Enter **Developer Mode** on your phone/tablet, enable **USB Debugging**, and allow simulating click actions via USB debugging (e.g. Xiaomi devices also need **USB debugging (Security settings)**).

<p align="center"><img src="images/setup-permissions.jpeg" alt="Enable permissions" width="600"></p>

### Step 2: Launch

Download the latest package from [Releases](https://github.com/kinewe/scrcpy-ez/releases), extract it, and double-click **`scrcpy-ez.exe`** to open the GUI.

<p align="center"><img src="images/gui-main.png" alt="GUI main" width="480"></p>

### Step 3: Pair a device (either way)

**Option A: USB cable pairing (recommended for first time)**

Connect the phone/tablet to the PC with a USB cable, **allow the authorization dialog** on the device, and the paired device appears in the GUI device list.

**Option B: Wireless debugging pairing (no cable needed)**

1. Enable **Developer options → Wireless debugging** on the device, and allow the authorization dialog
2. In the GUI, click **Pair new device from "Wireless debugging"**
3. Choose either of:
   - **QR code pairing**: scan the QR code shown in the GUI to pair automatically
   - **Pairing code pairing**: enter the pairing code plus IP address and port shown on the device
4. If you see **"No pending device found"**, you can manually enter the address and pairing code shown on the device

### Step 4: Start mirroring

Click the **Mirror** button on the device card. Once paired, no re-pairing is needed for later sessions (unless you remove the device).

- 🔌 **Keep the cable plugged in** → USB mode (higher specs)
- 📡 **Unplug the cable** → automatically switches to wireless (LAN) mode

---

## ✨ Key Features

### 🖥️ GUI Device Management

Paired devices are discovered automatically and shown as cards, **with multiple devices mirroring simultaneously**. Right-click a card to remove pairing info; the batch management view supports batch removal, renaming and batch mirroring.

<p align="center"><img src="images/gui-devices.png" alt="Device list" width="420">&nbsp;<img src="images/gui-batch.png" alt="Batch management" width="420"></p>

### ⌨️ Keyboard Friendly

No extra setup needed — type directly into the mirroring window with your computer keyboard.

<p align="center"><img src="images/typing-1.jpeg" alt="Keyboard friendly 1" width="360"></p>

<p align="center"><img src="images/typing-2.jpeg" alt="Keyboard friendly 2"></p>

> Note: **Android 13+** supports UHID keyboard; on lower versions direct typing is not available.

### 🖼️ Image Clipboard

Inspired by scrcpy PR #6676 — not only text: **images** copied on the PC can enter the device clipboard as well.

<p align="center"><img src="images/clipboard-1.jpeg" alt="Image clipboard" width="1100"></p>

In apps that support clipboard images you can **paste and send** (long-press the input field and choose paste / Ctrl+V), e.g. WeChat:

<p align="center"><img src="images/clipboard-wechat.jpeg" alt="WeChat paste example" width="600"></p>

> Note: mobile apps may crop/convert clipboard images, so direct paste can be blurry (and animated images won't move) — in that case press **Ctrl+G** to save the image to the device gallery, then send the original from the gallery. (Gallery saving requires Android 7.0+, clipboard may not display on some devices.)

<p align="center"><img src="images/clipboard-gallery.jpeg" alt="Save to gallery example" width="700"></p>

> Save it, then pick the image from the gallery and send!

### 🎯 Responsive Mirroring

Adaptive **frame rate / bitrate** keeps the image responsive (not game-grade, not designed for that), greatly reducing the latency feel. Temporary frame drops or blurring during use are **normal** (the system adjusts to load).

> Note: older devices (Android 9 or below) are fixed to low refresh rate and bitrate.

### 🎛️ Spec Customization

Open the **Specs panel** to adjust the mirroring parameters for the current device and connection mode. Wired and wireless modes are independent, and each device keeps its own settings.

**2.4.0 now uses scrcpy 5.0.1.** After upgrading, open the **Specs panel** and select higher resolution, frame-rate and bitrate settings as your hardware allows. Existing settings are preserved; configure USB and wireless independently.

<p align="center"><img src="images/gui-spec.png" alt="Specs panel" width="480"></p>

### 🔔 Phone Notifications and Verification Codes

New phone notifications can appear in Windows whenever a paired device has an active ADB connection. **Mirroring and a companion phone app are not required.** Configure global, per-device and per-app rules, code-only delivery, message previews and copy retention. Notification management is available from the app-window panel and batch management.

Copy a detected code by clicking its body or copy button. On Xiaomi system SMS notifications, a separate code field may remain available while the phone is locked; ez can display and copy that field. If both text and code are hidden, ez only has the system-supplied summary, and code-only mode skips notifications without a recognizable code. This depends on the ROM and source app; tested on REDMI K80 / Android 16, with other brands not yet individually verified.

### 🔋 Keep the Phone Awake During Main Mirroring

The global **keep-awake setting is enabled by default** for wired and wireless main mirroring on all devices. Changes apply after restarting main mirroring. Ending main mirroring restores normal idle sleep; app mirroring does not enable keep-awake. **Ctrl+P** still locks or wakes the phone manually, and holding the shortcut does not send a long power-button press.

### 🔧 Upload Permission Repair for Rooted Devices

Normal devices retain the usual startup flow. On an already rooted device, enable automatic root repair in its specs panel. Repair starts only after a server upload permission error and requires authorizing the Shell `su` request on the phone. It repairs the server directory/file permissions and labels, then runs the server as ordinary Shell. Automated regression tests pass; repair on a physical rooted device remains unverified.

### 🎮 Shortcuts

| Shortcut | Function |
|---|---|
| **Ctrl+F** | Toggle the overlay controls (drag them while holding **Alt**) |
| **Ctrl+G** | Save the image copied on the PC to the device gallery |
| **Ctrl+H** | Black out the device screen to save power (the device may enter power-saving mode limiting the refresh rate) |
| **Ctrl+P** | Short press of the phone power button to lock or wake it, in main or app mirroring |
| **Ctrl+T** | Toggle always-on-top for the mirroring window (or hold **Alt** and click the indicator on the left of the controls, same effect) |
| **Alt+F** | Fullscreen |

> 💡 **Pin indicator**: the small light on the left of the overlay controls — orange when pinned, grey when not. Hold **Alt** and click the indicator to toggle pinning (same as **Ctrl+T**); click again to unpin.

<p align="center"><img src="images/overlay-indicator-1.png" alt="Pin indicator 1" width="380"></p>

<p align="center"><img src="images/overlay-indicator-2.png" alt="Pin indicator 2" width="380"></p>

### 📲 Device Notification & Stop from Phone

While mirroring, a persistent "scrcpy-ez is mirroring" notification appears on the phone (tablet) — **tap it to stop mirroring directly from the device**, no need to return to the PC. The notification disappears automatically when mirroring stops.

<p align="center"><img src="images/notify-stop.jpeg" alt="Device notification" width="600"></p>

---

## 📜 Version History

| Version | Features |
|---|---|
| **[v2.4.0](https://github.com/kinewe/scrcpy-ez/releases/tag/v2.4.0)** | Upgrade to **scrcpy 5.0.1**, default hardware decoding and the new video buffer, with updated FFmpeg / SDL. Choose higher resolution, frame-rate and bitrate settings in the Specs panel. Add per-device notification-to-mirroring actions (OTP clicks still copy only), improve notification artwork, orientation and consecutive WeChat banners, and reuse compatible background application pages with confirmation for active or incompatible apps. Remove runtime PowerShell calls in favor of native Windows COM/WMI and event supervision; retain image clipboard, ABR, multiple devices, notifications and main-mirroring keep-awake. See the [release notes](docs/release-2.4.0.md) |
| **[v2.3.0](https://github.com/kinewe/scrcpy-ez/releases/tag/v2.3.0)** | Phone notification sync with per-device/app rules, code-only mode, previews and copy retention; Xiaomi SMS code metadata on locked phones where available, with no companion app; default global keep-awake for main mirroring only and Ctrl+P power-button shortcut; optional authorized root repair after upload permission failures; background reconnect for paired devices, notification recovery and battery refresh. See [release notes](docs/release-2.3.0.md) and the [Gitee download](https://gitee.com/kinewe/scrcpy-ez/releases/tag/v2.3.0) |
| **v2.2.2** | Event-driven USB switching without 2-second polling; fixes missed batch starts and delayed device cards; improves profile identity/address isolation and application icon caching; protects Xiaomi/REDMI/POCO physical-screen gestures and widget sizes during application mirroring, with a compatibility setting for other brands; coordinates multi-window image/text clipboard and keeps Windows images pasteable after all sessions close; consistent UHID/SDK labels and clearer settings; fixes repeated update-success notices and bundled protocol mismatches in application list/icon queries; keeps device renaming focused and uninterrupted during mirroring |
| **v2.2.1** | In-app updates with automatic GitHub / Gitee source selection, resumable downloads, restart installation and rollback; improved USB learning, wireless address synchronization, app-list refresh and UI layout |
| **v2.2.0** | New "App windows": run a single phone app in its own desktop window (multi-open / window-follow / UI density / per-app settings memory); selectable A/V codecs — H.264/H.265/AV1/VP8/VP9 × Opus/AAC/FLAC/RAW; fixed pairing/startup "operation failed" caused by old adb servers |
| **v2.1.0** | Device-side "Now mirroring" notification (tap to stop mirroring); lower resource usage; new "Settings" panel: adjust default on-screen controls visibility; minimize to tray on window close (mirroring keeps running) |
| **v2.0** | New GUI: device cards, multi-device parallel mirroring, batch management, wireless QR/pairing-code pairing, independent wired/wireless specs, TLS indicator |
| **v1.0.1** | Ctrl+T always-on-top toggle, glowing indicator on the left of overlay controls (orange when pinned / grey when not; hold Alt and click it to toggle) |
| **v1.0** | Initial release: keyboard friendly (UHID), image clipboard (PR #6676 + Ctrl+G to gallery), responsive mirroring (adaptive ABR), TSF input method, legacy device support, mirroring loop + USB/WiFi auto-switch |

---

## 🛠️ Build from source

```bash
# Server (Android side):
cd server && export JAVA_HOME=/path/to/jdk17
gradle --no-daemon assembleRelease
# Output → copy as dist/scrcpy-server

# Client (Windows side):
ninja -C build
# Output → copy as dist/scrcpy.exe
```

> The Windows GUI is built via GitHub Actions (see `.github/workflows/gui-win-build.yml`), artifact `scrcpy-ez-win64`.

---

## 🙏 Acknowledgments

- [scrcpy](https://github.com/Genymobile/scrcpy) (Genymobile) — the foundation of this project, Apache License 2.0
- [yume-chan's PR #6676 (Support image clipboard)](https://github.com/Genymobile/scrcpy/pull/6676) — the image clipboard feature is inspired by this PR

---

> 💻 **Requirements**: Windows 10 / 11 (64-bit). The GUI relies on the Microsoft WebView2 Runtime — included in Windows 11; most Windows 10 systems already have it via system updates (if missing, install the Evergreen Runtime from [Microsoft](https://developer.microsoft.com/microsoft-edge/webview2/)). All other components are bundled — no extra runtimes needed.

## 📄 License

Apache License 2.0 (inherited from scrcpy upstream).

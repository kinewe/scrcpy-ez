package com.genymobile.scrcpy.device;

import com.genymobile.scrcpy.AndroidVersions;
import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.display.DisplayInfo;
import com.genymobile.scrcpy.model.DeviceApp;
import com.genymobile.scrcpy.util.Ln;
import com.genymobile.scrcpy.wrappers.ClipboardManager;
import com.genymobile.scrcpy.wrappers.ClipboardManager.ClipboardImage;
import com.genymobile.scrcpy.wrappers.DisplayControl;
import com.genymobile.scrcpy.wrappers.InputManager;
import com.genymobile.scrcpy.wrappers.ServiceManager;
import com.genymobile.scrcpy.wrappers.SurfaceControl;
import com.genymobile.scrcpy.wrappers.WindowManager;

import android.annotation.SuppressLint;
import android.app.ActivityOptions;
import android.content.Intent;
import android.content.pm.ApplicationInfo;
import android.content.pm.PackageManager;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.drawable.BitmapDrawable;
import android.graphics.drawable.Drawable;
import android.os.Build;
import android.os.Bundle;
import android.os.IBinder;
import android.os.SystemClock;
import android.view.InputDevice;
import android.view.InputEvent;
import android.view.KeyCharacterMap;
import android.view.KeyEvent;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

public final class Device {
    public static final String WECHAT_PACKAGE = "com.tencent.mm";

    public static final int DISPLAY_ID_NONE = -1;

    public static final int POWER_MODE_OFF = SurfaceControl.POWER_MODE_OFF;
    public static final int POWER_MODE_NORMAL = SurfaceControl.POWER_MODE_NORMAL;

    public static final int INJECT_MODE_ASYNC = InputManager.INJECT_INPUT_EVENT_MODE_ASYNC;
    public static final int INJECT_MODE_WAIT_FOR_RESULT = InputManager.INJECT_INPUT_EVENT_MODE_WAIT_FOR_RESULT;
    public static final int INJECT_MODE_WAIT_FOR_FINISH = InputManager.INJECT_INPUT_EVENT_MODE_WAIT_FOR_FINISH;

    // The new display power method introduced in Android 15 does not work as expected:
    // <https://github.com/Genymobile/scrcpy/issues/5530>
    private static final boolean USE_ANDROID_15_DISPLAY_POWER = false;

    private Device() {
        // not instantiable
    }

    public static String getDeviceName() {
        return Build.MODEL;
    }

    public static boolean supportsInputEvents(int displayId) {
        // main display or any display on Android >= 10
        return displayId == 0 || Build.VERSION.SDK_INT >= AndroidVersions.API_29_ANDROID_10;
    }

    public static boolean injectEvent(InputEvent inputEvent, int displayId, int injectMode) {
        if (!supportsInputEvents(displayId)) {
            throw new AssertionError("Could not inject input event if !supportsInputEvents()");
        }

        if (displayId != 0 && !InputManager.setDisplayId(inputEvent, displayId)) {
            return false;
        }

        return ServiceManager.getInputManager().injectInputEvent(inputEvent, injectMode);
    }

    public static boolean injectKeyEvent(int action, int keyCode, int repeat, int metaState, int displayId, int injectMode) {
        long now = SystemClock.uptimeMillis();
        KeyEvent event = new KeyEvent(now, now, action, keyCode, repeat, metaState, KeyCharacterMap.VIRTUAL_KEYBOARD, 0, 0,
                InputDevice.SOURCE_KEYBOARD);
        return injectEvent(event, displayId, injectMode);
    }

    public static boolean pressReleaseKeycode(int keyCode, int displayId, int injectMode) {
        return injectKeyEvent(KeyEvent.ACTION_DOWN, keyCode, 0, 0, displayId, injectMode)
                && injectKeyEvent(KeyEvent.ACTION_UP, keyCode, 0, 0, displayId, injectMode);
    }

    public static boolean isScreenOn(int displayId) {
        assert displayId != DISPLAY_ID_NONE;
        return ServiceManager.getPowerManager().isScreenOn(displayId);
    }

    public static void expandNotificationPanel() {
        ServiceManager.getStatusBarManager().expandNotificationsPanel();
    }

    public static void expandSettingsPanel() {
        ServiceManager.getStatusBarManager().expandSettingsPanel();
    }

    public static void collapsePanels() {
        ServiceManager.getStatusBarManager().collapsePanels();
    }

    public static String getClipboardText() {
        ClipboardManager clipboardManager = ServiceManager.getClipboardManager();
        if (clipboardManager == null) {
            return null;
        }
        CharSequence s = clipboardManager.getText();
        if (s == null) {
            return null;
        }
        return s.toString();
    }

    public static ClipboardImage getClipboardImage(int imageLimit) {
        ClipboardManager clipboardManager = ServiceManager.getClipboardManager();
        if (clipboardManager == null) {
            return null;
        }
        return clipboardManager.getImage(imageLimit);
    }

    public static boolean setClipboardText(String text, long epoch, long version) {
        ClipboardManager clipboardManager = ServiceManager.getClipboardManager();
        if (clipboardManager == null) {
            return false;
        }

        return clipboardManager.setText(text, epoch, version);
    }

    public static boolean setClipboardImage(byte[] imageData, String mimeType, long epoch, long version) {
        ClipboardManager clipboardManager = ServiceManager.getClipboardManager();
        if (clipboardManager == null) {
            return false;
        }

        return clipboardManager.setImage(imageData, mimeType, epoch, version);
    }

    public static boolean setDisplayPower(int displayId, boolean on) {
        assert displayId != Device.DISPLAY_ID_NONE;

        if (USE_ANDROID_15_DISPLAY_POWER && Build.VERSION.SDK_INT >= AndroidVersions.API_35_ANDROID_15) {
            return ServiceManager.getDisplayManager().requestDisplayPower(displayId, on);
        }

        boolean applyToMultiPhysicalDisplays = Build.VERSION.SDK_INT >= AndroidVersions.API_29_ANDROID_10;

        if (applyToMultiPhysicalDisplays
                && Build.VERSION.SDK_INT >= AndroidVersions.API_34_ANDROID_14
                && Build.BRAND.equalsIgnoreCase("honor")
                && SurfaceControl.hasGetBuildInDisplayMethod()) {
            // Workaround for Honor devices with Android 14:
            //  - <https://github.com/Genymobile/scrcpy/issues/4823>
            //  - <https://github.com/Genymobile/scrcpy/issues/4943>
            applyToMultiPhysicalDisplays = false;
        }

        int mode = on ? POWER_MODE_NORMAL : POWER_MODE_OFF;
        if (applyToMultiPhysicalDisplays) {
            // On Android 14, these internal methods have been moved to DisplayControl
            boolean useDisplayControl =
                    Build.VERSION.SDK_INT >= AndroidVersions.API_34_ANDROID_14 && !SurfaceControl.hasGetPhysicalDisplayIdsMethod();

            // Change the power mode for all physical displays
            long[] physicalDisplayIds = useDisplayControl ? DisplayControl.getPhysicalDisplayIds() : SurfaceControl.getPhysicalDisplayIds();
            if (physicalDisplayIds == null) {
                Ln.e("Could not get physical display ids");
                return false;
            }

            boolean allOk = true;
            for (long physicalDisplayId : physicalDisplayIds) {
                IBinder binder = useDisplayControl ? DisplayControl.getPhysicalDisplayToken(
                        physicalDisplayId) : SurfaceControl.getPhysicalDisplayToken(physicalDisplayId);
                allOk &= SurfaceControl.setDisplayPowerMode(binder, mode);
            }
            return allOk;
        }

        // Older Android versions, only 1 display
        IBinder d = SurfaceControl.getBuiltInDisplay();
        if (d == null) {
            Ln.e("Could not get built-in display");
            return false;
        }
        return SurfaceControl.setDisplayPowerMode(d, mode);
    }

    public static boolean powerOffScreen(int displayId) {
        assert displayId != DISPLAY_ID_NONE;

        if (!isScreenOn(displayId)) {
            return true;
        }
        return pressReleaseKeycode(KeyEvent.KEYCODE_POWER, displayId, Device.INJECT_MODE_ASYNC);
    }

    public static IBinder getSinglePhysicalDisplayToken() {
        boolean useDisplayControl = Build.VERSION.SDK_INT >= AndroidVersions.API_34_ANDROID_14 && !SurfaceControl.hasGetPhysicalDisplayIdsMethod();
        long[] ids = useDisplayControl ? DisplayControl.getPhysicalDisplayIds() : SurfaceControl.getPhysicalDisplayIds();
        if (ids == null || ids.length != 1) {
            return null;
        }
        return useDisplayControl ? DisplayControl.getPhysicalDisplayToken(ids[0]) : SurfaceControl.getPhysicalDisplayToken(ids[0]);
    }

    /**
     * Disable auto-rotation (if enabled), set the screen rotation and re-enable auto-rotation (if it was enabled).
     */
    public static void rotateDevice(int displayId) {
        assert displayId != DISPLAY_ID_NONE;

        WindowManager wm = ServiceManager.getWindowManager();

        boolean accelerometerRotation = !wm.isRotationFrozen(displayId);

        int currentRotation = getCurrentRotation(displayId);
        int newRotation = (currentRotation & 1) ^ 1; // 0->1, 1->0, 2->1, 3->0
        String newRotationString = newRotation == 0 ? "portrait" : "landscape";

        Ln.i("Device rotation requested: " + newRotationString);
        wm.freezeRotation(displayId, newRotation);

        // restore auto-rotate if necessary
        if (accelerometerRotation) {
            wm.thawRotation(displayId);
        }
    }

    private static int getCurrentRotation(int displayId) {
        assert displayId != DISPLAY_ID_NONE;

        if (displayId == 0) {
            return ServiceManager.getWindowManager().getRotation();
        }

        DisplayInfo displayInfo = ServiceManager.getDisplayManager().getDisplayInfo(displayId);
        return displayInfo.getRotation();
    }

    public static List<DeviceApp> listApps() {
        List<DeviceApp> apps = new ArrayList<>();
        PackageManager pm = FakeContext.get().getPackageManager();
        for (ApplicationInfo appInfo : getLaunchableApps(pm)) {
            apps.add(toApp(pm, appInfo));
        }

        return apps;
    }

    // ez 自定义（二期 Step 1c）：应用图标 PNG 导出目录（shell 身份可写）。
    private static final String ICONS_DIR = "/data/local/tmp/scrcpy/icons";

    // 图标统一缩到长边 ≤144px（桌面网格显示够用，体积小）。
    private static final int ICON_MAX_SIZE = 144;

    // 临时诊断（实验）：图标导出耗时拆解（纳秒累计；exportAppIcons 末尾输出后清零）。
    private static long tmGetNs, tmDrawNs, tmScaleNs, tmEncodeNs, tmTotalNs;

    /**
     * ez 自定义：导出全部可启动应用的图标为 PNG（<pkg>.png），
     * 供桌面端 adb pull 后本地缓存使用。全量重导（先清旧目录）。
     * @return 成功导出的数量
     */
    public static int exportAppIcons() {
        return exportAppIcons(null);
    }

    /**
     * ez 自定义（v2.1.16）：导出应用图标 PNG（<pkg>.png）。
     * @param onlyPkgs null = 全量（先清空目录，卸载残留自然清）；
     *                 非 null = 逗号分隔包名清单（定向：保留其他文件，只覆盖指定包；
     *                 已卸载的包自动跳过）
     * @return 成功导出的数量
     */
    public static int exportAppIcons(String onlyPkgs) {
        return exportAppIcons(onlyPkgs, null);
    }

    public static int exportAppIcons(String onlyPkgs, String directory) {
        PackageManager pm = FakeContext.get().getPackageManager();
        File baseDir = new File(directory == null ? ICONS_DIR : directory);
        if (onlyPkgs == null) {
            // 全量：先清旧目录
            File[] olds = baseDir.listFiles();
            if (olds != null) {
                for (File f : olds) {
                    //noinspection ResultOfMethodCallIgnored
                    f.delete();
                }
            }
            if (!baseDir.exists() && !baseDir.mkdirs()) {
                Ln.e("Could not create icons directory: " + baseDir);
                return 0;
            }

            int ok = 0;
            for (ApplicationInfo appInfo : getLaunchableApps(pm)) {
                File out = new File(baseDir, appInfo.packageName + ".png");
                if (saveAppIconPng(appInfo.packageName, out)) {
                    ok++;
                }
            }
            logIconTiming(ok);
            return ok;
        }
        // 定向：不清目录（保留其他图标），只覆盖指定包
        if (!baseDir.exists() && !baseDir.mkdirs()) {
            Ln.e("Could not create icons directory: " + baseDir);
            return 0;
        }
        int ok = 0;
        String[] requested = onlyPkgs.split(",");
        AppCatalog.progress("icons", 0, requested.length);
        int attempted = 0;
        for (String raw : requested) {
            String pkg = raw.trim();
            if (!pkg.matches("[A-Za-z0-9_]+(?:\\.[A-Za-z0-9_]+)*")) {
                continue;
            }
            File out = new File(baseDir, pkg + ".png");
            if (saveAppIconPng(pkg, out)) {
                ok++;
            }
            attempted++;
            AppCatalog.progress("icons", attempted, requested.length);
        }
        logIconTiming(ok);
        return ok;
    }

    // 临时诊断（实验）：输出图标导出耗时拆解并清零累计。
    private static void logIconTiming(int apps) {
        Ln.i("ICON_TIMING apps=" + apps
                + " get=" + (tmGetNs / 1000000) + "ms draw=" + (tmDrawNs / 1000000)
                + "ms scale=" + (tmScaleNs / 1000000) + "ms encode=" + (tmEncodeNs / 1000000)
                + "ms total=" + (tmTotalNs / 1000000) + "ms");
        tmGetNs = tmDrawNs = tmScaleNs = tmEncodeNs = tmTotalNs = 0;
    }

    @SuppressLint("QueryPermissionsNeeded")
    private static Drawable getAppIcon(String packageName) {
        PackageManager pm = FakeContext.get().getPackageManager();
        try {
            ApplicationInfo appInfo = pm.getApplicationInfo(packageName, 0);
            return pm.getApplicationIcon(appInfo);
        } catch (PackageManager.NameNotFoundException e) {
            Ln.e("Package not found: " + packageName);
            return null;
        }
    }

    private static boolean saveAppIconPng(String packageName, File outFile) {
        long t0 = System.nanoTime();
        List<Bitmap> owned = new ArrayList<>();
        try {
            Drawable icon = getAppIcon(packageName);
            long tGet = System.nanoTime();
            if (icon == null) { return false; }
            Bitmap bitmap;
            if (icon instanceof BitmapDrawable) {
                bitmap = ((BitmapDrawable) icon).getBitmap();
            } else {
                int[] target = IconDimensions.fit(icon.getIntrinsicWidth(), icon.getIntrinsicHeight(), ICON_MAX_SIZE);
                bitmap = Bitmap.createBitmap(target[0], target[1], Bitmap.Config.ARGB_8888);
                owned.add(bitmap);
                Canvas canvas = new Canvas(bitmap);
                icon.setBounds(0, 0, canvas.getWidth(), canvas.getHeight());
                icon.draw(canvas);
            }
            long tDraw = System.nanoTime();
            int w = bitmap.getWidth();
            int h = bitmap.getHeight();
            if (w > ICON_MAX_SIZE || h > ICON_MAX_SIZE) {
                float scale = Math.min((float) ICON_MAX_SIZE / w, (float) ICON_MAX_SIZE / h);
                int nw = Math.max(1, Math.round(w * scale));
                int nh = Math.max(1, Math.round(h * scale));
                Bitmap scaled = Bitmap.createScaledBitmap(bitmap, nw, nh, true);
                if (scaled != bitmap) {
                    bitmap = scaled;
                    owned.add(bitmap);
                }
            }
            // v2.1.17：裁掉最外 2px——去掉系统渲染的灰色描边（1px 灰 + 抗锯齿余量），
            // 显示端回到 100%（内容等效放大 ~3%，换边缘干净、接近原始观感）。
            if (bitmap.getWidth() > 8 && bitmap.getHeight() > 8) {
                bitmap = Bitmap.createBitmap(bitmap, 2, 2, bitmap.getWidth() - 4, bitmap.getHeight() - 4);
                owned.add(bitmap);
            }
            long tScale = System.nanoTime();
            FileOutputStream fos = null;
            boolean ok = false;
            try {
                fos = new FileOutputStream(outFile);
                ok = bitmap.compress(Bitmap.CompressFormat.PNG, 100, fos);
                fos.flush();
            } finally {
                if (fos != null) {
                    try {
                        fos.close();
                    } catch (IOException ignored) {
                        // ignore
                    }
                }
            }
            long tEnd = System.nanoTime();
            tmGetNs += tGet - t0;
            tmDrawNs += tDraw - tGet;
            tmScaleNs += tScale - tDraw;
            tmEncodeNs += tEnd - tScale;
            tmTotalNs += tEnd - t0;
            return ok;
        } catch (Exception | OutOfMemoryError e) {
            Ln.e("Error saving icon for " + packageName + ": " + e.getMessage());
            return false;
        } finally {
            // Framework-owned BitmapDrawable data is never recycled here.
            for (Bitmap bitmap : owned) {
                if (!bitmap.isRecycled()) { bitmap.recycle(); }
            }
        }
    }

    @SuppressLint("QueryPermissionsNeeded")
    private static List<ApplicationInfo> getLaunchableApps(PackageManager pm) {
        List<ApplicationInfo> result = new ArrayList<>();
        for (ApplicationInfo appInfo : pm.getInstalledApplications(PackageManager.GET_META_DATA)) {
            if (appInfo.enabled && getLaunchIntent(pm, appInfo.packageName) != null) {
                result.add(appInfo);
            }
        }

        return result;
    }

    public static Intent getLaunchIntent(PackageManager pm, String packageName) {
        Intent launchIntent = pm.getLaunchIntentForPackage(packageName);
        if (launchIntent != null) {
            return launchIntent;
        }

        return pm.getLeanbackLaunchIntentForPackage(packageName);
    }

    private static DeviceApp toApp(PackageManager pm, ApplicationInfo appInfo) {
        String name = pm.getApplicationLabel(appInfo).toString();
        boolean system = (appInfo.flags & ApplicationInfo.FLAG_SYSTEM) != 0;
        return new DeviceApp(appInfo.packageName, name, system);
    }

    @SuppressLint("QueryPermissionsNeeded")
    public static DeviceApp findByPackageName(String packageName) {
        PackageManager pm = FakeContext.get().getPackageManager();
        // No need to filter by "launchable" apps, an error will be reported on start if the app is not launchable
        for (ApplicationInfo appInfo : pm.getInstalledApplications(PackageManager.GET_META_DATA)) {
            if (packageName.equals(appInfo.packageName)) {
                return toApp(pm, appInfo);
            }
        }

        return null;
    }

    @SuppressLint("QueryPermissionsNeeded")
    public static List<DeviceApp> findByName(String searchName) {
        List<DeviceApp> result = new ArrayList<>();
        searchName = searchName.toLowerCase(Locale.getDefault());

        PackageManager pm = FakeContext.get().getPackageManager();
        for (ApplicationInfo appInfo : getLaunchableApps(pm)) {
            String name = pm.getApplicationLabel(appInfo).toString();
            if (name.toLowerCase(Locale.getDefault()).startsWith(searchName)) {
                boolean system = (appInfo.flags & ApplicationInfo.FLAG_SYSTEM) != 0;
                result.add(new DeviceApp(appInfo.packageName, name, system));
            }
        }

        return result;
    }

    public static void startApp(String packageName, int displayId, boolean forceStop) {
        PackageManager pm = FakeContext.get().getPackageManager();

        Intent launchIntent = getLaunchIntent(pm, packageName);
        if (launchIntent == null) {
            Ln.w("Cannot create launch intent for app " + packageName);
            return;
        }

        if (forceStop) {
            ServiceManager.getActivityManager().forceStopPackage(packageName);
        }
        startActivityOnDisplay(launchIntent, displayId);
    }

    public static boolean startWeChatScanner(int displayId) {
        // Match the dynamic "扫一扫" shortcut published by WeChat's launcher.
        Intent intent = new Intent("com.tencent.mm.ui.ShortCutDispatchAction")
                .setPackage(WECHAT_PACKAGE)
                .putExtra("LauncherUI.Shortcut.LaunchType", "launch_type_scan_qrcode");
        int result = startActivityOnDisplay(intent, displayId);
        if (result < 0) {
            Ln.w("WeChat Scanner shortcut launch failed: " + result);
        }
        return result >= 0;
    }

    private static int startActivityOnDisplay(Intent intent, int displayId) {
        intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);

        Bundle options = null;
        if (Build.VERSION.SDK_INT >= AndroidVersions.API_26_ANDROID_8_0) {
            ActivityOptions launchOptions = ActivityOptions.makeBasic();
            launchOptions.setLaunchDisplayId(displayId);
            options = launchOptions.toBundle();
        }

        return ServiceManager.getActivityManager().startActivity(intent, options);
    }

    public static void sendBroadcast(Intent intent) {
        ServiceManager.getActivityManager().sendBroadcast(intent);
    }
}

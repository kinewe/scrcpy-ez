package com.genymobile.scrcpy;

import com.genymobile.scrcpy.device.Device;
import com.genymobile.scrcpy.power.AppPowerSave;
import com.genymobile.scrcpy.power.MainDisplayBacklight;
import com.genymobile.scrcpy.util.Ln;
import com.genymobile.scrcpy.util.Settings;
import com.genymobile.scrcpy.util.SettingsException;
import com.genymobile.scrcpy.wrappers.ServiceManager;

import android.os.BatteryManager;
import android.os.Looper;
import android.system.ErrnoException;
import android.system.Os;

import java.io.File;
import java.io.IOException;
import java.io.OutputStream;

/**
 * Handle the cleanup of scrcpy, even if the main process is killed.
 * <p>
 * This is useful to restore some state when scrcpy is closed, even on device disconnection (which kills the scrcpy process).
 */
public final class CleanUp {

    // Dynamic options
    private static final int PENDING_CHANGE_DISPLAY_POWER = 1 << 0;
    private static final int PENDING_PERSPECTIVE_ASSOCIATION = 1 << 1;
    private static final int PENDING_TOGGLE_BACKLIGHT = 1 << 2;
    private static final int PENDING_RESTORE_BACKLIGHT = 1 << 3;
    private static final int PENDING_AUTO_BLACKOUT = 1 << 4;
    private static final int TOGGLE_BACKLIGHT = 3;
    private static final int RESTORE_BACKLIGHT = 4;
    private static final int AUTO_BLACKOUT = 5;
    private int pendingChanges;
    private boolean pendingRestoreDisplayPower;

    private String perspectiveMac;
    private final AppPowerSave appPowerSave;
    private Thread thread;
    private boolean interrupted;

    private CleanUp(Options options) {
        AppPowerSave powerSave = null;
        if (options.getNewDisplay() != null) {
            try {
                powerSave = new AppPowerSave();
            } catch (Exception e) {
                Ln.w("SCRCPY_EZ_APP_POWER_SAVE action=unavailable reason=" + e);
            }
        }
        appPowerSave = powerSave;
        thread = new Thread(() -> runCleanUp(options), "cleanup");
        thread.start();
    }

    public static CleanUp start(Options options) {
        return new CleanUp(options);
    }

    public void interrupt() {
        if (appPowerSave != null) {
            appPowerSave.close();
        }
        // Do not use thread.interrupt() because only the wait() call must be interrupted, not Command.exec()
        synchronized (this) {
            interrupted = true;
            notify();
        }
    }

    public void registerAppPowerSaveDisplay(int displayId) {
        if (appPowerSave != null) {
            appPowerSave.registerDisplay(displayId);
        }
    }

    public void acquireAppPowerSave(String pkg, int displayId) {
        if (appPowerSave != null) {
            appPowerSave.acquire(pkg, displayId);
        }
    }

    public boolean hasAppPowerSave() {
        return appPowerSave != null;
    }

    public void resumeAppPowerSave() {
        if (appPowerSave != null) {
            appPowerSave.resume();
        }
    }

    public void suspendAppPowerSave() throws Exception {
        if (appPowerSave != null) {
            appPowerSave.suspend();
        }
    }

    public void closeAppPowerSave() {
        if (appPowerSave != null) {
            appPowerSave.close();
        }
    }

    public void join() throws InterruptedException {
        thread.join();
    }

    /** Bounded join: a stuck cleanup stage must not keep the server process
     *  alive (the independent cleanup process owns the remaining work). */
    public void join(long timeoutMs) throws InterruptedException {
        thread.join(timeoutMs);
        if (thread.isAlive()) {
            Ln.w("Cleanup thread still running after " + timeoutMs + "ms; leaving it to the independent cleanup process");
        }
    }

    private void runCleanUp(Options options) {
        // Association ownership is registered after successful creation.
        boolean disableShowTouches = false;
        if (options.getShowTouches()) {
            try {
                String oldValue = Settings.getAndPutValue(Settings.TABLE_SYSTEM, "show_touches", "1");
                // If "show touches" was disabled, it must be disabled back on clean up
                disableShowTouches = !"1".equals(oldValue);
            } catch (SettingsException e) {
                Ln.e("Could not change \"show_touches\"", e);
            }
        }

        int restoreStayOn = -1;
        if (options.getStayAwake()) {
            int stayOn = BatteryManager.BATTERY_PLUGGED_AC | BatteryManager.BATTERY_PLUGGED_USB | BatteryManager.BATTERY_PLUGGED_WIRELESS;
            try {
                String oldValue = Settings.getAndPutValue(Settings.TABLE_GLOBAL, "stay_on_while_plugged_in", String.valueOf(stayOn));
                try {
                    int currentStayOn = Integer.parseInt(oldValue);
                    // Restore only if the current value is different
                    if (currentStayOn != stayOn) {
                        restoreStayOn = currentStayOn;
                    }
                } catch (NumberFormatException e) {
                    // ignore
                }
            } catch (SettingsException e) {
                Ln.e("Could not change \"stay_on_while_plugged_in\"", e);
            }
        }

        int restoreScreenOffTimeout = -1;
        int screenOffTimeout = options.getScreenOffTimeout();
        if (screenOffTimeout != -1) {
            try {
                String oldValue = Settings.getAndPutValue(Settings.TABLE_SYSTEM, "screen_off_timeout", String.valueOf(screenOffTimeout));
                try {
                    int currentScreenOffTimeout = Integer.parseInt(oldValue);
                    // Restore only if the current value is different
                    if (currentScreenOffTimeout != screenOffTimeout) {
                        restoreScreenOffTimeout = currentScreenOffTimeout;
                    }
                } catch (NumberFormatException e) {
                    // ignore
                }
            } catch (SettingsException e) {
                Ln.e("Could not change \"screen_off_timeout\"", e);
            }
        }

        int displayId = options.getDisplayId();

        int restoreDisplayImePolicy = -1;
        if (displayId > 0) {
            int displayImePolicy = options.getDisplayImePolicy();
            if (displayImePolicy != -1) {
                int currentDisplayImePolicy = ServiceManager.getWindowManager().getDisplayImePolicy(displayId);
                if (currentDisplayImePolicy != displayImePolicy) {
                    ServiceManager.getWindowManager().setDisplayImePolicy(displayId, displayImePolicy);
                    restoreDisplayImePolicy = currentDisplayImePolicy;
                }
            }
        }

        boolean powerOffScreen = options.getPowerOffScreenOnClose();

        try {
            run(displayId, restoreStayOn, disableShowTouches, powerOffScreen, restoreScreenOffTimeout, restoreDisplayImePolicy);
        } catch (IOException e) {
            Ln.e("Clean up I/O exception", e);
        } finally {
            if (appPowerSave != null) {
                appPowerSave.close();
            }
        }
    }

    private void run(int displayId, int restoreStayOn, boolean disableShowTouches, boolean powerOffScreen, int restoreScreenOffTimeout,
            int restoreDisplayImePolicy) throws IOException {
        String[] cmd = {
                "app_process",
                "/",
                CleanUp.class.getName(),
                String.valueOf(displayId),
                String.valueOf(restoreStayOn),
                String.valueOf(disableShowTouches),
                String.valueOf(powerOffScreen),
                String.valueOf(restoreScreenOffTimeout),
                String.valueOf(restoreDisplayImePolicy),
                appPowerSave == null ? "" : appPowerSave.getOwner(),
        };

        ProcessBuilder builder = new ProcessBuilder(cmd);
        builder.environment().put("CLASSPATH", Server.SERVER_PATH);
        builder.redirectErrorStream(true);
        Process process = builder.start();
        OutputStream out = process.getOutputStream();
        Thread cleanupOutput = new Thread(() -> {
            try (java.io.BufferedReader reader = new java.io.BufferedReader(new java.io.InputStreamReader(
                    process.getInputStream(), java.nio.charset.StandardCharsets.UTF_8))) {
                String line;
                while ((line = reader.readLine()) != null) {
                    if (line.contains("SCRCPY_EZ_APP_POWER_SAVE_CLEANUP_READY") && appPowerSave != null) {
                        appPowerSave.ready();
                    }
                    if (line.contains("SCRCPY_EZ_APP_POWER_SAVE") || line.contains("SCRCPY_EZ_APP_MILLET")
                            || line.contains("SCRCPY_EZ_DISPLAY_BACKLIGHT")) {
                        Ln.i("[cleanup] " + line);
                    }
                }
            } catch (IOException e) {
                Ln.d("Cleanup output closed");
            } finally {
                // Do not leave new exemptions enabled if the independent
                // cleanup process exits unexpectedly while this server lives.
                if (appPowerSave != null) {
                    appPowerSave.close();
                }
            }
        }, "cleanup-output");
        cleanupOutput.setDaemon(true);
        cleanupOutput.start();

        try {
            while (true) {
                int localPendingChanges;
                boolean localPendingRestoreDisplayPower;
                String localPerspectiveMac;
                synchronized (this) {
                    while (!interrupted && pendingChanges == 0) {
                        try {
                            wait();
                        } catch (InterruptedException e) {
                            throw new AssertionError("Clean up thread MUST NOT be interrupted");
                        }
                    }
                    if (interrupted) {
                        break;
                    }
                    localPendingChanges = pendingChanges;
                    localPendingRestoreDisplayPower = pendingRestoreDisplayPower;
                    localPerspectiveMac = perspectiveMac;
                    pendingChanges = 0;
                }
                if ((localPendingChanges & PENDING_CHANGE_DISPLAY_POWER) != 0) {
                    out.write(localPendingRestoreDisplayPower ? 1 : 0);
                    out.flush();
                }
                if ((localPendingChanges & PENDING_PERSPECTIVE_ASSOCIATION) != 0) {
                    out.write(2);
                    out.write(localPerspectiveMac.getBytes(java.nio.charset.StandardCharsets.US_ASCII));
                    out.flush();
                }
                if ((localPendingChanges & PENDING_RESTORE_BACKLIGHT) != 0) {
                    out.write(RESTORE_BACKLIGHT);
                    out.flush();
                }
                if ((localPendingChanges & PENDING_AUTO_BLACKOUT) != 0) {
                    out.write(AUTO_BLACKOUT);
                    out.flush();
                }
                if ((localPendingChanges & PENDING_TOGGLE_BACKLIGHT) != 0) {
                    out.write(TOGGLE_BACKLIGHT);
                    out.flush();
                }
            }
        } finally {
            // EOF triggers the independent device-side crash cleanup, including
            // a retry when normal whitelist restoration failed.
            out.close();
        }
    }

    public synchronized void setPerspectiveAssociation(String mac) {
        perspectiveMac = mac; pendingChanges |= PENDING_PERSPECTIVE_ASSOCIATION; notify();
    }

    public synchronized void setRestoreDisplayPower(boolean restoreDisplayPower) {
        pendingRestoreDisplayPower = restoreDisplayPower;
        pendingChanges |= PENDING_CHANGE_DISPLAY_POWER;
        notify();
    }

    public synchronized void toggleMainDisplayBacklight() {
        pendingChanges ^= PENDING_TOGGLE_BACKLIGHT;
        notify();
    }

    public synchronized void restoreMainDisplayBacklight() {
        pendingChanges = (pendingChanges & ~(PENDING_TOGGLE_BACKLIGHT | PENDING_AUTO_BLACKOUT)) | PENDING_RESTORE_BACKLIGHT;
        notify();
    }

    public synchronized void requestAutoBlackout() {
        pendingChanges |= PENDING_AUTO_BLACKOUT;
        notify();
    }

    public static void unlinkSelf() {
        try {
            new File(Server.SERVER_PATH).delete();
        } catch (Exception e) {
            Ln.e("Could not unlink server", e);
        }
    }

    @SuppressWarnings("deprecation")
    private static void prepareMainLooper() {
        Looper.prepareMainLooper();
    }

    public static void main(String... args) {
        try {
            // Start a new session to avoid being terminated along with the server process on some devices
            Os.setsid();
        } catch (ErrnoException e) {
            Ln.e("setsid() failed", e);
        }
        unlinkSelf();

        // Needed for workarounds
        prepareMainLooper();
        Workarounds.apply();

        int displayId = Integer.parseInt(args[0]);
        int restoreStayOn = Integer.parseInt(args[1]);
        boolean disableShowTouches = Boolean.parseBoolean(args[2]);
        boolean powerOffScreen = Boolean.parseBoolean(args[3]);
        int restoreScreenOffTimeout = Integer.parseInt(args[4]);
        int restoreDisplayImePolicy = Integer.parseInt(args[5]);
        if (args.length > 6 && !args[6].isEmpty()) {
            Ln.i("SCRCPY_EZ_APP_POWER_SAVE_CLEANUP_READY");
        }

        // Dynamic option
        boolean restoreDisplayPower = false;
        String perspectiveAssociation = "";

        try (MainDisplayBacklight backlight = new MainDisplayBacklight()) {
            // Wait for the server to die
            int msg;
            while ((msg = System.in.read()) != -1) {
                if (msg == 2) {
                    byte[] address = new byte[17];
                    new java.io.DataInputStream(System.in).readFully(address);
                    perspectiveAssociation = new String(address, java.nio.charset.StandardCharsets.US_ASCII);
                    continue;
                }
                if (msg == TOGGLE_BACKLIGHT) {
                    if (displayId == 0) {
                        backlight.toggle();
                    }
                    continue;
                }
                if (msg == RESTORE_BACKLIGHT) {
                    backlight.restore();
                    continue;
                }
                if (msg == AUTO_BLACKOUT) {
                    if (displayId == 0) {
                        backlight.turnOffAfterFirstFrame();
                    }
                    continue;
                }
                // Only restore display power
                assert msg == 0 || msg == 1;
                restoreDisplayPower = msg != 0;
            }
        } catch (IOException e) {
            // Expected when the server is dead
        }

        Ln.i("Cleaning up");
        if (args.length > 6 && !args[6].isEmpty()) {
            AppPowerSave.releaseOwner(args[6]);
        }
        if (!perspectiveAssociation.isEmpty()) {
            com.genymobile.scrcpy.video.PerspectiveCamera.removeAssociationWithRetry(perspectiveAssociation);
        }

        if (disableShowTouches) {
            Ln.i("Disabling \"show touches\"");
            try {
                Settings.putValue(Settings.TABLE_SYSTEM, "show_touches", "0");
            } catch (SettingsException e) {
                Ln.e("Could not restore \"show_touches\"", e);
            }
        }

        if (restoreStayOn != -1) {
            Ln.i("Restoring \"stay awake\"");
            try {
                Settings.putValue(Settings.TABLE_GLOBAL, "stay_on_while_plugged_in", String.valueOf(restoreStayOn));
            } catch (SettingsException e) {
                Ln.e("Could not restore \"stay_on_while_plugged_in\"", e);
            }
        }

        if (restoreScreenOffTimeout != -1) {
            Ln.i("Restoring \"screen off timeout\"");
            try {
                Settings.putValue(Settings.TABLE_SYSTEM, "screen_off_timeout", String.valueOf(restoreScreenOffTimeout));
            } catch (SettingsException e) {
                Ln.e("Could not restore \"screen_off_timeout\"", e);
            }
        }

        if (restoreDisplayImePolicy != -1) {
            Ln.i("Restoring \"display IME policy\"");
            ServiceManager.getWindowManager().setDisplayImePolicy(displayId, restoreDisplayImePolicy);
        }

        // Change the power of the main display when mirroring a virtual display
        int targetDisplayId = displayId != Device.DISPLAY_ID_NONE ? displayId : 0;
        if (restoreDisplayPower) {
            Ln.i("Restoring display power");
            Device.setDisplayPower(targetDisplayId, true);
        } else if (Device.isScreenOn(targetDisplayId) && powerOffScreen) {
            Ln.i("Power off screen");
            Device.powerOffScreen(targetDisplayId);
        }

        System.exit(0);
    }
}

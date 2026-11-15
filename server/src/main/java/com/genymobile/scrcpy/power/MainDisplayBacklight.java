package com.genymobile.scrcpy.power;

import com.genymobile.scrcpy.AndroidVersions;
import com.genymobile.scrcpy.device.Device;
import com.genymobile.scrcpy.util.Ln;
import com.genymobile.scrcpy.wrappers.DisplayManager;
import com.genymobile.scrcpy.wrappers.ServiceManager;
import com.genymobile.scrcpy.wrappers.SurfaceControl;

import android.net.LocalServerSocket;
import android.os.Build;
import android.os.Handler;
import android.os.HandlerThread;
import android.os.IBinder;
import android.os.SystemClock;

import java.io.IOException;

/** Owned by the independent cleanup process for the lifetime of the blackout. */
public final class MainDisplayBacklight implements AutoCloseable {

    private static final String LOCK_NAME = "scrcpy_ez_main_display_backlight";
    private static final String LOG_PREFIX = "SCRCPY_EZ_DISPLAY_BACKLIGHT";
    private static final long BRIGHTNESS_SETTLE_MS = 1_000;
    private static final long PANEL_REAPPLY_DELAY_MS = 1_050;

    private LocalServerSocket lock;
    private IBinder panel;
    private float previousBrightness;
    private boolean temporaryBrightnessActive;
    private BrightnessMonitor brightnessMonitor;

    public synchronized void toggle() {
        if (lock != null) {
            restore();
            return;
        }

        turnOff(false);
    }

    public synchronized void turnOffAfterFirstFrame() {
        if (lock == null) {
            turnOff(true);
        }
    }

    private void turnOff(boolean requireUnlocked) {
        try {
            if (!Device.isScreenOn(0)) {
                Ln.w(LOG_PREFIX + " action=unavailable reason=display_asleep");
                return;
            }
            if (requireUnlocked && ServiceManager.getWindowManager().isKeyguardLocked()) {
                Ln.i(LOG_PREFIX + " action=skip_auto reason=keyguard_locked");
                return;
            }
            IBinder token = Device.getSinglePhysicalDisplayToken();
            if (token == null || !SurfaceControl.getDisplayBrightnessSupport(token)) {
                Ln.w(LOG_PREFIX + " action=unavailable reason=unsupported_panel (use MOD+O for display power)");
                return;
            }

            // Two main casts on the same device must not capture or restore each other's brightness.
            lock = new LocalServerSocket(LOCK_NAME);
            DisplayManager displayManager = ServiceManager.getDisplayManager();
            previousBrightness = displayManager.getAdjustedBrightness(0);
            if (!(previousBrightness >= 0 && previousBrightness <= 1)) {
                Ln.w(LOG_PREFIX + " action=unavailable reason=brightness_unknown");
                return;
            }

            try {
                displayManager.setTemporaryBrightness(0, 0f);
                temporaryBrightnessActive = true;
                // Let the system's brightness animation finish before zeroing the panel.
                SystemClock.sleep(BRIGHTNESS_SETTLE_MS);
            } catch (ReflectiveOperationException | RuntimeException e) {
                // Some Android builds do not expose this call to shell. Preserve the panel-only toggle there.
                Ln.i(LOG_PREFIX + " temporary_brightness=unavailable reason=" + e);
            }

            if (!Device.isScreenOn(0) || (requireUnlocked && ServiceManager.getWindowManager().isKeyguardLocked())) {
                Ln.i(LOG_PREFIX + " action=unavailable reason=display_state_changed");
                return;
            }

            if (SurfaceControl.setDisplayBrightness(token, -1f)) {
                panel = token;
                Ln.i(LOG_PREFIX + " action=off" + (requireUnlocked ? " trigger=first_frame" : "")
                        + " temporary_brightness=" + temporaryBrightnessActive);
                startBrightnessMonitor(displayManager);
            } else {
                Ln.w(LOG_PREFIX + " action=unavailable reason=brightness_rejected (use MOD+O for display power)");
            }
        } catch (IOException | ReflectiveOperationException | RuntimeException e) {
            Ln.w(LOG_PREFIX + " action=unavailable reason=" + e);
        } finally {
            if (panel == null) {
                restore();
            }
        }
    }

    public synchronized void restore() {
        if (lock == null) {
            return;
        }

        if (brightnessMonitor != null) {
            BrightnessMonitor monitor = brightnessMonitor;
            brightnessMonitor = null;
            monitor.close();
        }
        boolean hadBlackout = panel != null;
        restoreBrightness();
        if (panel != null || temporaryBrightnessActive) {
            SystemClock.sleep(100);
            restoreBrightness();
        }
        if (panel == null && !temporaryBrightnessActive) {
            releaseLock();
            if (hadBlackout) {
                Ln.i(LOG_PREFIX + " action=restore");
            }
        }
    }

    private void startBrightnessMonitor(DisplayManager displayManager) {
        if (Build.VERSION.SDK_INT < AndroidVersions.API_31_ANDROID_12) {
            return;
        }
        BrightnessMonitor monitor = new BrightnessMonitor(displayManager);
        try {
            monitor.start();
            brightnessMonitor = monitor;
            Ln.i(LOG_PREFIX + " maintenance=brightness_events");
        } catch (ReflectiveOperationException | RuntimeException e) {
            monitor.close();
            Ln.i(LOG_PREFIX + " maintenance=unavailable reason=" + e);
        }
    }

    private final class BrightnessMonitor implements AutoCloseable {
        private final DisplayManager displayManager;
        private final HandlerThread thread = new HandlerThread("main-display-brightness");
        private final Runnable afterRamp = () -> reapply(true);
        private DisplayManager.DisplayListenerHandle listener;
        private Handler handler;
        private boolean failed;

        BrightnessMonitor(DisplayManager displayManager) {
            this.displayManager = displayManager;
        }

        void start() throws ReflectiveOperationException {
            thread.start();
            handler = new Handler(thread.getLooper());
            listener = displayManager.registerBrightnessListener(this::onBrightnessChanged, handler);
        }

        private void onBrightnessChanged(int displayId) {
            if (displayId == 0) {
                reapply(false);
            }
        }

        private void reapply(boolean settled) {
            synchronized (MainDisplayBacklight.this) {
                if (brightnessMonitor != this || panel == null) {
                    return;
                }
                if (!settled) {
                    // The display controller can still write the panel during its brightness ramp.
                    handler.removeCallbacks(afterRamp);
                    handler.postDelayed(afterRamp, PANEL_REAPPLY_DELAY_MS);
                }
                try {
                    if (!Device.isScreenOn(0) || ServiceManager.getWindowManager().isKeyguardLocked()) {
                        return;
                    }
                    if (SurfaceControl.setDisplayBrightness(panel, -1f)) {
                        failed = false;
                        if (settled) {
                            Ln.i(LOG_PREFIX + " action=reapply trigger=brightness_changed");
                        }
                    } else if (!failed) {
                        failed = true;
                        Ln.w(LOG_PREFIX + " reapply_error reason=brightness_rejected");
                    }
                } catch (ReflectiveOperationException | RuntimeException e) {
                    if (!failed) {
                        failed = true;
                        Ln.w(LOG_PREFIX + " reapply_error reason=" + e);
                    }
                }
            }
        }

        @Override
        public void close() {
            if (handler != null) {
                handler.removeCallbacksAndMessages(null);
            }
            if (listener != null) {
                displayManager.unregisterDisplayListener(listener);
            }
            thread.quitSafely();
        }
    }

    private void restoreBrightness() {
        if (panel != null) {
            try {
                if (SurfaceControl.setDisplayBrightness(panel, previousBrightness)) {
                    panel = null;
                } else {
                    Ln.w(LOG_PREFIX + " panel_restore_error reason=brightness_rejected");
                }
            } catch (ReflectiveOperationException | RuntimeException e) {
                Ln.w(LOG_PREFIX + " panel_restore_error reason=" + e);
            }
        }
        if (temporaryBrightnessActive) {
            try {
                ServiceManager.getDisplayManager().setTemporaryBrightness(0, Float.NaN);
                temporaryBrightnessActive = false;
                Ln.i(LOG_PREFIX + " temporary_brightness=cleared");
            } catch (ReflectiveOperationException | RuntimeException e) {
                Ln.w(LOG_PREFIX + " temporary_brightness_clear_error reason=" + e);
            }
        }
    }

    @Override
    public synchronized void close() {
        restore();
        releaseLock();
    }

    private void releaseLock() {
        if (lock != null) {
            try {
                lock.close();
            } catch (IOException e) {
                Ln.w(LOG_PREFIX + " action=lock_close_error reason=" + e);
            }
            lock = null;
        }
    }
}

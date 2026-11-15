package com.genymobile.scrcpy.power;

import com.genymobile.scrcpy.device.Device;
import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.util.Ln;
import com.genymobile.scrcpy.wrappers.DeviceIdleController;

import android.database.ContentObserver;
import android.os.Handler;
import android.os.Looper;
import android.os.Process;

import java.io.File;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;
import java.util.Timer;
import java.util.TimerTask;

/** A session only registers while its independent crash-cleanup process is ready. */
public final class AppPowerSave {
    private static final File FOLDER = new File("/data/local/tmp");
    private final String owner;
    private final Map<String, Integer> packages = new LinkedHashMap<>();
    private boolean ready;
    private boolean closed;
    private boolean suspended;
    private int registeredDisplay = -1;
    private int registeredMilletDisplay = -1;
    private int desiredDisplay = -1;
    private Timer diagnostics;
    private Timer milletMaintenance;
    private ContentObserver milletObserver;
    private boolean milletRefreshQueued;
    private long lastMilletFailureNs;

    public AppPowerSave() throws IOException {
        owner = read(new File("/proc/sys/kernel/random/boot_id")) + ":" + Process.myPid() + ":" + startTime(Process.myPid());
    }

    public String getOwner() {
        return owner;
    }

    public synchronized void ready() {
        ready = true;
        notifyAll();
    }

    public synchronized void registerDisplay(int display) {
        if (!closed) {
            desiredDisplay = display;
        }
        long deadline = System.nanoTime() + 2000000000L;
        while (!ready && !closed && System.nanoTime() < deadline) {
            try {
                wait(Math.max(1, (deadline - System.nanoTime()) / 1000000));
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return;
            }
        }
        if (!ready || closed || suspended) {
            Ln.w("SCRCPY_EZ_APP_POWER_SAVE action=skipped display=" + display + " reason=cleanup-not-ready");
            return;
        }
        if (registeredDisplay != display) {
            try {
                store().registerDisplay(owner, display);
                registeredDisplay = display;
            } catch (Exception e) {
                failure("register-failed", "-", display, e);
            }
        }
        if (registeredDisplay == display && registeredMilletDisplay != display) {
            try {
                PowerSaveLeaseStore millet = milletStore();
                if (millet != null) {
                    millet.registerDisplay(owner, display);
                    registeredMilletDisplay = display;
                    startMilletMonitor();
                }
            } catch (Exception e) {
                milletFailure("register-failed", e);
            }
        }
    }

    public synchronized void acquire(String pkg, int display) {
        if (!closed) {
            packages.put(pkg, display);
        }
        registerDisplay(display);
        if (!ready || closed || suspended || registeredDisplay != display) {
            Ln.w("SCRCPY_EZ_APP_POWER_SAVE action=skipped pkg=" + pkg + " display=" + display + " reason=session-or-display-not-ready");
            return;
        }
        acquireForDisplay(pkg, display);
        packages.put(pkg, display);
        if (diagnostics == null) {
            diagnostics = new Timer("app-power-save-log", true);
            diagnostics.schedule(new TimerTask() {
                @Override
                public void run() {
                    logState();
                }
            }, AppRuntimeDiagnostics.ENABLED ? 0 : 15000, AppRuntimeDiagnostics.ENABLED ? 5000 : 30000);
        }
    }

    public static void acquireForDisplay(String pkg, int display) {
        if (display <= 0) {
            return;
        }
        try {
            store().acquire(pkg, display);
        } catch (Exception e) {
            failure("add-failed", pkg, display, e);
        }
        boolean milletGranted = false;
        try {
            PowerSaveLeaseStore millet = milletStore();
            if (millet != null) {
                milletGranted = millet.acquire(pkg, display);
            }
        } catch (Exception e) {
            milletFailure("add-failed", e);
        }
        if (milletGranted) {
            try {
                int result = XiaomiMillet.requestThaw(pkg);
                Ln.i("SCRCPY_EZ_APP_MILLET action=thaw-requested pkg=" + pkg + " display=" + display
                        + " user=0 option=-1 dispatch_result=" + result);
            } catch (Exception e) {
                milletFailure("thaw-unavailable", e);
            }
        }
    }

    private void logState() {
        Map<String, Integer> snapshot;
        boolean suspendedSnapshot;
        synchronized (this) {
            if (closed) {
                return;
            }
            snapshot = new LinkedHashMap<>(packages);
            suspendedSnapshot = suspended;
        }
        // Slow/unsupported diagnostic Binder calls must not block renewal,
        // shutdown, or input delivery on the session's monitor.
        for (Map.Entry<String, Integer> entry : snapshot.entrySet()) {
            try {
                boolean exempt = new DeviceIdleController().contains(entry.getKey());
                Ln.i("SCRCPY_EZ_APP_POWER_SAVE action=state pkg=" + entry.getKey() + " display=" + entry.getValue()
                        + " exempt=" + exempt + " main_screen_on=" + Device.isScreenOn(0)
                        + " vd_screen_on=" + Device.isScreenOn(entry.getValue()) + " suspended=" + suspendedSnapshot);
            } catch (Exception e) {
                failure("state-failed", entry.getKey(), entry.getValue(), e);
            }
            if (XiaomiMillet.isCandidate()) {
                try {
                    Ln.i("SCRCPY_EZ_APP_MILLET action=state pkg=" + entry.getKey() + " display=" + entry.getValue()
                            + " exempt=" + XiaomiMillet.backend().contains(entry.getKey()) + " suspended=" + suspendedSnapshot);
                } catch (Exception e) {
                    milletFailure("state-unavailable", e);
                }
            }
            AppRuntimeDiagnostics.log(entry.getKey(), entry.getValue());
        }
    }

    public synchronized void suspend() throws Exception {
        if (closed) {
            return;
        }
        boolean first = !suspended;
        suspended = true;
        registeredDisplay = -1;
        registeredMilletDisplay = -1;
        if (first) {
            Ln.i("SCRCPY_EZ_APP_POWER_SAVE action=heartbeat-timeout timeout_ms=30000 owner=" + owner);
        }
        // Remains retryable when Binder/removal readback fails. A shared app's
        // exemption stays in place for any other live session owner.
        Exception error = null;
        try {
            store().release(owner, this::rememberOwned);
        } catch (Exception e) {
            error = e;
        }
        try {
            PowerSaveLeaseStore millet = milletStore();
            if (millet != null) {
                millet.release(owner, this::rememberOwned);
            }
        } catch (Exception e) {
            if (error == null) { error = e; } else { error.addSuppressed(e); }
        }
        if (error != null) {
            throw error;
        }
    }

    public synchronized void resume() {
        if (closed || !suspended) {
            return;
        }
        suspended = false;
        if (desiredDisplay > 0) {
            registerDisplay(desiredDisplay);
            if (registeredDisplay == desiredDisplay) {
                for (Map.Entry<String, Integer> entry : packages.entrySet()) {
                    acquireForDisplay(entry.getKey(), desiredDisplay);
                }
            }
        }
        Ln.i("SCRCPY_EZ_APP_POWER_SAVE action=heartbeat-resumed display=" + desiredDisplay);
    }

    public synchronized void close() {
        if (closed) {
            return;
        }
        closed = true;
        notifyAll();
        if (diagnostics != null) {
            diagnostics.cancel();
        }
        if (milletMaintenance != null) {
            milletMaintenance.cancel();
        }
        if (milletObserver != null) {
            try {
                FakeContext.get().getContentResolver().unregisterContentObserver(milletObserver);
            } catch (Exception e) {
                milletFailure("observer-close-failed", e);
            }
        }
        releaseOwner(owner);
    }

    private void rememberOwned(Set<String> owned) {
        for (String pkg : owned) {
            packages.put(pkg, desiredDisplay);
        }
    }

    public static void releaseOwner(String owner) {
        if (owner == null || owner.isEmpty()) {
            return;
        }
        try {
            store().release(owner);
        } catch (Exception e) {
            failure("cleanup-failed", "-", -1, e);
        }
        try {
            PowerSaveLeaseStore millet = milletStore();
            if (millet != null) {
                millet.release(owner);
            }
        } catch (Exception e) {
            milletFailure("cleanup-failed", e);
        }
    }

    private static PowerSaveLeaseStore store() throws ReflectiveOperationException {
        return new PowerSaveLeaseStore(FOLDER, new DeviceIdleController(), AppPowerSave::ownerAlive, Ln::i);
    }

    private static PowerSaveLeaseStore milletStore() {
        return XiaomiMillet.isCandidate()
                ? new PowerSaveLeaseStore(FOLDER, XiaomiMillet.backend(), AppPowerSave::ownerAlive, Ln::i,
                        "millet", "SCRCPY_EZ_APP_MILLET ") : null;
    }

    private void startMilletMonitor() {
        if (milletMaintenance != null || closed) {
            return;
        }
        milletMaintenance = new Timer("millet-session-policy", true);
        milletMaintenance.schedule(new TimerTask() {
            @Override public void run() { refreshMillet(); }
        }, 1000, 1000);
        try {
            milletObserver = new ContentObserver(new Handler(Looper.getMainLooper())) {
                @Override public void onChange(boolean selfChange) { requestMilletRefresh(); }
            };
            FakeContext.get().getContentResolver().registerContentObserver(
                    android.provider.Settings.System.getUriFor(XiaomiMillet.KEY), false, milletObserver);
            Ln.i("SCRCPY_EZ_APP_MILLET action=monitor observer=true poll_ms=1000 user=0");
        } catch (Exception e) {
            milletFailure("observer-unavailable", e);
        }
    }

    private synchronized void requestMilletRefresh() {
        if (closed || suspended || milletMaintenance == null || milletRefreshQueued) {
            return;
        }
        milletRefreshQueued = true;
        milletMaintenance.schedule(new TimerTask() {
            @Override public void run() {
                synchronized (AppPowerSave.this) { milletRefreshQueued = false; }
                refreshMillet();
            }
        }, 0);
    }

    private synchronized void refreshMillet() {
        if (closed || suspended) {
            return;
        }
        try {
            PowerSaveLeaseStore millet = milletStore();
            if (millet != null) {
                millet.refresh(owner);
            }
        } catch (Exception e) {
            long now = System.nanoTime();
            if (lastMilletFailureNs == 0 || now - lastMilletFailureNs > 30000000000L) {
                lastMilletFailureNs = now;
                milletFailure("refresh-failed", e);
            }
        }
    }

    private static void milletFailure(String action, Exception e) {
        Throwable cause = e;
        while (cause.getCause() != null && cause.getCause() != cause) { cause = cause.getCause(); }
        // SettingsException may contain the whole shared list; keep failures content-free.
        Ln.w("SCRCPY_EZ_APP_MILLET action=" + action + " reason=" + cause.getClass().getSimpleName());
    }

    private static boolean ownerAlive(String owner) {
        try {
            String[] fields = owner.split(":", -1);
            if (fields.length != 3 || !read(new File("/proc/sys/kernel/random/boot_id")).equals(fields[0])) {
                return false;
            }
            int pid = Integer.parseInt(fields[1]);
            File proc = new File("/proc/" + pid);
            if (!proc.exists()) {
                return false;
            }
            return startTime(pid).equals(fields[2]);
        } catch (Exception e) {
            // Inaccessible proc metadata does not prove another session died.
            return true;
        }
    }

    private static String startTime(int pid) throws IOException {
        String stat = read(new File("/proc/" + pid + "/stat"));
        int end = stat.lastIndexOf(')');
        if (end < 0) {
            throw new IOException("Invalid process metadata");
        }
        String[] fields = stat.substring(end + 1).trim().split("\\s+");
        if (fields.length <= 19 || !fields[19].matches("[0-9]+")) {
            throw new IOException("Missing process start time");
        }
        return fields[19];
    }

    private static String read(File file) throws IOException {
        return new String(Files.readAllBytes(file.toPath()), StandardCharsets.US_ASCII).trim();
    }

    private static void failure(String action, String pkg, int display, Exception e) {
        Ln.w("SCRCPY_EZ_APP_POWER_SAVE action=" + action + " pkg=" + pkg + " display=" + display
                + " reason=" + PowerSaveLeaseStore.error(e));
    }
}

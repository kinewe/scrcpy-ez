package com.genymobile.scrcpy.video;

import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.util.Ln;

import android.app.AppOpsManager;
import android.content.Context;
import android.content.pm.PackageManager;

import java.io.Closeable;
import java.io.InputStream;
import java.util.Locale;

/** Reads only camera service warnings in memory; never changes permissions. */
public final class CameraPermissionMonitor implements Closeable {
    interface PermissionProbe { boolean denied() throws Exception; }
    private final String pkg;
    private final Thread worker;
    private final PermissionProbe permissionProbe;
    private final Runnable notice;
    private boolean closed, reported;
    private Process process;
    private final long since = System.currentTimeMillis();

    public CameraPermissionMonitor(String packageName) {
        this(packageName, null, null);
    }

    CameraPermissionMonitor(String packageName, PermissionProbe probe, Runnable listener) {
        pkg = packageName == null ? "" : packageName.replaceFirst("^\\+", "");
        permissionProbe = probe == null ? this::denied : probe;
        notice = listener == null ? () -> Ln.w("SCRCPY_EZ_CAMERA_PERMISSION_REQUIRED") : listener;
        worker = new Thread(this::read, "camera-permission-notice");
        worker.setDaemon(true);
    }

    public void start() {
        if (pkg.matches("[A-Za-z0-9_]+(\\.[A-Za-z0-9_]+)+")) { worker.start(); }
    }

    public static boolean isRejection(String line, String pkg) {
        if (line == null || pkg == null || pkg.isEmpty()) { return false; }
        int at = line.indexOf(pkg);
        boolean owns = false;
        while (at >= 0) {
            int end = at + pkg.length();
            if ((at == 0 || !packageChar(line.charAt(at - 1)))
                    && (end == line.length() || !packageChar(line.charAt(end)))) { owns = true; break; }
            at = line.indexOf(pkg, end);
        }
        if (!owns) { return false; }
        String lower = line.toLowerCase(Locale.ROOT);
        return lower.contains("camera") && (lower.contains("disabled by policy")
                || lower.contains("restricted") || lower.contains("permission denied")
                || lower.contains("permission denial") || lower.contains("not allowed")
                || lower.contains("without camera permission"));
    }

    private static boolean packageChar(char c) {
        return Character.isLetterOrDigit(c) || c == '_' || c == '.';
    }

    private boolean denied() throws Exception {
        Context context = FakeContext.get();
        PackageManager pm = context.getPackageManager();
        if (pm.checkPermission("android.permission.CAMERA", pkg) != PackageManager.PERMISSION_GRANTED) { return true; }
        int uid = pm.getApplicationInfo(pkg, 0).uid;
        AppOpsManager ops = (AppOpsManager) context.getSystemService(Context.APP_OPS_SERVICE);
        int mode = ops.checkOpNoThrow(AppOpsManager.OPSTR_CAMERA, uid, pkg);
        return mode == AppOpsManager.MODE_IGNORED || mode == AppOpsManager.MODE_ERRORED;
    }

    void consume(String line) throws Exception {
        synchronized (this) { if (closed || reported) { return; } }
        // A background-app restriction is not automatically a permission issue.
        // Confirm the current package permission/AppOp before advising the user.
        if (!isRejection(line, pkg) || !permissionProbe.denied()) { return; }
        synchronized (this) {
            if (closed || reported) { return; }
            reported = true;
        }
        notice.run();
    }

    private void read() {
        String time = String.format(Locale.ROOT, "%d.%03d", since / 1000, since % 1000);
        Process owned = null;
        try {
            owned = new ProcessBuilder("logcat", "-b", "main,system", "-v", "raw", "-T", time,
                    "-s", "CameraService:I", "PermissionChecker:W", "*:S").redirectErrorStream(true).start();
            synchronized (this) {
                if (closed) { owned.destroy(); return; }
                process = owned;
            }
            try (InputStream input = owned.getInputStream()) {
                byte[] bytes = new byte[4096];
                StringBuilder line = new StringBuilder();
                boolean overflow = false;
                int n;
                while ((n = input.read(bytes)) >= 0) {
                    synchronized (this) { if (closed || reported) { break; } }
                    for (int i = 0; i < n; ++i) {
                        int b = bytes[i] & 255;
                        if (b == '\n') {
                            if (!overflow) { consume(line.toString()); }
                            line.setLength(0); overflow = false;
                        } else if (line.length() < 4096) { line.append((char) b); }
                        else { overflow = true; }
                    }
                    synchronized (this) { if (reported) { break; } }
                }
            }
        } catch (Exception e) {
            synchronized (this) {
                if (!closed) { Ln.w("Camera permission diagnostics unavailable; projection continues"); }
            }
        } finally {
            if (owned != null) { owned.destroy(); }
            synchronized (this) { process = null; }
        }
    }

    @Override public void close() {
        synchronized (this) { closed = true; if (process != null) { process.destroy(); } }
        worker.interrupt();
    }
}

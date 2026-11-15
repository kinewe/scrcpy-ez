package com.genymobile.scrcpy.video;

import java.io.Closeable;
import java.io.InputStream;
import java.util.Locale;

/** Confirms a close against CameraService; no continuous log reader or periodic poll. */
final class CameraEvictionNotice implements Closeable {
    interface Listener { void onEvicted(); void onUnavailable(); }
    interface LogProbe { boolean containsEviction(String cameraId, long sinceMs) throws Exception; }
    private static final String PREFIX = "CameraService::connect evicting conflicting client for camera ID ";
    private final String cameraId;
    private final Listener listener;
    private final LogProbe probe;
    private long generation, openedMs;
    private long requestedGeneration = -1;
    private boolean closed, pending, reported, unavailableReported;
    private volatile Process process;
    private Thread worker;

    CameraEvictionNotice(String id, Listener listener) {
        this(id, listener, null);
    }

    CameraEvictionNotice(String id, Listener listener, LogProbe probe) {
        if (id == null || !id.matches("[A-Za-z0-9_./:-]{1,128}")) {
            throw new IllegalArgumentException("Invalid owned virtual camera id");
        }
        this.cameraId = id;
        this.listener = listener;
        this.probe = probe == null ? this::readRecentEviction : probe;
    }

    synchronized void onStreamConfigured() {
        ++generation;
        openedMs = System.currentTimeMillis();
        reported = false;
    }

    synchronized void onLastStreamClosed() {
        if (closed || reported || openedMs == 0) { return; }
        requestedGeneration = generation;
        if (pending) { return; }
        pending = true;
        long attempt = generation;
        long since = Math.max(openedMs, System.currentTimeMillis() - 3000);
        worker = new Thread(() -> check(attempt, since), "camera-close-confirmation");
        worker.setDaemon(true);
        worker.start();
    }

    private void check(long attempt, long since) {
        boolean notice = false, unavailable = false;
        try {
            // Surface reconfiguration and immediate recovery should finish without a popup.
            Thread.sleep(120);
            synchronized (this) { if (closed || generation != attempt) { return; } }
            boolean found = probe.containsEviction(cameraId, since);
            synchronized (this) {
                if (!closed && generation == attempt && found && !reported) {
                    reported = true;
                    notice = true;
                }
            }
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        } catch (Exception e) {
            synchronized (this) {
                if (!closed && !unavailableReported) { unavailableReported = true; unavailable = true; }
            }
        } finally {
            synchronized (this) {
                pending = false;
                if (!closed && generation != attempt && requestedGeneration == generation) { onLastStreamClosed(); }
            }
        }
        if (notice) { listener.onEvicted(); }
        if (unavailable) { listener.onUnavailable(); }
    }

    static boolean isEvictionLine(String line, String cameraId) {
        return line != null && line.trim().equals(PREFIX + cameraId);
    }

    private boolean readRecentEviction(String id, long since) throws Exception {
        String time = String.format(Locale.ROOT, "%d.%03d", since / 1000, since % 1000);
        Process owned = new ProcessBuilder("logcat", "-b", "main", "-d", "-v", "raw", "-T", time,
                "-s", "CameraService:E", "*:S").redirectErrorStream(true).start();
        synchronized (this) {
            if (closed) { owned.destroy(); return false; }
            process = owned;
        }
        boolean found = false;
        try (InputStream in = owned.getInputStream()) {
            long deadline = System.nanoTime() + 600000000L;
            byte[] buffer = new byte[4096];
            StringBuilder line = new StringBuilder();
            boolean overflow = false;
            int total = 0;
            while (System.nanoTime() < deadline && total < 65536) {
                synchronized (this) { if (closed) { return false; } }
                int available = in.available();
                int count;
                if (available == 0) {
                    if (owned.isAlive()) { Thread.sleep(5); continue; }
                    // Its only writer has exited: safely drain the final bytes and EOF.
                    count = in.read(buffer);
                } else { count = in.read(buffer, 0, Math.min(buffer.length, available)); }
                if (count < 0) { break; }
                total += count;
                for (int i = 0; i < count; ++i) {
                    int c = buffer[i] & 255;
                    if (c == '\n') {
                        if (!overflow && isEvictionLine(line.toString(), id)) { found = true; }
                        line.setLength(0); overflow = false;
                    } else if (line.length() < 2048) { line.append((char) c); }
                    else { overflow = true; }
                }
            }
            if (!overflow && isEvictionLine(line.toString(), id)) { found = true; }
            if (!found && owned.isAlive()) { throw new java.io.IOException("Camera log query exceeded its budget"); }
            if (!found && owned.exitValue() != 0) { throw new java.io.IOException("Camera log query failed"); }
        } finally {
            owned.destroy();
            synchronized (this) { if (process == owned) { process = null; } }
        }
        return found;
    }

    @Override public void close() {
        Process owned;
        Thread pendingWorker;
        synchronized (this) {
            closed = true; ++generation;
            owned = process; pendingWorker = worker;
        }
        if (owned != null) { owned.destroy(); }
        if (pendingWorker != null) { pendingWorker.interrupt(); }
    }
}

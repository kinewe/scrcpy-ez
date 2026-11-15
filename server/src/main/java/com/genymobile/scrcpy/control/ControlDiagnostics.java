package com.genymobile.scrcpy.control;

/** Content-free counters; never hold this monitor while injecting an event. */
final class ControlDiagnostics {
    private long heartbeats;
    private long lastHeartbeat = -1;
    private long received;
    private long completed;
    private long rejected;
    private long lastInput = -1;

    synchronized void heartbeat(long now) {
        heartbeats++;
        lastHeartbeat = now;
    }

    synchronized void received(long now) {
        received++;
        lastInput = now;
    }

    synchronized void completed(boolean accepted) {
        completed++;
        if (!accepted) {
            rejected++;
        }
    }

    synchronized String snapshot(long now) {
        return "heartbeats=" + heartbeats + " heartbeat_age_ms=" + age(now, lastHeartbeat)
                + " input_received=" + received + " input_completed=" + completed + " input_rejected=" + rejected
                + " input_pending=" + (received - completed) + " input_age_ms=" + age(now, lastInput);
    }

    private static long age(long now, long previous) {
        return previous < 0 ? -1 : Math.max(0, now - previous);
    }
}

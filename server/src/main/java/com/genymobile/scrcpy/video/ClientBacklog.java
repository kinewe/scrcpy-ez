package com.genymobile.scrcpy.video;

/** Connection-owned feedback mailbox. Never shared by concurrent server sessions. */
public final class ClientBacklog {
    private static final int VALID = 1;
    private static final int HARDWARE = 2;
    private static final int BLOCKED = 4;
    static final int IDLE = 8;
    static final int MAX_SAMPLE_AGE_MS = 800;

    public static final class Sample {
        public final int epoch, sequence, queueMs, deliveryMs, flags, queueCount, workUs, receiveBps;
        public final long receivedMs;
        Sample(int epoch, int sequence, int queueMs, int deliveryMs, int flags, long nowMs) {
            this(epoch, sequence, queueMs, deliveryMs, flags, 0, 0, nowMs);
        }
        Sample(int epoch, int sequence, int queueMs, int deliveryMs, int flags, int queueCount, int workUs, long nowMs) {
            this(epoch, sequence, queueMs, deliveryMs, flags, queueCount, workUs, 0, nowMs);
        }
        Sample(int epoch, int sequence, int queueMs, int deliveryMs, int flags, int queueCount, int workUs, int receiveBps, long nowMs) {
            this.epoch = epoch;
            this.sequence = sequence;
            this.queueMs = queueMs;
            this.deliveryMs = deliveryMs;
            this.flags = flags;
            this.queueCount = queueCount;
            this.workUs = workUs;
            this.receiveBps = receiveBps;
            this.receivedMs = nowMs;
        }
        boolean valid(long nowMs) {
            return (flags & VALID) != 0 && fresh(nowMs);
        }

        boolean idle(long nowMs) {
            return (flags & (IDLE | VALID | BLOCKED)) == IDLE && queueCount == 0 && queueMs == 0 && fresh(nowMs);
        }

        private boolean fresh(long nowMs) {
            return nowMs >= receivedMs && nowMs - receivedMs <= MAX_SAMPLE_AGE_MS;
        }
    }
    private volatile Sample latest;

    public synchronized void update(int epoch, int sequence, int queueMs, int deliveryMs, int flags, long nowMs) {
        update(epoch, sequence, queueMs, deliveryMs, flags, 0, 0, nowMs);
    }
    public synchronized void update(int epoch, int sequence, int queueMs, int deliveryMs, int flags, int queueCount, int workUs, long nowMs) {
        update(epoch, sequence, queueMs, deliveryMs, flags, queueCount, workUs, 0, nowMs);
    }
    public synchronized void update(int epoch, int sequence, int queueMs, int deliveryMs, int flags, int queueCount, int workUs, int receiveBps, long nowMs) {
        if (queueMs < 0 || queueMs > 10000 || deliveryMs < 0 || deliveryMs > 10000
                || (flags & ~(VALID | HARDWARE | BLOCKED | IDLE)) != 0) return;
        if (queueCount < 0 || queueCount > 1000000 || workUs < 0 || workUs > 10000000) return;
        if (receiveBps < 0 || receiveBps > 1000000000) return;
        Sample old = latest;
        // Integer.compareUnsigned requires Android 8; this mailbox also runs
        // on the supported Android 5-7 compatibility path.
        if (old != null && ((epoch ^ Integer.MIN_VALUE) < (old.epoch ^ Integer.MIN_VALUE)
                || (epoch == old.epoch && (sequence ^ Integer.MIN_VALUE) <= (old.sequence ^ Integer.MIN_VALUE)))) return;
        latest = new Sample(epoch, sequence, queueMs, deliveryMs, flags, queueCount, workUs, receiveBps, nowMs);
    }

    Sample get() {
        return latest;
    }
}

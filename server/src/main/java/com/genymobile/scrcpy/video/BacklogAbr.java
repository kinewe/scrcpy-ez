package com.genymobile.scrcpy.video;

/** Time-based, confirmed backlog control. Independent of Android/MediaCodec for replay tests. */
final class BacklogAbr {
    static final class Decision {
        final int bitrate;
        final float fps;
        final String reason;
        final long sourceMs, deliveryMs, queueMs;
        Decision(int bitrate, float fps, String reason, long sourceMs, long deliveryMs, long queueMs) {
            this.bitrate = bitrate;
            this.fps = fps;
            this.reason = reason;
            this.sourceMs = sourceMs;
            this.deliveryMs = deliveryMs;
            this.queueMs = queueMs;
        }
    }
    private static final int[] LEVELS = {240, 180, 144, 120, 90, 60, 45, 30, 24, 15, 10, 5};
    private static final int FPS_RECOVERY_MS = 2000;
    private static final class Evidence {
        long since = -1;
        int count;
        void reset() { since = -1; count = 0; }
        void add(long now, boolean overloaded) {
            if (!overloaded) { reset(); return; }
            if (since < 0) since = now;
            ++count;
        }
        boolean ready(long now, boolean severe) {
            return since >= 0 && now - since >= (severe ? 150 : 350) && count >= (severe ? 2 : 3);
        }
    }
    private final int maxBitrate;
    private final float maxFps;
    private final boolean lockBitrate, lockFps;
    private final Evidence source = new Evidence(), delivery = new Evidence(), decode = new Evidence();
    private int bitrate;
    private float fps;
    private long firstMs = -1, lastMs, baseline, lastAction = -10000, healthySince = -1;
    private long idleSince = -1, lastIdleFeedbackMs;
    private int idleEpoch;
    private long lastSourcePts = -1;
    private double periodUs;
    private int clientEpoch = -1, clientSequence = -1;
    private long clientWarmUntil;
    private int lastDecodeDepth = -1;
    private boolean decodeLimited;
    private boolean bitrateMutable = true;
    private long lastDeliveryActionAge = -1, lastDeliveryAge = -1, deliveryFlatSince = -1;
    private boolean calibrated;
    private long lastSourceExcess, driftRemainder;
    private long lastFpsRaise = -10000, fpsBlockedUntil, fpsProbeDelay = 2000;
    private long lastBitrateRaise = -10000, bitrateBlockedUntil, bitrateProbeDelay = 1000;
    private long lastFpsAction = -10000, lastBitrateAction = -10000;

    BacklogAbr(int bitrate, float maxFps, boolean lockBitrate, boolean lockFps) {
        this.maxBitrate = bitrate;
        this.bitrate = bitrate;
        this.maxFps = maxFps > 0 ? maxFps : 120;
        this.fps = this.maxFps;
        this.lockBitrate = lockBitrate;
        this.lockFps = lockFps;
    }

    int bitrate() { return bitrate; }
    float fps() { return fps; }
    long sourceExcessMs() { return lastSourceExcess; }
    long baselineMs() { return baseline; }
    boolean canRecoverFps() { return !lockFps && fps < maxFps; }
    void reject(int oldBitrate, float oldFps) { bitrate = oldBitrate; fps = oldFps; clearEvidence(); }
    void rejectBitrate(int oldBitrate, float oldFps) {
        reject(oldBitrate, oldFps);
        bitrateMutable = false;
        lastDeliveryActionAge = -1;
    }

    void resetSource() {
        calibrated = false;
        firstMs = -1;
        lastSourcePts = -1;
        periodUs = 0;
        lastDeliveryActionAge = lastDeliveryAge = deliveryFlatSince = -1;
        clearEvidence();
    }

    private void clearEvidence() {
        source.reset(); delivery.reset(); decode.reset(); healthySince = -1; lastDecodeDepth = -1;
        idleSince = -1;
    }

    Decision observeIdle(long now, ClientBacklog.Sample sample) {
        if (!calibrated || !canRecoverFps() || sample == null || !sample.idle(now)) {
            idleSince = -1;
            return null;
        }
        if (idleSince < 0 || sample.epoch != idleEpoch || sample.receivedMs - lastIdleFeedbackMs > ClientBacklog.MAX_SAMPLE_AGE_MS) {
            idleSince = now;
            idleEpoch = sample.epoch;
        }
        lastIdleFeedbackMs = sample.receivedMs;
        if (!canProbeFps(now, idleSince)) {
            return null;
        }
        float nextFps = nextRecoveryFps(sample);
        if (nextFps == fps) {
            return null;
        }
        fps = nextFps;
        lastAction = lastFpsAction = lastFpsRaise = now;
        return new Decision(bitrate, fps, "idle-recovery", 0, 0, 0);
    }

    private boolean canProbeFps(long now, long stableSince) {
        return canRecoverFps() && now - stableSince >= FPS_RECOVERY_MS
                && now - lastFpsAction >= FPS_RECOVERY_MS && now >= fpsBlockedUntil;
    }

    private float nextRecoveryFps(ClientBacklog.Sample sample) {
        float nextFps = maxFps;
        for (int i = LEVELS.length - 1; i >= 0; --i) {
            if (LEVELS[i] > fps && LEVELS[i] < nextFps) {
                nextFps = LEVELS[i];
                break;
            }
        }
        if (decodeLimited && sample != null && sample.workUs > 0 && nextFps > 1000000.0 / sample.workUs * .95) {
            return fps;
        }
        return nextFps;
    }

    Decision observe(long now, long rawSourceDelayMs, long ptsUs, long lastWriteMs, ClientBacklog.Sample sample) {
        return observe(now, rawSourceDelayMs, ptsUs, lastWriteMs, 0, sample);
    }
    Decision observe(long now, long rawSourceDelayMs, long ptsUs, long lastWriteMs, long codedBps, ClientBacklog.Sample sample) {
        idleSince = -1;
        if (!calibrated || now < lastMs || now - lastMs > 3000 || (lastSourcePts >= 0 && ptsUs <= lastSourcePts)) {
            resetSource();
            baseline = rawSourceDelayMs;
            firstMs = now;
            calibrated = true;
        }
        long elapsed = Math.max(0, now - lastMs);
        // Slew only for slow clock drift, never chase a subsecond backlog.
        driftRemainder += elapsed;
        baseline += driftRemainder / 10000;
        driftRemainder %= 10000;
        baseline = Math.min(baseline, rawSourceDelayMs);
        long sourceMs = Math.max(0, rawSourceDelayMs - baseline);
        lastSourceExcess = sourceMs;
        if (lastSourcePts >= 0) {
            long gap = ptsUs - lastSourcePts;
            if (gap > 0 && gap < 200000) periodUs = periodUs == 0 ? gap : periodUs * .9 + gap * .1;
        }
        lastSourcePts = ptsUs;
        lastMs = now;
        boolean valid = sample != null && sample.valid(now);
        boolean fresh = valid && (sample.epoch != clientEpoch || sample.sequence != clientSequence);
        if (sample != null && sample.epoch != clientEpoch) {
            clearEvidence();
            clientEpoch = sample.epoch;
            clientWarmUntil = now + 800;
            lastDecodeDepth = -1;
            lastDeliveryActionAge = lastDeliveryAge = deliveryFlatSince = -1;
        }
        long deliveryMs = Math.max(lastWriteMs, valid ? sample.deliveryMs : 0);
        long queueMs = valid && (sample.flags & 4) == 0 ? sample.queueMs : 0;
        long deliveryBudget = valid && sample.receiveBps > 0 ? (long) (sample.receiveBps * .85) : 0;
        boolean deliveryDraining = deliveryMs >= 60 && deliveryBudget > 0 && codedBps > 0 && codedBps <= deliveryBudget;
        // At least three distinct feedback reports are required for ordinary
        // client pressure. Re-reading one cached report never adds evidence.
        source.add(now, sourceMs >= 60 && deliveryMs < 60);
        boolean confirmedSlowDecoder = queueMs >= 180 && valid && sample.workUs > 0
                && periodUs > 0 && sample.workUs > periodUs * 1.2;
        if (fresh && (now >= clientWarmUntil || confirmedSlowDecoder)) {
            if (lastDeliveryAge >= 0 && Math.abs(deliveryMs - lastDeliveryAge) <= 10) {
                if (deliveryFlatSince < 0) deliveryFlatSince = now;
            } else deliveryFlatSince = -1;
            lastDeliveryAge = deliveryMs;
            delivery.add(now, deliveryMs >= 60 && !deliveryDraining);
            if (lastDecodeDepth < 0 || sample.queueCount > lastDecodeDepth) lastDecodeDepth = sample.queueCount;
            boolean draining = sample.queueCount + 2 <= lastDecodeDepth;
            decode.add(now, queueMs >= 60 && !draining);
            if (draining) lastDecodeDepth = sample.queueCount;
            clientSequence = sample.sequence;
        } else if (!valid || now < clientWarmUntil) {
            if (lastWriteMs < 60) delivery.reset();
            decode.reset();
            if (fresh) clientSequence = sample.sequence;
            deliveryFlatSince = -1;
        }
        // Synchronous socket blocking is measured locally, even without feedback.
        if (!valid && lastWriteMs >= 60) delivery.add(now, true);
        boolean severeSource = sourceMs >= 180;
        boolean severeDelivery = deliveryMs >= 180;
        boolean severeDecode = queueMs >= 180;
        boolean deliverySettled = valid && deliveryFlatSince >= 0 && now - deliveryFlatSince >= 2000
                && sample.receiveBps > 0 && codedBps > 0 && codedBps <= sample.receiveBps * 1.1
                && lastWriteMs < 30 && queueMs < 30;
        boolean overloaded = sourceMs >= 60 || (deliveryMs >= 60 && !deliverySettled) || queueMs >= 60;
        if (overloaded) healthySince = -1;
        if (now - firstMs < 800 || now - lastAction < 600) return null;

        String reason = null;
        // Receiver decode pressure cannot be solved by repeatedly blurring the picture.
        if (decode.ready(now, severeDecode) && queueMs >= 60) reason = "decode-backlog";
        else if (delivery.ready(now, severeDelivery) && deliveryMs >= 60 && !deliveryDraining && !deliverySettled
                && (lastDeliveryActionAge < 0 || deliveryMs > lastDeliveryActionAge + 20 || lastWriteMs >= 60)) reason = "delivery-backlog";
        else if (source.ready(now, severeSource) && sourceMs >= 60) reason = "source-backlog";
        if (reason != null) {
            int newBitrate = bitrate;
            float newFps = fps;
            if ("delivery-backlog".equals(reason) && !lockBitrate && bitrateMutable) {
                newBitrate = Math.max(Math.min(1000000, maxBitrate), (int) (bitrate * (severeDelivery ? .65 : .85)));
                if (deliveryBudget > 0) newBitrate = (int) Math.max(Math.min(1000000, maxBitrate), Math.min(newBitrate, deliveryBudget));
            }
            if (newBitrate == bitrate && !lockFps) {
                // Choose a lower level that actually reduces the observed source
                // cadence, including a 60fps source under a 120/144fps ceiling.
                double useful = periodUs > 0 ? Math.min(fps, 1000000 / periodUs * 1.05) : fps;
                double bound = useful * .95;
                if ("decode-backlog".equals(reason) && valid && sample.workUs > 0) {
                    bound = Math.min(bound, 1000000.0 / sample.workUs * .85);
                }
                if ("delivery-backlog".equals(reason) && deliveryBudget > 0 && codedBps > deliveryBudget) {
                    bound = Math.min(bound, useful * deliveryBudget / codedBps * .95);
                }
                for (int level : LEVELS) {
                    if (level < fps && level <= bound) { newFps = level; break; }
                }
            } else if (newBitrate == bitrate && !lockBitrate && bitrateMutable && "source-backlog".equals(reason)) {
                newBitrate = Math.max(Math.min(1000000, maxBitrate), (int) (bitrate * .85));
            }
            if (newBitrate != bitrate || newFps != fps) {
                if ("delivery-backlog".equals(reason)) lastDeliveryActionAge = deliveryMs;
                if (newBitrate != bitrate) lastBitrateAction = now;
                if (newFps != fps) lastFpsAction = now;
                if (newFps < fps) {
                    decodeLimited = "decode-backlog".equals(reason);
                    fpsProbeDelay = now - lastFpsRaise < 5000 ? Math.min(30000, fpsProbeDelay * 2) : 2000;
                    fpsBlockedUntil = now + fpsProbeDelay;
                }
                if (newBitrate < bitrate) {
                    bitrateProbeDelay = now - lastBitrateRaise < 5000 ? Math.min(10000, bitrateProbeDelay * 2) : 1000;
                    bitrateBlockedUntil = now + bitrateProbeDelay;
                }
                bitrate = newBitrate;
                fps = newFps;
                lastAction = now;
                clearEvidence();
                return new Decision(bitrate, fps, reason, sourceMs, deliveryMs, queueMs);
            }
            return null;
        }
        boolean healthy = sourceMs < 35 && (deliveryMs < 30 || deliverySettled) && queueMs < 30
                && (sample == null || valid);
        if (!healthy) { healthySince = -1; return null; }
        if (healthySince < 0) healthySince = now;
        int newBitrate = bitrate;
        float newFps = fps;
        if (canProbeFps(now, healthySince)) {
            newFps = nextRecoveryFps(valid ? sample : null);
        }
        if (newFps == fps && !lockBitrate && bitrateMutable && bitrate < maxBitrate && now - healthySince >= 1500
                && now - lastBitrateAction >= bitrateProbeDelay && now >= bitrateBlockedUntil) {
            long step = bitrate < 8000000 ? Math.max(500000L, bitrate / 2L) : Math.max(250000L, bitrate / 10L);
            long ceiling = bitrate < 8000000 ? Math.min(maxBitrate, 8000000) : maxBitrate;
            newBitrate = (int) Math.min(ceiling, bitrate + step);
        }
        if (newBitrate == bitrate && newFps == fps) return null;
        if (newBitrate != bitrate) lastBitrateAction = now;
        if (newFps != fps) lastFpsAction = now;
        if (newFps > fps) lastFpsRaise = now;
        if (newBitrate > bitrate) lastBitrateRaise = now;
        bitrate = newBitrate;
        fps = newFps;
        lastAction = now;
        return new Decision(bitrate, fps, "healthy-recovery", sourceMs, deliveryMs, queueMs);
    }
}

package com.genymobile.scrcpy.video;

import com.genymobile.scrcpy.AndroidVersions;
import com.genymobile.scrcpy.AsyncProcessor;
import com.genymobile.scrcpy.Options;
import com.genymobile.scrcpy.control.Controller;
import com.genymobile.scrcpy.control.DeviceMessage;
import com.genymobile.scrcpy.control.DeviceMessageSender;
import com.genymobile.scrcpy.device.Streamer;
import com.genymobile.scrcpy.model.Codec;
import com.genymobile.scrcpy.model.CodecOption;
import com.genymobile.scrcpy.model.ConfigurationException;
import com.genymobile.scrcpy.model.Size;
import com.genymobile.scrcpy.util.CodecUtils;
import com.genymobile.scrcpy.util.IO;
import com.genymobile.scrcpy.util.Ln;
import com.genymobile.scrcpy.util.LogUtils;

import android.media.MediaCodec;
import android.media.MediaCodecInfo;
import android.media.MediaFormat;
import android.os.Build;
import android.os.Bundle;
import android.os.Looper;
import android.os.SystemClock;
import android.view.Surface;

import java.io.IOException;
import java.nio.ByteBuffer;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;

public class SurfaceEncoder implements AsyncProcessor {

    private static final int DEFAULT_I_FRAME_INTERVAL = 10; // seconds
    private static final int REPEAT_FRAME_DELAY_US = 100_000; // repeat after 100ms
    private static final String KEY_MAX_FPS_TO_ENCODER = "max-fps-to-encoder";

    // Keep the values in descending order
    private static final int[] MAX_SIZE_FALLBACK = {2560, 1920, 1600, 1280, 1024, 800};
    private static final int MAX_CONSECUTIVE_ERRORS = 3;

    // === ABR: short-window adaptive bitrate reduction ===
    // When the encoder is overloaded (output frame rate drops well below
    // the target for a short window, e.g. during boot animations or scene
    // switches), reduce the bitrate step by step so the stream stays
    // smooth (blurrier but not stuttering). Resolution is never changed.
    private static final long ABR_WINDOW_NS = 50_000_000L; // 50ms detection window: boot animations and
                                                       // scene-switch spikes are short; at 120fps a 50ms
                                                       // window expects ~6 frames and catches short spikes
                                                       // without being diluted by a longer average.
    // 50ms window: 2 frames ~40fps; below that the screen is considered static
    // (was 5 for the legacy 200ms window ~25fps; 5/50ms ~100fps wrongly marked
    // 60/90fps streams as static, disabling the window-confirmation layer)
    private static final int ABR_MIN_FRAMES_IN_WINDOW = 2;
    private static final long ABR_MIN_DOWN_INTERVAL_NS = 200_000_000L; // 200ms debounce between downgrades (fast, less backlog)
    private static final long ABR_UP_DELAY_NS = 150_000_000L; // stable for 150ms before trying to raise (fast recovery)
    private static final long ABR_MIN_UP_INTERVAL_NS = 200_000_000L; // 0.2s between raises at high rates (16M+; keeps high end stable)
    private static final long ABR_MIN_UP_INTERVAL_FAST_NS = 100_000_000L; // 0.1s at low rates (<16M): blur->watchable fast (2nd speedup: the initial interval was still too slow)
    private static final int ABR_FAST_UP_THRESHOLD = 16_000_000; // below this bitrate, use the fast up interval
    private static final long ABR_BITRATE_RESTORE_NS = 1_000_000_000L; // fps=120 must stay stable 1s before the bitrate restore may start (keeps the image from staying blurry too long after fps recovery)
    // Early low-bitrate restore while fps is still recovering: a static
    // screen at the 1M floor stays blurry for several seconds because the
    // bitrate path is gated behind full fps + 1s stable. When the current
    // fps level is already stable and the delay is healthy, allow the
    // bitrate to climb only up to this ceiling (escape the blur floor);
    // above it the normal full-fps restore owns the climb.
    private static final int ABR_EARLY_BITRATE_RESTORE_MAX = 8_000_000; // early restore stops at 8M
    // Ceiling release: after this long without overload while capped, the
    // recovery ceiling is doubled (up to the initial rate) so the ceiling
    // tracks the raise cadence instead of blocking the recovery forever.
    private static final long ABR_CEILING_RELEASE_DELAY_NS = 300_000_000L; // 0.3s (fast ceiling probe, matches raise cadence)
    private static final int ABR_MIN_BITRATE = 1_000_000; // floor (1M: fps-first policy, bitrate is the buffer)
    // === ABR second dimension: dynamic frame rate ===
    // When the bitrate hits the floor (1M) and the encoder is still
    // overloaded (e.g. UI animations whose ME-search cost is bitrate-
    // independent), degrade the frame rate instead. Restore is fps-first:
    // after 300ms stable the fps probes one step up; once back at full fps
    // and stable again, the bitrate restore path is allowed.
    private static final int[] ABR_FPS_LEVELS = {120, 90, 60, 30}; // 90 shares load with bitrate under mild pressure (120->90->60->30); restore climbs 30->60->90->120 smooth; the bitrate buffer alone is not enough when 120fps is maxed
    private static final long ABR_FPS_STABLE_PROBE_NS = 300_000_000L; // stable 300ms before probing fps up (the 500ms value was too slow to restore; revert cooldown + mild-blip immunity already guard oscillation)
    private static final long ABR_FPS_PROBE_WATCH_NS = 300_000_000L; // watch 300ms after probing; overload reverts (fast climb back to full fps)
    private static final long ABR_FPS_REVERT_COOLDOWN_NS = 3_000_000_000L; // after a revert, wait 3s before probing again (anti-oscillation)
    // fps dimension: the bitrate is the primary buffer; a real overload
    // (instant >=90ms, single-frame flag: one overload signal means
    // overload) degrades fps immediately via the fast path even above the
    // bitrate floor. Anti-oscillation is handled by the 300ms drain grace
    // + 3s revert cooldown; the restore phase may also degrade fps when it
    // fails twice within 1s (below).
    private static final int ABR_FPS_INSTANT_STREAK = 1; // 1 heavy frame (>=90ms) is enough to drop fps
    // Restore-phase guard: 2 overloads within this window while the bitrate
    // is climbing -> the bitrate buffer keeps failing, the fps level must
    // share the load (drop one level and stop the restore). Sliding/video
    // frames quantize small at 1M so they never sustain overloads -> no
    // false fps drops there.
    private static final long ABR_FPS_RESTORE_OVERLOAD_WINDOW_NS = 1_000_000_000L; // 1s
    private static final long ABR_DELAY_TRIGGER_MS = 30; // window avg delay delta (ms) above baseline -> overloaded (more sensitive)
    // Instant single-frame trigger: a single frame whose calibrated
    // delayDelta exceeds this threshold degrades the BITRATE immediately (no
    // window wait), catching startup bursts within ~10ms. 35ms: the old 25ms
    // threshold fired on 26-32ms static jitter and caused constant 60M<->42M
    // bitrate churn; fps decisions have their own higher evidence bar below.
    private static final long ABR_INSTANT_TRIGGER_MS = 35; // 35ms: above the 21-22ms static-scene jitter with margin
    private static final long ABR_MILD_INSTANT_MS = 100; // instant overload below this is MILD (25-100ms: the delay-trend + gentle x0.7 layers already cover 20-80ms queueing, so fps joins the fast path only on backlog >=90ms per ABR_FPS_OVERLOAD_MS); >=100ms is a real overload and drops hard x0.3 + counts for the fps streak
    private static final long ABR_FPS_OVERLOAD_MS = 90; // fps streak counts overloads >=90ms (fps degrade is more decisive than waiting for the window); MILD 100ms stays for the bitrate gentle/aggressive split and the recovery debounce
    // Fps evidence bar: only delay events >=40ms may touch the fps level
    // (buffer 90, mild-overload branch, single-animation burst). 26-36ms
    // events stay a bitrate-only matter, so low-delay jitter does not churn
    // 120<->90<->60. The tablet app-open bursts that matter are 42-62ms.
    private static final long ABR_FPS_EVIDENCE_MS = 40; // fps decisions require >=40ms
    // Interactive vs idle thresholds: while the user is actively injecting
    // input, the encoder must stay snappy (the 35/40/30/20ms bars above).
    // After the last input, video playback/static screens switch to tolerant
    // idle bars so 30-49ms video jitter no longer drives bitrate to 1M or
    // churns the fps level. One input immediately re-arms the sharp bars.
    private static final long ABR_INTERACTION_ACTIVE_NS = 2_000_000_000L; // input within 2s => interactive
    private static final long ABR_IDLE_INSTANT_TRIGGER_MS = 60; // idle bitrate instant trigger
    private static final long ABR_IDLE_DELAY_TRIGGER_MS = 50; // idle window overload threshold
    private static final long ABR_IDLE_FPS_EVIDENCE_MS = 80; // idle fps evidence bar
    private static final long ABR_IDLE_DELAY_RECOVER_MS = 35; // idle "healthy" bar (video jitter can recover)
    private static final long ABR_INSTANT_INTERVAL_NS = 5_000_000L; // 5ms debounce between instant triggers (fast response: 3-step chain 60M->18M->5.4M->1M in ~15ms)
    // The window overload threshold is 30ms (ABR_DELAY_TRIGGER_MS, above
    // the 21-22ms static-scene jitter): with a 50ms window the mean is more
    // volatile than with a longer window, so the same threshold is already
    // more sensitive than with a 500ms window (a short spike contributes a
    // larger share of the average). Lowering it further would risk false
    // downgrades on ordinary scrolling jitter.
    private static final long ABR_DELAY_RECOVER_MS = 20; // window avg delay delta below this -> healthy
    // Negative-delay re-zero: the first-10-frames calibration can be polluted
    // by the cold-start backlog, so steady-state delayDelta reads negative
    // (faster than baseline). Negative values are clamped to 0 for all ABR
    // thresholds/logs so they never cancel positive evidence; sustained
    // negative evidence (10 frames, average >=10ms below baseline) then pulls
    // the baseline down in bounded 20ms steps until it sits at the real
    // low-latency floor. One-off jitter cannot move it.
    private static final int ABR_DELAY_REZERO_FRAMES = 10; // negative frames per evidence batch
    private static final long ABR_DELAY_REZERO_MIN_STEP_MS = 10; // ignore negative noise below this
    private static final long ABR_DELAY_REZERO_STEP_MS = 20; // max baseline correction per batch
    private static final long ABR_DELAY_REZERO_LOG_NS = 2_000_000_000L; // rate-limit the re-zero log (2s)
    // Persistent mild overload at an already-low bitrate: some high-refresh
    // tablets (e.g. Xiaomi Pad 8 Pro) never produce >=90ms delay spikes,
    // only a sustained 30-60ms stream. The bitrate-only path pushes the rate
    // to the floor and the fps buffer loop (120->90->120) cannot escape.
    // When the bitrate is already low (<=5M and below the configured rate,
    // or at the 1M floor) and the window below collects several mild
    // overload evidence events, the remaining overload is dominated by
    // per-frame encode (ME) cost, not data volume: force the fast fps
    // degrade (120->60 / 90->60) instead of waiting for a 90ms spike.
    private static final int ABR_MILD_FPS_BITRATE_THRESHOLD = 5_000_000; // <=5M: the bitrate buffer is exhausted
    private static final long ABR_MILD_FPS_EVENT_INTERVAL_NS = 100_000_000L; // one evidence event per 100ms (bursts must not count as persistent)
    private static final long ABR_MILD_FPS_WINDOW_NS = 1_000_000_000L; // evidence expires after 1s
    private static final int ABR_MILD_FPS_EVENTS = 3; // 3 evidence events inside the window => persistent
    // Repeated fps-buffer cycles at high bitrate: the complex-frame lookahead
    // keeps pushing 120->90 while the encoder itself is fast (negative
    // delayDelta), so the delay-based paths never see an overload. If the
    // 120->90 buffer drop repeats inside a short window, the 90 level is not
    // enough: force the fast fps degrade 120->60 instead of looping 90->120.
    // 2 drops inside 5s: tablets show repeated app-open animations as
    // 30-60ms mild overloads; waiting for a third cycle was too slow, the
    // 90->120 restore finished before the next drop could act.
    private static final long ABR_FPS_BUFFER_REPEAT_WINDOW_NS = 5_000_000_000L; // 5s: catch the ~1.5-2s restore cycles
    private static final int ABR_FPS_BUFFER_REPEAT_COUNT = 2; // 2 buffer drops inside the window => structural
    // Single-animation burst: a real-world app-open stutter is ONE animation,
    // not repeated 120->90 cycles. Count any two load evidence events inside
    // 1.5s while still at full fps (a 120->90 buffer drop, an instant 30-90ms
    // overload, or a 30-90ms window overload). Two events inside the window
    // mean this animation is structurally overloaded: force 120->60 and hold
    // 60 for at least 2s so the animation finishes before any restore probe.
    // Occasional single spikes never reach two events -> no over-trigger.
    private static final long ABR_FULL_FPS_BURST_WINDOW_NS = 1_500_000_000L; // 1.5s burst window
    private static final long ABR_FULL_FPS_BURST_EVENT_INTERVAL_NS = 100_000_000L; // one evidence event per 100ms (same frame must not count twice)
    private static final int ABR_FULL_FPS_BURST_EVENTS = 2; // 2 events inside the window => structural
    private static final long ABR_FULL_FPS_BURST_DWELL_NS = 2_000_000_000L; // hold the degraded level for the animation duration

    private int currentBitRate;
    // Recovery ceiling (TCP-like congestion control): the restore phase must
    // not exceed this rate. Reset to the downgraded rate on every overload
    // (x0.3 hard or x0.7 gentle) so the restore converges to a stable
    // bitrate instead of oscillating drop -> restore -> drop (e.g. 60M
    // overloaded forever).
    private int abrCeiling;
    private long abrCeilingStableSinceNs;
    private long abrWindowStartNs;
    private int abrWindowFrames;
    private long abrLastDownNs;
    private long abrStableSinceNs;
    private long abrLastUpNs;
    // Output delay tracking: ptsUs comes from a monotonic clock whose base
    // differs from elapsedRealtimeNanos (observed ~300s offset on a test device), so
    // only the delta from the calibrated baseline is meaningful.
    private static final int ABR_CALIB_FRAMES = 10;
    private long abrCalibSum;
    private int abrCalibCount;
    private long abrBaselineDelayMs;
    private long abrWindowDelaySum;
    private int abrWindowDelayCount;
    private long abrNegDelaySum; // accumulated negative delayDelta for the current re-zero batch
    private int abrNegDelayCount; // negative frames collected in the current re-zero batch
    private long abrRezeroLogNs; // rate-limit the baseline re-zero log
    private boolean abrRestoring; // bitrate is climbing after a raise; the next overload must degrade immediately
    private boolean abrStateInitialized; // ABR state kept across encoder rebuilds
    // Pulse detection for absolute-latency measurement: frames whose PTS
    // gap from the previous frame exceeds 500ms are pulse frames (the test
    // video flashes a red frame every second). Log the PTS so the PC-side
    // script can timestamp the pulse via logcat arrival time.
    private long pulseLastPtsUs = -1;
    private long pulseLastLogNs;
    private long frameLogLastNs; // rate-limit the FRAME diagnosis log
    // Dynamic frame-rate state (ABR second dimension), kept across encoder
    // rebuilds like the bitrate/ceiling state.
    private int abrFps; // current fps level (120/90/60/30)
    private long fpsStableSinceNs; // no-overload timer for the fps dimension
    private long fpsProbeUntilNs; // probe watch window end (0 = not probing)
    private int fpsProbeFrom; // fps level before the probe (for revert)
    private long fpsRevertUntilNs; // probe cooldown after a revert (anti-oscillation)
    private int fpsRecoverOverloads; // consecutive real overloads during fps recovery; >=2 interrupts the recovery (single big frames must not)
    // Exponential-backoff restore cooldown after an fps DEGRADE: repeated
    // degrades within 3s mean the load is structural (e.g. video encode:
    // 120fps ME cost exceeds encoder throughput) — double the cooldown so
    // fps settles on the lowest workable level instead of flapping.
    private long fpsRestoreBlockedUntilNs; // fps restore blocked until this instant
    private long fpsLastDegradeNs; // last fps-degrade instant
    private long fpsRestoreCooldownNs = 1_000_000_000L; // current cooldown (1s start, 8s cap)
    private int instantOverloadStreak; // consecutive instant-overload frames (fps drop needs >= ABR_FPS_INSTANT_STREAK)
    private long fpsDropCooldownUntilNs; // drain grace after a fps drop: pre-drop queue frames must flush
    private long abrFpsCooldownLogNs; // rate-limit the cooldown log (1/s)
    // Delay-trend lookahead (GCC trendline idea): track the delayDelta
    // direction, not just its absolute value.
    private long lastDelayDeltaMs; // previous frame delayDelta (trend comparison)
    private int trendStreak; // consecutive rising frames
    private long abrTrendLogNs; // rate-limit the trend log (1/s)
    private long abrMildOverloadFirstNs; // first evidence event of the current persistent-mild-overload window
    private int abrMildOverloadCount; // evidence events collected inside the window
    private long abrMildOverloadLastEventNs; // event debounce (one evidence event per 100ms)
    private long abrFpsBufferDropFirstNs; // first 120->90 buffer drop of the current repeat window
    private int abrFpsBufferDropCount; // 120->90 buffer drops collected inside the window
    private long abrFullFpsBurstFirstNs; // first load evidence of the current single-animation burst window
    private int abrFullFpsBurstCount; // load evidence events collected inside the burst window
    private long abrFullFpsBurstLastEventNs; // burst event debounce (one evidence per 100ms)
    private long brOverloadFirstNs; // first overload of the current restore-phase window
    private int brOverloadCount; // overloads counted inside the restore-phase window
    private long abrFpsFloorLogNs; // rate-limit the "fps already at floor" log
    // Frame-complexity lookahead: the encoded frame size reflects content
    // complexity (a complex frame costs more to encode, so the current
    // frame is already slow). A frame larger than the per-frame bitrate
    // budget x1.5 degrades immediately (one-frame response ~8ms, before
    // the backlog-based detection fires ~75ms later), giving preventive
    // low bitrate during animations. Static frames are small and never
    // trigger, keeping high bitrate on still scenes.
    private static final int ABR_COMPLEXITY_FACTOR = 3; // x1.5 as budget*3/2 (reverted from x4: UI animations stutter without the complex lookahead; must not change back)
    private long abrLastComplexLogNs; // rate-limit the complex-frame log (1/s)
    private long abrInstantLogNs; // rate-limit the instant-overload log (1/s; keeps the bitrate/fps steps readable)
    private long abrWindowLogNs; // rate-limit the window-overload log (1/s)

    private final SurfaceCapture capture;
    private final Streamer streamer;
    private final DeviceMessageSender deviceMessageSender;
    private long lastAbrMsgNs;
    private final String encoderName;
    private final List<CodecOption> codecOptions;
    private final int videoBitRate;
    private final int maxSize;
    private final float maxFps;
    private final boolean downsizeOnError;
    private final int minSizeAlignment;
    private final boolean ignoreVideoEncoderConstraints;
    // Old devices (Android < 10) have high encoder/capture delay jitter: the
    // ABR would mistake it for permanent overload and oscillate between 1M
    // and the configured bitrate, freezing the picture. Keep the configured
    // bitrate/fps untouched on these devices.
    private final boolean legacyDevice;

    private boolean firstFrameSent;
    private int consecutiveErrors;

    private Thread thread;
    private final AtomicBoolean stopped = new AtomicBoolean();

    private final CaptureControl captureControl = new CaptureControl();

    private VideoConstraints videoConstraints;

    public SurfaceEncoder(SurfaceCapture capture, Streamer streamer, Options options,
            DeviceMessageSender deviceMessageSender) {
        this.capture = capture;
        this.streamer = streamer;
        this.deviceMessageSender = deviceMessageSender;
        this.videoBitRate = options.getVideoBitRate();
        this.maxSize = options.getMaxSize();
        this.maxFps = options.getMaxFps();
        // fps level must be initialized before streamCapture() builds the
        // MediaFormat (effectiveMaxFps()); encode() re-applies it on the
        // first session too (idempotent).
        this.abrFps = fpsRestoreCeiling();
        this.codecOptions = options.getVideoCodecOptions();
        this.encoderName = options.getVideoEncoder();
        this.downsizeOnError = options.getDownsizeOnError();
        this.minSizeAlignment = options.getMinSizeAlignment();
        this.ignoreVideoEncoderConstraints = options.getIgnoreVideoEncoderConstraints();
        this.legacyDevice = Build.VERSION.SDK_INT < Build.VERSION_CODES.Q;
        if (legacyDevice) {
            Ln.i("Video ABR disabled for legacy device (SDK=" + Build.VERSION.SDK_INT + ")");
        }
    }

    private void streamCapture() throws IOException, ConfigurationException {
        Codec codec = streamer.getCodec();
        MediaCodec mediaCodec = createMediaCodec(codec, encoderName);
        // NOTE: the MediaFormat is created per encoder session (inside the
        // reset loop below): the ABR fps level may change and trigger a
        // rebuild, and KEY_MAX_FPS_TO_ENCODER is only read at configure()
        // time. createFormat() therefore takes the current effective fps.
        MediaFormat format = null;

        MediaCodecInfo.VideoCapabilities caps;
        int alignment;
        if (ignoreVideoEncoderConstraints) {
            caps = null;
            alignment = 1;
        } else {
            caps = mediaCodec.getCodecInfo().getCapabilitiesForType(codec.getMimeType()).getVideoCapabilities();
            assert caps != null; // caps cannot be null for a video codec
            alignment = Math.max(caps.getWidthAlignment(), caps.getHeightAlignment());
            Ln.d("Video codec size alignment requirement: " + alignment + "px");
        }
        if (alignment < minSizeAlignment) {
            alignment = minSizeAlignment;
            Ln.d("Actual video size alignment: " + alignment + "px");
        }

        // Do not constrain by the declared video encoder capabilities before encoding actually fails
        videoConstraints = new VideoConstraints(maxSize, alignment, null);

        capture.init(captureControl, videoConstraints);

        try {
            boolean alive;

            streamer.writeVideoHeader();

            int retainedResetReasons = 0;

            do {
                int resetReasons = captureControl.consumeReset();
                if ((resetReasons & CaptureControl.RESET_REASON_TERMINATED) != 0) {
                    break;
                }
                if (retainedResetReasons != 0) {
                    // The reasons for the previous failed encoding must be preserved when retrying
                    resetReasons |= retainedResetReasons;
                    retainedResetReasons = 0;
                }

                capture.prepare();
                Size size = capture.getSize();

                // Rebuild the format per session so a changed ABR fps level
                // (KEY_MAX_FPS_TO_ENCODER) is picked up by configure().
                format = createFormat(codec.getMimeType(), videoBitRate, effectiveMaxFps(), codecOptions);
                format.setInteger(MediaFormat.KEY_WIDTH, size.getWidth());
                format.setInteger(MediaFormat.KEY_HEIGHT, size.getHeight());

                Surface surface = null;
                boolean mediaCodecStarted = false;
                boolean captureStarted = false;
                try {
                    mediaCodec.configure(format, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE);
                    surface = mediaCodec.createInputSurface();

                    capture.start(surface);
                    captureStarted = true;

                    mediaCodec.start();
                    mediaCodecStarted = true;

                    // Set the MediaCodec instance to "interrupt" (by signaling an EOS) on reset
                    captureControl.setRunningMediaCodec(mediaCodec);

                    if (stopped.get()) {
                        alive = false;
                    } else {
                        if (!captureControl.isResetRequested()) {
                            // The reset is due to a resize initiated by the client
                            boolean isClientResize = (resetReasons & CaptureControl.RESET_REASON_CLIENT_RESIZED) != 0
                                    && (resetReasons & CaptureControl.RESET_REASON_DISPLAY_PROPERTIES_CHANGED) == 0;
                            streamer.writeSessionMeta(size.getWidth(), size.getHeight(), isClientResize);

                            // If a reset is requested during encode(), it will interrupt the encoding by an EOS
                            encode(mediaCodec, streamer);
                        }

                        // The capture might have been closed internally (for example if the camera is disconnected)
                        alive = !stopped.get() && !capture.isClosed();
                    }
                } catch (IllegalStateException | IllegalArgumentException | IOException e) {
                    if (IO.isBrokenPipe(e)) {
                        // Do not retry on broken pipe, which is expected on close because the socket is closed by the client
                        throw e;
                    }
                    Ln.e("Capture/encoding error: " + e.getClass().getName() + ": " + e.getMessage());
                    if (!prepareRetry(caps, size)) {
                        throw e;
                    }
                    // Keep the current resetReasons flags for the retry
                    retainedResetReasons = resetReasons;
                    alive = true;
                } finally {
                    captureControl.setRunningMediaCodec(null);
                    if (captureStarted) {
                        capture.stop();
                    }
                    if (mediaCodecStarted) {
                        try {
                            mediaCodec.stop();
                        } catch (IllegalStateException e) {
                            // ignore (just in case)
                        }
                    }
                    mediaCodec.reset();
                    if (surface != null) {
                        surface.release();
                    }
                }
            } while (alive);
        } finally {
            mediaCodec.release();
            capture.release();
        }
    }

    private boolean prepareRetry(MediaCodecInfo.VideoCapabilities caps, Size currentSize) {
        if (firstFrameSent) {
            ++consecutiveErrors;
            if (consecutiveErrors < MAX_CONSECUTIVE_ERRORS) {
                // Wait a bit to increase the probability that retrying will fix the problem
                SystemClock.sleep(50);
                return true;
            }
        }

        if (!downsizeOnError) {
            // Must fail immediately
            return false;
        }

        if (caps != null && videoConstraints.getEncoderCapabilities() == null) {
            assert !ignoreVideoEncoderConstraints : "caps != null implies !ignoreVideoEncoderConstraints";
            Ln.i("Applying video encoder constraints");
            videoConstraints = videoConstraints.withCapabilities(caps);
            boolean accepted = capture.applyNewVideoConstraints(videoConstraints);
            if (accepted) {
                return true;
            }
        }

        if (consecutiveErrors >= MAX_CONSECUTIVE_ERRORS) {
            // Definitively fail
            return false;
        }

        // Downsizing on error is only enabled if an encoding failure occurs before the first frame, or if the video constraints were not applied
        // (downsizing later could be surprising)

        int newMaxSize = chooseMaxSizeFallback(currentSize);
        if (newMaxSize == 0) {
            // Must definitively fail
            return false;
        }

        boolean accepted = capture.applyNewVideoConstraints(videoConstraints.withMaxSize(newMaxSize));
        if (!accepted) {
            return false;
        }

        // Retry with a smaller size
        Ln.i("Retrying with -m" + newMaxSize + "...");
        return true;
    }

    private static int chooseMaxSizeFallback(Size failedSize) {
        int currentMaxSize = Math.max(failedSize.getWidth(), failedSize.getHeight());
        for (int value : MAX_SIZE_FALLBACK) {
            if (value < currentMaxSize) {
                // We found a smaller value to reduce the video size
                return value;
            }
        }
        // No fallback, fail definitively
        return 0;
    }

    private void encode(MediaCodec codec, Streamer streamer) throws IOException {
        MediaCodec.BufferInfo bufferInfo = new MediaCodec.BufferInfo();
        if (!abrStateInitialized) {
            // First encoder session: start from the configured bitrate.
            currentBitRate = videoBitRate;
            abrCeiling = videoBitRate;
            abrFps = fpsRestoreCeiling();
            abrStateInitialized = true;
        } else {
            // Encoder rebuilt (rotation/size change -> MediaCodec reset):
            // KEEP the ABR state instead of resetting to the initial bitrate
            // (which would re-accumulate backlog and oscillate), and re-apply
            // the kept bitrate to the new encoder instance.
            Bundle p = new Bundle();
            p.putInt(MediaCodec.PARAMETER_KEY_VIDEO_BITRATE, currentBitRate);
            codec.setParameters(p);
            if (abrFps < fpsRestoreCeiling()) {
                // Re-apply the kept fps level to the rebuilt encoder format
                // as a backstop; the primary limiter is the GL layer drop
                // (capture.setTargetFps), which survives encoder rebuilds.
                applyFps(codec, abrFps);
                capture.setTargetFps(abrFps);
            }
            Ln.i("ABR: encoder rebuilt, keeping bitrate=" + currentBitRate
                    + " ceiling=" + abrCeiling + " fps=" + abrFps);
        }
        // Window/calibration state always resets for the new session.
        abrCeilingStableSinceNs = 0;
        abrWindowStartNs = 0;
        abrWindowFrames = 0;
        abrLastDownNs = 0;
        abrLastUpNs = 0;
        abrStableSinceNs = 0;
        abrCalibSum = 0;
        abrCalibCount = 0;
        abrBaselineDelayMs = 0;
        abrWindowDelaySum = 0;
        abrWindowDelayCount = 0;
        // Negative-delay evidence is per session too: a rebuild drains the
        // queue and the next calibration starts from a fresh baseline.
        abrNegDelaySum = 0;
        abrNegDelayCount = 0;
        abrRezeroLogNs = 0;
        // The mild-overload evidence window is per encoder session too:
        // a rebuild drains the queue, so old evidence must not carry over.
        abrMildOverloadFirstNs = 0;
        abrMildOverloadCount = 0;
        abrMildOverloadLastEventNs = 0;
        abrFpsBufferDropFirstNs = 0;
        abrFpsBufferDropCount = 0;
        abrFullFpsBurstFirstNs = 0;
        abrFullFpsBurstCount = 0;
        abrFullFpsBurstLastEventNs = 0;
        // The fps LEVEL and its probe/watch state are kept across rebuilds
        // (an fps change does not rebuild the encoder by itself: the level
        // is applied via GL-layer thinning, and the encoder's configure-time
        // max-fps is refreshed on the next rebuild);
        // only the down-debounce restarts with the fresh encoder.

        boolean eos;
        do {
            int outputBufferId = codec.dequeueOutputBuffer(bufferInfo, -1);
            try {
                eos = (bufferInfo.flags & MediaCodec.BUFFER_FLAG_END_OF_STREAM) != 0;
                // On EOS, there might be data or not, depending on bufferInfo.size
                if (outputBufferId >= 0 && bufferInfo.size > 0) {
                    boolean isConfig = (bufferInfo.flags & MediaCodec.BUFFER_FLAG_CODEC_CONFIG) != 0;
                    if (!isConfig) {
                        // If this is not a config packet, then it contains a frame
                        firstFrameSent = true;
                        consecutiveErrors = 0;
                        long ptsUs = bufferInfo.presentationTimeUs;
                        long nowNs = SystemClock.elapsedRealtimeNanos();
                        boolean isKeyFrame = (bufferInfo.flags & MediaCodec.BUFFER_FLAG_KEY_FRAME) != 0;
                        maybeAdaptBitrate(codec, ptsUs, nowNs, isKeyFrame);
                        // Frame-complexity lookahead: complex frame (bigger
                        // than the per-frame bitrate budget x1.5) -> degrade
                        // immediately, before the backlog-based detection ever
                        // fires. Budget uses the current frame rate estimated
                        // from the frame gap (normal 8-17ms; a gap >500ms is a
                        // dropped frame, not a normal frame interval).
                        if (!legacyDevice) {
                            long gapUs = ptsUs - pulseLastPtsUs;
                            if (gapUs > 0 && gapUs < 500_000) {
                                long fps = 1_000_000 / gapUs;
                                if (fps > 0) {
                                    long budgetBytes = currentBitRate / fps / 8;
                                    if (budgetBytes > 0
                                            && !isKeyFrame // I frames are large by nature; never trigger complex
                                            && bufferInfo.size > budgetBytes * ABR_COMPLEXITY_FACTOR / 2
                                            && nowNs - abrLastDownNs >= ABR_INSTANT_INTERVAL_NS) {
                                        // Complex is a CONTENT signal, not an overload signal:
                                        // it must NOT clear the bitrate-restore stable timer or
                                        // the restoring flag. At the 1M floor a 5KB video frame
                                        // trips complex on every frame (budget ~2KB) — clearing
                                        // the timer there starved the restore forever and locked
                                        // the bitrate at 1M during video playback (the bitrate
                                        // never rises while watching video). The complex degrade
                                        // below (gentle x0.7 while restoring) still applies, and
                                        // the restore raises the rate until the budget grows and
                                        // complex stops firing on its own.
                                        if (nowNs - abrLastComplexLogNs >= 1_000_000_000L) {
                                            Ln.i("ABR: complex frame (bytes="
                                                    + (bufferInfo.size / 1024) + "KB > budget x1.5), degrading");
                                            abrLastComplexLogNs = nowNs;
                                        }
                                        onOverload(codec, nowNs, false, false, -1); // complex: probe-insensitive, never counts restore-overloads (gentle x0.7 via field abrRestoring)
                                    }
                                }
                            }
                        }
                        // Absolute-latency pulse detection: PTS gap > 500ms
                        // marks a pulse frame (test video: 1 red frame per
                        // second). Rate-limited to 200ms so one pulse logs
                        // once (the 83ms pulse spans ~5 frames, only the
                        // first has a gap > 500ms anyway).
                        if (pulseLastPtsUs >= 0
                                && ptsUs - pulseLastPtsUs > 500_000
                                && nowNs - pulseLastLogNs >= 200_000_000L) {
                            Ln.i("PULSE: pts=" + ptsUs);
                            pulseLastLogNs = nowNs;
                        }
                        pulseLastPtsUs = ptsUs;
                        // Frame diagnosis: type (I/P) + calibrated delay
                        // delta + size. I frames always logged; P frames when
                        // delayed >100ms (rate-limited to 500ms so a sustained
                        // delay does not spam) or sampled every 500ms.
                        // delayDelta = encode duration of this frame (the
                        // suspect for the splash first-frame latency: a large
                        // 2K I frame may take 200-500ms to encode).
                        boolean isKey = (bufferInfo.flags & MediaCodec.BUFFER_FLAG_KEY_FRAME) != 0;
                        // Negative delayDelta means "faster than the startup
                        // baseline", not an overload: clamp to 0 for diagnosis.
                        long fdelay = abrBaselineDelayMs > 0
                                ? Math.max(0, (nowNs - ptsUs * 1000) / 1_000_000 - abrBaselineDelayMs) : -1;
                        if (isKey || (fdelay > 100 && nowNs - frameLogLastNs >= 500_000_000L)
                                || nowNs - frameLogLastNs >= 500_000_000L) {
                            Ln.i("FRAME: type=" + (isKey ? "I" : "P") + " pts=" + ptsUs
                                    + " delayDelta=" + fdelay + "ms size="
                                    + (bufferInfo.size / 1024) + "KB");
                            frameLogLastNs = nowNs;
                        }
                        // ABR state report to the client overlay (bitrate +
                        // fps level), 500ms. The sender is null in non-display
                        // scenarios (e.g. camera), so guard against it.
                        if (deviceMessageSender != null
                                && nowNs - lastAbrMsgNs >= 500_000_000L) {
                            lastAbrMsgNs = nowNs;
                            deviceMessageSender.send(
                                    DeviceMessage.createAbrState(currentBitRate, (int) effectiveMaxFps()));
                        }
                    }

                    ByteBuffer codecBuffer = codec.getOutputBuffer(outputBufferId);
                    streamer.writePacket(codecBuffer, bufferInfo);
                }
            } finally {
                if (outputBufferId >= 0) {
                    codec.releaseOutputBuffer(outputBufferId, false);
                }
            }
        } while (!eos);
    }

    /**
     * Short-window encoder overload detection and adaptive bitrate reduction.
     * Called once per encoded (output) frame. Every ABR_WINDOW_NS, compare the
     * actual number of encoded frames with the expected number (maxFps x
     * window). If the screen is not static and the ratio is too low, the
     * encoder is overloaded: step the bitrate down (seamless via
     * MediaCodec.setParameters, no resolution change). After a stable period
     * the bitrate is raised back step by step.
     */
    private void maybeAdaptBitrate(MediaCodec codec, long ptsUs, long nowNs, boolean isKeyFrame) {
        if (legacyDevice) {
            // Legacy compatibility: no dynamic bitrate/fps adjustment.
            return;
        }
        // Delay calibration: the first frames measure the baseline encoder
        // delay (ptsUs and elapsedRealtime use different monotonic bases).
        long delayMs = (nowNs - ptsUs * 1000) / 1_000_000;
        if (abrCalibCount < ABR_CALIB_FRAMES) {
            abrCalibSum += delayMs;
            abrCalibCount++;
            if (abrCalibCount == ABR_CALIB_FRAMES) {
                abrBaselineDelayMs = abrCalibSum / ABR_CALIB_FRAMES;
                Ln.i("ABR: delay baseline=" + abrBaselineDelayMs + "ms");
                // Clock anchor for absolute-latency measurement: the device
                // epoch at this SystemClock instant. PTS is device
                // SystemClock (uptime-based) microseconds, NOT epoch; device
                // encoding time (epoch) of any frame is boot_epoch + pts/1000
                // where boot_epoch = epoch - pts_us/1000 captured here. The
                // pc-side script parses this line to convert frame pts to
                // device epoch, then applies its own pc<->device offset.
                Ln.i("ABR: clock anchor pts_us=" + ptsUs
                        + " epoch=" + System.currentTimeMillis());
            }
            return;
        }
        long rawDelayDelta = delayMs - abrBaselineDelayMs;
        // Negative-delay re-zero: negative means the current pipeline is
        // FASTER than the startup-calibrated baseline (the first frames were
        // measured with a cold encoder queue). It must never cancel positive
        // evidence in the window average, so it is clamped to 0 below. If
        // negative deltas persist, the baseline was polluted by the startup
        // backlog: pull it down in bounded steps toward the real low-latency
        // floor, which restores the delay-trigger sensitivity.
        if (rawDelayDelta < 0) {
            abrNegDelaySum += rawDelayDelta;
            abrNegDelayCount++;
            if (abrNegDelayCount >= ABR_DELAY_REZERO_FRAMES) {
                long correction = -abrNegDelaySum / abrNegDelayCount;
                abrNegDelaySum = 0;
                abrNegDelayCount = 0;
                if (correction >= ABR_DELAY_REZERO_MIN_STEP_MS) {
                    correction = Math.min(correction, ABR_DELAY_REZERO_STEP_MS);
                    long newBaseline = Math.max(0, abrBaselineDelayMs - correction);
                    if (nowNs - abrRezeroLogNs >= ABR_DELAY_REZERO_LOG_NS) {
                        Ln.i("ABR: baseline re-zero " + abrBaselineDelayMs + "->" + newBaseline
                                + " (negative delay evidence, correction=" + correction + "ms)");
                        abrRezeroLogNs = nowNs;
                    }
                    abrBaselineDelayMs = newBaseline;
                }
            }
        } else {
            abrNegDelaySum = 0;
            abrNegDelayCount = 0;
        }
        long delayDelta = Math.max(0, rawDelayDelta);
        // Delay-trend lookahead (GCC trendline idea): a delayDelta that keeps
        // rising means the encoder queue is STARTING to build — act before the
        // absolute threshold (25ms) is ever reached. 3 consecutive rising
        // frames (each +2ms, current >10ms) concedes the bitrate gently x0.7.
        // Static scenes jitter randomly (never 3 monotonic rises), video
        // keeps delayDelta flat — no false triggers.
        if (delayDelta > lastDelayDeltaMs + 2 && delayDelta > 10) {
            trendStreak++;
        } else {
            trendStreak = 0;
        }
        lastDelayDeltaMs = delayDelta;
        if (trendStreak >= 3 && nowNs - abrLastDownNs >= 50_000_000L
                && currentBitRate > ABR_MILD_FPS_BITRATE_THRESHOLD) {
            abrLastDownNs = nowNs;
            if (nowNs - abrTrendLogNs >= 1_000_000_000L) {
                Ln.i("ABR: delay trend rising (streak=" + trendStreak
                        + ", delayDelta=" + delayDelta + "ms), conceding");
                abrTrendLogNs = nowNs;
            }
            lowerBitrate(codec, nowNs, true); // gentle x0.7: it is only a trend, not a confirmed overload
        }
        // Instant single-frame trigger: one frame over the threshold degrades
        // immediately (startup bursts are caught within ~10ms instead of
        // waiting a full window). Unified 80ms debounce for the down chain;
        // the window detection below remains as a backstop. Restore/ceiling
        // mechanism is untouched.
        if (delayDelta > instantTriggerMs(nowNs)) {
            // The fps-degrade streak counts REAL overloads only (>=90ms):
            // mild blips (<100ms) must never drop the frame rate — at the
            // 1M floor they used to trip streak=3 and oscillate fps 120<->60
            // forever, which blocked the bitrate restore (6s to get
            // from blur to watchable was too slow). Mild blips still degrade the bitrate
            // below (gently x0.7) when it is above the floor.
            if (delayDelta >= ABR_FPS_OVERLOAD_MS) {
                instantOverloadStreak++;
            } else {
                instantOverloadStreak = 0; // a mild blip breaks the real-overload streak
            }
            if (nowNs - abrLastDownNs >= ABR_INSTANT_INTERVAL_NS) {
                // Mild blips (<100ms) concede the bitrate gently but must not
                // starve the bitrate-restore stable timer (same starvation
                // class as the fps restore lock); real overloads still reset it.
                if (delayDelta >= ABR_MILD_INSTANT_MS) {
                    abrStableSinceNs = 0;
                }
                boolean wasRestoring = abrRestoring;
                // A mild blip keeps the restore phase alive (bitrate still
                // climbing); only a real overload ends it.
                if (!wasRestoring || delayDelta >= ABR_MILD_INSTANT_MS) {
                    abrRestoring = false;
                }
                // Rate-limit the log (1/s): with the 5ms debounce a sustained
                // overload would otherwise spam one line per trigger and bury
                // the bitrate/fps step lines the user watches.
                if (nowNs - abrInstantLogNs >= 1_000_000_000L) {
                    Ln.i("ABR: instant overload (delayDelta=" + delayDelta + "ms), degrading");
                    abrInstantLogNs = nowNs;
                }
                // Persistent mild overload at low bitrate: tablets with
                // "gentle" sustained overloads never reach the 90ms fast
                // path. When the evidence window is full, force the fps
                // step now, BEFORE the bitrate path touches the 90 buffer
                // level (so 120 goes directly to 60 via the fast path).
                if (notePersistentMildOverload(codec, nowNs, delayDelta)) {
                    return;
                }
                // Single-animation burst at full fps: count this instant
                // overload as one evidence event even when the bitrate is
                // still high. Real overloads (>=90ms) stay on the existing
                // fast path and do not participate in the burst.
                if (delayDelta >= fpsEvidenceThreshold(nowNs) && delayDelta < ABR_FPS_OVERLOAD_MS) {
                    if (noteFullFpsOverloadBurst(codec, nowNs)) {
                        onOverload(codec, nowNs, !isKeyFrame, wasRestoring, delayDelta);
                        return;
                    }
                }
                // An I-frame spike is momentary (its encode cost is one frame);
                // during a probe watch window it must not abort the probe.
                onOverload(codec, nowNs, !isKeyFrame, wasRestoring, delayDelta);
            }
        } else {
            instantOverloadStreak = 0; // non-instant frame resets the streak
        }
        abrWindowDelaySum += delayDelta;
        abrWindowDelayCount++;

        if (abrWindowStartNs == 0) {
            abrWindowStartNs = nowNs;
            abrWindowFrames = 1;
            return;
        }
        abrWindowFrames++;
        long elapsed = nowNs - abrWindowStartNs;
        if (elapsed < ABR_WINDOW_NS) {
            return;
        }

        // Window complete: evaluate both overload signals.
        long expected = (long) (maxFps * (elapsed / 1_000_000_000.0));
        int actual = abrWindowFrames;
        long delayAvg = abrWindowDelayCount > 0
                ? abrWindowDelaySum / abrWindowDelayCount : 0;
        abrWindowStartNs = nowNs;
        abrWindowFrames = 0;
        abrWindowDelaySum = 0;
        abrWindowDelayCount = 0;

        // Static screens produce almost no frames: never treat them as overload.
        boolean staticScreen = actual < ABR_MIN_FRAMES_IN_WINDOW;
        boolean delayOk = delayAvg < delayTriggerMs(nowNs);
        // Only the encoder output delay (delta vs calibrated baseline) is the
        // overload signal: a low frame count alone is NOT overload (the source
        // may render few frames, e.g. animations or idle scenes).
        boolean healthy = staticScreen || delayOk;

        if (!healthy) {
            abrStableSinceNs = 0;
            // Overload during a restore phase must interrupt the restore
            // immediately: skip the down-debounce so the bitrate drops right
            // away instead of staying high while the page keeps stuttering.
            boolean restoring = abrRestoring;
            abrRestoring = false;
            // A window-average in the 30-90ms band is evidence too: the
            // tablet failure mode is a sustained mild average, not a single
            // >=90ms spike. If this completes the evidence window, the fps
            // drop is forced and the bitrate path is skipped for this window.
            if (notePersistentMildOverload(codec, nowNs, delayAvg)) {
                return;
            }
            // Count this window overload as burst evidence too (only at the
            // fps evidence bar; 30-39ms averages remain bitrate-only).
            if (delayAvg >= fpsEvidenceThreshold(nowNs) && delayAvg < ABR_FPS_OVERLOAD_MS) {
                noteFullFpsOverloadBurst(codec, nowNs);
            }
            if (restoring || nowNs - abrLastDownNs >= ABR_MIN_DOWN_INTERVAL_NS) {
                if (nowNs - abrWindowLogNs >= 1_000_000_000L) {
                    Ln.i("ABR: overloaded"
                            + (restoring ? " during restore, degrading" : "")
                            + " (delayDelta=" + delayAvg
                            + "ms, frames=" + actual + "/" + expected + ")");
                    abrWindowLogNs = nowNs;
                }
                onOverload(codec, nowNs, true, restoring, -1); // window overload: sustained, probe-sensitive
            }
            return;
        }
        brOverloadFirstNs = 0; // not restoring anymore: the restore-phase window is void
        brOverloadCount = 0;

        // Healthy: fps-first restore, bitrate last. The fps dimension owns
        // the recovery while below full fps; the bitrate restore (existing
        // 150ms + ceiling logic) only runs after fps is back at full rate
        // AND has been stable for ABR_FPS_STABLE_PROBE_NS.
        if (fpsStableSinceNs == 0) {
            fpsStableSinceNs = nowNs;
        }
        // Probe watch window: no overload -> confirm the probed level.
        if (fpsProbeUntilNs > 0) {
            if (nowNs >= fpsProbeUntilNs) {
                Ln.i("ABR: fps probe confirmed " + abrFps + " (stable)");
                fpsProbeUntilNs = 0;
                fpsProbeFrom = 0;
                fpsRecoverOverloads = 0; // fresh level: fresh overload debounce
                // The watch window itself proved the new level stable — credit
                // it so the next probe fires immediately (fast climb back to
                // full fps: the restore was too slow when each level re-waited
                // the full stable period after every confirm).
                fpsStableSinceNs = nowNs - ABR_FPS_STABLE_PROBE_NS;
            }
            return;
        }
        // Below full fps: after 300ms stable (ABR_FPS_STABLE_PROBE_NS), probe
        // one level up. Bitrate stays frozen at the floor while fps < full
        // (ceiling not released). A recent revert blocks probing for the
        // cooldown window.
        if (abrFps < fpsRestoreCeiling()) {
            if (nowNs < fpsRevertUntilNs || nowNs < fpsRestoreBlockedUntilNs) {
                return;
            }
            // Early low-bitrate restore: the fps climb can take seconds
            // (probe watch + backoff), but a static healthy screen must not
            // stay at 1M for that long. Raise the bitrate below the 8M
            // ceiling while the current fps level is stable and the delay is
            // healthy; the fps probe runs in a later window.
            if (nowNs - fpsStableSinceNs >= ABR_FPS_STABLE_PROBE_NS
                    && currentBitRate < ABR_EARLY_BITRATE_RESTORE_MAX
                    && currentBitRate < videoBitRate
                    && delayAvg < delayRecoverMs(nowNs)
                    && nowNs - abrLastUpNs >= (currentBitRate < ABR_FAST_UP_THRESHOLD
                            ? ABR_MIN_UP_INTERVAL_FAST_NS : ABR_MIN_UP_INTERVAL_NS)) {
                Ln.i("ABR: early bitrate restore below full fps (fps=" + abrFps
                        + " stable, delayDelta=" + delayAvg + "ms)");
                raiseBitrate(codec, nowNs);
                return; // let the bitrate step settle before the next fps probe
            }
            if (nowNs - fpsStableSinceNs >= ABR_FPS_STABLE_PROBE_NS) {
                probeFpsUp(codec, nowNs);
            }
            return;
        }
        // Full fps: still require 2s fps-stable before the bitrate restore
        // path (existing logic below) may run.
        if (nowNs - fpsStableSinceNs < ABR_BITRATE_RESTORE_NS) {
            return;
        }

        // Existing bitrate recovery (unchanged): stable period, then raise
        // step by step inside the ceiling.
        if (abrStableSinceNs == 0) {
            abrStableSinceNs = nowNs;
        } else if (nowNs - abrStableSinceNs >= ABR_UP_DELAY_NS
                && currentBitRate < videoBitRate
                && nowNs - abrLastUpNs >= (currentBitRate < ABR_FAST_UP_THRESHOLD
                        ? ABR_MIN_UP_INTERVAL_FAST_NS : ABR_MIN_UP_INTERVAL_NS)
                && delayAvg < delayRecoverMs(nowNs)) {
            // reached the target again: restore phase finished
            abrRestoring = false;
            raiseBitrate(codec, nowNs);
        }
    }

    /**
     * Unified overload action. The bitrate is the primary buffer (instant /
     * complex / window overloads degrade the bitrate first, down to the 1M
     * floor). The fps level additionally degrades via the fast path on a
     * real overload (instant >=90ms), and via the restore-phase guard when
     * the bitrate buffer keeps failing while climbing. Probe watch: a
     * sustained overload reverts the probed fps level; a momentary spike
     * (I frame / complex frame) only lowers the bitrate and never touches fps.
     */
    private void onOverload(MediaCodec codec, long nowNs, boolean probeSensitive, boolean wasRestoring, long delayDelta) {
        // Restore-phase repeated overload (wasRestoring: the bitrate was
        // climbing and fps was at full rate): the bitrate buffer keeps
        // failing, so the fps level must share the load. 2 overloads within
        // ABR_FPS_RESTORE_OVERLOAD_WINDOW_NS -> drop one fps level and stop
        // the restore (abrRestoring was already cleared by the trigger).
        // Sliding/video never sustain overloads at 1M, so a single spike
        // (or a lone flicker) never triggers this path.
        if (wasRestoring && fpsProbeUntilNs <= 0) {
            // Only REAL overloads (window -1, or instant >=100ms) count toward
            // the restore-phase fps drop; mild blips (<100ms) are a bitrate
            // matter (gentle x0.7) and must NOT knock the fps level back down —
            // they were keeping fps permanently locked at 30 (fps never
            // recovered; 60 was dropped back to 30 right after each probe).
            boolean realOverload = delayDelta < 0 || delayDelta >= ABR_MILD_INSTANT_MS;
            if (realOverload) {
                if (brOverloadFirstNs == 0 || nowNs - brOverloadFirstNs > ABR_FPS_RESTORE_OVERLOAD_WINDOW_NS) {
                    brOverloadFirstNs = nowNs;
                    brOverloadCount = 1;
                } else if (++brOverloadCount >= 2) {
                    brOverloadFirstNs = 0;
                    brOverloadCount = 0;
                    Ln.i("ABR: repeated overload during restore, fps degrade (gl drop)");
                    lowerFps(codec, nowNs);
                    return;
                }
            }
        }
        // Overload inside a probe watch window: the probed fps level cannot
        // sustain the load, revert immediately to the previous level.
        if (fpsProbeUntilNs > 0) {
            // Only a REAL overload (window -1 / instant >=100ms) reverts the
            // probed fps level; a mild blip (<100ms) is ignored (during the
            // fps recovery the bitrate normally sits at the 1M floor, so
            // there is nothing to concede) so the probe can survive and fps
            // can actually climb back. One real overload is immune (lone I
            // frame / wallpaper frame would otherwise starve the 60->120
            // climb forever); TWO consecutive real overloads = sustained
            // load, revert.
            if (probeSensitive && (delayDelta < 0 || delayDelta >= ABR_MILD_INSTANT_MS)) {
                if (++fpsRecoverOverloads >= 2) {
                    fpsRecoverOverloads = 0;
                    revertFps(codec, nowNs);
                } else if (currentBitRate > ABR_MILD_FPS_BITRATE_THRESHOLD) {
                    lowerBitrate(codec, nowNs); // lone spike: bitrate only
                }
            } else {
                fpsRecoverOverloads = 0;
            }
            return;
        }
        // Sustained/real overloads (window delayDelta=-1, or instant >=100ms)
        // break the fps stable timer; MILD instant blips (<100ms) only concede
        // the bitrate below — they must NOT starve the fps restore (fps
        // used to stay locked at a low level and never recovered). A momentary complex-frame
        // spike must also not (at the 1M floor the complexity threshold is
        // ~1.5KB so almost every frame qualifies, which would otherwise starve
        // the fps/bitrate recovery forever).
        if (probeSensitive && (delayDelta < 0 || delayDelta >= ABR_MILD_INSTANT_MS)) {
            // Recovery debounce: ONE real overload (window -1 / instant
            // >=100ms) must not kill the stable timer — a lone big frame
            // (I frame, wallpaper) would otherwise starve the fps restore
            // forever (60->120 never recovered). Two consecutive real
            // overloads = sustained load, reset the timer.
            if (++fpsRecoverOverloads >= 2) {
                fpsRecoverOverloads = 0;
                fpsStableSinceNs = 0;
            }
        } else {
            fpsRecoverOverloads = 0; // healthy/mild frames reset the debounce
        }
        // Staged bitrate-first fast path: a REAL overload (instant >=90ms)
        // consumes the bitrate buffer first and the fps level stays stable.
        // fps drops only when the bitrate is already low (<=5M below target
        // or at the 1M floor) and the overload persists. This avoids
        // simultaneous fps+bitrate adjustments and states like 60fps+42M.
        if (probeSensitive && instantOverloadStreak >= ABR_FPS_INSTANT_STREAK) {
            boolean bitrateAlreadyLow = isLowBitrateFpsTier();
            if (bitrateAlreadyLow) {
                if (nowNs < fpsDropCooldownUntilNs) {
                    if (nowNs - abrFpsCooldownLogNs >= 1_000_000_000L) {
                        Ln.i("ABR: fps drop cooldown (draining old frames)");
                        abrFpsCooldownLogNs = nowNs;
                    }
                    return;
                }
                lowerFps(codec, nowNs);
                return;
            }
            // Bitrate still has room: fall through to the bitrate path below
            // and let it consume this overload; fps stays unchanged.
        }
        if (currentBitRate > ABR_MIN_BITRATE) {
            if (isLowBitrateFpsTier()) {
                // Bitrate buffer exhausted: further bitrate cuts cannot help.
                // The fps evidence paths above/outside own the response.
                return;
            }
            // MILD instant blip (30-80ms) concedes gently (x0.7): it is a
            // momentary hiccup — bouncing the bitrate back to the floor on
            // a 39ms blip created the 1M<->2M restore loops the user saw
            // after every splash. REAL overloads (instant >=80ms, or any
            // window overload which passes delayDelta=-1) drop hard (x0.3).
            // A complex-frame spike during the recovery phase also concedes
            // gently (x0.7) so the bitrate keeps climbing afterwards.
            boolean gentle = (!probeSensitive && abrRestoring)
                    || (probeSensitive && delayDelta >= 0 && delayDelta < ABR_MILD_INSTANT_MS);
            boolean mildInstant = probeSensitive && delayDelta >= 0 && delayDelta < ABR_MILD_INSTANT_MS;
            // Restore-phase exemption: during the bitrate climb, ONLY a
            // confirmed overload (window overload / instant >=100ms) knocks
            // the bitrate back down. Everything momentary is exempt:
            //   - mild instant blips (25-100ms): the wireless link jitters
            //     28-30ms constantly at the 15M/60fps spec (the bitrate
            //     never climbed back, leaving the image permanently blurry);
            //   - complex-frame spikes (!probeSensitive): at the 1M floor the
            //     budget is ~1KB so ALMOST EVERY frame is "complex" — they
            //     used to bounce the restore 1M<->2M forever.
            // "Climbing" = bitrate below the target, NOT the abrRestoring
            // flag: abrRestoring is only true right after a raiseBitrate()
            // (the wire jitter keeps delayAvg at the 50ms recover edge, so
            // raiseBitrate stalls and abrRestoring stays false for the whole
            // climb — the exemption must not depend on it).
            boolean climbing = currentBitRate < videoBitRate;
            boolean exempt = climbing && (mildInstant || !probeSensitive);
            if (exempt) {
                return;
            }
            // Buffer-mode fps share: whenever the bitrate buffer engages
            // at full fps (120), drop to 90 together — 90 is the buffer
            // partner (at 120fps the bitrate buffer alone cannot keep the
            // stream responsive: only bitrate callbacks were seen, no 90
            // step). Only delay events at/above the fps evidence bar (or
            // window/complex overloads) may touch fps: 35-39ms instant
            // events are bitrate-only. No-op when fps is already below 120.
            if (abrFps == ABR_FPS_LEVELS[0]
                    && (delayDelta < 0 || delayDelta >= fpsEvidenceThreshold(nowNs))) {
                if (noteFullFpsOverloadBurst(codec, nowNs)) {
                    // This 120->90 buffer drop completed a single-animation
                    // burst: the fast path already took 120->60. Skip the 90
                    // partner level; the bitrate buffer below still runs.
                } else if (noteRepeatedFpsBufferDrop(codec, nowNs)) {
                    // Repeated 120->90 cycles: 90 is not enough. The fast
                    // path already took 120->60; the bitrate buffer below
                    // still runs so the ceiling drops with the fps level.
                } else {
                    Ln.i("ABR: fps buffer 120->90 (shares bitrate load)");
                    capture.setTargetFps(90);
                    abrFps = 90;
                    fpsStableSinceNs = 0;
                    fpsProbeUntilNs = 0;
                    fpsProbeFrom = 0;
                    abrMildOverloadFirstNs = 0; // 90 gets a fresh mild-evidence window
                    abrMildOverloadCount = 0;
                    fpsDropCooldownUntilNs = nowNs + 300_000_000L;
                    abrLastDownNs = nowNs;
                }
            }
            lowerBitrate(codec, nowNs, gentle);
            return;
        }
    }

    /**
     * Single-animation burst at full fps: a real app-open stutter is one
     * animation, not repeated 120->90 cycles. Count any two load evidence
     * events inside 1.5s while still at 120 (a 120->90 buffer drop, an
     * instant 30-90ms overload, or a 30-90ms window overload). Two events
     * mean this animation is structurally overloaded: force 120->60 and
     * hold the degraded level for at least 2s so the animation finishes
     * before any restore probe. A lone spike never reaches two events.
     */
    private boolean noteFullFpsOverloadBurst(MediaCodec codec, long nowNs) {
        if (abrFps != ABR_FPS_LEVELS[0]) {
            abrFullFpsBurstFirstNs = 0;
            abrFullFpsBurstCount = 0;
            return false;
        }
        if (nowNs - abrFullFpsBurstLastEventNs < ABR_FULL_FPS_BURST_EVENT_INTERVAL_NS) {
            return false;
        }
        if (abrFullFpsBurstFirstNs == 0
                || nowNs - abrFullFpsBurstFirstNs > ABR_FULL_FPS_BURST_WINDOW_NS) {
            abrFullFpsBurstFirstNs = nowNs;
            abrFullFpsBurstCount = 1;
            abrFullFpsBurstLastEventNs = nowNs;
            return false;
        }
        abrFullFpsBurstCount++;
        abrFullFpsBurstLastEventNs = nowNs;
        if (abrFullFpsBurstCount < ABR_FULL_FPS_BURST_EVENTS) {
            return false;
        }
        abrFullFpsBurstFirstNs = 0;
        abrFullFpsBurstCount = 0;
        if (nowNs < fpsDropCooldownUntilNs) {
            // Pre-drop queue frames are still draining; start fresh after
            // the drain grace.
            return false;
        }
        Ln.i("ABR: full-fps overload burst (" + ABR_FULL_FPS_BURST_EVENTS
                + " events in " + (ABR_FULL_FPS_BURST_WINDOW_NS / 1_000_000L)
                + "ms), forcing fast fps degrade");
        lowerFps(codec, nowNs);
        // Hold 60 through the animation: block the restore probe for at
        // least the burst dwell even if the exponential backoff is shorter.
        fpsRestoreBlockedUntilNs = Math.max(fpsRestoreBlockedUntilNs,
                nowNs + ABR_FULL_FPS_BURST_DWELL_NS);
        return true;
    }

    /**
     * Repeated 120->90 buffer drops inside a short window: the bitrate
     * buffer keeps engaging while the delay paths see nothing (e.g. complex
     * frames at full bitrate with negative delayDelta). The 90 partner level
     * is not enough, so force the fast fps degrade 120->60. This also keeps
     * the bitrate restore gated behind a full-fps stable period, so the rate
     * stops oscillating back to the configured maximum.
     */
    private boolean noteRepeatedFpsBufferDrop(MediaCodec codec, long nowNs) {
        if (abrFpsBufferDropFirstNs == 0
                || nowNs - abrFpsBufferDropFirstNs > ABR_FPS_BUFFER_REPEAT_WINDOW_NS) {
            abrFpsBufferDropFirstNs = nowNs;
            abrFpsBufferDropCount = 1;
            return false;
        }
        abrFpsBufferDropCount++;
        if (abrFpsBufferDropCount < ABR_FPS_BUFFER_REPEAT_COUNT) {
            return false;
        }
        abrFpsBufferDropFirstNs = 0;
        abrFpsBufferDropCount = 0;
        if (nowNs < fpsDropCooldownUntilNs) {
            // Old frames are still draining from a previous fps change; let
            // the next window start fresh after the drain grace.
            return false;
        }
        Ln.i("ABR: repeated fps buffer cycles 120->90 (count="
                + ABR_FPS_BUFFER_REPEAT_COUNT + "), forcing fast fps degrade");
        lowerFps(codec, nowNs);
        return true;
    }

    /**
     * Persistent mild overload at an already-low bitrate: count 30-90ms
     * evidence events (instant frames or window averages) inside a 1s
     * window. Once the evidence repeats, the remaining overload is encode
     * (ME) cost rather than bitrate cost, so force the fast fps degrade.
     * This is the tablet "gentle sustained overload" path that never reaches
     * the >=90ms fast-degrade trigger.
     */
    private boolean notePersistentMildOverload(MediaCodec codec, long nowNs, long delayDelta) {
        // Only engage once ABR has already pushed the rate low (<=5M below
        // the configured target) or the rate is at the absolute floor.
        if (!isMildFpsCandidate()) {
            abrMildOverloadFirstNs = 0;
            abrMildOverloadCount = 0;
            return false;
        }
        // Real overloads (>=90ms) are owned by the existing fast path: clear
        // the mild evidence so a single big frame cannot complete the count.
        if (delayDelta >= ABR_FPS_OVERLOAD_MS) {
            abrMildOverloadFirstNs = 0;
            abrMildOverloadCount = 0;
            return false;
        }
        if (delayDelta < fpsEvidenceThreshold(nowNs)) {
            return false;
        }
        // One evidence event per 100ms: a burst of 3 consecutive slow frames
        // must not count as persistent overload.
        if (nowNs - abrMildOverloadLastEventNs < ABR_MILD_FPS_EVENT_INTERVAL_NS) {
            return false;
        }
        if (abrMildOverloadFirstNs == 0 || nowNs - abrMildOverloadFirstNs > ABR_MILD_FPS_WINDOW_NS) {
            abrMildOverloadFirstNs = nowNs;
            abrMildOverloadCount = 1;
            abrMildOverloadLastEventNs = nowNs;
            return false;
        }
        abrMildOverloadCount++;
        abrMildOverloadLastEventNs = nowNs;
        if (abrMildOverloadCount < ABR_MILD_FPS_EVENTS) {
            return false;
        }
        // Evidence window full: force the fps step.
        abrMildOverloadFirstNs = 0;
        abrMildOverloadCount = 0;
        if (abrFps == ABR_FPS_LEVELS[ABR_FPS_LEVELS.length - 1]) {
            // Already at the fps floor: there is no lower step.
            return false;
        }
        if (nowNs < fpsDropCooldownUntilNs) {
            // Pre-drop queue frames are still draining; start a fresh
            // evidence window after the drain grace.
            return false;
        }
        if (fpsProbeUntilNs > 0) {
            // A probed level cannot sustain the persistent mild load: revert
            // it and let the 3s revert cooldown block another probe.
            Ln.i("ABR: persistent mild overload during fps probe (delayDelta=" + delayDelta
                    + "ms, bitrate=" + currentBitRate + "), forcing revert");
            revertFps(codec, nowNs);
        } else {
            Ln.i("ABR: persistent mild overload at low bitrate (delayDelta=" + delayDelta
                    + "ms, bitrate=" + currentBitRate + "), forcing fps degrade");
            lowerFps(codec, nowNs);
        }
        return true;
    }

    private boolean isLowBitrateFpsTier() {
        // The rate must already be reduced below the configured target and
        // at <=5M, OR be at the absolute 1M floor (a configured floor counts
        // too: bitrate can no longer act as the buffer).
        return currentBitRate == ABR_MIN_BITRATE
                || (currentBitRate < videoBitRate
                        && currentBitRate <= ABR_MILD_FPS_BITRATE_THRESHOLD);
    }

    private boolean isMildFpsCandidate() {
        return isLowBitrateFpsTier();
    }

    private boolean interactiveNow(long nowNs) {
        long lastInteractionNs = Controller.getLastInteractionNs();
        return lastInteractionNs > 0 && nowNs - lastInteractionNs < ABR_INTERACTION_ACTIVE_NS;
    }

    private long instantTriggerMs(long nowNs) {
        return interactiveNow(nowNs) ? ABR_INSTANT_TRIGGER_MS : ABR_IDLE_INSTANT_TRIGGER_MS;
    }

    private long delayTriggerMs(long nowNs) {
        return interactiveNow(nowNs) ? ABR_DELAY_TRIGGER_MS : ABR_IDLE_DELAY_TRIGGER_MS;
    }

    private long delayRecoverMs(long nowNs) {
        return interactiveNow(nowNs) ? ABR_DELAY_RECOVER_MS : ABR_IDLE_DELAY_RECOVER_MS;
    }

    /**
     * fps evidence bar is bitrate-aware and interaction-aware. Interactive:
     * at low bitrate the bitrate buffer is exhausted so 30ms already
     * qualifies; at high bitrate 40ms. Idle (video/static): always 80ms —
     * 30-49ms video jitter must not churn fps or drive the bitrate to 1M.
     */
    private long fpsEvidenceThreshold(long nowNs) {
        if (!interactiveNow(nowNs)) {
            return ABR_IDLE_FPS_EVIDENCE_MS;
        }
        return isLowBitrateFpsTier() ? ABR_DELAY_TRIGGER_MS : ABR_FPS_EVIDENCE_MS;
    }

    private void lowerFps(MediaCodec codec, long nowNs) {
        brOverloadFirstNs = 0; // new fps level: fresh restore-overload window
        brOverloadCount = 0;
        fpsRecoverOverloads = 0; // new fps level: fresh overload debounce
        abrMildOverloadFirstNs = 0; // new fps level: fresh mild-evidence window
        abrMildOverloadCount = 0;
        abrFpsBufferDropFirstNs = 0; // new fps level: fresh buffer-cycle window
        abrFpsBufferDropCount = 0;
        abrFullFpsBurstFirstNs = 0; // new fps level: fresh burst window
        abrFullFpsBurstCount = 0;
        abrFullFpsBurstLastEventNs = 0;
        int idx = indexOfFps(abrFps);
        if (idx < 0 || idx >= ABR_FPS_LEVELS.length - 1) {
            if (nowNs - abrFpsFloorLogNs >= 1_000_000_000L) {
                Ln.i("ABR: fps already at floor " + abrFps + " (bitrate at floor)");
                abrFpsFloorLogNs = nowNs;
            }
            return;
        }
        // Fast-degrade SKIPS the 90 buffer level (120->60->30 decisive).
        // The 90 level exists ONLY as the bitrate-buffer partner (bitrate
        // engages -> fps 120->90 together) and the smooth restore step
        // (30->60->90->120). No 90 mid-step on the fast path.
        int nextIdx = idx + 1;
        if (nextIdx + 1 < ABR_FPS_LEVELS.length && ABR_FPS_LEVELS[nextIdx] == 90) {
            nextIdx++;
        }
        int newFps = ABR_FPS_LEVELS[nextIdx];
        fpsProbeUntilNs = 0; // cancel any in-flight probe
        fpsProbeFrom = 0;
        Ln.i("ABR: fps degrade " + abrFps + "->" + newFps + " (fast path, gl drop)");
        capture.setTargetFps(newFps);
        abrFps = newFps;
        fpsStableSinceNs = 0;
        abrLastDownNs = nowNs; // same down-debounce as the bitrate dimension
        // Drain grace for the next drop decision: the encoder queue still
        // holds frames captured before this drop (at the old fps); they take
        // up to ~400ms to flush and would fake an overload of the new level.
        fpsDropCooldownUntilNs = nowNs + 300_000_000L; // 300ms drain exemption (more decisive degrades, less lag while draining)
        // Exponential backoff for the fps-restore cooldown: a single degrade
        // (e.g. splash animation) recovers fast (1s); repeated degrades within
        // 3s mean the load is STRUCTURAL (e.g. video encode: 120fps ME cost
        // exceeds the encoder throughput) — double the cooldown so fps
        // settles on the lowest workable level instead of oscillating
        // 120<->60 forever (a video session showed endless
        // degrade/restore flapping).
        if (nowNs - fpsLastDegradeNs < 3_000_000_000L) {
            fpsRestoreCooldownNs = Math.min(fpsRestoreCooldownNs * 2, 8_000_000_000L);
        } else {
            fpsRestoreCooldownNs = 1_000_000_000L;
        }
        fpsLastDegradeNs = nowNs;
        fpsRestoreBlockedUntilNs = nowNs + fpsRestoreCooldownNs;
    }

    private void probeFpsUp(MediaCodec codec, long nowNs) {
        int idx = indexOfFps(abrFps);
        if (idx <= 0) {
            return; // already at the highest level
        }
        int newFps = ABR_FPS_LEVELS[idx - 1];
        fpsProbeFrom = abrFps;
        fpsRecoverOverloads = 0; // fresh probe: fresh overload debounce
        abrMildOverloadFirstNs = 0; // fresh level: fresh mild-evidence window
        abrMildOverloadCount = 0;
        // NOTE: the buffer-cycle window deliberately survives the 90->120
        // probe: repeated 120->90 buffer drops are counted across restore
        // cycles, otherwise the counter would never reach the threshold.
        // Full level = no fps cap: pass 0 to the GL layer (render every
        // frame, no fixed-interval thinning). abrFps still records the full
        // level so effectiveMaxFps() (the encoder MediaFormat cap, still
        // 120) and the client overlay keep reporting it; only the GL
        // thinning is released. Thinning at the full level used to drop
        // jittery frames: a real 120fps source was clipped to ~60-80fps
        // after the first ABR cycle by the fixed 8.33ms interval.
        int glTargetFps = newFps >= fpsRestoreCeiling() ? 0 : newFps;
        Ln.i("ABR: fps restore " + abrFps + "->" + newFps
                + (glTargetFps == 0 ? " (stable, gl unlock)" : " (stable, gl drop)"));
        capture.setTargetFps(glTargetFps);
        abrFps = newFps;
        fpsProbeUntilNs = nowNs + ABR_FPS_PROBE_WATCH_NS;
        fpsStableSinceNs = 0;
    }

    private void revertFps(MediaCodec codec, long nowNs) {
        int from = abrFps;
        int back = fpsProbeFrom > 0 ? fpsProbeFrom : abrFps;
        fpsProbeUntilNs = 0;
        fpsProbeFrom = 0;
        if (back != from) {
            Ln.i("ABR: fps revert " + from + "->" + back + " (overload after probe, gl drop)");
            capture.setTargetFps(back);
            abrFps = back;
        }
        fpsStableSinceNs = 0;
        abrLastDownNs = nowNs;
        fpsRevertUntilNs = nowNs + ABR_FPS_REVERT_COOLDOWN_NS; // no probing for 3s
        fpsRecoverOverloads = 0; // fresh level: fresh debounce
        abrMildOverloadFirstNs = 0; // fresh level: fresh mild-evidence window
        abrMildOverloadCount = 0;
        abrFpsBufferDropFirstNs = 0; // fresh level: fresh buffer-cycle window
        abrFpsBufferDropCount = 0;
        abrFullFpsBurstFirstNs = 0; // fresh level: fresh burst window
        abrFullFpsBurstCount = 0;
        abrFullFpsBurstLastEventNs = 0;
    }

    /**
     * The max-fps actually applied to the encoder MediaFormat: the client
     * cap lowered by the ABR fps level. An fps-level change is applied
     * immediately via GL-layer thinning (capture.setTargetFps); the
     * configure-time KEY_MAX_FPS_TO_ENCODER (which most encoders only honor
     * at configure() time) is refreshed on the next encoder rebuild.
     */
    private float effectiveMaxFps() {
        // At the ABR ceiling (healthy state) the client's exact max-fps
        // must govern. The previous Math.min(maxFps, abrFps) snapped
        // non-standard caps down to the highest standard level below
        // them, because abrFps starts at fpsRestoreCeiling(): 45 -> 30,
        // 75 -> 60 (the encoder was capped at 30/60 while the client
        // asked 45/75). Only a real ABR throttle level below the
        // ceiling may lower the cap further.
        if (abrFps >= fpsRestoreCeiling()) {
            return maxFps;
        }
        return Math.min(maxFps, abrFps);
    }

    private void applyFps(MediaCodec codec, int fps) {
        try {
            Bundle params = new Bundle();
            params.putFloat(KEY_MAX_FPS_TO_ENCODER, fps);
            codec.setParameters(params);
        } catch (Exception e) {
            Ln.w("ABR: setParameters(max-fps) failed: " + e.getMessage());
        }
    }

    private static int indexOfFps(int fps) {
        for (int i = 0; i < ABR_FPS_LEVELS.length; i++) {
            if (ABR_FPS_LEVELS[i] == fps) {
                return i;
            }
        }
        return -1;
    }

    /** Highest fps level allowed by the client max-fps (default 120). */
    private int fpsRestoreCeiling() {
        for (int level : ABR_FPS_LEVELS) {
            if (level <= maxFps) {
                return level;
            }
        }
        return ABR_FPS_LEVELS[ABR_FPS_LEVELS.length - 1];
    }

    private void lowerBitrate(MediaCodec codec, long nowNs) {
        lowerBitrate(codec, nowNs, false);
    }

    private void lowerBitrate(MediaCodec codec, long nowNs, boolean gentle) {
        // Aggressive down-shift: x0.3 (60M -> 18M -> 5.4M -> 5M in 3 steps)
        // so mid/high bitrates drop instantly on stutter.
        // gentle (recovery-phase complex-frame concession): x0.7, stay well
        // above the floor and keep the climb going (no floor bounce).
        int factorNum = gentle ? 7 : 3;
        int newRate = Math.max(ABR_MIN_BITRATE, currentBitRate * factorNum / 10);
        if (newRate == currentBitRate) {
            return;
        }
        // Recovery ceiling = the downgraded rate itself: the restore must
        // not climb back above the rate that just overloaded the encoder
        // (oscillation guard). The ceiling is slowly released (x1.1) after
        // ABR_CEILING_RELEASE_DELAY_NS without overload.
        abrCeiling = newRate;
        abrCeilingStableSinceNs = 0;
        Ln.i("ABR: ceiling capped at " + abrCeiling + " (oscillation guard)");
        Ln.i("ABR: bitrate " + currentBitRate + " -> " + newRate);
        applyBitrate(codec, newRate);
        currentBitRate = newRate;
        abrLastDownNs = nowNs;
    }

    private void raiseBitrate(MediaCodec codec, long nowNs) {
        // Fast recovery: uniform x2 steps (1M -> 2M -> 4M -> 8M -> 16M ->
        // 32M -> 60M), gated by the stable delay (ABR_UP_DELAY_NS) and the
        // up interval (100ms below / 200ms above 16M). Capped by the
        // recovery ceiling, which itself doubles after 300ms without
        // overload; an overload during restore degrades immediately via
        // abrRestoring, so the ceiling is not the primary oscillation guard.
        int factorNum = 2;
        int factorDen = 1;
        int target = currentBitRate * factorNum / factorDen;
        boolean capped = target > abrCeiling;
        if (capped) {
            target = abrCeiling;
        }
        int newRate = Math.min(videoBitRate, target);
        if (newRate == currentBitRate) {
            // Already at the ceiling: start/keep the stable timer, and
            // slowly release the ceiling after a long healthy period.
            if (abrCeilingStableSinceNs == 0) {
                abrCeilingStableSinceNs = nowNs;
            } else if (abrCeiling < videoBitRate
                    && nowNs - abrCeilingStableSinceNs >= ABR_CEILING_RELEASE_DELAY_NS) {
                int oldCeiling = abrCeiling;
                abrCeiling = Math.min(videoBitRate, abrCeiling * 2);
                abrCeilingStableSinceNs = nowNs;
                Ln.i("ABR: ceiling released " + oldCeiling + " -> " + abrCeiling);
            }
            return;
        }
        Ln.i("ABR: stable, restoring bitrate " + currentBitRate + " -> " + newRate
                + (capped ? " (capped at ceiling " + abrCeiling + ")" : ""));
        applyBitrate(codec, newRate);
        currentBitRate = newRate;
        abrLastUpNs = nowNs;
        if (currentBitRate >= abrCeiling) {
            if (abrCeilingStableSinceNs == 0) {
                abrCeilingStableSinceNs = nowNs;
            }
        } else {
            abrCeilingStableSinceNs = 0;
        }
        abrRestoring = newRate < videoBitRate; // an overload during the climb must degrade immediately (cleared once the target is reached)
        brOverloadFirstNs = 0; // fresh restore-phase overload window (repeated-overload fps guard)
        brOverloadCount = 0;
        abrMildOverloadFirstNs = 0; // fresh bitrate step: fresh mild-evidence window
        abrMildOverloadCount = 0;
        // NOTE: the buffer-cycle window deliberately survives the bitrate
        // restore; the next 120->90 drop completes the cycle count.
    }

    private void applyBitrate(MediaCodec codec, int bitrate) {
        try {
            Bundle params = new Bundle();
            params.putInt(MediaCodec.PARAMETER_KEY_VIDEO_BITRATE, bitrate);
            codec.setParameters(params);
        } catch (Exception e) {
            Ln.w("ABR: setParameters failed: " + e.getMessage());
        }
    }

    private static MediaCodec createMediaCodec(Codec codec, String encoderName) throws IOException, ConfigurationException {
        if (encoderName != null) {
            Ln.d("Creating encoder by name: '" + encoderName + "'");
            try {
                MediaCodec mediaCodec = MediaCodec.createByCodecName(encoderName);
                String mimeType = Codec.getMimeType(mediaCodec);
                if (!codec.getMimeType().equals(mimeType)) {
                    Ln.e("Video encoder type for \"" + encoderName + "\" (" + mimeType + ") does not match codec type (" + codec.getMimeType() + ")");
                    throw new ConfigurationException("Incorrect encoder type: " + encoderName);
                }
                return mediaCodec;
            } catch (IllegalArgumentException e) {
                Ln.e("Video encoder '" + encoderName + "' for " + codec.getName() + " not found\n" + LogUtils.buildVideoEncoderListMessage());
                throw new ConfigurationException("Unknown encoder: " + encoderName);
            } catch (IOException e) {
                Ln.e("Could not create video encoder '" + encoderName + "' for " + codec.getName() + "\n" + LogUtils.buildVideoEncoderListMessage());
                throw e;
            }
        }

        try {
            MediaCodec mediaCodec = MediaCodec.createEncoderByType(codec.getMimeType());
            Ln.d("Using video encoder: '" + mediaCodec.getName() + "'");
            return mediaCodec;
        } catch (IOException | IllegalArgumentException e) {
            Ln.e("Could not create default video encoder for " + codec.getName() + "\n" + LogUtils.buildVideoEncoderListMessage());
            throw e;
        }
    }

    private static MediaFormat createFormat(String videoMimeType, int bitRate, float maxFps, List<CodecOption> codecOptions) {
        MediaFormat format = new MediaFormat();
        format.setString(MediaFormat.KEY_MIME, videoMimeType);
        format.setInteger(MediaFormat.KEY_BIT_RATE, bitRate);
        // must be present to configure the encoder, but does not impact the actual frame rate, which is variable
        format.setInteger(MediaFormat.KEY_FRAME_RATE, 60);
        format.setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface);
        if (Build.VERSION.SDK_INT >= AndroidVersions.API_24_ANDROID_7_0) {
            format.setInteger(MediaFormat.KEY_COLOR_RANGE, MediaFormat.COLOR_RANGE_LIMITED);
        }
        format.setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, DEFAULT_I_FRAME_INTERVAL);
        // Make I frames encode faster (measured ~40% smaller/faster): the
        // splash first-frame I frame is the main encoder stall source. This
        // is a vendor key (no android.jar constant); ignored silently by
        // encoders that do not support it.
        format.setInteger("i-frame-qp", 32);
        // display the very first frame, and recover from bad quality when no new frames
        format.setLong(MediaFormat.KEY_REPEAT_PREVIOUS_FRAME_AFTER, REPEAT_FRAME_DELAY_US); // µs
        if (Build.VERSION.SDK_INT >= AndroidVersions.API_23_ANDROID_6_0) {
            // real-time priority
            format.setInteger(MediaFormat.KEY_PRIORITY, 0);
        }
        if (Build.VERSION.SDK_INT >= AndroidVersions.API_26_ANDROID_8_0) {
            // output 1 frame as soon as 1 frame is queued
            format.setInteger(MediaFormat.KEY_LATENCY, 1);
        }
        if (maxFps > 0) {
            // The key existed privately before Android 10:
            // <https://android.googlesource.com/platform/frameworks/base/+/625f0aad9f7a259b6881006ad8710adce57d1384%5E%21/>
            // <https://github.com/Genymobile/scrcpy/issues/488#issuecomment-567321437>
            format.setFloat(KEY_MAX_FPS_TO_ENCODER, maxFps);
        }

        if (codecOptions != null) {
            for (CodecOption option : codecOptions) {
                String key = option.getKey();
                Object value = option.getValue();
                CodecUtils.setCodecOption(format, key, value);
                Ln.d("Video codec option set: " + key + " (" + value.getClass().getSimpleName() + ") = " + value);
            }
        }

        return format;
    }

    @Override
    public void start(TerminationListener listener) {
        thread = new Thread(() -> {
            // Some devices (Meizu) deadlock if the video encoding thread has no Looper
            // <https://github.com/Genymobile/scrcpy/issues/4143>
            Looper.prepare();

            try {
                streamCapture();
            } catch (ConfigurationException e) {
                // Do not print stack trace, a user-friendly error-message has already been logged
            } catch (IOException e) {
                // Broken pipe is expected on close, because the socket is closed by the client
                if (!IO.isBrokenPipe(e)) {
                    Ln.e("Video encoding error", e);
                }
            } finally {
                Ln.d("Screen streaming stopped");
                listener.onTerminated(true);
            }
        }, "video");
        thread.start();
    }

    @Override
    public void stop() {
        if (thread != null) {
            stopped.set(true);
            captureControl.reset(CaptureControl.RESET_REASON_TERMINATED);
        }
    }

    @Override
    public void join() throws InterruptedException {
        if (thread != null) {
            thread.join();
        }
    }
}

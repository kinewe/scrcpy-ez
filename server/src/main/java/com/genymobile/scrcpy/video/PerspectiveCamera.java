package com.genymobile.scrcpy.video;

import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.device.Device;
import com.genymobile.scrcpy.util.Command;
import com.genymobile.scrcpy.util.Ln;

import android.content.Context;
import android.hardware.display.VirtualDisplay;
import android.media.MediaCodec;
import android.media.MediaFormat;
import android.net.LocalSocket;
import android.os.Build;
import android.os.Looper;
import android.view.Surface;

import java.io.DataInputStream;
import java.io.IOException;
import java.lang.reflect.Field;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.lang.reflect.Proxy;
import java.nio.ByteBuffer;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.Locale;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Executor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;

/** Session-scoped desktop input. No camera driver, APK, or default-device permission is modified. */
public final class PerspectiveCamera implements AutoCloseable {
    public interface AssociationOwner { void onOwned(String mac); }
    public static final int WIDTH = 1920;
    public static final int HEIGHT = 1080;
    private final LocalSocket socket;
    private static final int maxFps = 30;
    private final String mac;
    private final AtomicBoolean closed = new AtomicBoolean();
    private final ConcurrentHashMap<Integer, Decoder> decoders = new ConcurrentHashMap<>();
    private final ArrayList<Thread> threads = new ArrayList<>();
    private final String packageName;
    private volatile Object device;
    private volatile Object camera;
    private volatile CameraEvictionNotice evictionNotice;
    private volatile int associationId;
    private volatile boolean associationOwned;
    private volatile long lastInput = System.nanoTime();
    private volatile String startupStage = "association";

    private void traceStage(String stage) {
        startupStage = stage;
        Ln.i("Perspective lifecycle stage=" + stage);
    }

    public static String associationMac(int scid) {
        return String.format(Locale.ROOT, "02:53:%02x:%02x:%02x:%02x", (scid >> 24) & 255, (scid >> 16) & 255, (scid >> 8) & 255, scid & 255);
    }
    public String getAssociationMac() { return mac; }

    public static void removeAssociation(String address) {
        if (address == null || !address.matches("02:53:([0-9a-f]{2}:){3}[0-9a-f]{2}")) { return; }
        try {
            Command.exec("cmd", "companiondevice", "disassociate", "0", "com.android.shell", address);
        } catch (Exception e) {
            Ln.w("Perspective camera association cleanup could not complete");
        }
    }

    /**
     * Removal is retried and verified: the camera service may still be tearing
     * down the virtual provider when the first disassociate lands, and a single
     * best-effort call is how stale associations used to survive.
     */
    public static void removeAssociationWithRetry(String address) {
        if (address == null || address.isEmpty()) { return; }
        for (int attempt = 1; attempt <= 3; ++attempt) {
            removeAssociation(address);
            try {
                if (findAssociation(address) == 0) {
                    Ln.i("Perspective association removed");
                    return;
                }
            } catch (Exception e) {
                Ln.w("Perspective association verification failed: " + e);
            }
            try { Thread.sleep(200L * attempt); } catch (InterruptedException e) { Thread.currentThread().interrupt(); return; }
        }
        Ln.w("Perspective association could not be removed: " + address);
    }

    private static Object call(Object object, String name, Class<?>[] types, Object... args) throws Exception {
        try {
            return object.getClass().getMethod(name, types).invoke(object, args);
        } catch (InvocationTargetException e) {
            throw new IOException("Perspective camera: " + name, e.getTargetException());
        }
    }

    private static void attribute(Object manager, Context context) throws Exception {
        for (Field field : manager.getClass().getDeclaredFields()) {
            if (field.getType() == Context.class) {
                field.setAccessible(true); field.set(manager, context);
            }
        }
    }

    private static int findAssociation(String address) throws Exception {
        String listing = Command.execReadOutput("cmd", "companiondevice", "list", "0");
        for (String line : listing.split("\\r?\\n")) {
            String[] fields = line.split("\\|");
            if (fields.length == 3 && "com.android.shell".equals(fields[1].trim()) && address.equalsIgnoreCase(fields[2].trim())) {
                return Integer.parseInt(fields[0].trim());
            }
        }
        return 0;
    }

    public PerspectiveCamera(int scid, String pkg, LocalSocket input, AssociationOwner owner) throws IOException {
        this(scid, pkg, input, owner, false);
    }

    public PerspectiveCamera(int scid, String pkg, LocalSocket input, AssociationOwner owner, boolean notices) throws IOException {
        socket = input;
        mac = associationMac(scid);
        String target = pkg == null ? "" : pkg.replaceFirst("^\\+", "");
        packageName = (Device.WECHAT_PACKAGE + ":scan").equals(target) ? Device.WECHAT_PACKAGE : target;
        if (Build.VERSION.SDK_INT < 36 || !packageName.matches("[A-Za-z0-9_]+(\\.[A-Za-z0-9_]+)+")) {
            throw new IOException("Perspective camera requires Android 16 and an explicit application package");
        }
        // Vendor camera services may block inside synchronous Binder calls.
        // Interrupting a Java thread cannot cancel those calls. Bound startup,
        // but never halt before attempting the same staged teardown as a normal
        // close: an abrupt halt is what leaves stale dynamic camera mappings
        // behind in cameraserver.
        AtomicBoolean initializing = new AtomicBoolean(true);
        Thread startupDeadline = new Thread(() -> {
            try { Thread.sleep(12000); } catch (InterruptedException e) { return; }
            if (initializing.compareAndSet(true, false)) {
                String message = "SCRCPY_EZ_PERSPECTIVE_UNAVAILABLE: virtual camera initialization did not respond at stage="
                        + startupStage + "; restart the device or disable perspective camera";
                Ln.e(message);
                emergencyCleanup("startup deadline at stage=" + startupStage);
            }
        }, "perspective-startup-deadline");
        startupDeadline.setDaemon(true); startupDeadline.start();
        // SIGTERM/System.exit paths do not run the normal finally block. Run the
        // same bounded teardown from a daemon helper so an orderly process stop
        // still unregisters the virtual camera instead of relying on CleanUp.
        try {
            Thread hook = new Thread(() -> {
                Thread closeThread = new Thread(this::close, "perspective-shutdown-close");
                closeThread.setDaemon(true);
                closeThread.start();
                try { closeThread.join(6000); } catch (InterruptedException e) { Thread.currentThread().interrupt(); }
            }, "perspective-shutdown-hook");
            Runtime.getRuntime().addShutdownHook(hook);
        } catch (IllegalStateException | SecurityException e) {
            Ln.w("Perspective shutdown hook unavailable: " + e);
        }
        try {
            Context context = FakeContext.get();
            // Never adopt/remove an association that predates this session.
            if (findAssociation(mac) != 0) {
                throw new IOException("Perspective camera session association already exists; retry the window");
            }
            Command.exec("cmd", "companiondevice", "associate", "0", "com.android.shell", mac,
                    "android.app.role.COMPANION_DEVICE_APP_STREAMING", "true");
            associationOwned = true;
            if (owner != null) { owner.onOwned(mac); }
            Ln.i("Perspective association command completed");
            for (int attempt = 0; attempt < 20; ++attempt) {
                associationId = findAssociation(mac);
                if (associationId != 0) { break; }
                Thread.sleep(100);
            }
            if (associationId == 0) { throw new IOException("Perspective camera association unavailable"); }
            traceStage("device-configuration");
            Object manager = context.getSystemService("virtualdevice");
            attribute(manager, context);
            Class<?> params = Class.forName("android.companion.virtual.VirtualDeviceParams");
            Object builder = Class.forName(params.getName() + "$Builder").getConstructor().newInstance();
            call(builder, "setName", new Class[] {String.class}, "scrcpy-ez-perspective");
            // Match scrcpy's existing application-window behavior. This applies
            // only to our virtual display, never to the phone's lock settings.
            call(builder, "setLockState", new Class[] {int.class}, params.getField("LOCK_STATE_ALWAYS_UNLOCKED").getInt(null));
            call(builder, "setDevicePolicy", new Class[] {int.class, int.class},
                    params.getField("POLICY_TYPE_CAMERA").getInt(null), params.getField("DEVICE_POLICY_CUSTOM").getInt(null));
            traceStage("device-create");
            device = call(manager, "createVirtualDevice", new Class[] {int.class, params}, associationId,
                    call(builder, "build", new Class[] {}));
            traceStage("camera-configuration");
            Class<?> config = Class.forName("android.companion.virtual.camera.VirtualCameraConfig");
            Object cb = Class.forName(config.getName() + "$Builder").getConstructor(String.class).newInstance("scrcpy-ez-screen");
            Class<?> callback = Class.forName("android.companion.virtual.camera.VirtualCameraCallback");
            Object listener = Proxy.newProxyInstance(callback.getClassLoader(), new Class[] {callback}, (proxy, method, args) -> {
                switch (method.getName()) {
                    case "onStreamConfigured":
                        CameraEvictionNotice configuredNotice = evictionNotice;
                        if (configuredNotice != null) { configuredNotice.onStreamConfigured(); }
                        Ln.i("Perspective stream: id=" + args[0] + " output=" + args[2] + "x" + args[3] + " format=" + args[4]);
                        startDecoder((Integer) args[0], (Surface) args[1]); break;
                    case "onProcessCaptureRequest":
                        Decoder requested = decoders.get((Integer) args[0]);
                        if (requested != null) { requested.onRequest(); }
                        break;
                    case "onStreamClosed": closeDecoder((Integer) args[0]); break;
                    case "hashCode": return System.identityHashCode(proxy);
                    case "equals": return proxy == args[0];
                    case "toString": return "PerspectiveCameraCallback";
                    default: break;
                }
                return null;
            });
            call(cb, "addStreamConfig", new Class[] {int.class, int.class, int.class, int.class}, WIDTH, HEIGHT, 1, maxFps);
            call(cb, "setLensFacing", new Class[] {int.class}, 1);
            call(cb, "setSensorOrientation", new Class[] {int.class}, 90);
            call(cb, "setVirtualCameraCallback", new Class[] {Executor.class, callback}, (Executor) Runnable::run, listener);
            traceStage("camera-create");
            camera = call(device, "createVirtualCamera", new Class[] {config}, call(cb, "build", new Class[] {}));
            traceStage("camera-created");
            try {
                String id = (String) call(camera, "getId", new Class[] {});
                Ln.i("Perspective camera events: id=" + id);
                if (notices) {
                    evictionNotice = new CameraEvictionNotice(id, new CameraEvictionNotice.Listener() {
                        @Override public void onEvicted() {
                            if (!closed.get()) { Ln.i("SCRCPY_EZ_CAMERA_EVICTED"); state(4); }
                        }
                        @Override public void onUnavailable() {
                            Ln.w("Perspective camera occupancy notices unavailable; camera and projection continue");
                        }
                    });
                }
            } catch (Exception e) { Ln.w("Perspective camera ID or occupancy notice unavailable on this system: " + e); }
            String persistent = (String) call(device, "getPersistentDeviceId", new Class[] {});
            if (!("companion:" + associationId).equals(persistent)) {
                throw new IOException("Refusing a camera grant outside the session device");
            }
            traceStage("camera-permission");
            call(context.getSystemService("permission"), "grantRuntimePermission",
                    new Class[] {String.class, String.class, String.class}, packageName, "android.permission.CAMERA", persistent);
            traceStage("ready");
            Thread receiver = new Thread(this::receive, "perspective-input");
            receiver.setDaemon(true); threads.add(receiver); receiver.start();
            Thread lease = new Thread(() -> {
                while (!closed.get()) {
                    if (System.nanoTime() - lastInput > 10000000000L) {
                        fail("Desktop input timed out"); break;
                    }
                    try { Thread.sleep(500); } catch (InterruptedException e) { break; }
                }
            }, "perspective-lease");
            lease.setDaemon(true); threads.add(lease); lease.start();
            Ln.i("Perspective camera ready: " + WIDTH + "x" + HEIGHT + " / " + maxFps + "fps maximum");
        } catch (Exception e) {
            initializing.set(false);
            Ln.e("Perspective initialization failed", e);
            close(); throw new IOException("Could not initialize perspective camera", e);
        } finally {
            initializing.set(false); startupDeadline.interrupt();
        }
    }

    public VirtualDisplay createDisplay(int width, int height, int dpi, Surface surface, int flags) throws Exception {
        Class<?> config = Class.forName("android.hardware.display.VirtualDisplayConfig");
        Object builder = Class.forName(config.getName() + "$Builder").getConstructor(String.class, int.class, int.class, int.class)
                .newInstance("scrcpy-ez-perspective", width, height, dpi);
        call(builder, "setSurface", new Class[] {Surface.class}, surface);
        // VirtualDevice owns its display group. Keep only flags accepted by its API.
        call(builder, "setFlags", new Class[] {int.class}, flags & (1 | 2 | 8 | 64 | 128 | 512 | 1024 | 16384));
        VirtualDisplay display = (VirtualDisplay) call(device, "createVirtualDisplay",
                new Class[] {config, Executor.class, VirtualDisplay.Callback.class},
                call(builder, "build", new Class[] {}), (Executor) Runnable::run, null);
        try {
            // Preserve tasks even if the server dies; scoped to this newly owned display.
            Object wm = Class.forName("android.view.WindowManagerGlobal").getMethod("getWindowManagerService").invoke(null);
            call(wm, "setRemoveContentMode", new Class[] {int.class, int.class}, display.getDisplay().getDisplayId(), 1);
            Object mode = call(wm, "getRemoveContentMode", new Class[] {int.class}, display.getDisplay().getDisplayId());
            if (!Integer.valueOf(1).equals(mode)) { throw new IOException("Task retention policy was not applied"); }
            Ln.i("Perspective display task retention verified");
        } catch (Exception e) {
            display.release(); throw new IOException("Perspective camera cannot preserve application tasks on this device", e);
        }
        return display;
    }

    private synchronized void state(int value) {
        if (closed.get()) { return; }
        try { socket.getOutputStream().write(value); socket.getOutputStream().flush(); }
        catch (IOException e) { fail("Camera state channel disconnected"); }
    }

    private void fail(String reason) {
        if (!closed.get()) {
            Ln.w("Perspective camera: " + reason);
            Looper.getMainLooper().quitSafely();
        }
    }

    private void receive() {
        try {
            DataInputStream in = new DataInputStream(socket.getInputStream());
            if (in.readInt() != 0x455a5643 || in.readInt() != WIDTH || in.readInt() != HEIGHT) {
                throw new IOException("Unsupported desktop input format");
            }
            state(decoders.isEmpty() ? 0 : 3);
            while (!closed.get()) {
                int size = in.readInt(); long sequence = in.readLong();
                if (size < 0 || size > 8 * 1024 * 1024 || sequence < 0 || sequence > Long.MAX_VALUE / 33333L) {
                    throw new IOException("Invalid desktop packet");
                }
                byte[] data = new byte[size]; in.readFully(data); lastInput = System.nanoTime();
                if (size == 0) { continue; }
                Packet packet = new Packet(data, sequence);
                for (Decoder decoder : decoders.values()) { decoder.offer(packet); }
            }
        } catch (Exception e) { fail("Desktop input disconnected"); }
    }

    private void startDecoder(int id, Surface surface) {
        if (closed.get()) { surface.release(); return; }
        Decoder decoder = new Decoder(id, surface);
        Decoder previous = decoders.put(id, decoder);
        if (previous != null) { previous.alive.set(false); }
        Thread thread = new Thread(decoder, "perspective-decoder");
        thread.setDaemon(true);
        synchronized (threads) { threads.add(thread); }
        thread.start(); state(3);
    }

    private void closeDecoder(int id) {
        Decoder decoder = decoders.remove(id);
        if (decoder != null) { decoder.alive.set(false); }
        state(decoders.isEmpty() ? 0 : 1);
        CameraEvictionNotice notice = evictionNotice;
        if (notice != null && decoders.isEmpty()) { notice.onLastStreamClosed(); }
    }

    private static final class Packet {
        final byte[] bytes;
        final long sequence;
        final boolean keyframe;
        Packet(byte[] bytes, long sequence) {
            this.bytes = bytes; this.sequence = sequence;
            boolean key = false;
            for (int i = 0; i + 4 < bytes.length; ++i) {
                if (bytes[i] == 0 && bytes[i + 1] == 0 && bytes[i + 2] == 1 && (bytes[i + 3] & 31) == 5) { key = true; break; }
            }
            keyframe = key;
        }
    }

    private final class Decoder implements Runnable {
        final int id;
        final Surface surface;
        final AtomicBoolean alive = new AtomicBoolean(true);
        final ArrayBlockingQueue<Packet> queue = new ArrayBlockingQueue<>(6);
        boolean waitingKeyframe = true;
        final AtomicLong requestCount = new AtomicLong();
        final AtomicLong requestWindow = new AtomicLong(System.nanoTime());
        volatile long lastRequest = System.nanoTime();
        void onRequest() {
            lastRequest = System.nanoTime();
            requestCount.incrementAndGet();
            long now = System.nanoTime(), previous = requestWindow.get();
            if (now - previous >= 2000000000L && requestWindow.compareAndSet(previous, now)) {
                long count = requestCount.getAndSet(0);
                Ln.i("Perspective requests: stream=" + id + " rate=" + (count * 1000000000L / (now - previous)) + "fps max=" + maxFps);
            }
        }
        Decoder(int id, Surface surface) { this.id = id; this.surface = surface; }
        synchronized void offer(Packet p) {
            if (!alive.get()) { return; }
            if (waitingKeyframe) {
                if (!p.keyframe) { return; }
                waitingKeyframe = false;
            }
            if (!queue.offer(p)) {
                queue.clear(); waitingKeyframe = true; state(2);
                if (p.keyframe) { waitingKeyframe = false; queue.offer(p); }
            }
        }
        private boolean isCurrent() {
            return alive.get() && !closed.get() && decoders.get(id) == this;
        }

        private void resetInput() {
            synchronized (this) {
                queue.clear();
                waitingKeyframe = true;
            }
        }

        public void run() {
            int failures = 0;
            boolean recovering = false;
            try {
                while (isCurrent()) {
                    MediaCodec codec = null;
                    long started = System.nanoTime();
                    try {
                        MediaFormat format = MediaFormat.createVideoFormat("video/avc", WIDTH, HEIGHT);
                        format.setInteger(MediaFormat.KEY_LOW_LATENCY, 1);
                        codec = MediaCodec.createDecoderByType("video/avc");
                        Ln.i("Perspective decoder: " + codec.getName());
                        codec.configure(format, surface, null, 0);
                        codec.start();
                        if (recovering) {
                            // A fresh IDR includes the parameter sets needed by the new codec.
                            // Re-align as recovery may have interrupted the initial calibration.
                            state(3);
                            recovering = false;
                        }
                        MediaCodec.BufferInfo info = new MediaCodec.BufferInfo();
                        while (isCurrent()) {
                            boolean queued = false;
                            synchronized (this) {
                                if (!queue.isEmpty()) {
                                    int index = codec.dequeueInputBuffer(0);
                                    if (index >= 0) {
                                        Packet packet = queue.poll();
                                        ByteBuffer buffer = codec.getInputBuffer(index);
                                        buffer.clear();
                                        if (packet.bytes.length > buffer.remaining()) {
                                            throw new IOException("Desktop packet exceeds decoder input capacity");
                                        }
                                        buffer.put(packet.bytes);
                                        codec.queueInputBuffer(index, 0, packet.bytes.length, packet.sequence * 1000000L / maxFps, 0);
                                        queued = true;
                                    }
                                }
                            }
                            int output = codec.dequeueOutputBuffer(info, queued ? 0 : 2000);
                            if (output >= 0) {
                                int newest = output, next;
                                while ((next = codec.dequeueOutputBuffer(info, 0)) >= 0) {
                                    codec.releaseOutputBuffer(newest, false);
                                    newest = next;
                                }
                                codec.releaseOutputBuffer(newest, System.nanoTime());
                                if (System.nanoTime() - started > 10000000000L) { failures = 0; }
                            }
                        }
                    } catch (Exception e) {
                        // Camera surfaces can disappear before their close callback arrives.
                        long deadline = System.nanoTime() + 500000000L;
                        while (isCurrent() && System.nanoTime() < deadline) {
                            try { Thread.sleep(10); } catch (InterruptedException ignored) { break; }
                        }
                        if (!isCurrent()) { break; }
                        boolean requesting = System.nanoTime() - lastRequest < 1500000000L;
                        if (requesting && surface.isValid() && failures < 3) {
                            ++failures;
                            resetInput();
                            recovering = true;
                            Ln.w("Perspective decoder recovering: stream=" + id + " attempt=" + failures + " reason=" + e);
                            try { Thread.sleep(100L * failures); } catch (InterruptedException ignored) { break; }
                        } else {
                            // A reverse-camera stream failure must not take down projection,
                            // the application task, or any other camera stream in the session.
                            Ln.e("Perspective stream stopped; projection remains active. Reopen the camera to retry", e);
                            break;
                        }
                    } finally {
                        if (codec != null) {
                            try { codec.stop(); } catch (Exception ignored) { }
                            try { codec.release(); } catch (Exception ignored) { }
                        }
                    }
                }
            } finally {
                alive.set(false);
                if (decoders.remove(id, this)) { state(decoders.isEmpty() ? 0 : 1); }
                surface.release();
                synchronized (threads) { threads.remove(Thread.currentThread()); }
            }
        }
    }

    /** Run one teardown stage in a bounded daemon thread so a stuck Binder call
     *  cannot prevent the remaining stages (above all association removal)
     *  from running. */
    private static boolean runCloseStage(String name, long timeoutMs, Runnable action) {
        final java.util.concurrent.atomic.AtomicReference<Throwable> failure =
                new java.util.concurrent.atomic.AtomicReference<>();
        Thread thread = new Thread(() -> {
            try { action.run(); } catch (Throwable t) { failure.set(t); }
        }, "perspective-close-" + name);
        thread.setDaemon(true);
        thread.start();
        try { thread.join(timeoutMs); } catch (InterruptedException e) { Thread.currentThread().interrupt(); }
        if (thread.isAlive()) {
            Ln.w("Perspective close stage=" + name + " timed out after " + timeoutMs + "ms; continuing");
            return false;
        } else if (failure.get() != null) {
            Ln.w("Perspective close stage=" + name + " failed: " + failure.get());
            return false;
        }
        return true;
    }

    private void closeStages() {
        traceStage("close-decoders");
        CameraEvictionNotice notice = evictionNotice;
        if (notice != null) {
            try { notice.close(); } catch (Throwable t) { Ln.w("Perspective eviction notice close failed: " + t); }
        }
        for (Decoder decoder : decoders.values()) { decoder.alive.set(false); }
        try { socket.close(); } catch (IOException ignored) { }
        ArrayList<Thread> pending;
        synchronized (threads) { pending = new ArrayList<>(threads); }
        for (Thread thread : pending) { thread.interrupt(); }
        long joinDeadline = System.nanoTime() + 2000000000L;
        for (Thread thread : pending) {
            if (thread == Thread.currentThread()) { continue; }
            long remaining = joinDeadline - System.nanoTime();
            if (remaining <= 0) { break; }
            try { thread.join(Math.max(1L, remaining / 1000000L)); } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            }
        }
        traceStage("close-camera");
        boolean cameraClosed = true;
        Object cameraRef = camera;
        if (cameraRef != null) {
            cameraClosed = runCloseStage("camera", 3000L, () -> {
                try { call(cameraRef, "close", new Class[] {}); }
                catch (Exception e) { throw new RuntimeException(e); }
            });
        }
        traceStage("close-device");
        boolean deviceClosed = true;
        Object deviceRef = device;
        if (deviceRef != null) {
            deviceClosed = runCloseStage("device", 6000L, () -> {
                try { call(deviceRef, "close", new Class[] {}); }
                catch (Exception e) { throw new RuntimeException(e); }
            });
        }
        traceStage("close-association");
        if (associationOwned) { removeAssociationWithRetry(mac); }
        if (cameraClosed && deviceClosed) {
            Ln.i("Perspective camera closed");
        } else {
            Ln.w("Perspective camera release unconfirmed: camera=" + cameraClosed + " device=" + deviceClosed);
        }
    }

    /** Best-effort teardown before the device server is forced to stop. The
     *  independent CleanUp process still removes the association if this
     *  bounded attempt cannot finish. */
    private void emergencyCleanup(String reason) {
        Ln.e("Perspective emergency cleanup: " + reason);
        boolean owner = closed.compareAndSet(false, true);
        Thread cleanup = new Thread(() -> { if (owner) { closeStages(); } }, "perspective-emergency-cleanup");
        cleanup.setDaemon(true);
        cleanup.start();
        try { cleanup.join(12000); } catch (InterruptedException e) { Thread.currentThread().interrupt(); }
        if (cleanup.isAlive()) {
            Ln.w("Perspective emergency cleanup did not finish within 12s; stopping the device server");
        } else {
            Ln.i("Perspective emergency cleanup finished");
        }
        Runtime.getRuntime().halt(1);
    }

    @Override
    public void close() {
        if (!closed.compareAndSet(false, true)) { return; }
        closeStages();
    }
}

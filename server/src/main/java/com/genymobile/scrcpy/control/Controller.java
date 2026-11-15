package com.genymobile.scrcpy.control;

import com.genymobile.scrcpy.AndroidVersions;
import com.genymobile.scrcpy.AsyncProcessor;
import com.genymobile.scrcpy.CleanUp;
import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.Options;
import com.genymobile.scrcpy.device.AppTaskLauncher;
import com.genymobile.scrcpy.device.Device;
import com.genymobile.scrcpy.display.DisplayInfo;
import com.genymobile.scrcpy.model.DeviceApp;
import com.genymobile.scrcpy.model.Point;
import com.genymobile.scrcpy.model.Position;
import com.genymobile.scrcpy.model.Size;
import com.genymobile.scrcpy.power.AppRuntimeDiagnostics;
import com.genymobile.scrcpy.util.Ln;
import com.genymobile.scrcpy.util.LogUtils;
import com.genymobile.scrcpy.video.CameraCapture;
import com.genymobile.scrcpy.video.CaptureControl;
import com.genymobile.scrcpy.video.NewDisplayCapture;
import com.genymobile.scrcpy.video.SurfaceCapture;
import com.genymobile.scrcpy.video.VideoSource;
import com.genymobile.scrcpy.video.VirtualDisplayListener;
import com.genymobile.scrcpy.wrappers.ClipboardManager.ClipboardImage;
import com.genymobile.scrcpy.wrappers.ClipboardManager;
import com.genymobile.scrcpy.wrappers.InputManager;
import com.genymobile.scrcpy.wrappers.ServiceManager;

import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.os.IBinder;
import android.os.SystemClock;
import android.util.Pair;
import android.view.InputDevice;
import android.view.KeyCharacterMap;
import android.view.KeyEvent;
import android.view.MotionEvent;

import java.io.File;
import java.io.IOException;
import java.util.List;
import java.util.Timer;
import java.util.TimerTask;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

public class Controller implements AsyncProcessor, VirtualDisplayListener {

    /*
     * For event injection, there are two display ids:
     *  - the displayId passed to the constructor (which comes from --display-id passed by the client, 0 for the main display);
     *  - the virtualDisplayId used for mirroring, notified by the capture instance via the VirtualDisplayListener interface.
     *
     * (In case the ScreenCapture uses the "SurfaceControl API", then both ids are equals, but this is an implementation detail.)
     *
     * In order to make events work correctly in all cases:
     *  - virtualDisplayId must be used for events relative to the display (mouse and touch events with coordinates);
     *  - displayId must be used for other events (like key events).
     *
     * If a new separate virtual display is created (using --new-display), then displayId == Device.DISPLAY_ID_NONE. In that case, all events are
     * sent to the virtual display id.
     */

    private static final class DisplayData {
        private final int virtualDisplayId;
        private final PositionMapper positionMapper;

        private DisplayData(int virtualDisplayId, PositionMapper positionMapper) {
            this.virtualDisplayId = virtualDisplayId;
            this.positionMapper = positionMapper;
        }
    }

    /**
     * Last user-input instant (elapsedRealtimeNanos), shared with the video
     * ABR so it can switch between interactive and idle thresholds. Static:
     * the scrcpy server runs a single controller per session.
     */
    private static volatile long lastInteractionNs;
    private final com.genymobile.scrcpy.video.ClientBacklog clientBacklog = new com.genymobile.scrcpy.video.ClientBacklog();
    public com.genymobile.scrcpy.video.ClientBacklog getClientBacklog() { return clientBacklog; }

    public static long getLastInteractionNs() {
        return lastInteractionNs;
    }

    private static void markInteraction() {
        lastInteractionNs = SystemClock.elapsedRealtimeNanos();
    }

    private static final int DEFAULT_DEVICE_ID = 0;

    // control_msg.h values of the pointerId field in inject_touch_event message
    private static final int POINTER_ID_MOUSE = -1;

    private static final ScheduledExecutorService EXECUTOR = Executors.newSingleThreadScheduledExecutor();
    private ExecutorService startAppExecutor;
    private com.genymobile.scrcpy.video.CameraPermissionMonitor cameraPermissions;
    private boolean cameraDiagnosticsStopped;
    private final boolean reuseAppTask;

    private Thread thread;
    private Thread keepAwakeWatchdog;
    private final KeepAwakeLease keepAwakeLease;
    private final KeepAwakeLease appPowerSaveLease;
    private final ControlDiagnostics controlDiagnostics;
    private Timer controlDiagnosticsTimer;

    private UhidManager uhidManager;

    private final boolean camera;
    private final int displayId;
    private final boolean supportsInputEvents;
    private final ControlChannel controlChannel;
    private final CleanUp cleanUp;
    private final DeviceMessageSender sender;
    private final boolean clipboardAutosync;
    private final int clipboardImageLimit;
    private final boolean powerOn;
    private final boolean keepActive;

    private final KeyCharacterMap charMap = KeyCharacterMap.load(KeyCharacterMap.VIRTUAL_KEYBOARD);

    private final AtomicBoolean clipboardDirty = new AtomicBoolean();
    private final AtomicBoolean clipboardReading = new AtomicBoolean();
    private final ExecutorService clipboardWorker = Executors.newSingleThreadExecutor(r -> new Thread(r, "clipboard-read"));

    private final AtomicReference<DisplayData> displayData = new AtomicReference<>();
    private final Object displayDataAvailable = new Object(); // condition variable

    private long lastTouchDown;
    private final PointersState pointersState = new PointersState();
    private final MotionEvent.PointerProperties[] pointerProperties = new MotionEvent.PointerProperties[PointersState.MAX_POINTERS];
    private final MotionEvent.PointerCoords[] pointerCoords = new MotionEvent.PointerCoords[PointersState.MAX_POINTERS];

    private boolean keepDisplayPowerOff;

    // Used for resetting video encoding on RESET_VIDEO message or for sending camera controls
    private SurfaceCapture surfaceCapture;

    public Controller(ControlChannel controlChannel, CleanUp cleanUp, Options options) {
        this.reuseAppTask = options.getReuseAppTask();
        this.camera = options.getVideoSource() == VideoSource.CAMERA;
        this.controlChannel = controlChannel;
        this.cleanUp = cleanUp;
        this.controlDiagnostics = AppRuntimeDiagnostics.ENABLED && options.getNewDisplay() != null ? new ControlDiagnostics() : null;

        if (this.camera) {
            // Unused for camera
            this.displayId = Device.DISPLAY_ID_NONE;
            this.supportsInputEvents = false;
            this.sender = null;
            this.clipboardAutosync = false;
            this.clipboardImageLimit = 0;
            this.powerOn = false;
            this.keepActive = false;
            this.keepAwakeLease = null;
            this.appPowerSaveLease = null;
            return;
        }

        this.displayId = options.getDisplayId();

        this.clipboardAutosync = options.getClipboardAutosync();
        this.clipboardImageLimit = options.getClipboardImageLimit();
        this.powerOn = options.getPowerOn();
        this.keepActive = options.getKeepActive();
        this.keepAwakeLease = keepActive ? new KeepAwakeLease(() -> {
            final IBinder token = ServiceManager.getPowerManager().acquireScreenWakeLock(getActionDisplayId());
            return () -> ServiceManager.getPowerManager().releaseScreenWakeLock(token);
        }) : null;
        // Reuse the same heartbeat deadline and watchdog without enabling a
        // screen wake lock for application/notification virtual displays.
        this.appPowerSaveLease = options.getNewDisplay() != null && cleanUp != null && cleanUp.hasAppPowerSave()
                ? new KeepAwakeLease(() -> {
                    cleanUp.resumeAppPowerSave();
                    return cleanUp::suspendAppPowerSave;
                }) : null;
        initPointers();
        sender = new DeviceMessageSender(controlChannel);

        supportsInputEvents = Device.supportsInputEvents(displayId);
        if (!supportsInputEvents) {
            Ln.w("Input events are not supported for secondary displays before Android 10");
        }

        // Make sure the clipboard manager is always created from the main thread (even if clipboardAutosync is disabled)
        ClipboardManager clipboardManager = ServiceManager.getClipboardManager();
        if (clipboardAutosync) {
            // If control and autosync are enabled, synchronize Android clipboard to the computer automatically
            if (clipboardManager != null) {
                clipboardManager.addPrimaryClipChangedListener(() -> requestClipboardSync(clipboardManager));
            } else {
                Ln.w("No clipboard manager, copy-paste between device and computer will not work");
            }
        }
    }

    private void requestClipboardSync(ClipboardManager manager) {
        if (clipboardWorker.isShutdown()) {
            return;
        }
        clipboardDirty.set(true);
        if (!clipboardReading.compareAndSet(false, true)) {
            return;
        }
        try {
            clipboardWorker.execute(() -> {
                try {
                    while (clipboardDirty.getAndSet(false)) {
                        try {
                            ClipboardManager.ClipboardSnapshot snapshot = manager.readSnapshot(true, clipboardImageLimit);
                            if (snapshot != null && !clipboardDirty.get()) {
                                if (snapshot.image != null && snapshot.image.data().length > 0) {
                                    sender.send(DeviceMessage.createImageClipboard(
                                            snapshot.image.data(), snapshot.image.mimeType(), snapshot.revision));
                                } else if (snapshot.text != null) {
                                    sender.send(DeviceMessage.createClipboard(snapshot.text, snapshot.revision));
                                }
                            }
                        } catch (Exception e) {
                            Ln.e("Failed to synchronize clipboard from device", e);
                        }
                    }
                } finally {
                    clipboardReading.set(false);
                    if (clipboardDirty.get() && !clipboardWorker.isShutdown()) {
                        requestClipboardSync(manager);
                    }
                }
            });
        } catch (java.util.concurrent.RejectedExecutionException e) {
            clipboardReading.set(false); // a listener raced with shutdown
        }
    }

    @Override
    public void onNewVirtualDisplay(int virtualDisplayId, PositionMapper positionMapper) {
        DisplayData data = new DisplayData(virtualDisplayId, positionMapper);
        DisplayData old = this.displayData.getAndSet(data);
        if (old == null) {
            // The very first time the Controller is notified of a new virtual display
            synchronized (displayDataAvailable) {
                displayDataAvailable.notify();
            }
        }
        // Publish the display before waiting for the independent cleanup process:
        // app launch only waits one second for the display ID. Its exemption
        // acquisition separately waits for registration before starting the app.
        if (cleanUp != null) {
            cleanUp.registerAppPowerSaveDisplay(virtualDisplayId);
        } else {
            Ln.w("SCRCPY_EZ_APP_POWER_SAVE action=skipped display=" + virtualDisplayId + " reason=cleanup-disabled");
        }
    }

    public void setSurfaceCapture(SurfaceCapture surfaceCapture) {
        this.surfaceCapture = surfaceCapture;
    }

    public DeviceMessageSender getDeviceMessageSender() {
        return sender;
    }

    private UhidManager getUhidManager() {
        if (uhidManager == null) {
            int uhidDisplayId = displayId;
            if (Build.VERSION.SDK_INT >= AndroidVersions.API_35_ANDROID_15) {
                if (displayId == Device.DISPLAY_ID_NONE) {
                    // Mirroring a new virtual display id (using --new-display-id feature) on Android >= 15, where the UHID mouse pointer can be
                    // associated to the virtual display
                    try {
                        // Wait for at most 1 second until a virtual display id is known
                        DisplayData data = waitDisplayData(1000);
                        if (data != null) {
                            uhidDisplayId = data.virtualDisplayId;
                        }
                    } catch (InterruptedException e) {
                        // do nothing
                    }
                }
            }

            String displayUniqueId = null;
            if (uhidDisplayId > 0) {
                // Ignore Device.DISPLAY_ID_NONE and 0 (main display)
                DisplayInfo displayInfo = ServiceManager.getDisplayManager().getDisplayInfo(uhidDisplayId);
                if (displayInfo != null) {
                    displayUniqueId = displayInfo.getUniqueId();
                }
            }
            uhidManager = new UhidManager(sender, displayUniqueId);
        }

        return uhidManager;
    }

    private void initPointers() {
        for (int i = 0; i < PointersState.MAX_POINTERS; ++i) {
            MotionEvent.PointerProperties props = new MotionEvent.PointerProperties();
            props.toolType = MotionEvent.TOOL_TYPE_FINGER;

            MotionEvent.PointerCoords coords = new MotionEvent.PointerCoords();
            coords.orientation = 0;
            coords.size = 0;

            pointerProperties[i] = props;
            pointerCoords[i] = coords;
        }
    }

    private void control() throws IOException {
        // on start, power on the device
        if (!camera && powerOn && displayId == 0 && !Device.isScreenOn(displayId)) {
            Device.pressReleaseKeycode(KeyEvent.KEYCODE_POWER, displayId, Device.INJECT_MODE_ASYNC);

            // dirty hack
            // After POWER is injected, the device is powered on asynchronously.
            // To turn the device screen off while mirroring, the client will send a message that
            // would be handled before the device is actually powered on, so its effect would
            // be "canceled" once the device is turned back on.
            // Adding this delay prevents to handle the message before the device is actually
            // powered on.
            SystemClock.sleep(500);
        }

        boolean alive = true;
        while (!Thread.currentThread().isInterrupted() && alive) {
            alive = handleEvent();
        }
    }

    private void startKeepAwakeWatchdog() {
        keepAwakeWatchdog = new Thread(() -> {
            try {
                while (!Thread.currentThread().isInterrupted()) {
                    Thread.sleep(1000);
                    long now = SystemClock.elapsedRealtime();
                    if (keepAwakeLease != null) {
                        try {
                            keepAwakeLease.expire(now);
                        } catch (Exception e) {
                            Ln.e("Could not expire screen wake lock", e);
                        }
                    }
                    if (appPowerSaveLease != null) {
                        try {
                            appPowerSaveLease.expire(now);
                        } catch (Exception e) {
                            Ln.e("SCRCPY_EZ_APP_POWER_SAVE action=timeout-release-failed", e);
                        }
                    }
                }
            } catch (InterruptedException e) {
                // Normal session shutdown.
            } finally {
                releaseKeepAwake();
            }
        });
        keepAwakeWatchdog.setName("session-resource-lease");
        keepAwakeWatchdog.setDaemon(true);
        keepAwakeWatchdog.start();
    }

    private synchronized void startControlDiagnostics() {
        if (controlDiagnostics == null || controlDiagnosticsTimer != null) {
            return;
        }
        controlDiagnosticsTimer = new Timer("control-diagnostics", true);
        controlDiagnosticsTimer.schedule(new TimerTask() {
            @Override
            public void run() {
                DisplayData data = displayData.get();
                Ln.i("SCRCPY_EZ_CONTROL_RX display=" + (data == null ? -1 : data.virtualDisplayId)
                        + " " + controlDiagnostics.snapshot(SystemClock.elapsedRealtime()));
            }
        }, 0, 5000);
    }

    private synchronized void stopControlDiagnostics() {
        if (controlDiagnosticsTimer != null) {
            controlDiagnosticsTimer.cancel();
            controlDiagnosticsTimer = null;
        }
    }

    public void releaseKeepAwake() {
        stopControlDiagnostics();
        // Mark the application session closed before closing its lease, so a
        // normal shutdown is not reported as a heartbeat timeout and late app
        // launch/notification requests cannot reacquire it.
        if (cleanUp != null) {
            cleanUp.closeAppPowerSave();
        }
        if (appPowerSaveLease != null) {
            try {
                appPowerSaveLease.close();
            } catch (Exception e) {
                Ln.e("Could not close application power-save lease", e);
            }
        }
        if (keepAwakeLease != null) {
            try {
                keepAwakeLease.close();
            } catch (Exception e) {
                Ln.e("Could not release screen wake lock", e);
            }
        }
        if (keepAwakeWatchdog != null) {
            keepAwakeWatchdog.interrupt();
        }
    }

    @Override
    public void start(TerminationListener listener) {
        startControlDiagnostics();
        if (appPowerSaveLease != null) {
            try {
                // Establish the initial 30-second grace period even if the
                // first client heartbeat never reaches this controller.
                appPowerSaveLease.heartbeat(SystemClock.elapsedRealtime());
            } catch (Exception e) {
                Ln.e("Could not initialize application power-save lease", e);
            }
        }
        if (keepActive || appPowerSaveLease != null) {
            startKeepAwakeWatchdog();
        }

        thread = new Thread(() -> {
            try {
                control();
            } catch (IOException e) {
                Ln.e("Controller error", e);
            } finally {
                releaseKeepAwake();
                Ln.d("Controller stopped");
                if (uhidManager != null) {
                    uhidManager.closeAll();
                }
                listener.onTerminated(true);
            }
        }, "control-recv");
        thread.start();
        if (sender != null) {
            sender.start();
        }
    }

    @Override
    public void stop() {
        synchronized (this) {
            cameraDiagnosticsStopped = true;
            if (cameraPermissions != null) { cameraPermissions.close(); cameraPermissions = null; }
        }
        releaseKeepAwake();
        clipboardWorker.shutdownNow();
        if (thread != null) {
            thread.interrupt();
        }
        if (sender != null) {
            sender.stop();
        }
    }

    @Override
    public void join() throws InterruptedException {
        if (thread != null) {
            thread.join();
        }
        if (sender != null) {
            sender.join();
        }
    }

    private boolean handleEvent() throws IOException {
        ControlMessage msg;
        try {
            msg = controlChannel.recv();
        } catch (ControlProtocolException e) {
            Ln.e("Control protocol error", e);
            return false;
        } catch (IOException e) {
            // this is expected on close
            return false;
        }

        int type = msg.getType();

        // Events for all sources (display or camera)
        switch (type) {
            case ControlMessage.TYPE_KEEP_AWAKE_HEARTBEAT:
                if (controlDiagnostics != null) {
                    controlDiagnostics.heartbeat(SystemClock.elapsedRealtime());
                }
                if (appPowerSaveLease != null) {
                    try {
                        appPowerSaveLease.heartbeat(SystemClock.elapsedRealtime());
                        // A timed-out release may have failed before the
                        // lease dropped its handle; reconnect must still resume.
                        cleanUp.resumeAppPowerSave();
                    } catch (Exception e) {
                        Ln.e("Could not renew application power-save lease", e);
                    }
                }
                if (keepAwakeLease != null && getActionDisplayId() != Device.DISPLAY_ID_NONE) {
                    try {
                        keepAwakeLease.heartbeat(SystemClock.elapsedRealtime());
                    } catch (Exception e) {
                        Ln.e("Screen keep-awake is unavailable; mirroring will continue", e);
                    }
                }
                return true; // Does not count as user interaction or feed ABR.
            case ControlMessage.TYPE_ABR_FEEDBACK:
                int[] feedback = msg.getAbrFeedback();
                clientBacklog.update(feedback[0], feedback[1], feedback[2], feedback[3], feedback[4], feedback[5], feedback[6], feedback[7], SystemClock.elapsedRealtime());
                Ln.d("ABR2 feedback: epoch=" + feedback[0] + " sequence=" + feedback[1]
                        + " queue=" + feedback[2] + "ms delivery=" + feedback[3] + "ms flags=" + feedback[4] + " queued=" + feedback[5] + " work=" + feedback[6] + "us receivedBps=" + feedback[7]);
                return true;
            case ControlMessage.TYPE_RESET_VIDEO:
                resetVideo();
                return true;
            default:
                // fall through
        }

        if (!camera) {
            switch (type) {
                case ControlMessage.TYPE_INJECT_KEYCODE:
                    markInteraction();
                    if (supportsInputEvents) {
                        injectKeycode(msg.getAction(), msg.getKeycode(), msg.getRepeat(), msg.getMetaState());
                    }
                    return true;
                case ControlMessage.TYPE_INJECT_TEXT:
                    markInteraction();
                    if (supportsInputEvents) {
                        injectText(msg.getText());
                    }
                    return true;
                case ControlMessage.TYPE_INJECT_TOUCH_EVENT:
                    markInteraction();
                    if (controlDiagnostics != null) {
                        controlDiagnostics.received(SystemClock.elapsedRealtime());
                    }
                    boolean touchAccepted = supportsInputEvents && injectTouch(
                            msg.getAction(), msg.getPointerId(), msg.getPosition(), msg.getPressure(), msg.getActionButton(), msg.getButtons());
                    if (controlDiagnostics != null) {
                        controlDiagnostics.completed(touchAccepted);
                    }
                    return true;
                case ControlMessage.TYPE_INJECT_SCROLL_EVENT:
                    markInteraction();
                    if (controlDiagnostics != null) {
                        controlDiagnostics.received(SystemClock.elapsedRealtime());
                    }
                    boolean scrollAccepted = supportsInputEvents && injectScroll(msg.getPosition(), msg.getHScroll(), msg.getVScroll(), msg.getButtons());
                    if (controlDiagnostics != null) {
                        controlDiagnostics.completed(scrollAccepted);
                    }
                    return true;
                case ControlMessage.TYPE_BACK_OR_SCREEN_ON:
                    markInteraction();
                    if (supportsInputEvents) {
                        pressBackOrTurnScreenOn(msg.getAction());
                    }
                    return true;
                case ControlMessage.TYPE_EXPAND_NOTIFICATION_PANEL:
                    markInteraction();
                    Device.expandNotificationPanel();
                    return true;
                case ControlMessage.TYPE_EXPAND_SETTINGS_PANEL:
                    markInteraction();
                    Device.expandSettingsPanel();
                    return true;
                case ControlMessage.TYPE_COLLAPSE_PANELS:
                    markInteraction();
                    Device.collapsePanels();
                    return true;
                case ControlMessage.TYPE_GET_CLIPBOARD:
                    getClipboard(msg.getCopyKey());
                    return true;
                case ControlMessage.TYPE_SET_CLIPBOARD:
                    setClipboard(msg.getText(), msg.getPaste(), msg.getSequence(), msg.getClipboardEpoch(), msg.getClipboardVersion());
                    return true;
                case ControlMessage.TYPE_SET_IMAGE_CLIPBOARD:
                    setImageClipboard(msg.getSequence(), msg.getPaste(), msg.getText(), msg.getData(),
                            msg.getClipboardEpoch(), msg.getClipboardVersion()); // text field contains mimeType
                    return true;
                case ControlMessage.TYPE_SET_DISPLAY_POWER:
                    if (supportsInputEvents) {
                        setDisplayPower(msg.getOn());
                    }
                    return true;
                case ControlMessage.TYPE_TOGGLE_DISPLAY_BACKLIGHT:
                    if (displayId == 0) {
                        markInteraction();
                        if (keepDisplayPowerOff) {
                            setDisplayPower(true);
                        } else if (cleanUp != null) {
                            cleanUp.toggleMainDisplayBacklight();
                        } else {
                            Ln.w("Main display backlight toggle requires the cleanup process");
                        }
                    }
                    return true;
                case ControlMessage.TYPE_AUTO_BLACKOUT:
                    if (displayId == 0 && !keepDisplayPowerOff && cleanUp != null) {
                        cleanUp.requestAutoBlackout();
                    }
                    return true;
                case ControlMessage.TYPE_ROTATE_DEVICE:
                    int actionDisplayId = getActionDisplayId();
                    if (actionDisplayId != Device.DISPLAY_ID_NONE) {
                        Device.rotateDevice(actionDisplayId);
                    }
                    return true;
                case ControlMessage.TYPE_UHID_CREATE:
                    getUhidManager().open(msg.getId(), msg.getVendorId(), msg.getProductId(), msg.getText(), msg.getData());
                    return true;
                case ControlMessage.TYPE_UHID_INPUT:
                    markInteraction();
                    getUhidManager().writeInput(msg.getId(), msg.getData());
                    return true;
                case ControlMessage.TYPE_UHID_DESTROY:
                    getUhidManager().close(msg.getId());
                    return true;
                case ControlMessage.TYPE_OPEN_HARD_KEYBOARD_SETTINGS:
                    openHardKeyboardSettings();
                    return true;
                case ControlMessage.TYPE_START_APP:
                    startAppAsync(msg.getText());
                    return true;
                case ControlMessage.TYPE_RESIZE_DISPLAY:
                    resizeDisplay(msg.getWidth(), msg.getHeight());
                    return true;
                case ControlMessage.TYPE_SCAN_FILE:
                    scanFile(msg.getText());
                case ControlMessage.TYPE_SAVE_CLIPBOARD_IMAGE_TO_GALLERY:
                    saveClipboardImageToGallery();
                    return true;
                default:
                    // fall through
            }
        } else {
            assert surfaceCapture instanceof CameraCapture;
            CameraCapture cameraCapture = (CameraCapture) surfaceCapture;
            switch (type) {
                case ControlMessage.TYPE_CAMERA_SET_TORCH:
                    cameraCapture.setTorchEnabled(msg.getOn());
                    return true;
                case ControlMessage.TYPE_CAMERA_ZOOM_IN:
                    cameraCapture.zoomIn();
                    return true;
                case ControlMessage.TYPE_CAMERA_ZOOM_OUT:
                    cameraCapture.zoomOut();
                    return true;
                default:
                    // fall through
            }
        }

        throw new AssertionError("Unexpected message type: " + type);
    }

    private boolean injectKeycode(int action, int keycode, int repeat, int metaState) {
        if (displayId == 0 && cleanUp != null && action == KeyEvent.ACTION_DOWN && keycode == KeyEvent.KEYCODE_POWER) {
            cleanUp.restoreMainDisplayBacklight();
        }
        if (keepDisplayPowerOff && action == KeyEvent.ACTION_UP && (keycode == KeyEvent.KEYCODE_POWER || keycode == KeyEvent.KEYCODE_WAKEUP)) {
            assert displayId != Device.DISPLAY_ID_NONE;
            scheduleDisplayPowerOff(displayId);
        }
        return injectKeyEvent(action, keycode, repeat, metaState, Device.INJECT_MODE_ASYNC);
    }

    private boolean injectChar(char c) {
        String decomposed = KeyComposition.decompose(c);
        char[] chars = decomposed != null ? decomposed.toCharArray() : new char[]{c};
        KeyEvent[] events = charMap.getEvents(chars);
        if (events == null) {
            return false;
        }

        int actionDisplayId = getActionDisplayId();
        if (actionDisplayId != Device.DISPLAY_ID_NONE) {
            for (KeyEvent event : events) {
                if (!Device.injectEvent(event, actionDisplayId, Device.INJECT_MODE_ASYNC)) {
                    return false;
                }
            }
        }
        return true;
    }

    private int injectText(String text) {
        int successCount = 0;
        for (char c : text.toCharArray()) {
            if (!injectChar(c)) {
                Ln.w("Could not inject char u+" + String.format("%04x", (int) c));
                continue;
            }
            successCount++;
        }
        return successCount;
    }

    private Pair<Point, Integer> getEventPointAndDisplayId(Position position) {
        // it hides the field on purpose, to read it with atomic access
        @SuppressWarnings("checkstyle:HiddenField")
        DisplayData displayData = this.displayData.get();
        // In scrcpy, displayData should never be null (a touch event can only be generated from the client when a video frame is present).
        // However, it is possible to send events without video playback when using scrcpy-server alone (except for virtual displays).
        assert displayData != null || displayId != Device.DISPLAY_ID_NONE : "Cannot receive a positional event without a display";

        Point point;
        int targetDisplayId;
        if (displayData != null) {
            point = displayData.positionMapper.map(position);
            if (point == null) {
                if (Ln.isEnabled(Ln.Level.VERBOSE)) {
                    Size eventSize = position.getScreenSize();
                    Size currentSize = displayData.positionMapper.getVideoSize();
                    Ln.v("Ignore positional event generated for size " + eventSize + " (current size is " + currentSize + ")");
                }
                return null;
            }
            targetDisplayId = displayData.virtualDisplayId;
        } else {
            // No display, use the raw coordinates
            point = position.getPoint();
            targetDisplayId = displayId;
        }

        return Pair.create(point, targetDisplayId);
    }

    private boolean injectTouch(int action, long pointerId, Position position, float pressure, int actionButton, int buttons) {
        long now = SystemClock.uptimeMillis();

        Pair<Point, Integer> pair = getEventPointAndDisplayId(position);
        if (pair == null) {
            return false;
        }

        Point point = pair.first;
        int targetDisplayId = pair.second;

        int pointerIndex = pointersState.getPointerIndex(pointerId);
        if (pointerIndex == -1) {
            Ln.w("Too many pointers for touch event");
            return false;
        }
        Pointer pointer = pointersState.get(pointerIndex);
        pointer.setPoint(point);
        pointer.setPressure(pressure);

        int source;
        boolean activeSecondaryButtons = ((actionButton | buttons) & ~MotionEvent.BUTTON_PRIMARY) != 0;
        if (pointerId == POINTER_ID_MOUSE && (action == MotionEvent.ACTION_HOVER_MOVE || activeSecondaryButtons)) {
            // real mouse event, or event incompatible with a finger
            pointerProperties[pointerIndex].toolType = MotionEvent.TOOL_TYPE_MOUSE;
            source = InputDevice.SOURCE_MOUSE;
            pointer.setUp(buttons == 0);
        } else {
            // POINTER_ID_GENERIC_FINGER, POINTER_ID_VIRTUAL_FINGER or real touch from device
            pointerProperties[pointerIndex].toolType = MotionEvent.TOOL_TYPE_FINGER;
            source = InputDevice.SOURCE_TOUCHSCREEN;
            // Buttons must not be set for touch events
            buttons = 0;
            pointer.setUp(action == MotionEvent.ACTION_UP);
        }

        int pointerCount = pointersState.update(pointerProperties, pointerCoords);
        if (pointerCount == 1) {
            if (action == MotionEvent.ACTION_DOWN) {
                lastTouchDown = now;
            }
        } else {
            // secondary pointers must use ACTION_POINTER_* ORed with the pointerIndex
            if (action == MotionEvent.ACTION_UP) {
                action = MotionEvent.ACTION_POINTER_UP | (pointerIndex << MotionEvent.ACTION_POINTER_INDEX_SHIFT);
            } else if (action == MotionEvent.ACTION_DOWN) {
                action = MotionEvent.ACTION_POINTER_DOWN | (pointerIndex << MotionEvent.ACTION_POINTER_INDEX_SHIFT);
            }
        }

        /* If the input device is a mouse (on API >= 23):
         *   - the first button pressed must first generate ACTION_DOWN;
         *   - all button pressed (including the first one) must generate ACTION_BUTTON_PRESS;
         *   - all button released (including the last one) must generate ACTION_BUTTON_RELEASE;
         *   - the last button released must in addition generate ACTION_UP.
         *
         * Otherwise, Chrome does not work properly: <https://github.com/Genymobile/scrcpy/issues/3635>
         */
        if (Build.VERSION.SDK_INT >= AndroidVersions.API_23_ANDROID_6_0 && source == InputDevice.SOURCE_MOUSE) {
            if (action == MotionEvent.ACTION_DOWN) {
                if (actionButton == buttons) {
                    // First button pressed: ACTION_DOWN
                    MotionEvent downEvent = MotionEvent.obtain(lastTouchDown, now, MotionEvent.ACTION_DOWN, pointerCount, pointerProperties,
                            pointerCoords, 0, buttons, 1f, 1f, DEFAULT_DEVICE_ID, 0, source, 0);
                    if (!Device.injectEvent(downEvent, targetDisplayId, Device.INJECT_MODE_ASYNC)) {
                        return false;
                    }
                }

                // Any button pressed: ACTION_BUTTON_PRESS
                MotionEvent pressEvent = MotionEvent.obtain(lastTouchDown, now, MotionEvent.ACTION_BUTTON_PRESS, pointerCount, pointerProperties,
                        pointerCoords, 0, buttons, 1f, 1f, DEFAULT_DEVICE_ID, 0, source, 0);
                if (!InputManager.setActionButton(pressEvent, actionButton)) {
                    return false;
                }
                if (!Device.injectEvent(pressEvent, targetDisplayId, Device.INJECT_MODE_ASYNC)) {
                    return false;
                }

                return true;
            }

            if (action == MotionEvent.ACTION_UP) {
                // Any button released: ACTION_BUTTON_RELEASE
                MotionEvent releaseEvent = MotionEvent.obtain(lastTouchDown, now, MotionEvent.ACTION_BUTTON_RELEASE, pointerCount, pointerProperties,
                        pointerCoords, 0, buttons, 1f, 1f, DEFAULT_DEVICE_ID, 0, source, 0);
                if (!InputManager.setActionButton(releaseEvent, actionButton)) {
                    return false;
                }
                if (!Device.injectEvent(releaseEvent, targetDisplayId, Device.INJECT_MODE_ASYNC)) {
                    return false;
                }

                if (buttons == 0) {
                    // Last button released: ACTION_UP
                    MotionEvent upEvent = MotionEvent.obtain(lastTouchDown, now, MotionEvent.ACTION_UP, pointerCount, pointerProperties,
                            pointerCoords, 0, buttons, 1f, 1f, DEFAULT_DEVICE_ID, 0, source, 0);
                    if (!Device.injectEvent(upEvent, targetDisplayId, Device.INJECT_MODE_ASYNC)) {
                        return false;
                    }
                }

                return true;
            }
        }

        MotionEvent event = MotionEvent.obtain(lastTouchDown, now, action, pointerCount, pointerProperties, pointerCoords, 0, buttons, 1f, 1f,
                DEFAULT_DEVICE_ID, 0, source, 0);
        return Device.injectEvent(event, targetDisplayId, Device.INJECT_MODE_ASYNC);
    }

    private boolean injectScroll(Position position, float hScroll, float vScroll, int buttons) {
        long now = SystemClock.uptimeMillis();

        Pair<Point, Integer> pair = getEventPointAndDisplayId(position);
        if (pair == null) {
            return false;
        }

        Point point = pair.first;
        int targetDisplayId = pair.second;

        MotionEvent.PointerProperties props = pointerProperties[0];
        props.id = 0;

        MotionEvent.PointerCoords coords = pointerCoords[0];
        coords.x = point.getX();
        coords.y = point.getY();
        coords.setAxisValue(MotionEvent.AXIS_HSCROLL, hScroll);
        coords.setAxisValue(MotionEvent.AXIS_VSCROLL, vScroll);

        MotionEvent event = MotionEvent.obtain(lastTouchDown, now, MotionEvent.ACTION_SCROLL, 1, pointerProperties, pointerCoords, 0, buttons, 1f, 1f,
                DEFAULT_DEVICE_ID, 0, InputDevice.SOURCE_MOUSE, 0);
        return Device.injectEvent(event, targetDisplayId, Device.INJECT_MODE_ASYNC);
    }

    /**
     * Schedule a call to set display power to off after a small delay.
     */
    private static void scheduleDisplayPowerOff(int displayId) {
        EXECUTOR.schedule(() -> {
            Ln.i("Forcing display off");
            Device.setDisplayPower(displayId, false);
        }, 200, TimeUnit.MILLISECONDS);
    }

    private boolean pressBackOrTurnScreenOn(int action) {
        boolean injectBack;
        // Device.isScreenOn(displayId) ignores the displayId below Android 14
        if (Build.VERSION.SDK_INT >= AndroidVersions.API_34_ANDROID_14) {
            // Inject BACK if the screen is on for the current virtual display id
            int actionDisplayId = getActionDisplayId();
            injectBack = actionDisplayId == Device.DISPLAY_ID_NONE || Device.isScreenOn(actionDisplayId);
        } else {
            // Inject BACK if the display is not the main display, or if the main display is on
            injectBack = displayId != 0 || Device.isScreenOn(0);
        }
        if (injectBack) {
            return injectKeyEvent(action, KeyEvent.KEYCODE_BACK, 0, 0, Device.INJECT_MODE_ASYNC);
        }

        // Screen is off
        // Only press POWER on ACTION_DOWN
        if (action != KeyEvent.ACTION_DOWN) {
            // do nothing
            return true;
        }

        if (keepDisplayPowerOff) {
            assert displayId != Device.DISPLAY_ID_NONE;
            scheduleDisplayPowerOff(displayId);
        }
        return pressReleaseKeycode(KeyEvent.KEYCODE_POWER, Device.INJECT_MODE_ASYNC);
    }

    private void getClipboard(int copyKey) {
        // On Android >= 7, press the COPY or CUT key if requested
        if (copyKey != ControlMessage.COPY_KEY_NONE && Build.VERSION.SDK_INT >= AndroidVersions.API_24_ANDROID_7_0 && supportsInputEvents) {
            int key = copyKey == ControlMessage.COPY_KEY_COPY ? KeyEvent.KEYCODE_COPY : KeyEvent.KEYCODE_CUT;
            // Wait until the event is finished, to ensure that the clipboard text we read just after is the correct one
            pressReleaseKeycode(key, Device.INJECT_MODE_WAIT_FOR_FINISH);
        }

        // If clipboard autosync is enabled, then the device clipboard is synchronized to the computer clipboard whenever it changes, in
        // particular when COPY or CUT are injected, so it should not be synchronized twice. On Android < 7, do not synchronize at all rather than
        // copying an old clipboard content.
        if (!clipboardAutosync) {
            // Try to get image clipboard first
            ClipboardImage clipboardImage = Device.getClipboardImage(clipboardImageLimit);
            if (clipboardImage != null && clipboardImage.data().length > 0) {
                // Send image clipboard data
                DeviceMessage msg = DeviceMessage.createImageClipboard(clipboardImage.data(), clipboardImage.mimeType());
                sender.send(msg);
            } else {
                // Fall back to text clipboard
                String clipboardText = Device.getClipboardText();
                if (clipboardText != null) {
                    DeviceMessage msg = DeviceMessage.createClipboard(clipboardText);
                    sender.send(msg);
                }
            }
        }
    }

    private boolean setClipboard(String text, boolean paste, long sequence, long epoch, long version) {
        boolean ok = Device.setClipboardText(text, epoch, version);
        if (ok) {
            Ln.i("Device clipboard set");
        }

        // On Android >= 7, also press the PASTE key if requested
        if (ok && paste && Build.VERSION.SDK_INT >= AndroidVersions.API_24_ANDROID_7_0 && supportsInputEvents) {
            pressReleaseKeycode(KeyEvent.KEYCODE_PASTE, Device.INJECT_MODE_ASYNC);
        }

        if (sequence != ControlMessage.SEQUENCE_INVALID) {
            // Acknowledgement requested
            DeviceMessage msg = DeviceMessage.createAckClipboard(sequence < 0 && !ok ? sequence | (1L << 62) : sequence);
            sender.send(msg);
        }

        return ok;
    }

    private boolean setImageClipboard(long sequence, boolean paste, String mimeType, byte[] imageData, long epoch, long version) {
        boolean withinLimit = clipboardImageLimit == 0 || imageData.length <= clipboardImageLimit;
        if (!withinLimit) {
            Ln.i("Image clipboard: scanner skipped oversized image");
        }
        boolean ok = withinLimit && Device.setClipboardImage(imageData, mimeType, epoch, version);
        if (ok) {
            Ln.i("Device image clipboard set");
        }

        // On Android >= 7, also press the PASTE key if requested
        if (ok && paste && Build.VERSION.SDK_INT >= AndroidVersions.API_24_ANDROID_7_0 && supportsInputEvents) {
            pressReleaseKeycode(KeyEvent.KEYCODE_PASTE, Device.INJECT_MODE_ASYNC);
        }

        if (sequence != ControlMessage.SEQUENCE_INVALID) {
            // Acknowledgement requested
            DeviceMessage msg = DeviceMessage.createAckClipboard(sequence < 0 && !ok ? sequence | (1L << 62) : sequence);
            sender.send(msg);
        }

        return ok;
    }

    private void openHardKeyboardSettings() {
        Intent intent = new Intent("android.settings.HARD_KEYBOARD_SETTINGS");
        ServiceManager.getActivityManager().startActivity(intent);
    }

    private boolean injectKeyEvent(int action, int keyCode, int repeat, int metaState, int injectMode) {
        // POWER is a physical phone button, also from an app/virtual display.
        int actionDisplayId = keyCode == KeyEvent.KEYCODE_POWER ? 0 : getActionDisplayId();
        if (actionDisplayId == Device.DISPLAY_ID_NONE) {
            return false;
        }
        return Device.injectKeyEvent(action, keyCode, repeat, metaState, actionDisplayId, injectMode);
    }

    private boolean pressReleaseKeycode(int keyCode, int injectMode) {
        int actionDisplayId = getActionDisplayId();
        if (actionDisplayId == Device.DISPLAY_ID_NONE) {
            return false;
        }
        return Device.pressReleaseKeycode(keyCode, actionDisplayId, injectMode);
    }

    private int getActionDisplayId() {
        if (displayId != Device.DISPLAY_ID_NONE) {
            // Real screen mirrored, use the source display id
            return displayId;
        }

        // Virtual display created by --new-display, use the virtualDisplayId
        DisplayData data = displayData.get();
        if (data == null) {
            return Device.DISPLAY_ID_NONE;
        }

        return data.virtualDisplayId;
    }

    private void startAppAsync(String name) {
        if (startAppExecutor == null) {
            startAppExecutor = Executors.newSingleThreadExecutor();
        }

        // Listing and selecting the app may take a lot of time
        startAppExecutor.submit(() -> startApp(name));
    }

    private void startApp(String name) {
        boolean forceStopBeforeStart = name.startsWith("+");
        if (forceStopBeforeStart) {
            name = name.substring(1);
        }

        boolean wechatScanner = (Device.WECHAT_PACKAGE + ":scan").equals(name);
        if (wechatScanner) {
            name = Device.WECHAT_PACKAGE;
        }

        DeviceApp app;
        boolean searchByName = name.startsWith("?");
        if (searchByName) {
            name = name.substring(1);

            Ln.i("Processing Android apps... (this may take some time)");
            List<DeviceApp> apps = Device.findByName(name);
            if (apps.isEmpty()) {
                Ln.w("No app found for name \"" + name + "\"");
                return;
            }

            if (apps.size() > 1) {
                String title = "No unique app found for name \"" + name + "\":";
                Ln.w(LogUtils.buildAppListMessage(title, apps));
                return;
            }

            app = apps.get(0);
        } else {
            app = Device.findByPackageName(name);
            if (app == null) {
                Ln.w("No app found for package \"" + name + "\"");
                if (wechatScanner) {
                    Ln.i("SCRCPY_EZ_APP_LAUNCH=launch-failed");
                }
                return;
            }
        }

        int startAppDisplayId = getStartAppDisplayId();
        if (startAppDisplayId == Device.DISPLAY_ID_NONE) {
            Ln.e("No known display id to start app \"" + name + "\"");
            return;
        }
        if (wechatScanner && startAppDisplayId <= 0) {
            Ln.w("WeChat Scanner requires a virtual display");
            return;
        }

        Ln.i("Starting app \"" + app.getName() + "\" [" + app.getPackageName() + "] on display " + startAppDisplayId + "...");
        if (startAppDisplayId > 0) {
            synchronized (this) {
                if (!cameraDiagnosticsStopped) {
                    if (cameraPermissions != null) { cameraPermissions.close(); }
                    cameraPermissions = new com.genymobile.scrcpy.video.CameraPermissionMonitor(app.getPackageName());
                    cameraPermissions.start();
                }
            }
        }
        if (cleanUp != null) {
            cleanUp.acquireAppPowerSave(app.getPackageName(), startAppDisplayId);
        }
        if (wechatScanner) {
            if (forceStopBeforeStart) {
                ServiceManager.getActivityManager().forceStopPackage(Device.WECHAT_PACKAGE);
            }
            boolean started = Device.startWeChatScanner(startAppDisplayId);
            Ln.i("SCRCPY_EZ_APP_LAUNCH=" + (started ? "started" : "launch-failed"));
        } else if (reuseAppTask && !forceStopBeforeStart && startAppDisplayId > 0) {
            AppTaskLauncher.Result result = AppTaskLauncher.start(app.getPackageName(), startAppDisplayId);
            Ln.i("SCRCPY_EZ_APP_LAUNCH=" + result.code());
        } else {
            Device.startApp(app.getPackageName(), startAppDisplayId, forceStopBeforeStart);
            if (reuseAppTask) { Ln.i("SCRCPY_EZ_APP_LAUNCH=started"); }
        }
    }

    private int getStartAppDisplayId() {
        if (displayId != Device.DISPLAY_ID_NONE) {
            return displayId;
        }

        // Mirroring a new virtual display id (using --new-display-id feature)
        try {
            // Wait for at most 1 second until a virtual display id is known
            DisplayData data = waitDisplayData(1000);
            if (data != null) {
                return data.virtualDisplayId;
            }
        } catch (InterruptedException e) {
            // do nothing
        }

        // No display id available
        return Device.DISPLAY_ID_NONE;
    }

    private DisplayData waitDisplayData(long timeoutMillis) throws InterruptedException {
        long deadline = System.currentTimeMillis() + timeoutMillis;

        synchronized (displayDataAvailable) {
            DisplayData data = displayData.get();
            while (data == null) {
                long timeout = deadline - System.currentTimeMillis();
                if (timeout < 0) {
                    return null;
                }
                if (timeout > 0) {
                    displayDataAvailable.wait(timeout);
                }
                data = displayData.get();
            }

            return data;
        }
    }

    private void setDisplayPower(boolean on) {
        if (displayId == 0 && cleanUp != null) {
            cleanUp.restoreMainDisplayBacklight();
        }
        // Change the power of the main display when mirroring a virtual display
        int targetDisplayId = displayId != Device.DISPLAY_ID_NONE ? displayId : 0;
        boolean setDisplayPowerOk = Device.setDisplayPower(targetDisplayId, on);
        if (setDisplayPowerOk) {
            // Do not keep display power off for virtual displays: MOD+p must wake up the physical device
            keepDisplayPowerOff = displayId != Device.DISPLAY_ID_NONE && !on;
            Ln.i("Device display turned " + (on ? "on" : "off"));
            if (cleanUp != null) {
                boolean mustRestoreOnExit = !on;
                cleanUp.setRestoreDisplayPower(mustRestoreOnExit);
            }
        }
    }

    private void resetVideo() {
        if (surfaceCapture != null) {
            Ln.i("Video capture reset");
            surfaceCapture.getCaptureControl().reset(CaptureControl.RESET_REASON_CLIENT_RESET);
        }
    }

    private void resizeDisplay(int width, int height) {
        NewDisplayCapture newDisplayCapture = (NewDisplayCapture) surfaceCapture;
        newDisplayCapture.requestResize(width, height);
    }

    private void scanFile(String path) {
        try {
            @SuppressWarnings("deprecation")
            Intent intent = new Intent(Intent.ACTION_MEDIA_SCANNER_SCAN_FILE, Uri.fromFile(new File(path)));
            Device.sendBroadcast(intent);
        } catch (Throwable t) {
            Ln.e("MediaStore scan failed for " + path, t);
        }
    }

    private void saveClipboardImageToGallery() {
        ClipboardManager clipboardManager = ServiceManager.getClipboardManager();
        String savedPath = clipboardManager.saveLatestImageToGallery(clipboardImageLimit);
        if (savedPath != null) {
            // Direct file write fallback: trigger a media scan so the gallery
            // app picks it up. MediaScannerConnection is the reliable API on
            // modern Android; the broadcast is a fallback for older versions.
            Ln.i("Saved clipboard image to gallery: " + savedPath);
            try {
                android.media.MediaScannerConnection.scanFile(
                        FakeContext.get(), new String[]{savedPath}, null, null);
            } catch (Throwable t) {
                Ln.e("MediaScannerConnection scan failed", t);
            }
            scanFile(savedPath);
        }
        // savedPath == null: either the save failed (already logged) or the
        // image was inserted via MediaStore (also already logged).
    }
}

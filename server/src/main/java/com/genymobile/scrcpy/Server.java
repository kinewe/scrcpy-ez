package com.genymobile.scrcpy;

import com.genymobile.scrcpy.device.AppCatalog;
import com.genymobile.scrcpy.audio.AudioCapture;
import com.genymobile.scrcpy.audio.AudioCodec;
import com.genymobile.scrcpy.audio.AudioDirectCapture;
import com.genymobile.scrcpy.audio.AudioEncoder;
import com.genymobile.scrcpy.audio.AudioPlaybackCapture;
import com.genymobile.scrcpy.audio.AudioRawRecorder;
import com.genymobile.scrcpy.audio.AudioSource;
import com.genymobile.scrcpy.control.ControlChannel;
import com.genymobile.scrcpy.control.Controller;
import com.genymobile.scrcpy.control.DeviceMessage;
import com.genymobile.scrcpy.device.DesktopConnection;
import com.genymobile.scrcpy.device.Device;
import com.genymobile.scrcpy.device.Streamer;
import com.genymobile.scrcpy.model.ConfigurationException;
import com.genymobile.scrcpy.model.NewDisplay;
import com.genymobile.scrcpy.opengl.OpenGLRunner;
import com.genymobile.scrcpy.util.Ln;
import com.genymobile.scrcpy.util.LogUtils;
import com.genymobile.scrcpy.video.CameraCapture;
import com.genymobile.scrcpy.video.NewDisplayCapture;
import com.genymobile.scrcpy.video.PerspectiveCamera;
import com.genymobile.scrcpy.video.ScreenCapture;
import com.genymobile.scrcpy.video.SurfaceCapture;
import com.genymobile.scrcpy.video.SurfaceEncoder;
import com.genymobile.scrcpy.video.VideoSource;

import android.annotation.SuppressLint;
import android.os.Build;
import android.os.Looper;
import android.system.Os;

import java.io.File;
import java.io.IOException;
import java.lang.reflect.Field;
import java.util.ArrayList;
import java.util.List;

public final class Server {

    public static final String SERVER_PATH;

    static {
        String[] classPaths = System.getProperty("java.class.path").split(File.pathSeparator);
        // By convention, scrcpy is always executed with the absolute path of scrcpy-server.jar as the first item in the classpath
        SERVER_PATH = classPaths[0];
    }

    private static class Completion {
        private int running;
        private boolean fatalError;

        Completion(int running) {
            this.running = running;
        }

        synchronized void addCompleted(boolean fatalError) {
            --running;
            if (fatalError) {
                this.fatalError = true;
            }
            if (running == 0 || this.fatalError) {
                Looper.getMainLooper().quitSafely();
            }
        }
    }

    private Server() {
        // not instantiable
    }

    private static void scrcpy(Options options) throws IOException, ConfigurationException {
        if (Build.VERSION.SDK_INT < AndroidVersions.API_31_ANDROID_12 && options.getVideoSource() == VideoSource.CAMERA) {
            Ln.e("Camera mirroring is not supported before Android 12");
            throw new ConfigurationException("Camera mirroring is not supported");
        }

        if (Build.VERSION.SDK_INT < AndroidVersions.API_29_ANDROID_10) {
            if (options.getNewDisplay() != null) {
                Ln.e("New virtual display is not supported before Android 10");
                throw new ConfigurationException("New virtual display is not supported");
            }
            if (options.getDisplayImePolicy() != -1) {
                Ln.e("Display IME policy is not supported before Android 10");
                throw new ConfigurationException("Display IME policy is not supported");
            }
        }

        CleanUp cleanUp = null;

        if (options.getCleanup()) {
            cleanUp = CleanUp.start(options);
        }

        int scid = options.getScid();
        boolean tunnelForward = options.isTunnelForward();
        boolean control = options.getControl();
        boolean video = options.getVideo();
        boolean audio = options.getAudio();
        boolean sendDummyByte = options.getSendDummyByte();

        Workarounds.apply();

        List<AsyncProcessor> asyncProcessors = new ArrayList<>();

        BgNotification bgNotification = null;
        PerspectiveCamera perspective = null;
        Controller controller = null;

        DesktopConnection connection = DesktopConnection.open(scid, tunnelForward, video, audio, control, sendDummyByte, options.getPerspectiveCamera());
        try {
            if (options.getSendDeviceMeta()) {
                connection.sendDeviceMeta(Device.getDeviceName());
            }

            if (options.getPerspectiveCamera()) {
                if (options.getNewDisplay() == null || !control || !video) { throw new IOException("Perspective camera requires an application display"); }
                final CleanUp perspectiveCleanUp = cleanUp;
                try {
                    perspective = new PerspectiveCamera(scid, options.getPerspectivePackage(), connection.getPerspectiveSocket(),
                            perspectiveCleanUp == null ? null : perspectiveCleanUp::setPerspectiveAssociation, options.getCameraEvictionNotice());
                } catch (IOException e) {
                    // The enabled-by-default optional camera must not prevent
                    // ordinary casting on older or unsupported Android devices.
                    Ln.w("SCRCPY_EZ_PERSPECTIVE_UNAVAILABLE: normal projection continues; " + e.getMessage());
                    connection.getPerspectiveSocket().close();
                }
            }

            if (control) {
                ControlChannel controlChannel = connection.getControlChannel();
                controller = new Controller(controlChannel, cleanUp, options);
                asyncProcessors.add(controller);

                // scrcpy-ez: 设备端「正在投屏」提示通知（带「停止投屏」按钮）。
                // 点击停止 → 走控制通道告诉电脑端优雅停止（而非"意外断开"）。
                // 通知属于附加能力，任何失败都在 BgNotification 内部静默降级。
                // scid 传入：通知 id 会话唯一（多会话并行时通知/事件互不干扰，v2.1.30）。
                // v2.1.75+：副文优先显示**控制端名称**（电脑主机名，客户端经 ez_client_name
                // 参数传入）——通知是给看手机的人看的：他清楚自己拿着什么设备，
                // "哪台电脑在连我"才是关键信息（安全感知）；旧客户端不传 → 回退设备名。
                final Controller activeController = controller;
                String ezClientName = options.getEzClientName();
                String notifText = (ezClientName != null && !ezClientName.isEmpty())
                        ? ezClientName + " · 点按可停止"
                        : Device.getDeviceName() + " · 点按可停止";
                bgNotification = BgNotification.start(scid, "scrcpy-ez 正在投屏", notifText,
                        () -> activeController.getDeviceMessageSender().send(DeviceMessage.createStopMirroring()));
            }

            if (audio) {
                AudioCodec audioCodec = options.getAudioCodec();
                AudioSource audioSource = options.getAudioSource();
                AudioCapture audioCapture;
                if (audioSource.isDirect()) {
                    audioCapture = new AudioDirectCapture(audioSource);
                } else {
                    audioCapture = new AudioPlaybackCapture(options.getAudioDup());
                }

                Streamer audioStreamer = new Streamer(connection.getAudioFd(), audioCodec, options.getSendStreamMeta(), options.getSendFrameMeta());
                AsyncProcessor audioRecorder;
                if (audioCodec == AudioCodec.RAW) {
                    audioRecorder = new AudioRawRecorder(audioCapture, audioStreamer);
                } else {
                    audioRecorder = new AudioEncoder(audioCapture, audioStreamer, options);
                }
                asyncProcessors.add(audioRecorder);
            }

            if (video) {
                Streamer videoStreamer = new Streamer(connection.getVideoFd(), options.getVideoCodec(), options.getSendStreamMeta(),
                        options.getSendFrameMeta());
                SurfaceCapture surfaceCapture;
                if (options.getVideoSource() == VideoSource.DISPLAY) {
                    NewDisplay newDisplay = options.getNewDisplay();
                    if (newDisplay != null) {
                        surfaceCapture = new NewDisplayCapture(controller, options, perspective);
                    } else {
                        assert options.getDisplayId() != Device.DISPLAY_ID_NONE;
                        surfaceCapture = new ScreenCapture(controller, options);
                    }
                } else {
                    surfaceCapture = new CameraCapture(options);
                }
                SurfaceEncoder surfaceEncoder = new SurfaceEncoder(surfaceCapture, videoStreamer, options,
                        controller != null ? controller.getDeviceMessageSender() : null,
                        controller != null ? controller.getClientBacklog() : null);
                asyncProcessors.add(surfaceEncoder);

                if (controller != null) {
                    controller.setSurfaceCapture(surfaceCapture);
                }
            }

            Completion completion = new Completion(asyncProcessors.size());
            for (AsyncProcessor asyncProcessor : asyncProcessors) {
                asyncProcessor.start((fatalError) -> {
                    completion.addCompleted(fatalError);
                });
            }

            Looper.loop(); // interrupted by the Completion implementation
        } finally {
            // Release before notification/processor cleanup, which can fail or block.
            if (controller != null) {
                controller.releaseKeepAwake();
            }
            if (bgNotification != null) {
                bgNotification.stop();
            }
            if (cleanUp != null && perspective == null) {
                cleanUp.interrupt();
            }
            for (AsyncProcessor asyncProcessor : asyncProcessors) {
                asyncProcessor.stop();
            }

            connection.shutdown();

            try {
                if (cleanUp != null && perspective == null) {
                    cleanUp.join(4000);
                }
                for (AsyncProcessor asyncProcessor : asyncProcessors) {
                    asyncProcessor.join();
                }

            } catch (InterruptedException e) {
                // ignore
            } finally {
                if (perspective != null) {
                    try { perspective.close(); }
                    catch (Throwable t) { Ln.e("Perspective close failed", t); }
                }
                // Release the application's display (and camera client) before
                // unregistering its camera. The crash cleanup pipe stays alive
                // until normal camera/device teardown has completed.
                if (perspective != null && cleanUp != null) {
                    cleanUp.interrupt();
                    try { cleanUp.join(4000); } catch (InterruptedException e) { Thread.currentThread().interrupt(); }
                }
                try {
                    OpenGLRunner.shutdown();
                } catch (InterruptedException ignored) {
                    Thread.currentThread().interrupt();
                }
            }

            try { connection.close(); }
            catch (Throwable t) { Ln.w("Connection close failed: " + t); }
        }
    }

    private static void prepareMainLooper() {
        // Like Looper.prepareMainLooper(), but with quitAllowed set to true
        Looper.prepare();
        synchronized (Looper.class) {
            try {
                @SuppressLint("DiscouragedPrivateApi")
                Field field = Looper.class.getDeclaredField("sMainLooper");
                field.setAccessible(true);
                field.set(null, Looper.myLooper());
            } catch (ReflectiveOperationException e) {
                throw new AssertionError(e);
            }
        }
    }

    public static void main(String... args) {
        int status = 0;
        try {
            internalMain(args);
        } catch (Throwable t) {
            Ln.e(t.getMessage(), t);
            status = 1;
        } finally {
            // By default, the Java process exits when all non-daemon threads are terminated.
            // The Android SDK might start some non-daemon threads internally, preventing the scrcpy server to exit.
            // So force the process to exit explicitly.
            System.exit(status);
        }
    }

    private static void internalMain(String... args) throws Exception {
        Thread.UncaughtExceptionHandler defaultHandler = Thread.getDefaultUncaughtExceptionHandler();
        Thread.setDefaultUncaughtExceptionHandler((t, e) -> {
            Ln.e("Exception on thread " + t, e);
            if (defaultHandler != null) {
                defaultHandler.uncaughtException(t, e);
            }
        });

        dropRootPrivileges();

        prepareMainLooper();

        Options options = Options.parse(args);

        Ln.disableSystemStreams();
        Ln.initLogLevel(options.getLogLevel());

        Ln.i("Device: [" + Build.MANUFACTURER + "] " + Build.BRAND + " " + Build.MODEL + " (Android " + Build.VERSION.RELEASE + ")");

        if (options.getList()) {
            if (options.getCleanup()) {
                CleanUp.unlinkSelf();
            }

            if (options.getListEncoders()) {
                Ln.i(LogUtils.buildVideoEncoderListMessage());
                Ln.i(LogUtils.buildAudioEncoderListMessage());
            }
            if (options.getListDisplays()) {
                Ln.i(LogUtils.buildDisplayListMessage());
            }
            if (options.getListCameras() || options.getListCameraSizes()) {
                Workarounds.apply();
                Ln.i(LogUtils.buildCameraListMessage(options.getListCameraSizes()));
            }
            if (options.getListApps()) {
                Workarounds.apply();
                Ln.i("Processing Android apps... (this may take some time)");
                Ln.i(LogUtils.buildAppListMessage());
            }
            if (options.getAppCatalog()) {
                Workarounds.apply();
                Ln.i("SCEZ_APP_CATALOG:" + AppCatalog.build());
            }
            if (options.getExportAppIcons()) {
                // ez 自定义（二期 Step 1c）：导出应用图标 PNG → /data/local/tmp/scrcpy/icons/
                // v2.1.16：支持定向导出（export_app_icons=pkg1,pkg2；全量时 list=null）
                Workarounds.apply();
                String only = options.getExportAppIconsList();
                Ln.i("Exporting app icons..." + (only != null ? " (filtered)" : ""));
                Ln.i("SCEZ_ICON_OWNER:" + AppCatalog.serial());
                int count = Device.exportAppIcons(only, options.getExportAppIconsDir());
                Ln.i("Exported icons: " + count);
            }
            // Just print the requested data, do not mirror
            return;
        }

        try {
            scrcpy(options);
        } catch (ConfigurationException e) {
            // Do not print stack trace, a user-friendly error-message has already been logged
        }
    }

    @SuppressWarnings("deprecation")
    private static void dropRootPrivileges() {
        try {
            if (Os.getuid() == 0) {
                // Copy-paste does not work with root user
                // <https://github.com/Genymobile/scrcpy/issues/6224>
                Os.setuid(2000);
            }
        } catch (Exception e) {
            Ln.w("Cannot set UID", e);
        }
    }
}

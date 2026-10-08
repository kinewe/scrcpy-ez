package com.genymobile.scrcpy.notification;

import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.Workarounds;

import android.app.Notification;
import android.content.ComponentName;
import android.content.Context;
import android.content.pm.ApplicationInfo;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Rect;
import android.graphics.drawable.BitmapDrawable;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.Icon;
import android.net.LocalServerSocket;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.os.Parcelable;
import android.os.SystemClock;
import android.util.Base64;
import android.service.notification.NotificationListenerService;
import android.service.notification.StatusBarNotification;

import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.InputStreamReader;
import java.io.OutputStreamWriter;
import java.io.PrintWriter;
import java.lang.reflect.Method;
import java.security.MessageDigest;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.atomic.AtomicBoolean;

/** Optional shell sidecar. It never runs on the casting/encoding/control path. */
public final class NotificationServer extends NotificationListenerService {
    private static final int VERSION = 2;
    private static final int MAX_SNAPSHOT = 256;
    private static final int MAX_ICON_BYTES = 512 * 1024;
    private static final int MAX_ICON_DIMENSION = 1024;
    private final ArrayBlockingQueue<Item> queue = new ArrayBlockingQueue<>(256);
    private final AtomicBoolean stopping = new AtomicBoolean();
    private final PrintWriter output = new PrintWriter(new OutputStreamWriter(System.out, java.nio.charset.StandardCharsets.UTF_8), true);
    private final Map<String, String> labels = new LinkedHashMap<>();
    private final NotificationIconResolver<AppIcon> icons = new NotificationIconResolver<>(SystemClock::elapsedRealtime,
            this::loadApplicationIcon, artwork -> !artwork.png.isEmpty());

    private static final class AppIcon {
        String id = "";
        String png = "";
        boolean announced;
    }
    private String session;
    private String testTag;
    private long cutoff;
    private long sequence;
    private boolean connected;
    private Method currentUser;
    private Thread startupWatchdog;

    private static final class Item {
        final String type;
        final StatusBarNotification notification;
        final StatusBarNotification[] snapshot;

        Item(String type, StatusBarNotification notification, StatusBarNotification[] snapshot) {
            this.type = type;
            this.notification = notification;
            this.snapshot = snapshot;
        }
    }

    private NotificationServer(String session, String testTag) {
        this.session = session;
        this.testTag = testTag;
    }

    private static String text(CharSequence value, int limit) {
        if (value == null) {
            return "";
        }
        String result = value.toString();
        int end = Math.min(result.length(), limit);
        if (end > 0 && Character.isHighSurrogate(result.charAt(end - 1))) {
            --end;
        }
        return result.substring(0, end);
    }

    private boolean selected(StatusBarNotification sbn) {
        // Initial candidate supports the main user only, without forwarding a different user's profile.
        if (!sbn.getUser().equals(android.os.Process.myUserHandle())) {
            return false;
        }
        if (testTag != null) {
            return FakeContext.PACKAGE_NAME.equals(sbn.getPackageName()) && testTag.equals(sbn.getTag());
        }
        return !FakeContext.PACKAGE_NAME.equals(sbn.getPackageName());
    }

    private boolean eligible(StatusBarNotification sbn) {
        int flags = sbn.getNotification().flags;
        return (flags & (Notification.FLAG_ONGOING_EVENT | Notification.FLAG_FOREGROUND_SERVICE | Notification.FLAG_GROUP_SUMMARY)) == 0;
    }

    private void offer(Item item) {
        if (!stopping.get() && !queue.offer(item)) {
            stop(); // Finite queue: host establishes another quiet baseline, never grows memory.
        }
    }

    private void stop() {
        if (stopping.compareAndSet(false, true)) {
            new Handler(Looper.getMainLooper()).post(() -> { throw new StopLoop(); });
        }
    }

    private static final class StopLoop extends RuntimeException {
        private static final long serialVersionUID = 1L;
    }

    @Override
    public void onListenerConnected() {
        if (connected) {
            stop(); // Rebinding in the same process must not create a second initialization epoch.
            return;
        }
        connected = true;
        offer(new Item("baseline", null, getActiveNotifications()));
    }

    @Override
    public void onListenerDisconnected() {
        stop();
    }

    @Override
    public void onNotificationPosted(StatusBarNotification sbn) {
        if (connected && selected(sbn)) {
            offer(new Item(eligible(sbn) ? "post" : "remove", sbn, null));
        }
    }

    @Override
    public void onNotificationRemoved(StatusBarNotification sbn) {
        if (connected && selected(sbn)) {
            offer(new Item("remove", sbn, null));
        }
    }

    private String label(String pkg) {
        String result = labels.get(pkg);
        if (result != null) {
            return result;
        }
        result = pkg;
        try {
            Context context = FakeContext.get();
            ApplicationInfo info = context.getPackageManager().getApplicationInfo(pkg, 0);
            result = text(context.getPackageManager().getApplicationLabel(info), 128);
        } catch (Exception ignored) {
            // Label lookup is optional. Do not log foreign notification extras or text.
        }
        if (labels.size() >= 128) {
            labels.clear();
        }
        labels.put(pkg, result);
        return result;
    }

    private String displayPackage(StatusBarNotification sbn) {
        return NotificationIdentity.displayPackage(sbn.getPackageName(), sbn.getNotification(), pkg -> {
            try {
                Context context = FakeContext.get();
                ApplicationInfo info = context.getPackageManager().getApplicationInfo(pkg, 0);
                if (labels.size() >= 128 && !labels.containsKey(pkg)) {
                    labels.clear();
                }
                labels.put(pkg, text(context.getPackageManager().getApplicationLabel(info), 128));
                return true;
            } catch (Exception ignored) {
                return false; // Uninstalled, another profile or not visible to this listener.
            }
        });
    }

    private AppIcon encodeIcon(Drawable drawable) throws Exception {
        AppIcon result = new AppIcon();
        if (drawable == null) {
            return result;
        }
        Rect bounds = new Rect(drawable.getBounds());
        Bitmap rendered = null;
        try {
            // Preserve bitmap pixels. Adaptive/vector resources are rasterized at intrinsic size.
            Bitmap bitmap;
            if (drawable instanceof BitmapDrawable) {
                bitmap = ((BitmapDrawable) drawable).getBitmap();
            } else {
                int width = drawable.getIntrinsicWidth();
                int height = drawable.getIntrinsicHeight();
                if (width < 1 || height < 1 || width > MAX_ICON_DIMENSION || height > MAX_ICON_DIMENSION) {
                    return result;
                }
                rendered = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
                drawable.setBounds(0, 0, width, height);
                drawable.draw(new Canvas(rendered));
                bitmap = rendered;
            }
            if (bitmap != null && bitmap.getWidth() <= MAX_ICON_DIMENSION && bitmap.getHeight() <= MAX_ICON_DIMENSION) {
                ByteArrayOutputStream stream = new ByteArrayOutputStream();
                if (bitmap.compress(Bitmap.CompressFormat.PNG, 100, stream)) {
                    byte[] bytes = stream.toByteArray();
                    if (bytes.length <= MAX_ICON_BYTES) {
                        result.id = digest(bytes);
                        result.png = Base64.encodeToString(bytes, Base64.NO_WRAP);
                    }
                }
            }
        } finally {
            drawable.setBounds(bounds);
            if (rendered != null) {
                rendered.recycle(); // The source bitmap remains owned by Android.
            }
        }
        return result;
    }

    private AppIcon loadApplicationIcon(String pkg) throws Exception {
        Context context = FakeContext.get();
        ApplicationInfo info = context.getPackageManager().getApplicationInfo(pkg, 0);
        if (labels.size() >= 128 && !labels.containsKey(pkg)) {
            labels.clear();
        }
        labels.put(pkg, text(context.getPackageManager().getApplicationLabel(info), 128));
        return encodeIcon(context.getPackageManager().getApplicationIcon(info));
    }

    private AppIcon icon(Notification notification, String owner, String display) {
        AppIcon artwork = icons.resolve(owner, display, () -> {
            Object value = notification.extras == null ? null : notification.extras.get("miui.appIcon");
            if (value instanceof Icon) {
                Icon supplied = (Icon) value;
                int type = supplied.getType();
                // Avoid resolving content/file URI artwork or fetching external resources.
                if (type == Icon.TYPE_BITMAP || type == Icon.TYPE_ADAPTIVE_BITMAP || type == Icon.TYPE_RESOURCE) {
                    AppIcon fallback = encodeIcon(supplied.loadDrawable(FakeContext.get()));
                    if (!fallback.png.isEmpty()) {
                        // Content-keyed fallback art retains its announced state without mixing promotions.
                        return icons.remember("notification:" + display + ":" + fallback.id, fallback);
                    }
                }
            }
            return null;
        });
        return artwork == null ? new AppIcon() : artwork;
    }

    private JSONObject record(StatusBarNotification sbn, boolean includeIcon) throws Exception {
        Notification notification = sbn.getNotification();
        Bundle extras = notification.extras == null ? Bundle.EMPTY : notification.extras;
        String display = displayPackage(sbn);
        JSONObject value = new JSONObject();
        if (includeIcon) {
            AppIcon artwork = icon(notification, sbn.getPackageName(), display);
            if (!artwork.png.isEmpty()) {
                if (!artwork.announced) {
                    if (!write("icon", new JSONObject().put("id", artwork.id).put("png", artwork.png))) {
                        throw new java.io.IOException("artwork transport closed");
                    }
                    artwork.announced = true;
                }
                value.put("iconId", artwork.id);
            }
        }
        value.put("key", key(sbn));
        value.put("package", sbn.getPackageName());
        value.put("displayPackage", display);
        value.put("app", label(display));
        value.put("postTime", sbn.getPostTime());
        // Android assigns each user a 100000-wide UID range; getIdentifier is not in SDK stubs.
        value.put("user", android.os.Process.myUid() / 100000);
        value.put("title", text(extras.getCharSequence(Notification.EXTRA_TITLE), 256));
        CharSequence body = extras.getCharSequence(Notification.EXTRA_BIG_TEXT);
        if (body == null) {
            body = extras.getCharSequence(Notification.EXTRA_TEXT);
        }
        long messageTime = 0;
        // MessagingStyle bundles use text/time. Read only the newest textual message.
        Parcelable[] messages = extras.getParcelableArray("android.messages");
        if (messages != null) {
            for (int i = messages.length - 1; i >= 0; --i) {
                if (messages[i] instanceof Bundle) {
                    Bundle message = (Bundle) messages[i];
                    CharSequence candidate = message.getCharSequence("text");
                    if (candidate != null && candidate.length() > 0) {
                        body = candidate;
                        messageTime = message.getLong("time");
                        break;
                    }
                }
            }
        }
        value.put("body", text(body, 4096));
        String verificationCode = NotificationVerificationCode.extract(sbn.getPackageName(), extras.get("verify_code"));
        if (!verificationCode.isEmpty()) {
            value.put("verificationCode", verificationCode);
        }
        value.put("messageTime", messageTime);
        value.put("onlyAlertOnce", (notification.flags & Notification.FLAG_ONLY_ALERT_ONCE) != 0);
        return value;
    }

    private static String key(StatusBarNotification sbn) throws Exception {
        return digest(sbn.getKey().getBytes(java.nio.charset.StandardCharsets.UTF_8));
    }

    private static String digest(byte[] bytes) throws Exception {
        byte[] digest = MessageDigest.getInstance("SHA-256").digest(bytes);
        StringBuilder result = new StringBuilder(64);
        final String hex = "0123456789abcdef";
        for (byte value : digest) {
            result.append(hex.charAt((value >>> 4) & 15));
            result.append(hex.charAt(value & 15));
        }
        return result.toString();
    }

    private JSONObject safeRecord(StatusBarNotification sbn, boolean includeIcon) {
        try {
            return record(sbn, includeIcon);
        } catch (Exception ignored) {
            // Malformed extras from one application must not stop other notifications.
            return null;
        }
    }

    private boolean write(String type, JSONObject record) throws Exception {
        JSONObject frame = new JSONObject();
        frame.put("v", VERSION);
        frame.put("session", session);
        frame.put("seq", ++sequence);
        frame.put("type", type);
        if (record != null) {
            frame.put("icon".equals(type) ? "icon" : "record", record);
        }
        if ("hello".equals(type)) {
            frame.put("pid", android.os.Process.myPid());
            frame.put("cutoff", cutoff);
        }
        output.println(frame.toString());
        boolean written = !output.checkError();
        if (written && "ready".equals(type)) {
            startupWatchdog.interrupt();
        }
        return written;
    }

    private void drain() {
        try {
            if (!write("hello", null)) {
                stop();
                return;
            }
            while (!stopping.get()) {
                Item item = queue.take();
                if (((Integer) currentUser.invoke(null)) != 0) {
                    stop(); // User switch: clear host state rather than forwarding another space.
                    return;
                }
                if ("baseline".equals(item.type)) {
                    int count = 0;
                    if (item.snapshot != null) {
                        for (StatusBarNotification sbn : item.snapshot) {
                            if (selected(sbn) && eligible(sbn) && count++ < MAX_SNAPSHOT) {
                                JSONObject value = safeRecord(sbn, sbn.getPostTime() > cutoff);
                                if (value != null && !write("snapshot", value)) {
                                    stop();
                                    return;
                                }
                            }
                        }
                    }
                    if (!write("ready", null)) {
                        stop();
                    }
                } else {
                    JSONObject value;
                    if ("remove".equals(item.type)) {
                        value = new JSONObject().put("key", key(item.notification));
                    } else {
                        value = safeRecord(item.notification, true);
                        if (value == null) {
                            continue;
                        }
                    }
                    if (!write(item.type, value)) {
                        stop();
                    }
                }
            }
        } catch (Exception ignored) {
            stop(); // Errors are status-only; never put notification contents into logs.
        }
    }

    public static void main(String[] args) {
        if (args.length < 2 || !args[0].matches("[a-f0-9]{32}") || args.length > 3) {
            return;
        }
        String session = args[0];
        String remote = "/data/local/tmp/scrcpy-ez-notification-" + session + ".jar";
        Thread startup = new Thread(() -> {
            try {
                Thread.sleep(20000);
                // Bound hangs before hello/PID and before the stdin lifetime reader starts.
                if (remote.equals(System.getenv("CLASSPATH"))) {
                    new File(remote).delete();
                }
                System.exit(0); // Binder death releases a partially registered listener.
            } catch (InterruptedException ignored) {
                // Ready or normal cleanup cancels this startup-only deadline.
            }
        }, "notification-startup-deadline");
        startup.setDaemon(true);
        startup.start();
        LocalServerSocket ownership = null;
        NotificationServer service = null;
        boolean registered = false;
        Thread writer = null;
        String failureCode = "unsupported";
        try {
            Looper.prepareMainLooper();
            Workarounds.apply();
            failureCode = "identity";
            Method property = Class.forName("android.os.SystemProperties").getDeclaredMethod("get", String.class);
            property.setAccessible(true);
            String serial = (String) property.invoke(null, "ro.serialno");
            String bootSerial = (String) property.invoke(null, "ro.boot.serialno");
            if (!args[1].equals(serial) && !args[1].equals(bootSerial)) {
                throw new IllegalStateException("device identity changed");
            }
            failureCode = "busy";
            ownership = new LocalServerSocket("scez_notification_v1");
            service = new NotificationServer(session, args.length == 3 ? args[2] : null);
            NotificationServer current = service;
            current.startupWatchdog = startup;
            current.currentUser = Class.forName("android.app.ActivityManager").getDeclaredMethod("getCurrentUser");
            current.currentUser.setAccessible(true);
            failureCode = "user";
            if (((Integer) current.currentUser.invoke(null)) != 0) {
                throw new IllegalStateException("unsupported user");
            }
            current.cutoff = System.currentTimeMillis();
            failureCode = "permission";
            Method register = NotificationListenerService.class.getDeclaredMethod("registerAsSystemService", Context.class, ComponentName.class, int.class);
            register.setAccessible(true);
            register.invoke(current, FakeContext.get(), new ComponentName(FakeContext.PACKAGE_NAME, NotificationServer.class.getName()), 0);
            registered = true;
            writer = new Thread(current::drain, "notification-output");
            writer.setUncaughtExceptionHandler((thread, failure) -> current.stop());
            writer.start();
            Thread input = new Thread(() -> {
                try {
                    BufferedReader reader = new BufferedReader(new InputStreamReader(System.in, java.nio.charset.StandardCharsets.UTF_8));
                    // No inbound commands or arbitrary intents: any input/EOF means stop.
                    reader.read();
                } catch (Exception ignored) {
                    // EOF and transport failure both release the listener.
                }
                current.stop();
            }, "notification-lifetime");
            input.setDaemon(true);
            input.start();
            try {
                Looper.loop();
            } catch (StopLoop ignored) {
                // Own sentinel only; resources are released below.
            }
        } catch (Exception failure) {
            // Fixed codes only; never expose exception messages, identities or notifications.
            if (writer == null) {
                try {
                    String code = failure instanceof NoSuchMethodException || failure instanceof ClassNotFoundException ? "unsupported" : failureCode;
                    JSONObject frame = new JSONObject().put("v", VERSION).put("session", session).put("seq", 1).put("type", "error").put("code", code);
                    System.out.println(frame.toString());
                } catch (Exception ignored) {
                    // The host still reports a bounded startup failure.
                }
            }
        } finally {
            startup.interrupt();
            if (service != null) {
                service.stopping.set(true);
            }
            if (writer != null) {
                writer.interrupt();
            }
            if (registered) {
                try {
                    Method unregister = NotificationListenerService.class.getDeclaredMethod("unregisterAsSystemService");
                    unregister.setAccessible(true);
                    unregister.invoke(service);
                } catch (Exception ignored) {
                    // Binder death is the final cleanup guard.
                }
            }
            if (ownership != null) {
                try {
                    ownership.close();
                } catch (Exception ignored) {
                    // No network listener is created, this abstract socket is an ownership lock.
                }
            }
            // Only this invocation's exact owned file, never the casting server or another session.
            if (remote.equals(System.getenv("CLASSPATH"))) {
                new File(remote).delete();
            }
        }
        System.exit(0);
    }
}

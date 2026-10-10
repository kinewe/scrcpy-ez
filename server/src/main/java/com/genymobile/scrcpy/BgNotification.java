package com.genymobile.scrcpy;

import com.genymobile.scrcpy.util.Ln;

import android.annotation.SuppressLint;
import android.annotation.TargetApi;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.PorterDuff;
import android.graphics.PorterDuffXfermode;
import android.graphics.drawable.Icon;
import android.os.Build;
import android.os.Process;
import android.util.Base64;
import android.util.DisplayMetrics;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.TimeUnit;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * scrcpy-ez 扩展：设备端「正在投屏」提示通知（纯事件驱动版）。
 *
 * 功能：
 * - 投屏期间常驻显示（ongoing），点击通知本体即停止投屏
 * - 用户点击通知 → 毫秒级触发 onStopRequested（Server 接的是"发 TYPE_STOP_MIRRORING"）
 * - 通知被划掉 → 自动重新发出（保持"常驻"语义）
 * - v2.1.75「设备级单条通知」：通知 id 固定（不区分会话）——多会话（主投屏 + 应用窗口）
 *   同 id 覆盖=通知栏始终一条；点击事件被同 key 的所有 server 共同消费=点一下停该设备
 *   全部投屏；会话退出撤销时存活者自动重发=通知活到最后一位
 *
 * 信号来源（本版本不轮询，纯事件驱动）：
 * 用户在通知栏点击 / 划掉一条通知时，system_server 的 NotificationManagerService 会向
 * Android events log buffer 写一条结构化事件（AOSP 标准行为，非 ROM 私有）：
 *
 *     I notification_clicked:  [<通知key>,<uid>,<pid>,...]
 *     I notification_canceled: [<通知key>,<reason>,...]      reason=2 表示用户划掉
 *
 * 事件里带完整通知 key（userId|pkg|id|tag|uid），因此可以精确匹配"是不是自己那条通知"。
 * 本类在投屏开始时启动一个常驻子进程：
 *
 *     /system/bin/logcat -b events -v epoch -T 1 -s notification_clicked notification_canceled
 *
 * 读取其标准输出并逐行解析（必须读 events buffer：main buffer 里没有这两个事件）。
 * logcat 与 scrcpy-server 同为 shell(uid 2000)、且带 AID_LOG(1007) 组，实测可读。
 *
 * 为什么不用 BroadcastReceiver / NotificationListenerService：
 * - shell/app_process 进程注册动态广播接收器会被 AMS 静默丢弃（实测整机 Registered
 *   Receiver 中 uid=2000 为 0；直调 IActivityManager 亦无效）
 * - NotificationListenerService 需要在 App manifest 里声明服务，等于要装 App
 * 事件流读取绕开了两者，且延迟在毫秒级（对比轮询版最长 1 秒）。
 *
 * 一切失败都只记日志并静默降级：提示通知绝不能影响投屏主流程。
 */
public final class BgNotification {

    private static final String CHANNEL_ID = "scrcpy_ez_mirroring";
    private static final String CHANNEL_NAME = "投屏提示";
    /** 通知 id 基值（"SCZ"）；实际 id = 基值 ^ scid（会话唯一，见 notificationId()）。 */
    private static final int NOTIFICATION_BASE_ID = 0x53435A; // "SCZ"

    private static final String ACTION_STOP = "com.genymobile.scrcpy.action.STOP_MIRRORING";
    private static final int REQ_STOP = 1;

    private static final String LOGCAT_BIN = "/system/bin/logcat";
    private static final String EVENT_CLICKED = "notification_clicked";
    private static final String EVENT_CANCELED = "notification_canceled";

    /**
     * logcat -v epoch 输出形如：
     * "  1789487906.531  3365  6547 I notification_clicked: [0|com.android.shell|5456730|null|2000,25253,...]"
     * 分组：1=秒, 2=毫秒, 3=事件名, 4=通知 key
     */
    private static final Pattern EVENT_LINE = Pattern.compile(
            "^\\s*(\\d{9,})\\.(\\d{3})\\s+\\d+\\s+\\d+\\s+[VDIWEF]\\s+(" + EVENT_CLICKED + "|" + EVENT_CANCELED + "):\\s*\\[([^,\\]]*)");

    /** 事件流子进程异常退出后的重启次数上限（仍属事件驱动，不是轮询） */
    private static final int MAX_READER_RESTARTS = 3;
    private static final long READER_RESTART_DELAY_MS = 1000L;

    /** 重发去抖：避免异常情况下短时间内反复重发 */
    private static final long REPOST_DEBOUNCE_MS = 300L;

    private final Context context;
    private final NotificationManager nm;
    private final String title;
    private final String text;
    private final Runnable onStopRequested;

    /** 本会话通知 id（会话唯一，见 notificationId()）：post/cancel/keySuffix 一致使用。 */
    private final int notificationId;

    /** 本会话通知 key 的后缀：|com.android.shell|<id>|null|<uid> */
    private final String keySuffix;

    /** 会话开始时刻（挂钟 ms）：早于它的事件（上一次会话残留）一律忽略 */
    private final long sessionStartWallMs;

    private volatile boolean stopped;
    private volatile boolean stopRequested;
    private volatile Thread readerThread;
    private volatile java.lang.Process logcatProcess;
    private volatile long lastPostWallMs;

    /**
     * 延迟补发调度（v2.1.75）：直接重发命中防抖窗口时延迟补发，避免通知空窗
     * （固定通知 id 后，"幸存者重发"是通知不消失的唯一保障）。
     */
    private final java.util.concurrent.ScheduledExecutorService repostScheduler =
            java.util.concurrent.Executors.newSingleThreadScheduledExecutor(r -> {
                Thread t = new Thread(r, "bg-notification-repost");
                t.setDaemon(true);
                return t;
            });

    private BgNotification(Context context, NotificationManager nm, int scid, String title, String text, Runnable onStopRequested) {
        this.context = context;
        this.nm = nm;
        this.title = title;
        this.text = text;
        this.onStopRequested = onStopRequested;
        this.notificationId = notificationId();
        this.keySuffix = "|" + FakeContext.PACKAGE_NAME + "|" + this.notificationId + "|null|" + Process.myUid();
        this.sessionStartWallMs = System.currentTimeMillis();
    }

    /**
     * 通知 id（v2.1.75：固定单条，不再 ^ scid——「设备级通知」语义）。
     *
     * 为什么改回固定：
     * ①多会话并行（主投屏 + 应用窗口虚拟屏）时同 id notify = 后发覆盖先发
     *   → 通知栏始终只有一条（产品语义：一个设备一条投屏通知，不区分会话）；
     * ②点击事件（events buffer）按通知 key 匹配——同 id 使所有 server 的 keySuffix
     *   相同：用户点一下，每个会话的 server 都会读到这次点击并各自优雅停止
     *   → 「点一下停该设备全部投屏」；
     * ③某个会话退出撤销通知时，仍存活的其它 server 会收到 canceled 事件并自动重发
     *   （见 handleEventLine）→ 通知"活到最后一位"，不会出现"还有会话在投屏而通知
     *   被先退出的会话撤走"的空窗。
     *
     * （v2.1.30 曾改为 ^ scid 会话唯一，用于"多会话互不干扰"；本次按主人拍板
     * 反向收编为"设备级单条 + 点击全停"，多会话停止判据由电脑端配套处理。）
     */
    private static int notificationId() {
        return NOTIFICATION_BASE_ID;
    }

    /**
     * 发出提示通知并开始监听用户操作。任何异常都返回 null（不影响投屏）。
     *
     * @param scid 会话 id（client 传入）：用于生成会话唯一的通知 id（多会话并行互不干扰）。
     */
    public static BgNotification start(int scid, String title, String text, Runnable onStopRequested) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) {
            return null; // Bitmap notification icons are unavailable before Android 6.
        }
        try {
            Context context = FakeContext.get();
            NotificationManager nm = (NotificationManager) context.getSystemService(Context.NOTIFICATION_SERVICE);
            if (nm == null) {
                Ln.w("NotificationManager unavailable, skipping device notification");
                return null;
            }

            BgNotification bg = new BgNotification(context, nm, scid, title, text, onStopRequested);
            bg.createChannel();
            // 先起事件流再发通知：保证不会漏掉紧随其后的点击
            bg.startEventReader();
            bg.post();
            return bg;
        } catch (Throwable t) {
            Ln.w("Cannot start device notification: " + t);
            return null;
        }
    }

    private void createChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) { // API 26-
            return;
        }
        NotificationChannel channel = new NotificationChannel(CHANNEL_ID, CHANNEL_NAME, NotificationManager.IMPORTANCE_LOW);
        channel.setDescription("投屏进行中的提示通知");
        channel.setShowBadge(false);
        nm.createNotificationChannel(channel);
    }

    /**
     * 通知小图标 = scrcpy-ez 品牌剪影（显示器 + 手机，无信号弧）。
     *
     * 构图与 GUI/应用图标（同一幅品牌插画）一致：显示器在右上、手机在左下，手机压住
     * 显示器的左边框；手机与显示器之间用 CLEAR 拉出 0.8u 的透明间隙，否则单色剪影下
     * 两块形体糊成一团（smallIcon 只保留 alpha，系统再按主题染色，形状必须自解释）。
     *
     * 尺寸按 24dp × 屏幕密度生成并声明 density：smallIcon 在系统里按 24dp 渲染，因此
     * 2x/3x 屏都不会被上采样发虚（原先固定 48px，在 3x 屏上只相当于 16dp）。
     *
     * 不能用 android.R.drawable.* ：shell 进程（uid 2000）不拥有 android 资源包，
     * Icon.createWithResource() 会抛 SecurityException("Package android is not owned
     * by uid 2000")。自造 Bitmap 完全不涉及资源包归属检查。
     */
    @TargetApi(Build.VERSION_CODES.M)
    private Icon makeIcon() {
        try {
            DisplayMetrics dm = context.getResources().getDisplayMetrics();
            int size = Math.max(48, Math.round(24 * dm.density)); // 24dp
            Bitmap bmp = brandSilhouette(size);
            bmp.setDensity(dm.densityDpi);
            return Icon.createWithBitmap(bmp);
        } catch (Throwable t) {
            Ln.w("Notification small icon: brand silhouette unavailable, using fallback: " + t);
            return fallbackIcon();
        }
    }

    /**
     * 品牌剪影位图。坐标基于 24 单位网格（u = size / 24），比例取自品牌插画实测值：
     * 显示器 x 6.21..22.80 / y 3.19..16.83，支架底座 y 17.40..20.19，
     * 手机 x 1.20..9.06 / y 6.70..20.81（左下，明显低于显示器）。
     */
    private static Bitmap brandSilhouette(int size) {
        Bitmap bmp = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(bmp);
        Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
        paint.setColor(0xFFFFFFFF);
        paint.setStyle(Paint.Style.FILL);
        float u = size / 24f;
        // 显示器（右上）
        canvas.drawRoundRect(6.21f * u, 3.19f * u, 22.80f * u, 16.83f * u, 0.95f * u, 0.95f * u, paint);
        // 支架：颈 + 底座
        canvas.drawRect(11.07f * u, 16.83f * u, 14.27f * u, 17.51f * u, paint);
        canvas.drawRoundRect(11.95f * u, 17.40f * u, 16.49f * u, 20.19f * u, 0.70f * u, 0.70f * u, paint);
        // 手机（左下）：先在显示器上挖出透明间隙，再画手机本体
        float gap = 0.80f * u;
        paint.setXfermode(new PorterDuffXfermode(PorterDuff.Mode.CLEAR));
        canvas.drawRoundRect(1.20f * u - gap, 6.70f * u - gap, 9.06f * u + gap, 20.81f * u + gap, 2.15f * u, 2.15f * u, paint);
        paint.setXfermode(null);
        canvas.drawRoundRect(1.20f * u, 6.70f * u, 9.06f * u, 20.81f * u, 1.35f * u, 1.35f * u, paint);
        return bmp;
    }

    /** 兜底小图标（旧的白色实心圆）：剪影构造万一失败，也不让通知整体失败。 */
    @TargetApi(Build.VERSION_CODES.M)
    private static Icon fallbackIcon() {
        int size = 48;
        Bitmap bmp = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(bmp);
        Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);
        paint.setColor(0xFFFFFFFF);
        canvas.drawCircle(size / 2f, size / 2f, size / 3f, paint);
        return Icon.createWithBitmap(bmp);
    }

    // ------------------------------------------------------------ 品牌大图标

    private static Bitmap largeIcon;
    private static boolean largeIconLoaded;

    /**
     * 通知大图标（scrcpy-ez 品牌插画）：内嵌 base64 PNG（见 NotificationIconData）
     * → BitmapFactory 解码。静态懒加载，只 decode 一次（失败也记一次，不重复尝试）。
     *
     * 任何异常（数据异常 / 无解码器 / OOM）一律返回 null：调用方跳过 setLargeIcon，
     * 通知照常发出（与"通知属于附加能力，失败静默降级"的既有原则一致）。
     */
    private static synchronized Bitmap loadLargeIcon() {
        if (largeIconLoaded) {
            return largeIcon;
        }
        largeIconLoaded = true;
        try {
            byte[] png = Base64.decode(NotificationIconData.base64(), Base64.DEFAULT);
            Bitmap bitmap = BitmapFactory.decodeByteArray(png, 0, png.length);
            if (bitmap != null) {
                largeIcon = bitmap;
            } else {
                Ln.w("Notification large icon: decode returned null, skipped");
            }
        } catch (Throwable t) {
            Ln.w("Notification large icon unavailable: " + t);
        }
        return largeIcon;
    }

    private PendingIntent stopIntent() {
        Intent intent = new Intent(ACTION_STOP).setPackage(FakeContext.PACKAGE_NAME);
        int flags = PendingIntent.FLAG_UPDATE_CURRENT;
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) { // API 23+
            flags |= PendingIntent.FLAG_IMMUTABLE;
        }
        return PendingIntent.getBroadcast(context, REQ_STOP, intent, flags);
    }

    @TargetApi(Build.VERSION_CODES.M)
    private Notification build() {
        Notification.Builder builder;
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) { // API 26+
            builder = new Notification.Builder(context, CHANNEL_ID);
        } else {
            builder = new Notification.Builder(context);
        }

        Icon icon = makeIcon();
        PendingIntent stop = stopIntent();

        // 通知本体点击 = 停止（既有能力，保持不变）。
        // 原先还有一个「停止投屏」action 按钮，但系统/ROM 对 action 点击不写任何可检测
        // 事件（events buffer 的 notification_action_clicked 恒为 0，SysUI 只记录本体
        // 点击），点它无法被 server 感知，故删除。渠道 id/文案/contentIntent 均未变。
        builder.setSmallIcon(icon)
                .setContentTitle(title)
                .setContentText(text)
                .setOngoing(true)
                .setShowWhen(false)
                .setContentIntent(stop);

        // 品牌大图标：MIUI 等 ROM 优先显示 largeIcon（覆盖 shell 的默认图标）。
        // 解码失败则跳过，通知照常发出。
        Bitmap large = loadLargeIcon();
        if (large != null) {
            builder.setLargeIcon(large);
        }

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP && Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            builder.setPriority(Notification.PRIORITY_LOW);
        }

        return builder.build();
    }

    private void post() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) {
            return;
        }
        lastPostWallMs = System.currentTimeMillis();
        postAsShell();
    }

    @TargetApi(Build.VERSION_CODES.M)
    @SuppressLint("NotificationPermission") // app_process uses the shell package permission; denial is caught by start/repost.
    private void postAsShell() {
        nm.notify(notificationId, build());
    }

    /**
     * 投屏结束：撤下通知并停止事件流。
     */
    public void stop() {
        stopped = true;
        destroyLogcat();
        Thread thread = readerThread;
        if (thread != null) {
            thread.interrupt();
        }
        repostScheduler.shutdownNow();
        try {
            nm.cancel(notificationId);
        } catch (Throwable t) {
            Ln.w("Cancel notification failed: " + t);
        }
    }

    // ------------------------------------------------------- 事件流读取（events buffer）

    private void startEventReader() {
        Thread thread = new Thread(this::readLoop, "bg-notification-events");
        thread.setDaemon(true);
        readerThread = thread;
        thread.start();
    }

    private void readLoop() {
        int restarts = 0;
        while (!stopped && !stopRequested) {
            java.lang.Process process = null;
            try {
                process = new ProcessBuilder(LOGCAT_BIN, "-b", "events", "-v", "epoch", "-T", "1",
                        "-s", EVENT_CLICKED, EVENT_CANCELED).redirectErrorStream(true).start();
                logcatProcess = process;
                Ln.d("Device notification event stream started");
                try (BufferedReader reader = new BufferedReader(new InputStreamReader(process.getInputStream(), StandardCharsets.UTF_8))) {
                    String line;
                    while (!stopped && !stopRequested && (line = reader.readLine()) != null) {
                        handleEventLine(line);
                    }
                }
            } catch (Throwable t) {
                if (!stopped && !stopRequested) {
                    Ln.w("Device notification event stream error: " + t);
                }
            } finally {
                if (process != null) {
                    process.destroy();
                }
                logcatProcess = null;
            }

            if (stopped || stopRequested) {
                return;
            }
            if (++restarts > MAX_READER_RESTARTS) {
                Ln.w("Device notification event stream unavailable, device-side stop disabled");
                return;
            }
            try {
                Thread.sleep(READER_RESTART_DELAY_MS);
            } catch (InterruptedException e) {
                return;
            }
        }
    }

    private void handleEventLine(String line) {
        if (stopped || stopRequested) {
            return;
        }
        Matcher matcher = EVENT_LINE.matcher(line);
        if (!matcher.find()) {
            return; // "beginning of events" 之类的非事件行
        }
        long eventWallMs = Long.parseLong(matcher.group(1)) * 1000L + Long.parseLong(matcher.group(2));
        if (eventWallMs < sessionStartWallMs) {
            // 上一次会话残留的历史事件：忽略（-T 1 只回放一行，这里再兜一层）
            return;
        }
        String event = matcher.group(3);
        String key = matcher.group(4).trim();
        if (!key.endsWith(keySuffix)) {
            return; // 不是本会话这条通知
        }

        if (EVENT_CLICKED.equals(event)) {
            stopRequested = true;
            long latencyMs = System.currentTimeMillis() - eventWallMs;
            Ln.i("Stop mirroring requested from device notification (event latency " + latencyMs + "ms)");
            try {
                onStopRequested.run();
            } catch (Throwable t) {
                Ln.w("Stop callback failed: " + t);
            }
        } else {
            // notification_canceled：用户划掉 / 其它会话退出撤销 → 重新发出，保持"划不掉"语义。
            // v2.1.75：固定通知 id 后本分支还承担「幸存者持有」语义——别的会话退出撤销通知时，
            // 本会话（仍存活）必须把通知抢回来；若直接重发命中防抖窗口，改为延迟补发
            // （否则会出现"会话还在跑但通知被前一个退出的会话撤走"的空窗）。
            long sincePost = System.currentTimeMillis() - lastPostWallMs;
            if (sincePost < REPOST_DEBOUNCE_MS) {
                long delay = REPOST_DEBOUNCE_MS - sincePost + 20;
                Ln.i("Device notification dismissed; scheduled re-post in " + delay + "ms");
                try {
                    repostScheduler.schedule(this::safeRepost, delay, TimeUnit.MILLISECONDS);
                } catch (Throwable t) {
                    Ln.w("Schedule re-post failed: " + t);
                }
                return;
            }
            Ln.i("Device notification dismissed, re-posting");
            safeRepost();
        }
    }

    /** 重发通知（直接调用与延迟补发共用）；已停止（stopped/stopRequested）时不再重发。 */
    private void safeRepost() {
        if (stopped || stopRequested) {
            return;
        }
        try {
            post();
        } catch (Throwable t) {
            Ln.w("Re-post notification failed: " + t);
        }
    }

    private void destroyLogcat() {
        java.lang.Process process = logcatProcess;
        if (process != null) {
            try {
                process.destroy();
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                    process.waitFor(200, TimeUnit.MILLISECONDS);
                }
            } catch (Throwable ignored) {
                // best effort
            }
        }
    }
}

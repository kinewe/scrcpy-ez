package lab;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.content.Context;
import android.graphics.Bitmap;
import android.graphics.drawable.Icon;
import android.os.Looper;

/** Emits only the tag selected by TestDeviceWithoutCastingToWindows. */
public final class NotifyProbe {
    public static void main(String[] args) throws Exception {
        if (android.os.Process.myUid() != 2000 || android.os.Build.VERSION.SDK_INT < 26) {
            throw new IllegalStateException("Requires an authorized Android shell on API 26+");
        }
        Looper.prepareMainLooper();
        Class.forName("com.genymobile.scrcpy.Workarounds").getMethod("apply").invoke(null);
        Context context = (Context) Class.forName("com.genymobile.scrcpy.FakeContext").getMethod("get").invoke(null);
        NotificationManager manager = (NotificationManager) context.getSystemService(Context.NOTIFICATION_SERVICE);
        String tag = "scez_notification_lab_20261004";
        int id = 20261004;
        Bitmap bitmap = Bitmap.createBitmap(24, 24, Bitmap.Config.ARGB_8888);
        bitmap.eraseColor(0xff43a884);
        try {
            manager.createNotificationChannel(new NotificationChannel(tag, "ez upgrade synthetic test", NotificationManager.IMPORTANCE_LOW));
            for (String code : new String[] {"824613", "824614"}) {
                Notification message = new Notification.Builder(context, tag).setSmallIcon(Icon.createWithBitmap(bitmap))
                        .setContentTitle("ez upgrade test").setContentText("Synthetic verification code " + code)
                        .setOnlyAlertOnce(true).build();
                manager.notify(tag, id, message);
                Thread.sleep(1500);
            }
        } finally {
            manager.cancel(tag, id);
            manager.deleteNotificationChannel(tag);
        }
        System.out.println("OWN_NOTIFICATION_AND_CHANNEL_REMOVED");
        System.out.flush();
        System.exit(0);
    }
}

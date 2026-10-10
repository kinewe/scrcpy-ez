package lab;

import android.app.NotificationManager;
import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.Workarounds;
import java.lang.reflect.Method;

// Optional HyperOS prerequisite, restricted to the newly installed test UID.
// No caller-supplied package/UID can change another application's permissions.
public final class LabNotificationPermissions {
    public static void main(String[] args) throws Exception {
        android.os.Looper.prepareMainLooper();
        Workarounds.apply();
        String pkg = "org.scrcpyez.notificationlab";
        int uid = FakeContext.get().getPackageManager().getApplicationInfo(pkg, 0).uid;
        if (uid / 100000 != 0) throw new IllegalStateException("not the main-user lab UID");
        Method getter = NotificationManager.class.getDeclaredMethod("getService");
        getter.setAccessible(true);
        Object service = getter.invoke(null);
        service.getClass().getMethod("setNotificationsEnabledForPackage", String.class, int.class, boolean.class)
                .invoke(service, pkg, uid, true);
        System.out.println("OWN_LAB_NOTIFICATIONS_ENABLED");
    }
}

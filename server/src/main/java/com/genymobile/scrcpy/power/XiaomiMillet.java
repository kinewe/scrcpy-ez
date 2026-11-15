package com.genymobile.scrcpy.power;

import com.genymobile.scrcpy.util.Settings;
import com.genymobile.scrcpy.wrappers.ServiceManager;

import android.content.Intent;
import android.os.Build;

/** A downstream HyperOS exemption, not the private PowerKeeper UI/database profile. */
public final class XiaomiMillet {
    public static final String KEY = "MILLET_NO_RESTRICT_APP";

    private XiaomiMillet() { }

    public static boolean isCandidate() {
        return "xiaomi".equalsIgnoreCase(Build.MANUFACTURER) || "redmi".equalsIgnoreCase(Build.BRAND)
                || "poco".equalsIgnoreCase(Build.BRAND);
    }

    public static PackageListBackend backend() {
        return new PackageListBackend(new PackageListBackend.Value() {
            @Override
            public String read() throws Exception {
                // The existing settings wrapper explicitly supplies owner user 0.
                return Settings.getValue(Settings.TABLE_SYSTEM, KEY);
            }

            @Override
            public void write(String value) throws Exception {
                Settings.putValue(Settings.TABLE_SYSTEM, KEY, value);
            }
        });
    }

    public static int requestThaw(String pkg) throws ReflectiveOperationException {
        if (!PowerSaveLeaseStore.validPackage(pkg)) {
            throw new IllegalArgumentException("Invalid thaw package");
        }
        // This OEM receiver thaws the resolved package before handling option.
        // -1 deliberately requests no MiPush membership change. Compatibility
        // and actual recovery must be checked in device logs, not dispatch status.
        Intent intent = new Intent("com.xiaomi.mipush.REFRESH_WHITE_LIST_ACTION");
        intent.setPackage("android");
        intent.putExtra("pkgName", pkg);
        intent.putExtra("option", -1);
        return ServiceManager.getActivityManager().sendBroadcastAsUser(intent, 0);
    }
}

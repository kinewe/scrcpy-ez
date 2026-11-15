package com.genymobile.scrcpy.device;

import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.util.Ln;

import android.content.pm.ApplicationInfo;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.content.Intent;
import android.content.res.Configuration;
import android.os.Build;
import android.provider.Settings;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.lang.reflect.Method;
import java.util.List;
import java.util.Map;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Set;

/** Metadata only: obtaining this catalog never renders or compresses icons. */
public final class AppCatalog {
    private AppCatalog() { }

    public static void progress(String phase, int current, int total) {
        Ln.i("SCEZ_APP_PROGRESS:{\"phase\":\"" + phase + "\",\"current\":" + current + ",\"total\":" + total + "}");
    }

    private static Set<String> launchablePackages(PackageManager pm) {
        Set<String> result = new HashSet<>();
        for (String category : new String[]{Intent.CATEGORY_INFO, Intent.CATEGORY_LAUNCHER, Intent.CATEGORY_LEANBACK_LAUNCHER}) {
            try {
                List<ResolveInfo> activities = pm.queryIntentActivities(new Intent(Intent.ACTION_MAIN).addCategory(category), 0);
                if (activities != null) {
                    for (ResolveInfo activity : activities) {
                        if (activity.activityInfo != null) { result.add(activity.activityInfo.packageName); }
                    }
                }
            } catch (RuntimeException e) {
                // Missing/filtered vendor queries fall back to the per-package API.
            }
        }
        return result;
    }

    private static String property(String name) {
        try {
            Method get = Class.forName("android.os.SystemProperties").getMethod("get", String.class);
            return (String) get.invoke(null, name);
        } catch (Exception e) {
            return "";
        }
    }

    private static String secureSetting(String name) {
        try {
            String value = Settings.Secure.getString(FakeContext.get().getContentResolver(), name);
            return value == null ? "" : value;
        } catch (RuntimeException e) {
            return "";
        }
    }

    // Shell can inspect Android resource overlays. Older/vendor implementations may omit this service.
    private static JSONArray overlays(PackageManager pm) {
        JSONArray result = new JSONArray();
        try {
            Class<?> sm = Class.forName("android.os.ServiceManager");
            Object binder = sm.getMethod("getService", String.class).invoke(null, "overlay");
            Class<?> stub = Class.forName("android.content.om.IOverlayManager$Stub");
            Object manager = stub.getMethod("asInterface", android.os.IBinder.class).invoke(null, binder);
            Class<?> contract = Class.forName("android.content.om.IOverlayManager");
            Object all = contract.getMethod("getAllOverlays", int.class).invoke(manager, android.os.Process.myUid() / 100000);
            for (Object list : ((Map<?, ?>) all).values()) {
                for (Object overlay : (List<?>) list) {
                    Class<?> type = overlay.getClass();
                    if (!((Boolean) type.getMethod("isEnabled").invoke(overlay))) {
                        continue;
                    }
                    String pkg = (String) type.getField("packageName").get(overlay);
                    PackageInfo p = pm.getPackageInfo(pkg, 0);
                    JSONArray row = new JSONArray();
                    row.put(overlay.toString()).put(p.lastUpdateTime);
                    result.put(row);
                }
            }
        } catch (Exception e) {
            // The standard theme setting and build/locale/density signature remain available.
        }
        return result;
    }

    public static String serial() {
        String value = property("ro.serialno");
        if (value.isEmpty() || "unknown".equalsIgnoreCase(value)) { value = property("ro.boot.serialno"); }
        return value;
    }

    @SuppressWarnings("deprecation")
    public static String build() throws JSONException, PackageManager.NameNotFoundException {
        PackageManager pm = FakeContext.get().getPackageManager();
        JSONObject result = new JSONObject();
        result.put("serial", serial());
        // Avoid volatile orientation/display-size fields, which would dirty every icon on rotation.
        Configuration c = FakeContext.get().getResources().getConfiguration();
        String locale = Build.VERSION.SDK_INT >= 24 ? c.getLocales().toLanguageTags() : c.locale.toLanguageTag();
        result.put("config", Build.FINGERPRINT + "|" + locale + "|" + c.densityDpi + "|"
                + (c.uiMode & Configuration.UI_MODE_NIGHT_MASK) + "|"
                + secureSetting("theme_customization_overlay_packages"));
        // Do not request manifest metadata for every installed package. Package
        // versions and timestamps can be obtained in one bulk Binder request.
        List<ApplicationInfo> installed = pm.getInstalledApplications(0);
        progress("catalog", 0, installed.size());
        Map<String, PackageInfo> packages = new HashMap<>();
        try {
            for (PackageInfo p : pm.getInstalledPackages(0)) { packages.put(p.packageName, p); }
        } catch (RuntimeException e) {
            // Retain compatibility with PackageManager implementations without bulk metadata.
        }
        Set<String> launchable = launchablePackages(pm);
        JSONArray items = new JSONArray();
        JSONArray failed = new JSONArray();
        int current = 0;
        long lastProgress = System.nanoTime();
        for (ApplicationInfo info : installed) {
            try {
                if (!info.enabled || (!launchable.contains(info.packageName) && Device.getLaunchIntent(pm, info.packageName) == null)) {
                    continue;
                }
                PackageInfo p = packages.get(info.packageName);
                if (p == null) { p = pm.getPackageInfo(info.packageName, 0); }
                long version = Build.VERSION.SDK_INT >= 28 ? p.getLongVersionCode() : p.versionCode;
                String label;
                try { label = pm.getApplicationLabel(info).toString(); }
                catch (RuntimeException e) { label = info.packageName; }
                if (label.length() > 1024) { label = label.substring(0, 1024); }
                JSONObject item = new JSONObject();
                item.put("pkg", info.packageName);
                item.put("name", label);
                item.put("sys", (info.flags & ApplicationInfo.FLAG_SYSTEM) != 0);
                item.put("version", Math.max(0, version));
                item.put("versionName", p.versionName == null ? "" : p.versionName);
                item.put("updated", Math.max(0, p.lastUpdateTime));
                item.put("icon", info.icon);
                items.put(item);
            } catch (PackageManager.NameNotFoundException e) {
                // The package may have been removed during this snapshot.
            } catch (RuntimeException e) {
                failed.put(info.packageName);
                Ln.w("Could not read app metadata: " + info.packageName);
            } finally {
                current++;
                if (current % 8 == 0 || System.nanoTime() - lastProgress >= 250000000L) {
                    progress("catalog", current, installed.size());
                    lastProgress = System.nanoTime();
                }
            }
        }
        progress("catalog", installed.size(), installed.size());
        result.put("resources", overlays(pm));
        result.put("items", items);
        result.put("failed", failed);
        return result.toString();
    }
}

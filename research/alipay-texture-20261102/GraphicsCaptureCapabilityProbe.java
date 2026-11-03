package com.genymobile.scrcpy;

import android.content.pm.ApplicationInfo;
import android.content.pm.PackageManager;
import android.os.Bundle;
import org.json.JSONArray;
import org.json.JSONObject;

/** Read package declarations only; no launch, injection or settings changes. */
public final class GraphicsCaptureCapabilityProbe {
    public static void main(String[] args) throws Exception {
        Thread.setDefaultUncaughtExceptionHandler((t, e) -> {
            e.printStackTrace(System.out); System.out.flush(); Runtime.getRuntime().halt(1);
        });
        if (android.os.Process.myUid() != 2000) throw new SecurityException();
        Thread deadline = new Thread(() -> {
            try { Thread.sleep(20000); } catch (InterruptedException e) { return; }
            Runtime.getRuntime().halt(124);
        }, "rc25-capability-deadline");
        deadline.setDaemon(true); deadline.start();
        Class<?> at = Class.forName("android.app.ActivityThread");
        Object pm = at.getMethod("getPackageManager").invoke(null);
        Class<?> ipc = Class.forName("android.content.pm.IPackageManager");
        java.lang.reflect.Method get = ipc.getMethod("getApplicationInfo", String.class, long.class, int.class);
        JSONArray apps = new JSONArray();
        for (String pkg : args) {
            JSONObject item = new JSONObject().put("package", pkg);
            ApplicationInfo ai = (ApplicationInfo) get.invoke(pm, pkg, (long) PackageManager.GET_META_DATA, 0);
            if (ai == null) { apps.put(item.put("installed", false)); continue; }
            item.put("installed", true).put("targetSdk", ai.targetSdkVersion)
                .put("debuggable", (ai.flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0);
            Bundle data = ai.metaData;
            for (String key : new String[]{"com.android.graphics.injectLayers.enable", "com.android.graphics.developerdriver.enable"}) {
                item.put(key, data != null && data.containsKey(key) ? data.get(key) : JSONObject.NULL);
            }
            apps.put(item);
        }
        System.out.println("GRAPHICS_CAPABILITIES_JSON " + new JSONObject()
            .put("sdk", android.os.Build.VERSION.SDK_INT).put("model", android.os.Build.MODEL).put("apps", apps));
        System.exit(0);
    }
}

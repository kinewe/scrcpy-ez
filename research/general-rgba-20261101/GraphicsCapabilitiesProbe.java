package com.genymobile.scrcpy;

import android.os.IBinder;
import android.os.Build;
import java.lang.reflect.*;
import java.util.*;

/** Read-only framework inventory. Does not capture or change any application. */
public final class GraphicsCapabilitiesProbe {
    public static void main(String[] args) throws Exception {
        if (android.os.Process.myUid() != 2000) throw new IllegalStateException("shell only");
        System.out.println("DEVICE sdk=" + Build.VERSION.SDK_INT + " model=" + Build.MODEL);
        String[] names = {"android.window.ScreenCapture", "android.window.ScreenCapture$CaptureArgs",
            "android.window.ScreenCapture$CaptureArgs$Builder", "android.window.ScreenCapture$LayerCaptureArgs",
            "android.window.ScreenCapture$LayerCaptureArgs$Builder", "android.view.SurfaceControl",
            "android.view.SurfaceControl$Transaction", "android.view.SurfaceView", "android.view.IWindow",
            "android.gui.ISurfaceComposer", "android.view.ViewDebug"};
        for (String name : names) try {
            Class<?> c = Class.forName(name);
            List<String> found = new ArrayList<>();
            for (Method m : c.getDeclaredMethods())
                if (m.getName().matches("(?i).*(capture|mirror|layer|handle|child|exclude|buffer|screenshot|renderingcommands).*"))
                    found.add(m.toString());
            if (name.contains("CaptureArgs")) for (Field f : c.getDeclaredFields()) found.add("FIELD " + f.toString());
            if (name.equals("android.window.ScreenCapture")) for (Field f : c.getDeclaredFields())
                if (Modifier.isStatic(f.getModifiers()) && f.getType() == int.class) {
                    f.setAccessible(true); found.add("CONSTANT " + f.getName() + "=" + f.getInt(null));
                }
            Collections.sort(found);
            System.out.println("CLASS " + name);
            for (String s : found) System.out.println("  " + s);
        } catch (Throwable e) { System.out.println("UNAVAILABLE " + name + " " + e); }
        Method get = Class.forName("android.os.ServiceManager").getMethod("getService", String.class);
        for (String name : new String[]{"SurfaceFlinger", "SurfaceFlingerAIDL"}) {
            IBinder b = (IBinder) get.invoke(null, name);
            System.out.println("SERVICE " + name + " descriptor=" + (b == null ? "missing" : b.getInterfaceDescriptor()));
        }
        System.out.println("READ_ONLY_DONE");
    }
}

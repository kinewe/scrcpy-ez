package com.genymobile.scrcpy.notification;

import android.annotation.TargetApi;
import android.app.ActivityOptions;
import android.app.PendingIntent;
import android.content.ComponentName;
import android.content.Intent;
import android.content.pm.ResolveInfo;
import android.os.Build;
import android.os.SystemClock;

import com.genymobile.scrcpy.FakeContext;

import java.lang.reflect.Method;
import java.util.ArrayList;
import java.util.List;
import java.util.HashMap;
import java.util.Map;
import java.util.LinkedHashMap;

/** Keeps the original phone-side capability and task stack; never starts an app home page. */
@TargetApi(26)
final class NotificationActivityLauncher {
    private final Map<String, Map<Integer, String>> routes = new LinkedHashMap<>();

    NotificationActivityLauncher() { }

    static String targetPackage(PendingIntent action, String fallback) {
        try {
            Method method = PendingIntent.class.getDeclaredMethod("getIntent");
            method.setAccessible(true);
            Intent intent = (Intent) method.invoke(action);
            if (intent.getComponent() != null) {
                return intent.getComponent().getPackageName();
            }
            ResolveInfo info = FakeContext.get().getPackageManager().resolveActivity(intent, 0);
            if (info != null && info.activityInfo != null) {
                return info.activityInfo.packageName;
            }
        } catch (Exception ignored) {
            // A delegated creator (e.g. Xiaomi push) is not the destination app.
        }
        return fallback;
    }

    boolean send(PendingIntent action, String pkg, int displayId) throws Exception {
        if (Build.VERSION.SDK_INT < 26) {
            return false;
        }
        Tasks tasks = new Tasks();
        List<Integer> moved = new ArrayList<>();
        Map<Integer, Integer> before = new HashMap<>();
        Map<Integer, String> destinations = routes.get(pkg);
        if (destinations == null) {
            if (routes.size() >= 128) { routes.remove(routes.keySet().iterator().next()); }
            destinations = new HashMap<>();
            routes.put(pkg, destinations);
        }
        boolean success = false;
        try {
            // A notification may forward into a singleTask/detail stack already
            // on the primary display. Move that app's existing stack first.
            for (Root root : tasks.roots()) {
                before.put(root.id, root.display);
                if (root.display == 0 && root.belongsTo(pkg, destinations)) {
                    tasks.move(root.id, displayId);
                    moved.add(root.id);
                }
            }
            ActivityOptions options = ActivityOptions.makeBasic().setLaunchDisplayId(displayId);
            if (Build.VERSION.SDK_INT >= 36) {
                // This is only called for an explicit, session-bound desktop click.
                options.setPendingIntentBackgroundActivityStartMode(ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_ALLOW_ALWAYS);
            } else if (Build.VERSION.SDK_INT >= 34) {
                options.setPendingIntentBackgroundActivityStartMode(ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_ALLOWED);
            }
            action.send(FakeContext.get(), 0, null, null, null, null, options.toBundle());

            long until = SystemClock.uptimeMillis() + 3000;
            long earliest = SystemClock.uptimeMillis() + 700;
            do {
                boolean present = false;
                for (Root root : tasks.roots()) {
                    if (root.display == displayId && root.applicationRoot()
                            && (root.belongsTo(pkg, destinations) || !Integer.valueOf(displayId).equals(before.get(root.id)))) {
                        // Original actions may legitimately delegate to another
                        // activity (e.g. MIUI's Settings → Securitycenter detail).
                        // Accept an app root the OS brought onto our display;
                        // never move that foreign app from the primary display.
                        present = true;
                        if (destinations.size() < 8 && root.belongsTo(root.base)) {
                            destinations.put(root.id, root.base);
                        }
                    }
                    if (!root.belongsTo(pkg, destinations)) {
                        continue;
                    }
                    // Some intermediate activities forward back into the main
                    // task despite launchDisplayId. Only this app's standard,
                    // unmixed primary-display roots may follow the click.
                    if (root.display == 0) {
                        tasks.move(root.id, displayId);
                        if (!moved.contains(root.id)) {
                            moved.add(root.id);
                        }
                    } else if (root.display == displayId) {
                        present = true;
                    }
                }
                if (present && SystemClock.uptimeMillis() >= earliest) {
                    success = true;
                    return true;
                }
                Thread.sleep(100);
            } while (SystemClock.uptimeMillis() < until);
            return false;
        } finally {
            if (!success) {
                // Restore only the exact app roots we moved and still own on
                // this display. Do not close a user's existing application.
                for (Root root : tasks.roots()) {
                    if (moved.contains(root.id) && root.display == displayId && root.belongsTo(pkg, destinations)) {
                        try { tasks.move(root.id, 0); } catch (Exception ignored) { /* Best effort, no force-stop. */ }
                    }
                }
            }
        }
    }

    private static final class Root {
        final int id;
        final int display;
        final String top;
        final String base;
        final String[] children;
        final int activityType;

        Root(Object value) throws Exception {
            Class<?> type = value.getClass();
            int task;
            try { task = type.getField("taskId").getInt(value); }
            catch (NoSuchFieldException ignored) { task = type.getField("stackId").getInt(value); }
            id = task;
            display = type.getField("displayId").getInt(value);
            ComponentName topActivity = (ComponentName) type.getField("topActivity").get(value);
            ComponentName baseActivity = (ComponentName) type.getField("baseActivity").get(value);
            top = topActivity == null ? "" : topActivity.getPackageName();
            base = baseActivity == null ? "" : baseActivity.getPackageName();
            children = (String[]) type.getField("childTaskNames").get(value);
            Object configuration = type.getField("configuration").get(value);
            Object window = configuration.getClass().getField("windowConfiguration").get(configuration);
            activityType = (Integer) window.getClass().getMethod("getActivityType").invoke(window);
        }

        boolean belongsTo(String pkg) {
            return NotificationTaskPolicy.belongsTo(pkg, top, base, children, activityType);
        }

        boolean belongsTo(String pkg, Map<Integer, String> destinations) {
            if (belongsTo(pkg)) { return true; }
            // Remember only the exact delegated task produced by an earlier
            // click, never every primary-display task in its destination app.
            return belongsTo(destinations.get(id));
        }

        boolean applicationRoot() {
            return activityType == 1 && !top.isEmpty();
        }
    }

    private static final class Tasks {
        final Object service;
        final Method list;
        final Method move;

        Tasks() throws Exception {
            Class<?> type = Class.forName(Build.VERSION.SDK_INT >= 29 ? "android.app.ActivityTaskManager" : "android.app.ActivityManager");
            Method getter = type.getDeclaredMethod("getService");
            getter.setAccessible(true);
            service = getter.invoke(null);
            list = service.getClass().getMethod(Build.VERSION.SDK_INT >= 31 ? "getAllRootTaskInfos" : "getAllStackInfos");
            move = service.getClass().getMethod(Build.VERSION.SDK_INT >= 31 ? "moveRootTaskToDisplay" : "moveStackToDisplay", int.class, int.class);
        }

        List<Root> roots() throws Exception {
            List<Root> out = new ArrayList<>();
            for (Object value : (List<?>) list.invoke(service)) {
                out.add(new Root(value));
            }
            return out;
        }

        void move(int task, int display) throws Exception { move.invoke(service, task, display); }
    }
}

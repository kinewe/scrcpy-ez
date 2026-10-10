package com.genymobile.scrcpy.device;

import com.genymobile.scrcpy.display.DisplayInfo;
import com.genymobile.scrcpy.util.Ln;
import com.genymobile.scrcpy.video.VirtualDisplayTaskRestorer;
import com.genymobile.scrcpy.wrappers.ServiceManager;

import android.content.ComponentName;
import android.os.Build;
import android.os.IBinder;
import android.os.SystemClock;

import java.lang.reflect.Method;
import java.util.ArrayList;
import java.util.List;

/** Explicit application-window launch: preserve a safe task before trying its launcher. */
public final class AppTaskLauncher {
    private AppTaskLauncher() { }

    public enum Result {
        REUSED("reused"), STARTED("started"), REUSE_FAILED("reuse-failed"), LAUNCH_FAILED("launch-failed"),
        LAYOUT_INCOMPATIBLE("layout-incompatible"), IN_USE("in-use");

        private final String code;
        Result(String code) { this.code = code; }
        public String code() { return code; }
    }

    static final class Root {
        final int id;
        final int display;
        final int activityType;
        final int mode;
        final String base;
        final String top;
        final String[] children;
        final Object identity;
        final boolean visible;

        Root(int id, int display, int activityType, int mode, String base, String top, String[] children, Object identity, boolean visible) {
            this.id = id;
            this.display = display;
            this.activityType = activityType;
            this.mode = mode;
            this.base = base;
            this.top = top;
            this.children = children;
            this.identity = identity;
            this.visible = visible;
        }

        boolean related(String pkg) {
            if (pkg.equals(base) || pkg.equals(top)) { return true; }
            if (activityType == 1 && children != null) {
                for (String child : children) {
                    if (child != null && (child.equals(pkg) || child.startsWith(pkg + "/"))) { return true; }
                }
            }
            return false;
        }

        boolean safe(String pkg) {
            if (identity == null || activityType != 1 || (mode != 0 && mode != 1) || !pkg.equals(base) || !pkg.equals(top)) {
                return false;
            }
            if (children != null) {
                for (String child : children) {
                    if (child == null || !(child.equals(pkg) || child.startsWith(pkg + "/"))) { return false; }
                }
            }
            return true;
        }

        boolean same(Root other) {
            return other != null && id == other.id && identity != null && identity.equals(other.identity);
        }
    }

    interface Tasks {
        List<Root> roots() throws Exception;
        void move(Root root, int display) throws Exception;
        void restore(Root root, int display) throws Exception;
        void launch() throws Exception;
        long now();
        void pause() throws InterruptedException;
        default void rejected(String reason) { }
        default boolean layoutCompatible(Root root, int display) throws Exception { return true; }
    }

    public static Result start(String pkg, int display, boolean flex) {
        // Token identity is required to recheck ownership safely. Older systems
        // can still use the explicitly requested fresh-start path.
        if (Build.VERSION.SDK_INT < 31 || display <= 0) { return Result.REUSE_FAILED; }
        try {
            return start(pkg, display, new AndroidTasks(pkg, display, flex));
        } catch (Exception ignored) {
            Ln.i("SCRCPY_EZ_APP_REUSE=task-query-unavailable");
            return Result.REUSE_FAILED;
        }
    }

    static Result start(String pkg, int display, Tasks tasks) throws Exception {
        if (pkg == null || pkg.isEmpty() || display <= 0) { return Result.REUSE_FAILED; }
        List<Root> candidates = new ArrayList<>();
        int onTarget = 0;
        for (Root root : tasks.roots()) {
            if (root.display == display && root.activityType == 1 && !root.related(pkg)) { return reject(tasks, "display-occupied"); }
            if (!root.related(pkg)) { continue; }
            if (!root.safe(pkg) || (root.display != 0 && root.display != display)) {
                return reject(tasks, "unsafe-root"); // Never steal another display or a mixed/split task.
            }
            if (root.display == display) { onTarget++; } else { candidates.add(root); }
        }
        if (onTarget > 0) { return onTarget == 1 && candidates.isEmpty() ? Result.REUSED : Result.REUSE_FAILED; }
        if (candidates.isEmpty()) {
            // Recheck before cold launch: a task may have appeared during startup.
            for (Root root : tasks.roots()) {
                if (root.related(pkg) || (root.display == display && root.activityType == 1)) { return reject(tasks, "task-appeared"); }
            }
            try {
                tasks.launch(); // No existing page to preserve: normal launcher, never force-stop.
                long until = tasks.now() + 2500;
                do {
                    for (Root root : tasks.roots()) {
                        // A launcher may legitimately delegate to a system app.
                        if (root.display == display && root.activityType == 1 && root.top != null && !root.top.isEmpty()) {
                            return Result.STARTED;
                        }
                    }
                    tasks.pause();
                } while (tasks.now() < until);
            } catch (InterruptedException interrupted) {
                Thread.currentThread().interrupt();
            } catch (Exception ignored) { /* Report launch failure without restarting. */ }
            return Result.LAUNCH_FAILED;
        }
        Root selected = null;
        if (candidates.size() == 1) {
            selected = candidates.get(0);
        } else {
            for (Root root : candidates) {
                if (!root.visible) { continue; }
                if (selected != null) { return reject(tasks, "multiple-visible-tasks"); }
                selected = root;
            }
        }
        if (selected == null) { return reject(tasks, "ambiguous-tasks"); }

        // Task IDs may be recycled while the window starts; compare Binder identity.
        Root current = find(tasks.roots(), selected);
        if (current == null || current.display != 0 || !current.safe(pkg)) { return reject(tasks, "task-changed"); }
        // Ordinary app casting must not take away a page currently used on the phone.
        // Explicit process restart remains a separate, confirmed user action.
        if (current.visible) {
            tasks.rejected("app-in-use");
            return Result.IN_USE;
        }
        if (!tasks.layoutCompatible(current, display)) {
            tasks.rejected("layout-change");
            return Result.LAYOUT_INCOMPATIBLE;
        }
        try {
            tasks.move(current, display);
            long until = tasks.now() + 1500;
            do {
                Root live = find(tasks.roots(), selected);
                if (live == null || !live.safe(pkg)) { break; }
                if (live.display == display) { return Result.REUSED; }
                if (live.display != 0) { break; }
                tasks.pause();
            } while (tasks.now() < until);
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
        } catch (Exception ignored) {
            // Failed reuse never falls through to a launcher or force-stop.
        }
        Root live = find(tasks.roots(), selected);
        if (live != null && live.display == display && live.safe(pkg)) {
            tasks.restore(live, display);
        }
        return reject(tasks, "move-unconfirmed");
    }

    private static Result reject(Tasks tasks, String reason) {
        tasks.rejected(reason);
        return Result.REUSE_FAILED;
    }

    private static Root find(List<Root> roots, Root selected) {
        for (Root root : roots) { if (selected.same(root)) { return root; } }
        return null;
    }

    static boolean compatibleLayout(String pkg, boolean flex, DisplayInfo source, DisplayInfo target) {
        if (!"com.tencent.mm".equals(pkg)) { return true; }
        // WeChat's retained process does not reliably refresh display metrics.
        // A resize-capable window can change them again after startup.
        return !flex && source != null && target != null && source.getDpi() > 0
                && source.getDpi() == target.getDpi() && source.getRotation() == target.getRotation()
                && source.getSize().equals(target.getSize());
    }

    private static final class AndroidTasks implements Tasks {
        private final String pkg;
        private final int display;
        private final boolean flex;
        private final Object service;
        private final Method list;
        private final Method move;

        AndroidTasks(String pkg, int display, boolean flex) throws Exception {
            this.pkg = pkg;
            this.display = display;
            this.flex = flex;
            Class<?> type = Class.forName("android.app.ActivityTaskManager");
            Method getter = type.getDeclaredMethod("getService");
            getter.setAccessible(true);
            service = getter.invoke(null);
            list = service.getClass().getMethod("getAllRootTaskInfos");
            move = service.getClass().getMethod("moveRootTaskToDisplay", int.class, int.class);
        }

        @Override public List<Root> roots() throws Exception {
            List<Root> out = new ArrayList<>();
            for (Object info : (List<?>) list.invoke(service)) {
                Class<?> type = info.getClass();
                ComponentName base = (ComponentName) type.getField("baseActivity").get(info);
                ComponentName top = (ComponentName) type.getField("topActivity").get(info);
                Object token = type.getField("token").get(info);
                IBinder identity = token == null ? null : (IBinder) token.getClass().getMethod("asBinder").invoke(token);
                Object config = type.getField("configuration").get(info);
                Object window = config.getClass().getField("windowConfiguration").get(config);
                boolean visible = false;
                try {
                    visible = type.getField("visible").getBoolean(info);
                } catch (NoSuchFieldException ignored) {
                    try { visible = type.getField("isVisible").getBoolean(info); } catch (NoSuchFieldException unavailable) { /* Unique task only. */ }
                }
                out.add(new Root(type.getField("taskId").getInt(info), type.getField("displayId").getInt(info),
                        (Integer) window.getClass().getMethod("getActivityType").invoke(window),
                        (Integer) window.getClass().getMethod("getWindowingMode").invoke(window),
                        base == null ? "" : base.getPackageName(), top == null ? "" : top.getPackageName(),
                        (String[]) type.getField("childTaskNames").get(info), identity, visible));
            }
            return out;
        }

        @Override public void move(Root root, int target) throws Exception { move.invoke(service, root.id, target); }
        @Override public void restore(Root root, int target) { VirtualDisplayTaskRestorer.restoreTaskToBackground(target, (IBinder) root.identity); }
        @Override public void launch() { Device.startApp(pkg, display, false); }
        @Override public long now() { return SystemClock.uptimeMillis(); }
        @Override public void pause() throws InterruptedException { Thread.sleep(50); }
        @Override public void rejected(String reason) { Ln.i("SCRCPY_EZ_APP_REUSE=" + reason); }
        @Override public boolean layoutCompatible(Root root, int target) {
            if (!"com.tencent.mm".equals(pkg)) { return true; }
            return compatibleLayout(pkg, flex, ServiceManager.getDisplayManager().getDisplayInfo(root.display),
                    ServiceManager.getDisplayManager().getDisplayInfo(target));
        }
    }
}

package com.genymobile.scrcpy.video;

import com.genymobile.scrcpy.util.Ln;

import android.os.Build;
import android.os.IBinder;

import java.lang.reflect.Method;
import java.util.ArrayList;
import java.util.List;

/** Returns retained application stacks to the primary display without presenting them there. */
public final class VirtualDisplayTaskRestorer {
    private VirtualDisplayTaskRestorer() { }

    static final class Root {
        final int id;
        final int display;
        final int activityType;
        final int windowingMode;
        final IBinder token;
        final Object containerToken;

        Root(int id, int display, int activityType, int windowingMode, IBinder token) {
            this(id, display, activityType, windowingMode, token, token);
        }

        Root(int id, int display, int activityType, int windowingMode, IBinder token, Object containerToken) {
            this.id = id;
            this.display = display;
            this.activityType = activityType;
            this.windowingMode = windowingMode;
            this.token = token;
            this.containerToken = containerToken;
        }

        boolean ownedBy(int displayId) {
            return displayId > 0 && display == displayId && activityType == 1
                    && (windowingMode == 0 || windowingMode == 1) && token != null;
        }
    }

    interface Tasks {
        List<Root> roots() throws Exception;
        void hide(List<Root> roots) throws Exception;
        void move(Root root) throws Exception;
        void restore(List<Root> hidden) throws Exception;
    }

    public static void restoreToBackground(int displayId) {
        if (Build.VERSION.SDK_INT < 31 || displayId <= 0) {
            return;
        }
        try {
            restoreToBackground(displayId, new AndroidTasks());
        } catch (Exception e) {
            // Never log a task description, app detail, Intent or notification.
            Ln.w("Retained display content could not be returned to the background");
        }
    }

    static void restoreToBackground(int displayId, Tasks tasks) throws Exception {
        restoreToBackground(displayId, tasks, null);
    }

    public static void restoreTaskToBackground(int displayId, IBinder identity) {
        if (Build.VERSION.SDK_INT < 31 || displayId <= 0 || identity == null) { return; }
        try {
            restoreToBackground(displayId, new AndroidTasks(), identity);
        } catch (Exception ignored) {
            Ln.w("Retained application task could not be returned to the background");
        }
    }

    static void restoreToBackground(int displayId, Tasks tasks, IBinder identity) throws Exception {
        List<Root> hidden = new ArrayList<>();
        for (Root root : tasks.roots()) {
            if (root.ownedBy(displayId) && (identity == null || identity.equals(root.token))) {
                hidden.add(root);
            }
        }
        if (hidden.isEmpty()) {
            return;
        }
        try {
            // Hide before moving: moveRootTaskToDisplay always requests onTop.
            // No unrelated root, primary-display task or task organizer is changed.
            tasks.hide(hidden);
            for (Root selected : hidden) {
                for (Root current : tasks.roots()) {
                    if (current.id == selected.id && current.ownedBy(displayId)
                            && (current.token == selected.token || current.token.equals(selected.token))) {
                        tasks.move(current);
                        break;
                    }
                }
            }
        } finally {
            // Unhide even if a move fails. Reordering and unhiding share one
            // window-container transaction, so the phone's foreground remains.
            try {
                tasks.restore(hidden);
            } catch (Exception first) {
                tasks.restore(hidden);
            }
        }
    }

    private static final class AndroidTasks implements Tasks {
        private final Object activityService;
        private final Method list;
        private final Method move;
        private final Object organizer;
        private final Class<?> transactionType;
        private final Method apply;
        private final Method setHidden;
        private final Method reorder;

        AndroidTasks() throws Exception {
            Class<?> activityManager = Class.forName("android.app.ActivityTaskManager");
            Method getter = activityManager.getDeclaredMethod("getService");
            getter.setAccessible(true);
            activityService = getter.invoke(null);
            list = activityService.getClass().getMethod("getAllRootTaskInfos");
            move = activityService.getClass().getMethod("moveRootTaskToDisplay", int.class, int.class);
            transactionType = Class.forName("android.window.WindowContainerTransaction");
            Class<?> organizerType = Class.forName("android.window.WindowOrganizer");
            organizer = organizerType.getConstructor().newInstance();
            apply = organizerType.getMethod("applyTransaction", transactionType);
            Class<?> containerTokenType = Class.forName("android.window.WindowContainerToken");
            setHidden = transactionType.getMethod("setHidden", containerTokenType, boolean.class);
            reorder = transactionType.getMethod("reorder", containerTokenType, boolean.class);
        }

        @Override
        public List<Root> roots() throws Exception {
            List<Root> roots = new ArrayList<>();
            for (Object value : (List<?>) list.invoke(activityService)) {
                Class<?> type = value.getClass();
                Object configuration = type.getField("configuration").get(value);
                Object window = configuration.getClass().getField("windowConfiguration").get(configuration);
                int activityType = (Integer) window.getClass().getMethod("getActivityType").invoke(window);
                int mode = (Integer) window.getClass().getMethod("getWindowingMode").invoke(window);
                Object token = type.getField("token").get(value);
                IBinder binder = token == null ? null : (IBinder) token.getClass().getMethod("asBinder").invoke(token);
                roots.add(new Root(type.getField("taskId").getInt(value), type.getField("displayId").getInt(value),
                        activityType, mode, binder, token));
            }
            return roots;
        }

        @Override
        public void hide(List<Root> roots) throws Exception {
            Object transaction = transactionType.getConstructor().newInstance();
            for (Root root : roots) {
                setHidden.invoke(transaction, root.containerToken, true);
            }
            apply.invoke(organizer, transaction);
        }

        @Override
        public void move(Root root) throws Exception {
            move.invoke(activityService, root.id, 0);
        }

        @Override
        public void restore(List<Root> hidden) throws Exception {
            List<Root> current;
            Exception snapshotFailure = null;
            try {
                current = roots();
            } catch (Exception e) {
                current = new ArrayList<>();
                snapshotFailure = e;
            }
            Object transaction = transactionType.getConstructor().newInstance();
            for (Root root : hidden) {
                for (Root live : current) {
                    if (live.id == root.id && live.token.equals(root.token)) {
                        if (live.display == 0) {
                            reorder.invoke(transaction, root.containerToken, false);
                        }
                        break;
                    }
                }
                // Restoring visibility never depends on another successful
                // task query. Detached tokens are ignored by the OS.
                setHidden.invoke(transaction, root.containerToken, false);
            }
            apply.invoke(organizer, transaction);
            if (snapshotFailure != null) {
                throw snapshotFailure;
            }
        }
    }
}

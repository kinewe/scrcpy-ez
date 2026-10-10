package com.genymobile.scrcpy.device;

import org.junit.Test;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

import static org.junit.Assert.assertEquals;

public class AppTaskLauncherTest {
    private static final String PKG = "app.test";
    private static final int DISPLAY = 40;

    private static AppTaskLauncher.Root root(int id, int display, Object token, boolean visible) {
        return new AppTaskLauncher.Root(id, display, 1, 1, PKG, PKG, new String[] {PKG + "/Detail"}, token, visible);
    }

    private static class Tasks implements AppTaskLauncher.Tasks {
        List<AppTaskLauncher.Root> roots = new ArrayList<>();
        List<Integer> moved = new ArrayList<>(), restored = new ArrayList<>();
        int launched, reads;
        long time;
        Runnable onRead, onMove, onLaunch;
        boolean failMove, ignoreMove;

        Tasks(AppTaskLauncher.Root... initial) { roots.addAll(Arrays.asList(initial)); }
        @Override public List<AppTaskLauncher.Root> roots() {
            reads++;
            if (onRead != null) { onRead.run(); }
            return new ArrayList<>(roots);
        }
        @Override public void move(AppTaskLauncher.Root selected, int display) throws Exception {
            moved.add(selected.id);
            if (!ignoreMove) {
                roots.removeIf(r -> r.same(selected));
                roots.add(root(selected.id, display, selected.identity, selected.visible));
            }
            if (onMove != null) { onMove.run(); }
            if (failMove) { throw new Exception("move"); }
        }
        @Override public void restore(AppTaskLauncher.Root selected, int display) { restored.add(selected.id); }
        @Override public void launch() { launched++; if (onLaunch != null) { onLaunch.run(); } }
        @Override public long now() { return time; }
        @Override public void pause() { time += 50; }
    }

    @Test public void movesExistingPageWithoutCallingLauncher() throws Exception {
        Object token = new Object();
        Tasks tasks = new Tasks(root(7, 0, token, false));
        assertEquals(AppTaskLauncher.Result.REUSED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
        assertEquals(Arrays.asList(7), tasks.moved);
        assertEquals(0, tasks.launched);
        assertEquals(token, tasks.roots.get(0).identity);
    }

    @Test public void coldLaunchIsNormalAndVerifiedOnNewDisplay() throws Exception {
        Tasks tasks = new Tasks();
        tasks.onLaunch = () -> tasks.roots.add(root(8, DISPLAY, new Object(), true));
        assertEquals(AppTaskLauncher.Result.STARTED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
        assertEquals(1, tasks.launched);
        assertEquals(0, tasks.moved.size());
    }

    @Test public void coldLaunchFailureDoesNotLoopOrForceRestart() throws Exception {
        Tasks tasks = new Tasks();
        assertEquals(AppTaskLauncher.Result.LAUNCH_FAILED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
        assertEquals(1, tasks.launched);
        assertEquals(2500, tasks.time);
    }

    @Test public void rejectsMixedSplitHomeForeignAndUnknownIdentity() throws Exception {
        Object token = new Object();
        for (AppTaskLauncher.Root unsafe : Arrays.asList(
                new AppTaskLauncher.Root(1, 0, 1, 1, PKG, "other", null, token, true),
                new AppTaskLauncher.Root(1, 0, 1, 1, PKG, PKG, new String[] {"other/Detail"}, token, true),
                new AppTaskLauncher.Root(1, 0, 1, 6, PKG, PKG, null, token, true),
                new AppTaskLauncher.Root(1, 0, 2, 1, PKG, PKG, null, token, true),
                root(1, 99, token, true), root(1, 0, null, true))) {
            Tasks tasks = new Tasks(unsafe);
            assertEquals(AppTaskLauncher.Result.REUSE_FAILED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
            assertEquals(0, tasks.launched + tasks.moved.size());
        }
    }

    @Test public void visibleAndAmbiguousTasksStayUntouched() throws Exception {
        Tasks tasks = new Tasks(root(1, 0, new Object(), false), root(2, 0, new Object(), true));
        assertEquals(AppTaskLauncher.Result.IN_USE, AppTaskLauncher.start(PKG, DISPLAY, tasks));
        assertEquals(0, tasks.launched + tasks.moved.size());
        for (boolean visible : new boolean[] {false, true}) {
            tasks = new Tasks(root(1, 0, new Object(), visible), root(2, 0, new Object(), visible));
            assertEquals(AppTaskLauncher.Result.REUSE_FAILED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
            assertEquals(0, tasks.launched + tasks.moved.size());
        }
    }

    @Test public void recycledTaskIdIsNeverMoved() throws Exception {
        Tasks tasks = new Tasks(root(1, 0, new Object(), true));
        tasks.onRead = () -> { if (tasks.reads == 2) { tasks.roots.set(0, root(1, 0, new Object(), true)); } };
        assertEquals(AppTaskLauncher.Result.REUSE_FAILED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
        assertEquals(0, tasks.launched + tasks.moved.size());
    }

    @Test public void newlyAppearingTaskPreventsColdLauncherReset() throws Exception {
        Tasks tasks = new Tasks();
        tasks.onRead = () -> { if (tasks.reads == 2) { tasks.roots.add(root(1, 0, new Object(), true)); } };
        assertEquals(AppTaskLauncher.Result.REUSE_FAILED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
        assertEquals(0, tasks.launched + tasks.moved.size());
    }

    @Test public void partiallyMovedTaskIsReturnedByIdentityOnly() throws Exception {
        Tasks tasks = new Tasks(root(1, 0, new Object(), false));
        tasks.failMove = true;
        tasks.onMove = () -> tasks.roots.add(root(2, DISPLAY, new Object(), false));
        assertEquals(AppTaskLauncher.Result.REUSE_FAILED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
        assertEquals(Arrays.asList(1), tasks.restored);
        assertEquals(0, tasks.launched);
    }

    @Test public void replacedTaskAfterMoveIsNeverRestored() throws Exception {
        Tasks tasks = new Tasks(root(1, 0, new Object(), false));
        tasks.failMove = true;
        tasks.onMove = () -> tasks.roots.set(0, root(1, DISPLAY, new Object(), true));
        assertEquals(AppTaskLauncher.Result.REUSE_FAILED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
        assertEquals(0, tasks.restored.size());
    }

    @Test public void moveTimeoutNeverFallsBackToLauncher() throws Exception {
        Tasks tasks = new Tasks(root(1, 0, new Object(), false));
        tasks.ignoreMove = true;
        assertEquals(AppTaskLauncher.Result.REUSE_FAILED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
        assertEquals(1500, tasks.time);
        assertEquals(0, tasks.launched);
    }

    @Test public void refusesOccupiedDisplayAndConflictingRelatedRoots() throws Exception {
        Tasks tasks = new Tasks(new AppTaskLauncher.Root(9, DISPLAY, 1, 1, "other", "other", null, new Object(), true));
        assertEquals(AppTaskLauncher.Result.REUSE_FAILED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
        assertEquals(0, tasks.launched);
        tasks = new Tasks(root(1, DISPLAY, new Object(), true), root(2, 99, new Object(), false));
        assertEquals(AppTaskLauncher.Result.REUSE_FAILED, AppTaskLauncher.start(PKG, DISPLAY, tasks));
    }

    @Test public void foregroundProtectionAppliesToEveryPackage() throws Exception {
        for (String pkg : Arrays.asList("com.tencent.mm", "com.android.settings", "app.other")) {
            Tasks tasks = new Tasks(new AppTaskLauncher.Root(1, 0, 1, 1, pkg, pkg, null, new Object(), true));
            assertEquals(AppTaskLauncher.Result.IN_USE, AppTaskLauncher.start(pkg, DISPLAY, tasks));
            assertEquals(0, tasks.launched + tasks.moved.size() + tasks.restored.size());
        }
    }

    @Test public void backgroundTaskBecomingVisibleDuringStartupIsProtected() throws Exception {
        Object token = new Object();
        Tasks tasks = new Tasks(root(1, 0, token, false));
        tasks.onRead = () -> { if (tasks.reads == 2) { tasks.roots.set(0, root(1, 0, token, true)); } };
        assertEquals(AppTaskLauncher.Result.IN_USE, AppTaskLauncher.start(PKG, DISPLAY, tasks));
        assertEquals(0, tasks.launched + tasks.moved.size() + tasks.restored.size());
    }
}

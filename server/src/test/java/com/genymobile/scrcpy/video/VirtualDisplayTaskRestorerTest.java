package com.genymobile.scrcpy.video;

import android.os.IBinder;

import org.junit.Test;

import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.fail;

public class VirtualDisplayTaskRestorerTest {
    @Test public void failedMigrationRestoresOnlyItsOwnToken() throws Exception {
        VirtualDisplayTaskRestorer.Root selected = root(1, 22, 1, 1);
        Tasks tasks = new Tasks(selected, root(2, 22, 1, 1), root(3, 23, 1, 1));
        VirtualDisplayTaskRestorer.restoreToBackground(22, tasks, selected.token);
        assertEquals(Arrays.asList(1), tasks.hidden);
        assertEquals(Arrays.asList(1), tasks.moved);
        assertEquals(Arrays.asList(1), tasks.restored);
    }
    private static IBinder token() {
        return (IBinder) Proxy.newProxyInstance(IBinder.class.getClassLoader(), new Class<?>[] {IBinder.class},
                (proxy, method, args) -> "equals".equals(method.getName()) ? proxy == args[0] : null);
    }

    private static VirtualDisplayTaskRestorer.Root root(int id, int display, int activityType, int mode) {
        return new VirtualDisplayTaskRestorer.Root(id, display, activityType, mode, token());
    }

    private static class Tasks implements VirtualDisplayTaskRestorer.Tasks {
        List<VirtualDisplayTaskRestorer.Root> roots;
        final List<Integer> hidden = new ArrayList<>();
        final List<Integer> moved = new ArrayList<>();
        final List<Integer> restored = new ArrayList<>();
        boolean failHide;
        boolean failMove;
        boolean failFirstRestore;
        int restoreAttempts;
        Runnable afterHide;

        Tasks(VirtualDisplayTaskRestorer.Root... roots) { this.roots = Arrays.asList(roots); }
        @Override public List<VirtualDisplayTaskRestorer.Root> roots() { return roots; }
        @Override public void hide(List<VirtualDisplayTaskRestorer.Root> selected) throws Exception {
            for (VirtualDisplayTaskRestorer.Root root : selected) { hidden.add(root.id); }
            if (afterHide != null) { afterHide.run(); }
            if (failHide) { throw new Exception("hide"); }
        }
        @Override public void move(VirtualDisplayTaskRestorer.Root root) throws Exception {
            if (failMove) { throw new Exception("move"); }
            moved.add(root.id);
        }
        @Override public void restore(List<VirtualDisplayTaskRestorer.Root> selected) throws Exception {
            if (++restoreAttempts == 1 && failFirstRestore) { throw new Exception("restore"); }
            for (VirtualDisplayTaskRestorer.Root root : selected) { restored.add(root.id); }
        }
    }

    @Test public void onlyReturnsFullscreenApplicationTasksOnTheOwnedDisplay() throws Exception {
        Tasks tasks = new Tasks(root(1, 22, 1, 1), root(2, 0, 1, 1), root(3, 23, 1, 1),
                root(4, 22, 2, 1), root(5, 22, 1, 2));
        VirtualDisplayTaskRestorer.restoreToBackground(22, tasks);
        assertEquals(Arrays.asList(1), tasks.hidden);
        assertEquals(Arrays.asList(1), tasks.moved);
        assertEquals(Arrays.asList(1), tasks.restored);
    }

    @Test public void noOperationOnPrimaryDisplayOrEmptyDisplay() throws Exception {
        Tasks tasks = new Tasks(root(1, 0, 1, 1));
        VirtualDisplayTaskRestorer.restoreToBackground(0, tasks);
        VirtualDisplayTaskRestorer.restoreToBackground(77, tasks);
        assertEquals(0, tasks.restoreAttempts);
        assertEquals(0, tasks.hidden.size());
    }

    @Test public void unhidesSelectedTasksWhenHideOrMoveFails() throws Exception {
        for (boolean failHide : new boolean[] {true, false}) {
            Tasks tasks = new Tasks(root(1, 22, 1, 1));
            tasks.failHide = failHide;
            tasks.failMove = !failHide;
            try {
                VirtualDisplayTaskRestorer.restoreToBackground(22, tasks);
                fail("failure missing");
            } catch (Exception expected) {
                assertEquals(failHide ? "hide" : "move", expected.getMessage());
            }
            assertEquals(Arrays.asList(1), tasks.restored);
        }
    }

    @Test public void retriesVisibilityRestoration() throws Exception {
        Tasks tasks = new Tasks(root(1, 22, 1, 1));
        tasks.failFirstRestore = true;
        VirtualDisplayTaskRestorer.restoreToBackground(22, tasks);
        assertEquals(2, tasks.restoreAttempts);
        assertEquals(Arrays.asList(1), tasks.restored);
    }

    @Test public void doesNotMoveAReplacedTaskOrATaskThatLeftTheDisplay() throws Exception {
        for (boolean replaced : new boolean[] {true, false}) {
            VirtualDisplayTaskRestorer.Root original = root(1, 22, 1, 1);
            Tasks tasks = new Tasks(original);
            tasks.afterHide = () -> tasks.roots = Arrays.asList(replaced ? root(1, 22, 1, 1)
                    : new VirtualDisplayTaskRestorer.Root(1, 23, 1, 1, original.token));
            VirtualDisplayTaskRestorer.restoreToBackground(22, tasks);
            assertEquals(0, tasks.moved.size());
            assertEquals(Arrays.asList(1), tasks.restored);
        }
    }
}

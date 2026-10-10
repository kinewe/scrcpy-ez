package com.genymobile.scrcpy.device;

import com.genymobile.scrcpy.display.DisplayInfo;
import com.genymobile.scrcpy.model.Size;

import org.junit.Test;

import java.util.Arrays;
import java.util.List;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public class AppReuseLayoutTest {
    private static DisplayInfo display(int w, int h, int dpi, int rotation) {
        return new DisplayInfo(0, new Size(w, h), rotation, 0, 0, dpi, "test");
    }

    @Test public void wechatOnlyAcceptsUnchangingNativeLayout() {
        DisplayInfo phone = display(1440, 3200, 600, 0);
        assertTrue(AppTaskLauncher.compatibleLayout("com.tencent.mm", false, phone, display(1440, 3200, 600, 0)));
        assertFalse(AppTaskLauncher.compatibleLayout("com.tencent.mm", true, phone, phone));
        assertFalse(AppTaskLauncher.compatibleLayout("com.tencent.mm", false, phone, display(582, 1296, 243, 0)));
        assertFalse(AppTaskLauncher.compatibleLayout("com.tencent.mm", false, phone, display(1440, 3200, 360, 0)));
        assertFalse(AppTaskLauncher.compatibleLayout("com.tencent.mm", false, phone, display(1440, 3200, 600, 3)));
        assertFalse(AppTaskLauncher.compatibleLayout("com.tencent.mm", false, phone, null));
        assertTrue(AppTaskLauncher.compatibleLayout("app.other", true, phone, display(582, 1296, 243, 0)));
    }

    @Test public void incompatibleLayoutDoesNotMoveLaunchOrRestartOriginalTask() throws Exception {
        AppTaskLauncher.Tasks tasks = new AppTaskLauncher.Tasks() {
            final AppTaskLauncher.Root root = new AppTaskLauncher.Root(1, 0, 1, 1, "com.tencent.mm", "com.tencent.mm",
                    null, new Object(), false);
            @Override public List<AppTaskLauncher.Root> roots() { return Arrays.asList(root); }
            @Override public boolean layoutCompatible(AppTaskLauncher.Root root, int display) { return false; }
            @Override public void move(AppTaskLauncher.Root root, int display) { throw new AssertionError("moved incompatible task"); }
            @Override public void restore(AppTaskLauncher.Root root, int display) { throw new AssertionError("nothing to restore"); }
            @Override public void launch() { throw new AssertionError("launcher changed original task"); }
            @Override public long now() { return 0; }
            @Override public void pause() { throw new AssertionError("must fail before moving"); }
        };
        assertEquals(AppTaskLauncher.Result.LAYOUT_INCOMPATIBLE, AppTaskLauncher.start("com.tencent.mm", 42, tasks));
    }
}

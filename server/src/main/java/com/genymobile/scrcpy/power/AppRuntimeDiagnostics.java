package com.genymobile.scrcpy.power;

import com.genymobile.scrcpy.BuildConfig;
import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.display.DisplayInfo;
import com.genymobile.scrcpy.util.Ln;
import com.genymobile.scrcpy.wrappers.ServiceManager;

import android.app.KeyguardManager;
import android.content.ComponentName;
import android.content.Context;
import android.content.res.Configuration;

import java.lang.reflect.Field;
import java.io.File;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.Arrays;
import java.util.List;

/** Read-only framework snapshots. Never invoke this on the input or lease threads. */
public final class AppRuntimeDiagnostics {
    public static final boolean ENABLED = BuildConfig.VERSION_NAME.endsWith("-thaw5") || BuildConfig.VERSION_NAME.endsWith("-layout6") || BuildConfig.VERSION_NAME.endsWith("-preview7") || BuildConfig.VERSION_NAME.endsWith("-resize8");

    private AppRuntimeDiagnostics() { }

    public static void log(String pkg, int display) {
        if (!ENABLED) {
            return;
        }
        String prefix = "SCRCPY_EZ_APP_RUNTIME pkg=" + pkg + " display=" + display;
        logDisplay(pkg, "main", 0);
        logDisplay(pkg, "virtual", display);
        try {
            Ln.i(prefix + " main_group=" + ServiceManager.getDisplayManager().getDisplayGroupId(0)
                    + " target_group=" + ServiceManager.getDisplayManager().getDisplayGroupId(display));
        } catch (Exception e) {
            unavailable(prefix, "display-groups", e);
        }
        try {
            KeyguardManager keyguard = (KeyguardManager) FakeContext.get().getSystemService(Context.KEYGUARD_SERVICE);
            Ln.i(prefix + " keyguard_locked=" + keyguard.isKeyguardLocked() + " device_locked=" + keyguard.isDeviceLocked());
        } catch (Exception e) {
            unavailable(prefix, "keyguard", e);
        }
        try {
            int uid = FakeContext.get().getPackageManager().getApplicationInfo(pkg, 0).uid;
            Object service = Class.forName("android.app.ActivityManager").getMethod("getService").invoke(null);
            String state;
            try {
                state = String.valueOf(service.getClass().getMethod("getUidProcessState", int.class, String.class)
                        .invoke(service, uid, FakeContext.PACKAGE_NAME));
            } catch (Exception e) {
                state = unavailable(e);
            }
            String frozen;
            try {
                int[] values = (int[]) service.getClass().getMethod("getUidFrozenState", int[].class)
                        .invoke(service, (Object) new int[] {uid});
                frozen = values != null && values.length == 1 ? String.valueOf(values[0]) : "unavailable";
            } catch (Exception e) {
                frozen = unavailable(e);
            }
            Ln.i(prefix + " uid=" + uid + " uid_process_state=" + state + " uid_frozen_state=" + frozen);
            logProcesses(prefix, pkg, service);
        } catch (Exception e) {
            unavailable(prefix, "process", e);
        }
        try {
            Object service = Class.forName("android.app.ActivityTaskManager").getMethod("getService").invoke(null);
            int found = 0;
            for (Object root : (List<?>) service.getClass().getMethod("getAllRootTaskInfos").invoke(service)) {
                Object base = root.getClass().getField("baseActivity").get(root);
                Object top = root.getClass().getField("topActivity").get(root);
                int rootDisplay = root.getClass().getField("displayId").getInt(root);
                if (rootDisplay != display && !matches(base, pkg) && !matches(top, pkg)) {
                    continue;
                }
                found++;
                Object configuration = root.getClass().getField("configuration").get(root);
                if (configuration instanceof Configuration) {
                    Configuration config = (Configuration) configuration;
                    Ln.i("SCRCPY_EZ_APP_LAYOUT pkg=" + pkg + " source=root-task display=" + rootDisplay
                            + " dpi=" + config.densityDpi + " width_dp=" + config.screenWidthDp
                            + " height_dp=" + config.screenHeightDp + " smallest_width_dp=" + config.smallestScreenWidthDp
                            + " screen_layout=" + config.screenLayout + " orientation=" + config.orientation);
                }
                Ln.i(prefix + " task_id=" + field(root, "taskId") + " task_display=" + rootDisplay
                        + " target_base=" + matches(base, pkg) + " target_top=" + matches(top, pkg)
                        + " visible=" + field(root, "isVisible", "visible") + " focused=" + field(root, "isFocused")
                        + " sleeping=" + field(root, "isSleeping") + " running=" + field(root, "isRunning"));
            }
            Ln.i(prefix + " matching_tasks=" + found);
        } catch (Exception e) {
            unavailable(prefix, "tasks", e);
        }
    }

    private static boolean matches(Object component, String pkg) {
        return component instanceof ComponentName && pkg.equals(((ComponentName) component).getPackageName());
    }

    private static void logDisplay(String pkg, String source, int display) {
        try {
            DisplayInfo info = ServiceManager.getDisplayManager().getDisplayInfo(display);
            if (info != null) {
                Ln.i("SCRCPY_EZ_APP_LAYOUT pkg=" + pkg + " source=" + source + " display=" + display
                        + " size=" + info.getSize() + " dpi=" + info.getDpi() + " rotation=" + info.getRotation());
            }
        } catch (Exception e) {
            unavailable("SCRCPY_EZ_APP_LAYOUT pkg=" + pkg + " source=" + source, "metrics", e);
        }
    }

    private static void logProcesses(String prefix, String pkg, Object service) {
        try {
            int count = 0;
            List<?> processes = (List<?>) service.getClass().getMethod("getRunningAppProcesses").invoke(service);
            if (processes == null) {
                Ln.i(prefix + " matching_processes=unavailable");
                return;
            }
            for (Object process : processes) {
                Class<?> type = process.getClass();
                String name = (String) type.getField("processName").get(process);
                String[] packages = (String[]) type.getField("pkgList").get(process);
                if (!pkg.equals(name) && (name == null || !name.startsWith(pkg + ":"))
                        && (packages == null || !Arrays.asList(packages).contains(pkg))) {
                    continue;
                }
                if (count++ == 8) {
                    Ln.i(prefix + " matching_processes=truncated");
                    return;
                }
                int pid = type.getField("pid").getInt(process);
                String frozen;
                try {
                    frozen = String.valueOf(service.getClass().getMethod("isProcessFrozen", int.class).invoke(service, pid));
                } catch (Exception e) {
                    frozen = unavailable(e);
                }
                Ln.i(prefix + " pid=" + pid + " process_uid=" + field(process, "uid") + " main_process=" + pkg.equals(name)
                        + " importance=" + field(process, "importance") + " process_frozen=" + frozen + " " + linuxState(pid));
            }
            Ln.i(prefix + " matching_processes=" + count);
        } catch (Exception e) {
            unavailable(prefix, "process-list", e);
        }
    }

    private static String linuxState(int pid) {
        if (pid <= 0) {
            return "linux_state=unavailable";
        }
        String state;
        try {
            String stat = read(new File("/proc/" + pid + "/stat"));
            state = String.valueOf(stat.substring(stat.lastIndexOf(')') + 1).trim().charAt(0));
        } catch (Exception e) {
            state = unavailable(e);
        }
        String freezer = "unavailable";
        try {
            for (String line : read(new File("/proc/" + pid + "/cgroup")).split("\n")) {
                String[] parts = line.split(":", 3);
                if (parts.length != 3 || !parts[2].startsWith("/") || parts[2].contains("..")) {
                    continue;
                }
                if (Arrays.asList(parts[1].split(",")).contains("freezer")) {
                    freezer = read(new File("/sys/fs/cgroup/freezer" + parts[2], "freezer.state"));
                    break;
                } else if (parts[0].equals("0") && parts[1].isEmpty()) {
                    for (String event : read(new File("/sys/fs/cgroup" + parts[2], "cgroup.events")).split("\n")) {
                        if (event.startsWith("frozen ")) {
                            freezer = event.trim().replace(' ', ':');
                        }
                    }
                    break;
                }
            }
        } catch (Exception e) {
            freezer = unavailable(e);
        }
        return "linux_state=" + state + " cgroup_freezer=" + freezer;
    }

    private static String read(File file) throws Exception {
        return new String(Files.readAllBytes(file.toPath()), StandardCharsets.US_ASCII).trim();
    }

    private static String field(Object object, String... names) {
        for (String name : names) {
            try {
                Field field = object.getClass().getField(name);
                return String.valueOf(field.get(object));
            } catch (ReflectiveOperationException ignored) {
                // Firmware versions expose different TaskInfo fields.
            }
        }
        return "unavailable";
    }

    private static String unavailable(Exception e) {
        Throwable cause = e;
        while (cause.getCause() != null && cause.getCause() != cause) {
            cause = cause.getCause();
        }
        return "unavailable:" + cause.getClass().getSimpleName();
    }

    private static void unavailable(String prefix, String part, Exception e) {
        Ln.i(prefix + " " + part + "=" + unavailable(e));
    }
}

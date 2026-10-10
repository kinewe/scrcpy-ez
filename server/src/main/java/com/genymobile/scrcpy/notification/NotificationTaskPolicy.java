package com.genymobile.scrcpy.notification;

final class NotificationTaskPolicy {
    private NotificationTaskPolicy() { }

    static boolean belongsTo(String pkg, String top, String base, String[] children, int activityType) {
        if (pkg == null || pkg.isEmpty() || top == null || top.isEmpty() || !pkg.equals(base) || activityType != 1) {
            return false;
        }
        if (children != null) {
            for (String child : children) {
                if (child == null || !(child.equals(pkg) || child.startsWith(pkg + "/"))) {
                    return false; // Never move home, recents, or a mixed split-screen root.
                }
            }
        }
        return true;
    }
}

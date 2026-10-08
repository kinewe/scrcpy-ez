package com.genymobile.scrcpy.notification;

/** Optional OEM display attribution. The actual notification owner/key never changes. */
final class NotificationIdentity {
    interface InstalledPackage {
        boolean contains(String pkg);
    }

    private NotificationIdentity() {
    }

    static String displayPackage(String owner, Object notification, InstalledPackage installed) {
        // Do not trust arbitrary applications' display overrides or infer an app from message text.
        if (!"com.miui.systemAdSolution".equals(owner) || notification == null) {
            return owner;
        }
        try {
            Object extension = notification.getClass().getField("extraNotification").get(notification);
            if (extension == null) {
                return owner;
            }
            Object value = extension.getClass().getMethod("getTargetPkg").invoke(extension);
            if (!(value instanceof String)) {
                return owner;
            }
            String target = (String) value;
            if (target.length() > 255 || !target.matches("[A-Za-z][A-Za-z0-9_]*(\\.[A-Za-z0-9_]+)+")
                    || target.equals(owner) || !installed.contains(target)) {
                return owner;
            }
            return target;
        } catch (Exception | LinkageError ignored) {
            // OEM extension removed, malformed, restricted or unavailable: keep the real owner.
            return owner;
        }
    }
}

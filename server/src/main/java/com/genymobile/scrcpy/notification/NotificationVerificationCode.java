package com.genymobile.scrcpy.notification;

/** The Xiaomi SMS app supplies its current code separately from its masked text. */
public final class NotificationVerificationCode {
    private NotificationVerificationCode() {
        /* not instantiable */
    }

    public static String extract(String owner, Object supplied) {
        if (!"com.android.mms".equals(owner) || !(supplied instanceof CharSequence)) {
            return "";
        }
        CharSequence value = (CharSequence) supplied;
        if (value.length() < 4 || value.length() > 8) {
            return "";
        }
        for (int i = 0; i < value.length(); ++i) {
            if (value.charAt(i) < '0' || value.charAt(i) > '9') {
                return "";
            }
        }
        return value.toString();
    }
}

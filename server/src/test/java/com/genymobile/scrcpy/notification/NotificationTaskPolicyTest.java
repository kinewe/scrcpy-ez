package com.genymobile.scrcpy.notification;

import org.junit.Test;
import static org.junit.Assert.*;

public class NotificationTaskPolicyTest {
    @Test public void existingAppStackCanFollowNotification() {
        assertTrue(NotificationTaskPolicy.belongsTo("com.example.app", "com.example.app", "com.example.app", new String[]{"com.example.app/.Main", "com.example.app/.Detail"}, 1));
    }
    @Test public void mixedStacksAndOtherAppsMustStayPut() {
        assertFalse(NotificationTaskPolicy.belongsTo("com.example.app", "com.example.app", "com.example.app", new String[]{"com.example.other/.Detail"}, 1));
        assertFalse(NotificationTaskPolicy.belongsTo("com.example.app", "com.example.other", "com.example.app", new String[]{"com.example.other/.Main"}, 1));
        assertFalse(NotificationTaskPolicy.belongsTo("com.example.app", "com.example.app", "com.example.other", null, 1));
        assertFalse(NotificationTaskPolicy.belongsTo("com.example.app", "com.example.app", "com.example.app", null, 2));
        assertFalse(NotificationTaskPolicy.belongsTo("com.example.app", "com.example.app", "com.example.app", new String[]{null}, 1));
    }
    @Test public void systemDelegatedTopRemainsInOriginalAppTask() {
        assertTrue(NotificationTaskPolicy.belongsTo("com.android.settings", "com.miui.securitycenter", "com.android.settings", new String[]{"com.android.settings/.applications.InstalledAppDetails"}, 1));
    }
}

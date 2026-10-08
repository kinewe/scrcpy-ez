package com.genymobile.scrcpy.notification;

import org.junit.Test;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.fail;

public class NotificationIdentityTest {
    private static final String OWNER = "com.miui.systemAdSolution";

    public static class Payload {
        @SuppressWarnings("checkstyle:VisibilityModifier") // Match the OEM's public reflection field.
        public Object extraNotification;

        Payload(Object extension) {
            extraNotification = extension;
        }
    }

    public static class Extension {
        private final Object target;

        Extension(Object target) {
            this.target = target;
        }

        public Object getTargetPkg() {
            return target;
        }
    }

    @Test
    public void installedTargetOverridesDisplayOnly() {
        for (String target : new String[] {"com.xiaomi.shop", "com.taobao.taobao", "com.greenpoint.android.mc10086.activity"}) {
            assertEquals(target, NotificationIdentity.displayPackage(OWNER, new Payload(new Extension(target)), target::equals));
        }
    }

    @Test
    public void arbitraryOwnersCannotClaimAnotherApplication() {
        assertEquals("com.synthetic", NotificationIdentity.displayPackage("com.synthetic", new Payload(new Extension("com.xiaomi.shop")), pkg -> {
            fail("Untrusted source must not resolve a claimed target");
            return true;
        }));
    }

    @Test
    public void uninstalledAndInvisibleTargetsKeepOwner() {
        assertEquals(OWNER, NotificationIdentity.displayPackage(OWNER, new Payload(new Extension("com.xiaomi.shop")), pkg -> false));
        assertEquals(OWNER, NotificationIdentity.displayPackage(OWNER, new Payload(new Extension("com.xiaomi.shop")), pkg -> {
            throw new IllegalStateException("package lookup unavailable");
        }));
    }

    @Test
    public void missingOrIncompatibleOemExtensionKeepsOwner() {
        for (Object payload : new Object[] {null, new Object(), new Payload(null), new Payload(new Object()), new Payload(new Extension(42))}) {
            assertEquals(OWNER, NotificationIdentity.displayPackage(OWNER, payload, pkg -> true));
        }
    }

    @Test
    public void invalidTargetsNeverReachPackageLookup() {
        for (String target : new String[] {"", " com.xiaomi.shop", "com.xiaomi.shop\n", "https://example.invalid", "com..target", OWNER,
                "com." + new String(new char[260]).replace('\0', 'a')}) {
            assertEquals(OWNER, NotificationIdentity.displayPackage(OWNER, new Payload(new Extension(target)), pkg -> {
                fail("Invalid target must not be looked up");
                return true;
            }));
        }
    }
}

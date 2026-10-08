package com.genymobile.scrcpy.notification;

import org.junit.Test;

import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertSame;
import static org.junit.Assert.fail;

public class NotificationIconResolverTest {
    @Test
    public void nativeTargetIconBypassesOemArtworkAndRepeatedEncoding() {
        AtomicInteger loads = new AtomicInteger();
        Object icon = new Object();
        NotificationIconResolver<Object> resolver = new NotificationIconResolver<>(() -> 0, pkg -> {
            assertEquals("target", pkg);
            loads.incrementAndGet();
            return icon;
        }, value -> true);
        for (int i = 0; i < 20; ++i) {
            assertSame(icon, resolver.resolve("proxy", "target", () -> {
                fail("Native icon available: notification artwork must not be loaded or encoded");
                return null;
            }));
        }
        assertEquals(1, loads.get());
    }

    @Test
    public void ordinaryNotificationDoesNotTrustOemOverrideWhenNativeIconMissing() {
        NotificationIconResolver<String> resolver = new NotificationIconResolver<>(() -> 0, pkg -> null, value -> true);
        assertNull(resolver.resolve("owner", "owner", () -> {
            fail("An ordinary notification must not claim an OEM display override");
            return "foreign";
        }));
    }

    @Test
    public void transientPackageFailureRetriesAfterShortNegativeCache() {
        AtomicLong clock = new AtomicLong();
        AtomicInteger loads = new AtomicInteger();
        NotificationIconResolver<String> resolver = new NotificationIconResolver<>(clock::get, pkg -> {
            if (loads.incrementAndGet() == 1) {
                throw new IllegalStateException("temporary package lookup failure");
            }
            return "native";
        }, value -> !value.isEmpty());
        assertNull(resolver.application("target"));
        clock.set(29999);
        assertNull(resolver.application("target"));
        assertEquals(1, loads.get());
        clock.set(30000);
        assertEquals("native", resolver.application("target"));
        assertEquals(2, loads.get());
    }

    @Test
    public void notificationSpecificFallbackCanChangeForTheSameTarget() {
        AtomicInteger loads = new AtomicInteger();
        NotificationIconResolver<String> resolver = new NotificationIconResolver<>(() -> 0, pkg -> {
            loads.incrementAndGet();
            return null;
        }, value -> !value.isEmpty());
        assertEquals("promotion-A", resolver.resolve("proxy", "target", () -> "promotion-A"));
        assertEquals("promotion-B", resolver.resolve("proxy", "target", () -> "promotion-B"));
        assertEquals(1, loads.get());
    }

    @Test
    public void identicalFallbackRetainsAnnouncementObjectWithoutMixingDifferentArtwork() {
        NotificationIconResolver<Object> resolver = new NotificationIconResolver<>(() -> 0, pkg -> null, value -> true);
        Object first = resolver.remember("notification:target:hash-A", new Object());
        assertSame(first, resolver.remember("notification:target:hash-A", new Object()));
        Object other = resolver.remember("notification:target:hash-B", new Object());
        if (first == other) {
            fail("Different notification artwork must not share an announcement object");
        }
    }

    @Test
    public void invalidArtworkAndBrokenOemResourcesFallBackToOwner() {
        NotificationIconResolver<String> resolver = new NotificationIconResolver<>(() -> 0,
                pkg -> "proxy".equals(pkg) ? "owner-artwork" : "", value -> !value.isEmpty());
        assertEquals("owner-artwork", resolver.resolve("proxy", "target", () -> ""));
        assertEquals("owner-artwork", resolver.resolve("proxy", "target", () -> {
            throw new IllegalArgumentException("malformed extras");
        }));
        assertEquals("owner-artwork", resolver.resolve("proxy", "target", () -> {
            throw new NoClassDefFoundError("incompatible OEM resource");
        }));
    }

    @Test
    public void allLookupFailuresReturnNoArtworkWithoutThrowing() {
        NotificationIconResolver<String> resolver = new NotificationIconResolver<>(() -> 0, pkg -> {
            throw new NoClassDefFoundError("resource unavailable");
        }, value -> true);
        assertNull(resolver.resolve("proxy", "target", () -> {
            throw new IllegalStateException("unavailable extras");
        }));
    }

    @Test
    public void nativeIconRefreshPreservesObjectUntilExpiryAndThenReloads() {
        AtomicLong clock = new AtomicLong();
        NotificationIconResolver<Object> resolver = new NotificationIconResolver<>(clock::get, pkg -> new Object(), value -> true);
        Object before = resolver.application("target");
        clock.set(299999);
        assertSame(before, resolver.application("target"));
        clock.set(300000);
        Object after = resolver.application("target");
        if (before == after) {
            fail("Expired resources must refresh after application/theme changes");
        }
        assertSame(after, resolver.application("target"));
    }

    @Test
    public void cachePressureEvictsOneOldEntryAndKeepsRecentlyUsedArtwork() {
        Map<String, Integer> loads = new HashMap<>();
        NotificationIconResolver<String> resolver = new NotificationIconResolver<>(() -> 0, pkg -> {
            loads.put(pkg, loads.getOrDefault(pkg, 0) + 1);
            return pkg;
        }, value -> true);
        for (int i = 0; i < 128; ++i) {
            resolver.application("pkg" + i);
        }
        resolver.application("pkg0");
        resolver.application("overflow");
        resolver.application("pkg0");
        resolver.application("pkg127");
        assertEquals(Integer.valueOf(1), loads.get("pkg0"));
        assertEquals(Integer.valueOf(1), loads.get("pkg127"));
        resolver.application("pkg1");
        assertEquals(Integer.valueOf(2), loads.get("pkg1"));
    }
}

package com.genymobile.scrcpy.power;

import com.genymobile.scrcpy.control.KeepAwakeLease;

import java.io.File;
import java.nio.file.Files;
import java.util.HashSet;
import java.util.LinkedHashSet;
import java.util.Set;

/** Exercises the real shared lease/store with a fake Android backend; no device I/O. */
public final class PowerSaveHeartbeatTest {
    private static final String A = "boot:100:1000";
    private static final String B = "boot:200:2000";
    private static final String APP = "org.example.app";
    private static final String DETAIL = "org.example.notification";
    private static int checks;

    private PowerSaveHeartbeatTest() { }

    private static void check(boolean value, String message) {
        checks++;
        if (!value) { throw new AssertionError(message); }
    }

    public static void main(String[] args) throws Exception {
        File dir = Files.createTempDirectory(new File(args[0]).toPath(), "heartbeat-").toFile();
        Set<String> exempt = new HashSet<>();
        Set<String> desired = new LinkedHashSet<>();
        PowerSaveLeaseStore store = new PowerSaveLeaseStore(dir, new PowerSaveLeaseStore.Backend() {
            @Override public boolean contains(String pkg) { return exempt.contains(pkg); }
            @Override public void add(String pkg) { exempt.add(pkg); }
            @Override public void remove(String pkg) { exempt.remove(pkg); }
        }, owner -> true, line -> { });
        store.registerDisplay(A, 1);
        store.registerDisplay(B, 2);
        store.acquire(APP, 1);
        store.acquire(APP, 2);
        // The notification process applies this directly via the display route,
        // without the parent knowing its target package in advance.
        store.acquire(DETAIL, 1);
        final int[] expired = {0};
        final int[] resumed = {0};
        KeepAwakeLease lease = new KeepAwakeLease(() -> {
            resumed[0]++;
            store.registerDisplay(A, 1);
            for (String pkg : desired) { store.acquire(pkg, 1); }
            return () -> {
                store.release(A, desired::addAll);
                expired[0]++;
            };
        });
        lease.heartbeat(0); // Initial grace period, even without a client packet.
        for (long now = 5000; now <= 120000; now += 5000) {
            lease.heartbeat(now);
            lease.expire(now + 4999);
        }
        check(expired[0] == 0, "idle/static/minimized views remain exempt with 5-second heartbeats");
        lease.expire(149999);
        check(exempt.contains(DETAIL), "exemption remains before the exact 30-second boundary");
        lease.expire(150000);
        check(!exempt.contains(DETAIL), "30 seconds without heartbeat releases notification exemption");
        check(exempt.contains(APP), "other online owner keeps its shared app exemption");
        check(desired.contains(APP) && desired.contains(DETAIL), "timeout atomically remembers app and notification packages");
        store.acquire(DETAIL, 1);
        check(!exempt.contains(DETAIL), "late notification cannot renew an expired session");
        lease.heartbeat(160000);
        check(exempt.contains(DETAIL), "reconnected heartbeat reacquires desired exemption");
        check(resumed[0] == 2, "recovery occurs once after the timeout");
        lease.heartbeat(165000);
        check(resumed[0] == 2, "ordinary heartbeats do not repeatedly acquire");
        lease.close();
        lease.heartbeat(170000);
        check(!exempt.contains(DETAIL), "a late heartbeat cannot reopen a normally closed lease");
        check(exempt.contains(APP), "closing one lease does not release another owner");
        store.release(B);
        check(!exempt.contains(APP), "last owner restores original whitelist");

        // No first client heartbeat: initialization alone must eventually expire.
        final int[] noClientReleases = {0};
        KeepAwakeLease noClient = new KeepAwakeLease(() -> () -> noClientReleases[0]++);
        noClient.heartbeat(500000);
        noClient.expire(529999);
        check(noClientReleases[0] == 0, "startup grace period is not shortened");
        noClient.expire(530000);
        check(noClientReleases[0] == 1, "missing first heartbeat still expires after 30 seconds");
        noClient.close();
        check(noClientReleases[0] == 1, "shutdown after expiry is idempotent");
        System.out.println("PowerSaveHeartbeatTest PASS checks=" + checks);
    }
}

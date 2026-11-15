package com.genymobile.scrcpy.power;

import com.genymobile.scrcpy.control.KeepAwakeLease;

import org.junit.Rule;
import org.junit.Test;
import org.junit.rules.TemporaryFolder;

import java.io.File;
import java.io.IOException;
import java.util.HashSet;
import java.util.Set;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.assertThrows;

public class MilletLeaseTest {
    private static final String A = "boot:10:100";
    private static final String B = "boot:20:200";
    private static final String PKG = "org.example.cast";
    @Rule public TemporaryFolder temp = new TemporaryFolder();

    private static PowerSaveLeaseStore store(File folder, PackageListBackend.Value value) {
        return new PowerSaveLeaseStore(folder, new PackageListBackend(value), owner -> true, log -> { },
                "millet", "SCRCPY_EZ_APP_MILLET ");
    }

    @Test public void existingExemptionIsNeverOwnedOrRemoved() throws Exception {
        PackageListBackendTest.Value value = new PackageListBackendTest.Value("org.example.keep," + PKG);
        PowerSaveLeaseStore store = store(temp.newFolder(), value);
        store.registerDisplay(A, 1);
        store.acquire(PKG, 1);
        store.release(A);
        assertEquals("org.example.keep," + PKG, value.raw);
        assertEquals(0, value.writes);
    }

    @Test public void rewriteIsRepairedButLateRefreshCannotReacquireClosedLease() throws Exception {
        PackageListBackendTest.Value value = new PackageListBackendTest.Value("org.example.keep");
        PowerSaveLeaseStore store = store(temp.newFolder(), value);
        store.registerDisplay(A, 1);
        store.acquire(PKG, 1);
        int writes = value.writes;
        store.refresh(A);
        assertEquals(writes, value.writes);
        value.raw = "org.example.keep,org.example.new";
        store.refresh(A);
        assertEquals("org.example.keep,org.example.new," + PKG, value.raw);
        store.release(A);
        store.refresh(A);
        store.acquire(PKG, 1);
        assertEquals("org.example.keep,org.example.new", value.raw);
    }

    @Test public void differentNamespacesDoNotReleaseEachOthersExemptions() throws Exception {
        File folder = temp.newFolder();
        PackageListBackendTest.Value android = new PackageListBackendTest.Value("");
        PackageListBackendTest.Value vendor = new PackageListBackendTest.Value("");
        PowerSaveLeaseStore standard = new PowerSaveLeaseStore(folder, new PackageListBackend(android), owner -> true, log -> { });
        PowerSaveLeaseStore millet = store(folder, vendor);
        standard.registerDisplay(A, 1);
        millet.registerDisplay(A, 1);
        standard.acquire(PKG, 1);
        millet.acquire(PKG, 1);
        standard.release(A);
        assertEquals("", android.raw);
        assertEquals(PKG, vendor.raw);
        assertTrue(new File(folder, ".scrcpy-ez-power-save.state").exists());
        assertTrue(new File(folder, ".scrcpy-ez-millet.state").exists());
        millet.release(A);
        assertEquals("", vendor.raw);
    }

    @Test public void heartbeatTimeoutRetainsOtherOwnerAndResumeOnlyReaddsOwnTarget() throws Exception {
        PackageListBackendTest.Value value = new PackageListBackendTest.Value("org.example.keep");
        PowerSaveLeaseStore store = store(temp.newFolder(), value);
        store.registerDisplay(B, 2);
        store.acquire(PKG, 2);
        KeepAwakeLease lease = new KeepAwakeLease(() -> {
            store.registerDisplay(A, 1);
            store.acquire(PKG, 1);
            return () -> store.release(A);
        });
        lease.heartbeat(0);
        lease.expire(29999);
        assertTrue(new PackageListBackend(value).contains(PKG));
        lease.expire(30000);
        store.refresh(A);
        assertTrue(new PackageListBackend(value).contains(PKG));
        store.release(B);
        assertFalse(new PackageListBackend(value).contains(PKG));
        lease.heartbeat(30001);
        assertTrue(new PackageListBackend(value).contains(PKG));
        lease.close();
        lease.heartbeat(40000);
        store.refresh(A);
        assertEquals("org.example.keep", value.raw);
    }

    @Test public void independentCleanupRecoversVendorEntryAfterOwnerDeath() throws Exception {
        File folder = temp.newFolder();
        PackageListBackendTest.Value value = new PackageListBackendTest.Value("org.example.keep");
        Set<String> alive = new HashSet<>();
        alive.add(A);
        PowerSaveLeaseStore first = new PowerSaveLeaseStore(folder, new PackageListBackend(value), alive::contains, log -> { },
                "millet", "SCRCPY_EZ_APP_MILLET ");
        first.registerDisplay(A, 1);
        first.acquire(PKG, 1);
        alive.clear();
        alive.add(B);
        PowerSaveLeaseStore cleanup = new PowerSaveLeaseStore(folder, new PackageListBackend(value), alive::contains, log -> { },
                "millet", "SCRCPY_EZ_APP_MILLET ");
        cleanup.registerDisplay(B, 2);
        assertEquals("org.example.keep", value.raw);
        first.refresh(A);
        assertEquals("org.example.keep", value.raw);
    }

    @Test public void startupGrantRequiresLiveDisplayEvenForPreexistingMembership() throws Exception {
        PackageListBackendTest.Value value = new PackageListBackendTest.Value(PKG);
        PowerSaveLeaseStore store = store(temp.newFolder(), value);
        assertFalse(store.acquire(PKG, 1));
        store.registerDisplay(A, 1);
        assertTrue(store.acquire(PKG, 1));
        store.release(A);
        assertFalse(store.acquire(PKG, 1));
        assertEquals(PKG, value.raw);
        assertEquals(0, value.writes);
    }

    @Test public void failedMembershipReadbackCannotAuthorizeStartupAction() throws Exception {
        PackageListBackend.Value ignoredWrites = new PackageListBackend.Value() {
            @Override public String read() { return ""; }
            @Override public void write(String value) { }
        };
        PowerSaveLeaseStore store = store(temp.newFolder(), ignoredWrites);
        store.registerDisplay(A, 1);
        assertThrows(IOException.class, () -> store.acquire(PKG, 1));
        store.release(A);
        assertFalse(store.acquire(PKG, 1));
    }
}

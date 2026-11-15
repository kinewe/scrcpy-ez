package com.genymobile.scrcpy.power;

import org.junit.Test;

import java.io.IOException;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;

public class PackageListBackendTest {
    private static final String PKG = "org.example.cast";
    static final class Value implements PackageListBackend.Value {
        String raw;
        int writes;
        boolean deny;
        Value(String raw) { this.raw = raw; }
        @Override public String read() { return raw; }
        @Override public void write(String next) throws IOException {
            if (deny) { throw new IOException("permission denied"); }
            writes++;
            raw = next;
        }
    }

    @Test public void addsToEmptyListAndRepeatedAcquisitionDoesNotWrite() throws Exception {
        Value value = new Value("");
        PackageListBackend backend = new PackageListBackend(value);
        backend.add(PKG);
        backend.add(PKG);
        assertEquals(PKG, value.raw);
        assertEquals(1, value.writes);
        assertTrue(backend.contains(PKG));
    }

    @Test public void matchesWholeTrimmedPackagesRatherThanSubstrings() throws Exception {
        Value value = new Value(" " + PKG + "2 , org.example.other ");
        PackageListBackend backend = new PackageListBackend(value);
        assertFalse(backend.contains(PKG));
        backend.add(PKG);
        assertEquals(" " + PKG + "2 , org.example.other ," + PKG, value.raw);
        value.raw = " " + PKG + " , org.example.other ";
        backend.add(PKG);
        assertEquals(1, value.writes);
    }

    @Test public void removalKeepsUnrelatedChangesMadeDuringSession() throws Exception {
        Value value = new Value(" org.example.old ");
        PackageListBackend backend = new PackageListBackend(value);
        backend.add(PKG);
        value.raw += ", org.example.new ";
        backend.remove(PKG);
        assertEquals(" org.example.old , org.example.new ", value.raw);
        int writes = value.writes;
        backend.remove(PKG);
        assertEquals(writes, value.writes);
    }

    @Test public void removesAllExactDuplicatesAndPreservesOtherRawTokens() throws Exception {
        Value value = new Value(PKG + ", org.example.keep ,, " + PKG + " ,");
        new PackageListBackend(value).remove(PKG);
        assertEquals(" org.example.keep ,,", value.raw);
    }

    @Test public void missingOrUnrecognizedSettingNeverGetsOverwritten() throws Exception {
        for (String raw : new String[] {null, "null", "[\"org.example.other\"]", "org.example.one;org.example.two"}) {
            Value value = new Value(raw);
            PackageListBackend backend = new PackageListBackend(value);
            try { backend.add(PKG); fail(); } catch (IOException | UnsupportedOperationException expected) { }
            try { backend.remove(PKG); fail(); } catch (IOException | UnsupportedOperationException expected) { }
            assertEquals(raw, value.raw);
            assertEquals(0, value.writes);
        }
    }

    @Test public void invalidTargetDoesNotWrite() throws Exception {
        Value value = new Value("org.example.keep");
        try { new PackageListBackend(value).add("org.example.bad;reboot"); fail(); } catch (IOException expected) { }
        assertEquals(0, value.writes);
    }

    @Test public void permissionDenialIsReportedAndOriginalListSurvives() throws Exception {
        Value value = new Value("org.example.keep");
        value.deny = true;
        try { new PackageListBackend(value).add(PKG); fail(); } catch (IOException expected) { }
        assertEquals("org.example.keep", value.raw);
        assertEquals(0, value.writes);
    }
}

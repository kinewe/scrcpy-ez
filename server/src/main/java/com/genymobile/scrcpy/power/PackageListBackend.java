package com.genymobile.scrcpy.power;

import java.io.IOException;
import java.util.ArrayList;
import java.util.List;

/** Target-only edits to an existing comma-separated package list. */
public final class PackageListBackend implements PowerSaveLeaseStore.Backend {
    public interface Value {
        String read() throws Exception;
        void write(String value) throws Exception;
    }

    private final Value value;

    public PackageListBackend(Value value) {
        this.value = value;
    }

    private String read() throws Exception {
        String raw = value.read();
        if (raw == null) {
            throw new UnsupportedOperationException("MILLET_NO_RESTRICT_APP is absent");
        }
        if (raw.length() > 65536) {
            throw new IOException("Package list is too large");
        }
        for (String item : raw.split(",", -1)) {
            if (!item.trim().isEmpty() && !PowerSaveLeaseStore.validPackage(item.trim())) {
                throw new IOException("Unrecognized package-list format");
            }
        }
        return raw;
    }

    private static boolean contains(String raw, String pkg) {
        for (String item : raw.split(",", -1)) {
            if (item.trim().equals(pkg)) {
                return true;
            }
        }
        return false;
    }

    private static void check(String pkg) throws IOException {
        if (!PowerSaveLeaseStore.validPackage(pkg)) {
            throw new IOException("Invalid target package");
        }
    }

    @Override
    public boolean contains(String pkg) throws Exception {
        check(pkg);
        return contains(read(), pkg);
    }

    @Override
    public void add(String pkg) throws Exception {
        check(pkg);
        String current = read();
        if (!contains(current, pkg)) {
            // Keep unrelated entries/formatting from the latest read. Never
            // overwrite the setting with a session-start snapshot.
            value.write(current.isEmpty() ? pkg : current + (current.endsWith(",") ? "" : ",") + pkg);
        }
    }

    @Override
    public void remove(String pkg) throws Exception {
        check(pkg);
        String current = read();
        if (!contains(current, pkg)) {
            return;
        }
        List<String> retained = new ArrayList<>();
        for (String item : current.split(",", -1)) {
            if (!item.trim().equals(pkg)) {
                retained.add(item);
            }
        }
        value.write(String.join(",", retained));
    }
}

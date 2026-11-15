package com.genymobile.scrcpy.power;

import java.io.File;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

/** Standalone JVM tests: never loads Android classes, ADB, or a connected device. */
public final class PowerSaveLeaseStoreTest {
    private static final String PKG = "org.example.projection";
    private static final String A = "boot:10:100";
    private static final String B = "boot:20:200";
    private static int checks;

    private PowerSaveLeaseStoreTest() { }

    private static final class Backend implements PowerSaveLeaseStore.Backend {
        private final Set<String> exempt = new HashSet<>();
        private int adds;
        private int removes;
        private boolean denyRead;
        private boolean throwAfterAdd;
        private boolean failRemove;
        private boolean ignoreAdd;
        private boolean ignoreRemove;

        @Override
        public boolean contains(String pkg) throws Exception {
            if (denyRead) { throw new SecurityException("permission denied"); }
            return exempt.contains(pkg);
        }

        @Override
        public void add(String pkg) throws Exception {
            adds++;
            if (!ignoreAdd) { exempt.add(pkg); }
            if (throwAfterAdd) { throw new IOException("Binder reply lost after mutation"); }
        }

        @Override
        public void remove(String pkg) throws Exception {
            removes++;
            if (failRemove) { throw new IOException("remove failed"); }
            if (!ignoreRemove) { exempt.remove(pkg); }
        }
    }

    private static void check(boolean condition, String message) {
        checks++;
        if (!condition) { throw new AssertionError(message); }
    }

    private static void fails(Throwing action) throws Exception {
        boolean failed = false;
        try { action.run(); } catch (Exception expected) { failed = true; }
        check(failed, "operation must report failure");
    }

    private interface Throwing { void run() throws Exception; }

    private static File folder(File root) throws IOException {
        return Files.createTempDirectory(root.toPath(), "power-save-").toFile();
    }

    public static void main(String[] args) throws Exception {
        if (args.length > 0 && "worker".equals(args[0])) {
            worker(new File(args[1]), args[2], Integer.parseInt(args[3]));
            return;
        }
        File root = new File(args[0]);
        List<String> logs = new ArrayList<>();
        Backend backend = new Backend();
        Set<String> live = new HashSet<>();
        live.add(A); live.add(B);
        PowerSaveLeaseStore store = new PowerSaveLeaseStore(folder(root), backend, live::contains, logs::add);
        store.registerDisplay(A, 1);
        store.registerDisplay(B, 2);
        store.acquire(PKG, 1);
        store.acquire(PKG, 1);
        store.acquire(PKG, 2);
        check(backend.adds == 1, "same package must be added once across owners and repeated launches");
        store.release(A);
        check(backend.exempt.contains(PKG) && backend.removes == 0, "other live window retains exemption");
        store.release(B);
        check(!backend.exempt.contains(PKG) && backend.removes == 1, "last owner restores original state");
        store.acquire(PKG, 2);
        check(backend.adds == 1, "late notification cannot reacquire a closed display");
        check(logs.stream().anyMatch(s -> s.contains("action=shared")), "shared result logged");
        check(logs.stream().anyMatch(s -> s.contains("action=restored") && s.contains("verified=true")), "verified restore logged");

        backend.exempt.add(PKG);
        store.registerDisplay(A, 1);
        store.acquire(PKG, 1);
        store.release(A);
        check(backend.exempt.contains(PKG) && backend.removes == 1, "pre-existing exemption must survive");
        check(logs.stream().anyMatch(s -> s.contains("action=preserved")), "preserved result logged");

        Backend crash = new Backend();
        File crashDir = folder(root);
        PowerSaveLeaseStore first = new PowerSaveLeaseStore(crashDir, crash, live::contains, logs::add);
        first.registerDisplay(A, 1);
        crash.throwAfterAdd = true;
        fails(() -> first.acquire(PKG, 1));
        check(crash.exempt.contains(PKG), "simulate mutation before lost Binder response");
        PowerSaveLeaseStore cleanup = new PowerSaveLeaseStore(crashDir, crash, live::contains, logs::add);
        cleanup.release(A);
        check(!crash.exempt.contains(PKG), "independent cleanup restores write-ahead intent");
        crash.throwAfterAdd = false;
        first.registerDisplay(A, 1);
        first.acquire(PKG, 1);
        live.remove(A);
        first.registerDisplay(B, 2);
        check(!crash.exempt.contains(PKG), "next session recovers dead owner");
        check(logs.stream().anyMatch(s -> s.contains("action=recovered")), "stale recovery logged");
        live.add(A);

        Backend rejected = new Backend();
        PowerSaveLeaseStore errors = new PowerSaveLeaseStore(folder(root), rejected, live::contains, logs::add);
        errors.registerDisplay(A, 1);
        rejected.denyRead = true;
        fails(() -> errors.acquire(PKG, 1));
        check(rejected.adds == 0, "unknown original state prevents mutation");
        rejected.denyRead = false;
        rejected.ignoreAdd = true;
        fails(() -> errors.acquire(PKG, 1));
        rejected.ignoreAdd = false;
        errors.acquire(PKG, 1);
        rejected.failRemove = true;
        fails(() -> errors.release(A));
        rejected.failRemove = false;
        errors.release(A);
        check(!rejected.exempt.contains(PKG), "independent cleanup retries failed restore");

        errors.registerDisplay(A, 1);
        errors.acquire(PKG, 1);
        rejected.failRemove = true;
        fails(() -> errors.release(A));
        errors.registerDisplay(B, 2);
        errors.acquire(PKG, 2);
        check(rejected.exempt.contains(PKG), "new session can adopt an EZ-owned failed restoration");
        rejected.failRemove = false;
        errors.release(B);
        check(!rejected.exempt.contains(PKG), "adopted pending restoration remains EZ-owned");

        errors.registerDisplay(A, 1);
        errors.acquire(PKG, 1);
        rejected.ignoreRemove = true;
        fails(() -> errors.release(A));
        rejected.ignoreRemove = false;
        errors.release(A);
        check(!rejected.exempt.contains(PKG), "removal readback failure retains restoration intent");
        int writes = rejected.adds;
        fails(() -> errors.acquire("org.example;reboot", 1));
        fails(() -> errors.registerDisplay(A, 0));
        check(rejected.adds == writes, "invalid targets do not mutate Android");

        File corrupt = folder(root);
        Files.write(new File(corrupt, ".scrcpy-ez-power-save.state").toPath(), "version=999\n".getBytes(StandardCharsets.US_ASCII));
        PowerSaveLeaseStore corruptStore = new PowerSaveLeaseStore(corrupt, rejected, live::contains, logs::add);
        fails(() -> corruptStore.registerDisplay(A, 1));
        check(rejected.adds == writes, "unknown journal format is preserved without mutations");
        crossProcess(root);
        System.out.println("PowerSaveLeaseStoreTest PASS checks=" + checks);
    }

    private static PowerSaveLeaseStore.Backend diskBackend(File dir) {
        return new PowerSaveLeaseStore.Backend() {
            private final File value = new File(dir, "fake-android-whitelist");
            @Override public boolean contains(String pkg) { return value.exists(); }
            @Override public void add(String pkg) throws Exception { Files.write(value.toPath(), new byte[] {1}); }
            @Override public void remove(String pkg) throws Exception { Files.deleteIfExists(value.toPath()); }
        };
    }

    private static void worker(File dir, String owner, int display) throws Exception {
        PowerSaveLeaseStore store = new PowerSaveLeaseStore(dir, diskBackend(dir), value -> true, value -> { });
        store.registerDisplay(owner, display);
        store.acquire(PKG, display);
        Files.write(new File(dir, "ready-" + display).toPath(), new byte[] {1});
        long until = System.nanoTime() + 10000000000L;
        while (!new File(dir, "release-" + display).exists()) {
            if (System.nanoTime() > until) { throw new IOException("test barrier timeout"); }
            Thread.sleep(10);
        }
        store.release(owner);
    }

    private static void crossProcess(File root) throws Exception {
        File dir = folder(root);
        String java = new File(System.getProperty("java.home"), "bin/java").toString();
        List<Process> processes = new ArrayList<>();
        try {
            for (int i = 1; i <= 2; i++) {
                processes.add(new ProcessBuilder(java, "-cp", System.getProperty("java.class.path"),
                        PowerSaveLeaseStoreTest.class.getName(), "worker", dir.toString(), i == 1 ? A : B,
                        String.valueOf(i)).inheritIO().start());
            }
            long until = System.nanoTime() + 10000000000L;
            while (!new File(dir, "ready-1").exists() || !new File(dir, "ready-2").exists()) {
                if (System.nanoTime() > until) { throw new IOException("worker startup timeout"); }
                Thread.sleep(10);
            }
            Files.write(new File(dir, "release-1").toPath(), new byte[] {1});
            check(processes.get(0).waitFor() == 0, "first process exits cleanly");
            check(new File(dir, "fake-android-whitelist").exists(), "other process still owns whitelist");
            Files.write(new File(dir, "release-2").toPath(), new byte[] {1});
            check(processes.get(1).waitFor() == 0, "second process exits cleanly");
            check(!new File(dir, "fake-android-whitelist").exists(), "last process restores whitelist");
        } finally {
            for (Process process : processes) { if (process.isAlive()) { process.destroyForcibly(); } }
        }
    }
}

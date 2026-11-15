package com.genymobile.scrcpy.power;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.RandomAccessFile;
import java.nio.channels.FileLock;
import java.nio.channels.OverlappingFileLockException;
import java.nio.file.Files;
import java.nio.file.StandardCopyOption;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashSet;
import java.util.Properties;
import java.util.Set;
import java.util.function.Consumer;
import java.util.function.Predicate;

/** Device-local ownership shared by application windows and the crash-cleanup process.
 * Only exemptions absent before the first EZ owner are recorded here. */
public final class PowerSaveLeaseStore {
    public interface Backend {
        boolean contains(String pkg) throws Exception;
        void add(String pkg) throws Exception;
        void remove(String pkg) throws Exception;
    }

    private interface Operation {
        void run(Properties state) throws Exception;
    }

    private static final Object LOCAL_LOCK = new Object();
    private final String namespace;
    private final String prefix;
    private final File folder;
    private final Backend backend;
    private final Predicate<String> alive;
    private final Consumer<String> logger;

    public PowerSaveLeaseStore(File folder, Backend backend, Predicate<String> alive, Consumer<String> logger) {
        this(folder, backend, alive, logger, "power-save", "SCRCPY_EZ_APP_POWER_SAVE ");
    }

    public PowerSaveLeaseStore(File folder, Backend backend, Predicate<String> alive, Consumer<String> logger,
            String namespace, String prefix) {
        if (namespace == null || !namespace.matches("[a-z0-9-]+")) {
            throw new IllegalArgumentException("Invalid lease namespace");
        }
        this.folder = folder;
        this.backend = backend;
        this.alive = alive;
        this.logger = logger;
        this.namespace = namespace;
        this.prefix = prefix;
    }

    public static boolean validPackage(String pkg) {
        return pkg != null && pkg.length() <= 255 && pkg.matches("[A-Za-z_][A-Za-z0-9_]*(\\.[A-Za-z_][A-Za-z0-9_]*)+");
    }

    public void registerDisplay(String owner, int display) throws Exception {
        if (display <= 0 || !validOwner(owner)) {
            throw new IOException("Invalid display owner");
        }
        transaction(state -> {
            recover(state);
            state.setProperty("display." + display, owner);
            save(state);
            log("registered", "-", display, "owner=" + owner);
        });
    }

    /** Returns true only after a live display grant and successful membership readback. */
    public boolean acquire(String pkg, int display) throws Exception {
        if (!validPackage(pkg) || display <= 0) {
            throw new IOException("Invalid application display");
        }
        boolean[] accepted = {false};
        transaction(state -> {
            recover(state);
            String owner = state.getProperty("display." + display);
            if (owner == null || !alive.test(owner)) {
                log("skipped", pkg, display, "reason=no-active-ez-display");
                return;
            }
            String key = "app." + pkg;
            Set<String> owners = owners(state.getProperty(key));
            boolean before = backend.contains(pkg);
            if (!state.containsKey(key) && before) {
                log("preserved", pkg, display, "before=true after=true verified=true");
                accepted[0] = true;
                return;
            }
            boolean shared = !owners.isEmpty();
            owners.add(owner);
            state.setProperty(key, String.join(",", owners));
            // Write intent before changing Android. A killed server or a Binder
            // error after a successful write must still be recoverable.
            save(state);
            if (!before) {
                backend.add(pkg);
            }
            if (!backend.contains(pkg)) {
                throw new IOException("Whitelist addition did not pass readback");
            }
            log(shared ? "shared" : "added", pkg, display,
                    "before=" + before + " after=true verified=true owners=" + owners.size());
            accepted[0] = true;
        });
        return accepted[0];
    }

    /** Reapply only currently owned entries, without creating a lease or display route. */
    public void refresh(String owner) throws Exception {
        if (!validOwner(owner) || !alive.test(owner)) {
            return;
        }
        transaction(state -> {
            for (String key : state.stringPropertyNames()) {
                if (!key.startsWith("app.") || !owners(state.getProperty(key)).contains(owner)) {
                    continue;
                }
                String pkg = key.substring(4);
                if (!backend.contains(pkg)) {
                    backend.add(pkg);
                    if (!backend.contains(pkg)) {
                        throw new IOException("Whitelist refresh did not pass readback");
                    }
                    log("reapplied", pkg, -1, "after=true verified=true");
                }
            }
        });
    }

    public void release(String owner) throws Exception {
        release(owner, packages -> { });
    }

    public void release(String owner, Consumer<Set<String>> snapshot) throws Exception {
        if (!validOwner(owner)) {
            return;
        }
        transaction(state -> {
            Set<String> packages = new LinkedHashSet<>();
            for (String key : state.stringPropertyNames()) {
                if (key.startsWith("app.") && owners(state.getProperty(key)).contains(owner)) {
                    packages.add(key.substring(4));
                }
            }
            // Capture notification-launched packages and disable their routes
            // under the same lock, before another process can launch into them.
            snapshot.accept(packages);
            // Remove display routes first. A late notification launch must not
            // recreate an exemption after this session has closed.
            for (String key : new ArrayList<>(state.stringPropertyNames())) {
                if (key.startsWith("display.") && owner.equals(state.getProperty(key))) {
                    state.remove(key);
                }
            }
            save(state);
            Exception failure = null;
            for (String key : new ArrayList<>(state.stringPropertyNames())) {
                if (!key.startsWith("app.")) {
                    continue;
                }
                Set<String> owners = owners(state.getProperty(key));
                if (!owners.remove(owner) && !owners.isEmpty()) {
                    continue;
                }
                state.setProperty(key, String.join(",", owners));
                save(state);
                try {
                    finishRelease(state, key, owners, "restored");
                } catch (Exception e) {
                    failure = e;
                    log("restore-failed", key.substring(4), -1, "reason=" + error(e));
                }
            }
            if (failure != null) {
                throw failure;
            }
        });
    }

    private void recover(Properties state) throws Exception {
        for (String key : new ArrayList<>(state.stringPropertyNames())) {
            if (key.startsWith("display.") && !alive.test(state.getProperty(key))) {
                state.remove(key);
            } else if (key.startsWith("app.")) {
                Set<String> owners = owners(state.getProperty(key));
                if (owners.removeIf(owner -> !alive.test(owner)) || owners.isEmpty()) {
                    state.setProperty(key, String.join(",", owners));
                    save(state);
                    try {
                        finishRelease(state, key, owners, "recovered");
                    } catch (Exception e) {
                        // A failed old package must not prevent an unrelated
                        // window from obtaining its own exemption.
                        log("restore-failed", key.substring(4), -1, "reason=" + error(e));
                    }
                }
            }
        }
        save(state);
    }

    private void finishRelease(Properties state, String key, Set<String> owners, String action) throws Exception {
        String pkg = key.substring(4);
        if (!owners.isEmpty()) {
            log("retained", pkg, -1, "owners=" + owners.size());
            return;
        }
        if (backend.contains(pkg)) {
            backend.remove(pkg);
        }
        if (backend.contains(pkg)) {
            throw new IOException("Whitelist removal did not pass readback");
        }
        state.remove(key);
        save(state);
        log(action, pkg, -1, "after=false verified=true");
    }

    private static Set<String> owners(String value) throws IOException {
        Set<String> result = new LinkedHashSet<>();
        if (value != null && !value.isEmpty()) {
            result.addAll(Arrays.asList(value.split(",", -1)));
            for (String owner : result) {
                if (!validOwner(owner)) {
                    throw new IOException("Invalid power-save journal owner");
                }
            }
        }
        return result;
    }

    private static boolean validOwner(String owner) {
        return owner != null && owner.matches("[A-Za-z0-9_-]+:[0-9]+:[0-9]+");
    }

    private void transaction(Operation operation) throws Exception {
        synchronized (LOCAL_LOCK) {
            File lockFile = new File(folder, ".scrcpy-ez-" + namespace + ".lock");
            try (RandomAccessFile lock = new RandomAccessFile(lockFile, "rw")) {
                long deadline = System.nanoTime() + 1500000000L;
                FileLock held = null;
                while (held == null && System.nanoTime() < deadline) {
                    try {
                        held = lock.getChannel().tryLock();
                    } catch (OverlappingFileLockException ignored) {
                        // Another class loader in this process may hold it.
                    }
                    if (held == null) {
                        Thread.sleep(20);
                    }
                }
                if (held == null) {
                    throw new IOException("Power-save journal lock timed out");
                }
                try (FileLock ignored = held) {
                    Properties state = new Properties();
                    File file = stateFile();
                    if (file.exists()) {
                        if (file.length() > 1024 * 1024) {
                            throw new IOException("Power-save journal is too large");
                        }
                        try (FileInputStream in = new FileInputStream(file)) {
                            state.load(in);
                        }
                        if (!"1".equals(state.getProperty("version"))) {
                            throw new IOException("Unsupported power-save journal version");
                        }
                        for (String key : state.stringPropertyNames()) {
                            if (key.startsWith("app.") && !validPackage(key.substring(4))) {
                                throw new IOException("Invalid journal package");
                            }
                        }
                    }
                    state.setProperty("version", "1");
                    operation.run(state);
                }
            }
        }
    }

    private File stateFile() {
        return new File(folder, ".scrcpy-ez-" + namespace + ".state");
    }

    private void save(Properties state) throws IOException {
        File next = new File(folder, ".scrcpy-ez-" + namespace + ".next");
        try (FileOutputStream out = new FileOutputStream(next)) {
            state.store(out, "EZ-owned battery exemptions; no application content");
            out.getFD().sync();
        }
        Files.move(next.toPath(), stateFile().toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
    }

    private void log(String action, String pkg, int display, String extra) {
        logger.accept(prefix + "action=" + action + " pkg=" + pkg + " display=" + display + " " + extra);
    }

    public static String error(Exception e) {
        Throwable cause = e;
        while (cause.getCause() != null && cause.getCause() != cause) {
            cause = cause.getCause();
        }
        return cause.getClass().getSimpleName() + ":" + String.valueOf(cause.getMessage()).replace('\n', ' ').replace('\r', ' ');
    }
}

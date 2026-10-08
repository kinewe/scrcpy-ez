package com.genymobile.scrcpy.notification;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.function.LongSupplier;
import java.util.function.Predicate;

/** Worker-owned, bounded application artwork cache. OEM artwork is only a fallback. */
final class NotificationIconResolver<T> {
    private static final int CACHE_LIMIT = 128;
    private static final long ICON_TTL_MS = 300000;
    private static final long FAILED_ICON_TTL_MS = 30000;

    interface Loader<T> {
        T load(String pkg) throws Exception;
    }

    interface Artwork<T> {
        T load() throws Exception;
    }

    private static final class Entry<T> {
        private final T value;
        private final long at;
        private final long ttl;

        Entry(T value, long at, long ttl) {
            this.value = value;
            this.at = at;
            this.ttl = ttl;
        }
    }

    private final Map<String, Entry<T>> cache = new LinkedHashMap<>(16, 0.75f, true);
    private final LongSupplier clock;
    private final Loader<T> loader;
    private final Predicate<T> usable;

    NotificationIconResolver(LongSupplier clock, Loader<T> loader, Predicate<T> usable) {
        this.clock = clock;
        this.loader = loader;
        this.usable = usable;
    }

    T application(String pkg) {
        Entry<T> cached = cache.get(pkg);
        if (cached != null && clock.getAsLong() - cached.at < cached.ttl) {
            return cached.value;
        }
        T value = null;
        try {
            value = loader.load(pkg);
        } catch (Exception | LinkageError ignored) {
            // Unavailable packages/resources must not suppress the notification text.
        }
        if (!valid(value)) {
            value = null;
        }
        return store(pkg, value);
    }

    T remember(String key, T value) {
        Entry<T> cached = cache.get(key);
        if (cached != null && clock.getAsLong() - cached.at < cached.ttl) {
            return cached.value;
        }
        return store(key, value);
    }

    private T store(String key, T value) {
        cache.put(key, new Entry<>(value, clock.getAsLong(), value == null ? FAILED_ICON_TTL_MS : ICON_TTL_MS));
        if (cache.size() > CACHE_LIMIT) {
            cache.remove(cache.keySet().iterator().next());
        }
        return value;
    }

    T resolve(String owner, String display, Artwork<T> fallback) {
        T value = application(display);
        if (value != null || owner.equals(display)) {
            return value;
        }
        try {
            value = fallback.load();
            if (valid(value)) {
                return value;
            }
        } catch (Exception | LinkageError ignored) {
            // Broken OEM extras must not prevent the owner-icon fallback.
        }
        // Do not cache notification-specific fallback art by package: it can change per notification.
        return application(owner);
    }

    private boolean valid(T value) {
        return value != null && usable.test(value);
    }
}

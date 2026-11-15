package com.genymobile.scrcpy.display;

import com.genymobile.scrcpy.model.Size;
import com.genymobile.scrcpy.util.Ln;

import android.os.SystemClock;

import java.util.function.LongSupplier;

public final class DisplayResizeDebouncer {

    private static final long DEBOUNCE_DELAY_MS = 300;

    public interface Callback {
        void trigger(Size size);
    }

    private final Callback callback;
    private final long delayMs;
    private final boolean leading;
    private final LongSupplier clock;
    private Size request;
    private long deadline;
    private long nextAllowed;
    private boolean stopped;

    private Thread thread;

    public DisplayResizeDebouncer(Callback callback) {
        this(callback, DEBOUNCE_DELAY_MS, false);
    }

    public DisplayResizeDebouncer(Callback callback, long delayMs, boolean leading) {
        this(callback, delayMs, leading, SystemClock::uptimeMillis);
    }

    DisplayResizeDebouncer(Callback callback, long delayMs, boolean leading, LongSupplier clock) {
        if (delayMs <= 0 || delayMs > 10000) {
            throw new IllegalArgumentException("Invalid resize interval");
        }
        this.callback = callback;
        this.delayMs = delayMs;
        this.leading = leading;
        this.clock = clock;
    }

    public synchronized void start() {
        if (thread != null || stopped) {
            throw new IllegalStateException("Resize worker already started or stopped");
        }
        thread = new Thread(this::debounce);
        thread.setName("debouncer");
        thread.setDaemon(true);
        thread.start();
    }

    public synchronized void stop() {
        stopped = true;
        request = null;
        notifyAll();
        if (thread != null) {
            thread.interrupt();
            thread = null;
        }
    }

    private void debounce() {
        try {
            while (true) {
                Size newSize;
                synchronized (this) {
                    while (true) {
                        if (stopped) {
                            return;
                        }
                        long now = clock.getAsLong();
                        if (request != null && now >= deadline) {
                            break;
                        }
                        if (request == null) {
                            wait();
                        } else {
                            assert now < deadline;
                            wait(deadline - now);
                        }
                    }
                    assert request != null : "An active deadline implies request != null";
                    newSize = request;
                    request = null;
                    // The worker is serial. Slow callbacks naturally delay the
                    // next update; requests arriving meanwhile keep only the latest.
                    nextAllowed = clock.getAsLong() + delayMs;
                }
                callback.trigger(newSize);
            }
        } catch (InterruptedException e) {
            // ignore
        } finally {
            Ln.d("Debouncer thread stopped");
        }
    }

    public synchronized void requestResize(Size size) {
        assert size != null;
        if (stopped) {
            return;
        }
        if (request == null) {
            long now = clock.getAsLong();
            deadline = leading ? Math.max(now, nextAllowed) : now + delayMs;
        }
        request = size;
        notify();
    }

    public synchronized void cancelResize() {
        request = null;
        deadline = 0;
        nextAllowed = 0;
        notify();
    }
}

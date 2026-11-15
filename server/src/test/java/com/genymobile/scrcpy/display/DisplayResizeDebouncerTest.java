package com.genymobile.scrcpy.display;

import com.genymobile.scrcpy.model.Size;
import com.genymobile.scrcpy.util.Ln;

import org.junit.After;
import org.junit.Before;
import org.junit.Test;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;

import static org.junit.Assert.*;

public class DisplayResizeDebouncerTest {
    private final AtomicLong clock = new AtomicLong();
    private final List<DisplayResizeDebouncer> workers = new ArrayList<>();

    @Before
    public void quietAndroidLogging() {
        Ln.initLogLevel(Ln.Level.ERROR);
    }

    @After
    public void stopWorkers() {
        for (DisplayResizeDebouncer worker : workers) {
            worker.stop();
        }
    }

    private DisplayResizeDebouncer start(DisplayResizeDebouncer.Callback callback, long interval, boolean leading) {
        DisplayResizeDebouncer worker = new DisplayResizeDebouncer(callback, interval, leading, clock::get);
        workers.add(worker);
        worker.start();
        return worker;
    }

    private void requestAt(DisplayResizeDebouncer worker, long now, Size size) {
        // Publish the fake clock and its arriving request atomically, so a real
        // worker wakeup cannot observe the new time before the test's event.
        synchronized (worker) {
            clock.set(now);
            worker.requestResize(size);
        }
    }

    private static Size take(BlockingQueue<Size> queue) throws Exception {
        Size size = queue.poll(2, TimeUnit.SECONDS);
        assertNotNull("Resize callback did not run at the supplied clock time", size);
        return size;
    }

    @Test
    public void firstUpdateIsImmediateAndContinuousRequestsKeepLatest() throws Exception {
        BlockingQueue<Size> queue = new LinkedBlockingQueue<>();
        DisplayResizeDebouncer worker = start(queue::add, 100, true);
        worker.requestResize(new Size(800, 600));
        assertEquals(new Size(800, 600), take(queue)); // fake clock never advanced
        requestAt(worker, 20, new Size(900, 600));
        requestAt(worker, 90, new Size(1000, 600));
        assertNull(queue.poll(40, TimeUnit.MILLISECONDS));
        requestAt(worker, 100, new Size(1100, 600));
        assertEquals(new Size(1100, 600), take(queue));
    }

    @Test
    public void finalSizeSurvivesWithoutSlidingItsDeadline() throws Exception {
        BlockingQueue<Size> queue = new LinkedBlockingQueue<>();
        DisplayResizeDebouncer worker = start(queue::add, 100, true);
        worker.requestResize(new Size(800, 600));
        take(queue);
        Size finalSize = new Size(800, 1200);
        requestAt(worker, 1, finalSize);
        requestAt(worker, 99, finalSize);
        assertNull(queue.poll(40, TimeUnit.MILLISECONDS));
        requestAt(worker, 100, finalSize); // wake the worker without extending 100
        assertEquals(finalSize, take(queue));
        assertNull(queue.poll(40, TimeUnit.MILLISECONDS));
    }

    @Test
    public void slowCallbacksNeverOverlapAndPendingRequestsAreCoalesced() throws Exception {
        CountDownLatch entered = new CountDownLatch(1);
        CountDownLatch release = new CountDownLatch(1);
        AtomicInteger active = new AtomicInteger();
        AtomicInteger maximum = new AtomicInteger();
        BlockingQueue<Size> queue = new LinkedBlockingQueue<>();
        DisplayResizeDebouncer worker = start(size -> {
            maximum.accumulateAndGet(active.incrementAndGet(), Math::max);
            try {
                if (size.getWidth() == 800) {
                    entered.countDown();
                    release.await(2, TimeUnit.SECONDS);
                }
                queue.add(size);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            } finally {
                active.decrementAndGet();
            }
        }, 100, true);
        try {
            worker.requestResize(new Size(800, 600));
            assertTrue(entered.await(2, TimeUnit.SECONDS));
            requestAt(worker, 100, new Size(900, 600));
            worker.requestResize(new Size(1000, 600));
            assertNull(queue.poll(40, TimeUnit.MILLISECONDS));
            release.countDown();
            assertEquals(new Size(800, 600), take(queue));
            assertEquals(new Size(1000, 600), take(queue));
            assertEquals(1, maximum.get());
        } finally {
            release.countDown();
        }
    }

    @Test
    public void cancelDropsPendingSizeAndNextUpdateCanStartImmediately() throws Exception {
        BlockingQueue<Size> queue = new LinkedBlockingQueue<>();
        DisplayResizeDebouncer worker = start(queue::add, 100, true);
        worker.requestResize(new Size(800, 600));
        take(queue);
        requestAt(worker, 20, new Size(900, 600));
        worker.cancelResize();
        worker.requestResize(new Size(1000, 600));
        assertEquals(new Size(1000, 600), take(queue));
        assertNull(queue.poll(40, TimeUnit.MILLISECONDS));
    }

    @Test
    public void stopDropsPendingAndLateRequests() throws Exception {
        BlockingQueue<Size> queue = new LinkedBlockingQueue<>();
        DisplayResizeDebouncer worker = start(queue::add, 100, true);
        worker.requestResize(new Size(800, 600));
        take(queue);
        worker.requestResize(new Size(900, 600));
        worker.stop();
        requestAt(worker, 1000, new Size(1000, 600));
        assertNull(queue.poll(80, TimeUnit.MILLISECONDS));
    }

    @Test
    public void legacyModeRetainsIts300MillisecondFirstWindow() throws Exception {
        BlockingQueue<Size> queue = new LinkedBlockingQueue<>();
        DisplayResizeDebouncer worker = start(queue::add, 300, false);
        worker.requestResize(new Size(800, 600));
        assertNull(queue.poll(40, TimeUnit.MILLISECONDS));
        requestAt(worker, 299, new Size(900, 600));
        assertNull(queue.poll(40, TimeUnit.MILLISECONDS));
        requestAt(worker, 300, new Size(1000, 600));
        assertEquals(new Size(1000, 600), take(queue));
    }

    @Test
    public void invalidIntervalsAreRejected() {
        assertThrows(IllegalArgumentException.class,
                () -> new DisplayResizeDebouncer(size -> { }, 0, true, clock::get));
        assertThrows(IllegalArgumentException.class,
                () -> new DisplayResizeDebouncer(size -> { }, 10001, true, clock::get));
    }
}

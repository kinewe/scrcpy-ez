package com.genymobile.scrcpy;

import android.os.IBinder;
import android.os.ParcelFileDescriptor;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;

/**
 * Bounded IWindow commands using the normal Android DUMP permission of shell.
 * NativeAlphaProbe reads the View tree and temporarily changes camera/background
 * properties through this interface; pixel capture uses HardwareBuffer instead.
 */
public final class WindowProbe {
    static Object invoke(Object o, String n, Class<?>[] t, Object... a) throws Exception {
        return o.getClass().getMethod(n, t).invoke(o, a);
    }

    static byte[] command(IBinder token, String command, String params, int cap) throws Exception {
        return command(token,command,params,cap,8000);
    }

    static byte[] command(IBinder token, String command, String params, int cap, int timeoutMs) throws Exception {
        Class<?> stub = Class.forName("android.view.IWindow$Stub");
        Object window = stub.getMethod("asInterface", IBinder.class).invoke(null, token);
        ParcelFileDescriptor[] pipe = ParcelFileDescriptor.createPipe();
        ExecutorService pool = Executors.newSingleThreadExecutor(r -> {
            Thread x = new Thread(r);
            x.setDaemon(true);
            return x;
        });
        Future<byte[]> read = pool.submit(() -> {
            try (InputStream in = new ParcelFileDescriptor.AutoCloseInputStream(pipe[0]);
                    ByteArrayOutputStream out = new ByteArrayOutputStream()) {
                byte[] b = new byte[8192];
                int n;
                while ((n = in.read(b)) >= 0) {
                    if (out.size() + n > cap) {
                        throw new IOException("diagnostic byte cap");
                    }
                    out.write(b, 0, n);
                }
                return out.toByteArray();
            }
        });
        try {
            invoke(window, "executeCommand",
                    new Class[]{String.class, String.class, ParcelFileDescriptor.class},
                    command, params, pipe[1]);
            pipe[1].close();
            return read.get(Math.max(1,timeoutMs), TimeUnit.MILLISECONDS);
        } finally {
            pipe[0].close();
            pipe[1].close();
            read.cancel(true);
            pool.shutdownNow();
        }
    }

    private WindowProbe() {
    }
}

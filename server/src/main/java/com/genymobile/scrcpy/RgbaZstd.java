package com.genymobile.scrcpy;

import android.os.Build;
import com.github.luben.zstd.Zstd;
import com.github.luben.zstd.ZstdCompressCtx;
import com.github.luben.zstd.util.Native;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;

final class RgbaZstd implements AutoCloseable {
    private ZstdCompressCtx compressor;
    private boolean unavailable;
    byte[] output = new byte[0];

    int compress(byte[] input, int length) {
        if (unavailable) return 0;
        try {
            if (compressor == null) open();
            int capacity = (int) Zstd.compressBound(length);
            if (output.length < capacity) output = new byte[capacity];
            return compressor.compressByteArray(output, 0, output.length, input, 0, length);
        } catch (IOException | LinkageError | RuntimeException failure) {
            unavailable = true;
            System.out.println("NATIVE_ALPHA_ZSTD unavailable=" + failure.getClass().getSimpleName()
                    + " fallback=deflate detail=" + failure.getMessage());
            close();
            return 0;
        }
    }

    private void open() throws IOException {
        if (!Native.isLoaded()) {
            String abi = Build.SUPPORTED_ABIS[0];
            String resource = "/ez-zstd/" + abi + "/libzstd-jni.so";
            InputStream packaged = RgbaZstd.class.getResourceAsStream(resource);
            if (packaged == null) {
                packaged = RgbaZstd.class.getResourceAsStream("/lib/" + abi + "/libzstd-jni-1.5.7-12.so");
            }
            try (InputStream binary = packaged) {
                if (binary == null) throw new IOException("No packaged library for " + abi);
                File deviceLibrary = File.createTempFile("ez-zstd-rc30-", ".so", new File("/data/local/tmp"));
                try {
                    try (FileOutputStream target = new FileOutputStream(deviceLibrary)) {
                        byte[] buffer = new byte[32768];
                        for (int count; (count = binary.read(buffer)) != -1;) {
                            target.write(buffer, 0, count);
                        }
                    }
                    if (!deviceLibrary.setReadOnly()) throw new IOException("Could not protect extracted library");
                    System.load(deviceLibrary.getAbsolutePath());
                    Native.assumeLoaded();
                } finally {
                    if (!deviceLibrary.delete()) {
                        System.out.println("NATIVE_ALPHA_ZSTD cleanup_pending=" + deviceLibrary.getName());
                    }
                }
            }
        }
        compressor = new ZstdCompressCtx();
        compressor.setLevel(1);
        System.out.println("NATIVE_ALPHA_ZSTD active level=1");
    }

    @Override
    public void close() {
        if (compressor != null) {
            compressor.close();
            compressor = null;
        }
    }
}

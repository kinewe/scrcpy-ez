package com.genymobile.scrcpy.wrappers;

import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.util.ClipboardJournal;
import com.genymobile.scrcpy.util.Ln;

import android.content.ClipData;
import android.content.ClipDescription;
import android.content.ContentResolver;
import android.content.ContentValues;
import android.content.Context;
import android.content.Intent;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.provider.MediaStore;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.UUID;

public final class ClipboardManager {

    public static class ClipboardImage {
        private final String mimeType;
        private final byte[] data;

        public ClipboardImage(String mimeType, byte[] data) {
            this.mimeType = mimeType;
            this.data = data;
        }

        public String mimeType() {
            return mimeType;
        }

        public byte[] data() {
            return data;
        }
    }

    private final android.content.ClipboardManager manager;

    private File cachedFolder;

    private ClipboardJournal journal;

    private synchronized ClipboardJournal journal() throws IOException {
        if (journal == null) {
            Context context = FakeContext.get();
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
                context = context.createDeviceProtectedStorageContext();
            }
            cachedFolder = new File(context.getFilesDir(), "bugreports");
            if (!cachedFolder.isDirectory() && !cachedFolder.mkdirs()) {
                throw new IOException("Cannot create clipboard cache");
            }
            journal = new ClipboardJournal(cachedFolder);
        }
        return journal;
    }

    private static byte[] snapshotKey(ClipData clip) {
        if (clip == null || clip.getItemCount() == 0) {
            return ClipboardJournal.digest("empty", new byte[0]);
        }
        ClipDescription desc = clip.getDescription();
        StringBuilder key = new StringBuilder();
        if (desc != null) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                key.append(desc.getTimestamp());
            }
            key.append('\0').append(desc.getLabel());
            for (int i = 0; i < desc.getMimeTypeCount(); ++i) {
                key.append('\0').append(desc.getMimeType(i));
            }
        }
        ClipData.Item item = clip.getItemAt(0);
        key.append('\0').append(item.getUri()).append('\0').append(item.getText());
        return ClipboardJournal.digest("snapshot", key.toString().getBytes(StandardCharsets.UTF_8));
    }

    @SuppressWarnings("checkstyle:VisibilityModifier") // immutable snapshot value
    public static final class ClipboardSnapshot {
        public final String text;
        public final ClipboardImage image;
        public final long revision;
        ClipboardSnapshot(String text, ClipboardImage image, long revision) {
            this.text = text;
            this.image = image;
            this.revision = revision;
        }
    }

    public ClipboardSnapshot readSnapshot(boolean ignoreRemote) {
        return readSnapshot(ignoreRemote, 0);
    }

    public ClipboardSnapshot readSnapshot(boolean ignoreRemote, int imageLimit) {
        ClipData clip = manager.getPrimaryClip();
        if (clip == null || clip.getItemCount() == 0) {
            return null;
        }
        byte[] key = snapshotKey(clip);
        try {
            long revision = journal().withLock(record -> {
                if (ignoreRemote && record.isRemote(key)) {
                    return 0L;
                }
                return record.observe(key);
            });
            if (revision == 0) {
                return null;
            }
            ClipboardImage image = getImage(clip, imageLimit);
            if (image == null && clip.getDescription() != null && clip.getDescription().hasMimeType("image/*")) {
                return null; // unreadable images must not replace the PC clipboard with fallback text
            }
            CharSequence text = clip.getItemAt(0).getText();
            // The URI may take time to read. Never enqueue a superseded copy.
            if (!Arrays.equals(key, snapshotKey(manager.getPrimaryClip()))) {
                return null;
            }
            return new ClipboardSnapshot(text == null ? null : text.toString(), image, revision);
        } catch (IOException e) {
            Ln.e("Clipboard coordination unavailable", e);
            return null;
        }
    }

    static ClipboardManager create() {
        android.content.ClipboardManager manager = (android.content.ClipboardManager) FakeContext.get().getSystemService(Context.CLIPBOARD_SERVICE);
        if (manager == null) {
            // Some devices have no clipboard manager
            // <https://github.com/Genymobile/scrcpy/issues/1440>
            // <https://github.com/Genymobile/scrcpy/issues/1556>
            return null;
        }
        return new ClipboardManager(manager);
    }

    private ClipboardManager(android.content.ClipboardManager manager) {
        this.manager = manager;
    }

    public CharSequence getText() {
        ClipData clipData = manager.getPrimaryClip();
        if (clipData == null || clipData.getItemCount() == 0) {
            return null;
        }
        return clipData.getItemAt(0).getText();
    }

    public ClipboardImage getImage(int imageLimit) {
        return getImage(manager.getPrimaryClip(), imageLimit);
    }

    private ClipboardImage getImage(ClipData clipData, int imageLimit) {
        if (clipData == null || clipData.getItemCount() == 0) {
            return null;
        }

        ClipData.Item item = clipData.getItemAt(0);

        // Check if it's an image URI
        ClipDescription description = clipData.getDescription();
        if (description != null) {
            String[] mimeTypes = description.filterMimeTypes("image/*");
            // Some ROMs (e.g. Xiaomi HyperOS) may return null instead of an empty array
            if (mimeTypes == null || mimeTypes.length == 0) {
                return null;
            }
            String mimeType = mimeTypes[0];
            if (mimeType == null) {
                return null;
            }

            Uri uri = item.getUri();
            if (uri == null) {
                return null;
            }

            try (InputStream inputStream = FakeContext.get().getContentResolver().openInputStream(uri);
                ByteArrayOutputStream outputStream = new ByteArrayOutputStream()) {

                if (inputStream != null) {
                    byte[] buffer = new byte[8192];
                    int bytesRead;
                    while ((bytesRead = inputStream.read(buffer)) != -1) {
                        if (outputStream.size() > (1 << 28) - 1024 - bytesRead) {
                            throw new IOException("Clipboard image exceeds protocol limit");
                        }
                        if (imageLimit > 0 && outputStream.size() > imageLimit - bytesRead) {
                            Ln.i("Image clipboard: scanner skipped oversized image");
                            return null;
                        }
                        outputStream.write(buffer, 0, bytesRead);
                    }
                    return new ClipboardImage(mimeType, outputStream.toByteArray());
                }
            } catch (IOException | RuntimeException e) {
                // Failed to read image data from URI (openInputStream may also throw SecurityException,
                // e.g. while another app is reading the URI, or the file is missing/inaccessible)
                Ln.e("Failed to read clipboard image", e);
            }
        }

        return null;
    }

    public boolean setImage(byte[] imageData, String mimeType, long epoch, long version) {
        try {
            ClipboardJournal sharedJournal = journal();
            byte[] content = ClipboardJournal.digest(mimeType, imageData);
            boolean duplicate = sharedJournal.withLock(record ->
                !record.accept(epoch, version)
                || (record.isRemote(snapshotKey(manager.getPrimaryClip())) && Arrays.equals(record.remoteContent, content)));
            if (duplicate) {
                return true;
            }

            // Convert the image to PNG when possible: Android supports BMP decoding only
            // since API 24, and some apps (WeChat, QQ...) show a BMP clipboard item as a
            // file instead of an image. A PNG clipboard item is recognized as an image.
            byte[] finalData = imageData;
            String finalMimeType = mimeType;
            String extension;
            if ("image/png".equals(mimeType)) {
                // Already PNG: keep the data as-is. The file MUST still have a
                // ".png" extension: without it, the bugreport content provider
                // reports application/octet-stream, and apps (WeChat, QQ...)
                // display the clipboard item as a plain file instead of an image.
                finalData = imageData;
                finalMimeType = "image/png";
                extension = ".png";
            } else if ("image/gif".equals(mimeType) || "image/webp".equals(mimeType)
                    || "image/jpeg".equals(mimeType) || "image/jpg".equals(mimeType)) {
                // Animated / special formats (GIF, WebP) and JPEG: keep the data
                // as-is. BitmapFactory.decodeByteArray() would only keep the
                // first frame of an animation (losing it), and re-encoding JPEG
                // to PNG would not improve quality while wasting space.
                finalData = imageData;
                finalMimeType = mimeType;
                extension = extensionFor(mimeType);
            } else {
                Bitmap bitmap = BitmapFactory.decodeByteArray(imageData, 0, imageData.length);
                if (bitmap != null) {
                    try (ByteArrayOutputStream outputStream = new ByteArrayOutputStream()) {
                        if (bitmap.compress(Bitmap.CompressFormat.PNG, 100, outputStream)) {
                            finalData = outputStream.toByteArray();
                            finalMimeType = "image/png";
                            extension = ".png";
                        } else {
                            // Compression failed: keep the original data, but use
                            // an extension matching the mime type.
                            extension = extensionFor(mimeType);
                        }
                    } finally {
                        bitmap.recycle();
                    }
                } else {
                    // Decoding failed: keep the original data, but use an
                    // extension matching the mime type.
                    extension = extensionFor(mimeType);
                }
            }

            // Use a unique file name per image (clipboard_<millis>_<uuid><ext>): some
            // apps (WeChat, QQ...) cache the content of a clipboard URI, so reusing
            // the same file name made them display a stale preview (the previously
            // copied image) while sending the new one.
            final byte[] savedData = finalData;
            final String savedMime = finalMimeType;
            final String savedExtension = extension;
            return sharedJournal.withLock(record -> {
                if (!record.accept(epoch, version)) {
                    return true;
                }
                if (record.isRemote(snapshotKey(manager.getPrimaryClip())) && Arrays.equals(record.remoteContent, content)) {
                    return true; // another server applied it while we decoded
                }
                String fileName = uniqueFileName(savedExtension);

                // Use atomic write: write to temporary file first, then move to final location
                File tempFile = File.createTempFile("clipboard_upload_", ".tmp", cachedFolder);
                try (FileOutputStream fos = new FileOutputStream(tempFile)) {
                    fos.write(savedData);
                } catch (IOException e) {
                    tempFile.delete();
                    throw e;
                }

                File finalFile = new File(cachedFolder, fileName);
                if (!tempFile.renameTo(finalFile)) {
                    tempFile.delete();
                    throw new IOException("Cannot commit clipboard image");
                }

                // Retain the previous URI for slow readers after replacing it.
                ClipData previous = manager.getPrimaryClip();
                Uri previousUri = previous != null && previous.getItemCount() > 0 ? previous.getItemAt(0).getUri() : null;
                String previousName = previousUri != null && "com.android.shell".equals(previousUri.getAuthority())
                        ? previousUri.getLastPathSegment() : null;
                if (previousName != null && previousName.startsWith("clipboard_") && !previousName.contains("/")) {
                    new File(cachedFolder, previousName).setLastModified(System.currentTimeMillis());
                }
                cleanupOldFiles(fileName, previousName);

                android.net.Uri uri = android.net.Uri.parse("content://com.android.shell/bugreports/" + fileName);

                ClipData clipData = new ClipData(
                    fileName,
                    new String[]{savedMime},
                    new ClipData.Item(uri)
                );
                manager.setPrimaryClip(clipData);
                ClipData applied = manager.getPrimaryClip();
                // A device app may race with setPrimaryClip. Only mark our own URI.
                if (applied != null && applied.getItemCount() > 0 && uri.equals(applied.getItemAt(0).getUri())) {
                    record.remote = snapshotKey(applied);
                    record.remoteContent = content;
                    record.observe(record.remote);
                }

                // Android does not automatically grant read access to the clipboard URI
                // to apps other than the current foreground one, so input methods and
                // other apps (WeChat, QQ...) trying to read it in the background (e.g.
                // to save it in the input method clipboard history) would fail with a
                // SecurityException. Explicitly grant read access to the default input
                // method and to common apps.
                grantClipboardUriReadPermission(uri);

                return true;
            });
        } catch (Exception e) {
            Ln.e("Failed to set image clipboard", e);
            return false;
        }
    }

    public boolean setText(CharSequence text, long epoch, long version) {
        try {
            return journal().withLock(record -> {
                if (!record.accept(epoch, version)) {
                    return true;
                }
                CharSequence current = getText();
                if (current != null && current.toString().contentEquals(text)) {
                    return true;
                }
                ClipData clipData = ClipData.newPlainText("scrcpy-ez", text);
                manager.setPrimaryClip(clipData);
                ClipData applied = manager.getPrimaryClip();
                if (applied != null && applied.getItemCount() > 0
                        && applied.getItemAt(0).getText() != null
                        && text.toString().contentEquals(applied.getItemAt(0).getText())) {
                    record.remote = snapshotKey(applied);
                    record.remoteContent = ClipboardJournal.digest("text/plain", text.toString().getBytes(StandardCharsets.UTF_8));
                    record.observe(record.remote);
                }
                return true;
            });
        } catch (Exception e) {
            Ln.e("Failed to set text clipboard", e);
            return false;
        }
    }

    /**
     * Map a MIME type to a file extension, used when the image cannot be
     * converted to PNG (the content provider derives the MIME type from the
     * file extension; a file without extension is reported as
     * application/octet-stream and is not recognized as an image by apps).
     */
    private static String extensionFor(String mimeType) {
        if (mimeType == null) {
            return ".img";
        }
        switch (mimeType) {
            case "image/png":
                return ".png";
            case "image/jpeg":
            case "image/jpg":
                return ".jpg";
            case "image/gif":
                return ".gif";
            case "image/webp":
                return ".webp";
            case "image/bmp":
                return ".bmp";
            case "image/heic":
            case "image/heif":
                return ".heic";
            default:
                return ".img";
        }
    }

    /**
     * Generate a unique file name for the clipboard image:
     * "clipboard_&lt;millis&gt;_&lt;uuid&gt;&lt;ext&gt;". Each call returns a different
     * name, so the content URI is unique and apps cannot use a cached content
     * for a previous image.
     */
    private static synchronized String uniqueFileName(String extension) {
        return "clipboard_" + System.currentTimeMillis() + "_" + UUID.randomUUID() + extension;
    }

    /**
     * Expire files older than one day, retaining the current and previous URI.
     */
    private void cleanupOldFiles(String currentFileName, String previousFileName) {
        File[] files = cachedFolder.listFiles();
        if (files == null) {
            return;
        }
        for (File file : files) {
            String name = file.getName();
            // Keep recent URIs readable by clipboard history and slow consumers.
            // Cleanup never touches the current URI or another in-flight write.
            if (name.startsWith("clipboard_") && !name.equals(currentFileName) && !name.equals(previousFileName)
                    && !name.startsWith("clipboard_upload_")
                    && System.currentTimeMillis() - file.lastModified() > 24L * 60 * 60 * 1000 && file.delete()) {
                Ln.d("Deleted old clipboard image file: " + name);
            }
        }
    }

    /**
     * Grant read access to the clipboard image URI to the current default input
     * method and to common apps which may need to read it in the background
     * (input method clipboard history, WeChat, QQ...).
     * <p>
     * The server runs with the shell uid, which owns the "com.android.shell"
     * content provider, so the grants are allowed.
     */
    private void grantClipboardUriReadPermission(Uri uri) {
        java.util.Set<String> packages = new java.util.HashSet<>();
        // The current default input method (e.g. "com.baidu.input_mi/.ImeService")
        try {
            String defaultIme = android.provider.Settings.Secure.getString(
                    FakeContext.get().getContentResolver(),
                    android.provider.Settings.Secure.DEFAULT_INPUT_METHOD);
            if (defaultIme != null) {
                String pkg = defaultIme.split("/")[0].trim();
                if (!pkg.isEmpty()) {
                    packages.add(pkg);
                }
            }
        } catch (Exception e) {
            Ln.d("Failed to get default input method: " + e.getMessage());
        }
        // Common input methods and apps which may read the clipboard URI in the background
        packages.add("com.baidu.input"); // Baidu IME
        packages.add("com.baidu.input_mi"); // Baidu IME (Xiaomi)
        packages.add("com.sohu.inputmethod.sogou"); // Sogou IME
        packages.add("com.sohu.inputmethod.sogou.xiaomi"); // Sogou IME (Xiaomi)
        packages.add("com.google.android.inputmethod.latin"); // Gboard
        packages.add("com.iflytek.inputmethod.miui"); // iFlytek IME (Xiaomi)
        packages.add("com.tencent.mm"); // WeChat
        packages.add("com.tencent.mobileqq"); // QQ
        packages.add("com.tencent.tim"); // TIM
        for (String pkg : packages) {
            try {
                FakeContext.get().grantUriPermission(pkg, uri, Intent.FLAG_GRANT_READ_URI_PERMISSION);
                Ln.i("Granted clipboard URI read permission to " + pkg);
            } catch (Exception e) {
                // The package may not be installed; the grant is best-effort.
                Ln.d("Failed to grant clipboard URI read permission to " + pkg + ": " + e.getMessage());
            }
        }
    }

    public void addPrimaryClipChangedListener(android.content.ClipboardManager.OnPrimaryClipChangedListener listener) {
        manager.addPrimaryClipChangedListener(listener);
    }

    /**
     * Copy the most recent clipboard image file (clipboard_*) from the device
     * clipboard cache (bugreports dir) to the public gallery directory and
     * return the destination path, or null on failure. The media scan is
     * triggered by the caller.
     */
    public String saveLatestImageToGallery(int imageLimit) {
        try {
            journal(); // another casting server may have cached the image
            ClipData current = manager.getPrimaryClip();
            Uri currentUri = current != null && current.getItemCount() > 0 ? current.getItemAt(0).getUri() : null;
            String currentName = currentUri != null && "com.android.shell".equals(currentUri.getAuthority())
                    ? currentUri.getLastPathSegment() : null;
            File[] files = cachedFolder.listFiles();
            if (files == null) {
                return null;
            }
            File latest = currentName != null && currentName.startsWith("clipboard_") && !currentName.contains("/")
                    ? new File(cachedFolder, currentName) : null;
            if (latest != null && !latest.isFile()) {
                latest = null;
            }
            long latestTime = Long.MIN_VALUE;
            for (File f : latest == null ? files : new File[0]) {
                String name = f.getName();
                if (!name.startsWith("clipboard_") || name.endsWith(".tmp")) {
                    continue;
                }
                // Retention refreshes mtime on the previous URI. Select by
                // creation time embedded in the filename instead.
                long created = f.lastModified();
                try {
                    created = Long.parseLong(name.substring(10, name.indexOf('_', 10)));
                } catch (IndexOutOfBoundsException | NumberFormatException ignored) {
                    // legacy filenames without an embedded timestamp
                }
                if (created > latestTime) {
                    latestTime = created;
                    latest = f;
                }
            }
            if (latest == null) {
                Ln.w("No clipboard image file found");
                return null;
            }
            if (imageLimit > 0 && latest.length() > imageLimit) {
                Ln.i("Image clipboard: scanner skipped saving oversized image to gallery");
                return null;
            }
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                // Android 10+ scoped storage: insert through MediaStore so the
                // gallery picks the image up automatically (a media scan is not
                // even needed).
                String name = latest.getName();
                String mime = "image/*";
                String lower = name.toLowerCase();
                if (lower.endsWith(".png")) {
                    mime = "image/png";
                } else if (lower.endsWith(".jpg") || lower.endsWith(".jpeg")) {
                    mime = "image/jpeg";
                } else if (lower.endsWith(".gif")) {
                    mime = "image/gif";
                } else if (lower.endsWith(".webp")) {
                    mime = "image/webp";
                }
                ContentValues values = new ContentValues();
                values.put(MediaStore.Images.Media.DISPLAY_NAME, name);
                values.put(MediaStore.Images.Media.MIME_TYPE, mime);
                values.put(MediaStore.Images.Media.RELATIVE_PATH,
                        Environment.DIRECTORY_DCIM + "/Camera");
                ContentResolver cr = FakeContext.get().getContentResolver();
                Uri uri = cr.insert(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, values);
                if (uri != null) {
                    try (java.io.OutputStream os = cr.openOutputStream(uri)) {
                        java.nio.file.Files.copy(latest.toPath(), os);
                    }
                    Ln.i("Saved clipboard image to gallery (MediaStore): " + uri);
                    return null; // inserted via MediaStore: no media scan needed
                }
                Ln.w("MediaStore insert returned null, falling back to direct write");
            }
            File dcim = Environment.getExternalStoragePublicDirectory(
                    Environment.DIRECTORY_DCIM);
            File cameraDir = new File(dcim, "Camera");
            if (!cameraDir.exists() && !cameraDir.mkdirs()) {
                Ln.e("Could not create gallery directory: " + cameraDir);
                return null;
            }
            File dest = new File(cameraDir, latest.getName());
            try (java.io.FileInputStream in = new java.io.FileInputStream(latest);
                 java.io.FileOutputStream out = new java.io.FileOutputStream(dest)) {
                byte[] buffer = new byte[8192];
                int read;
                while ((read = in.read(buffer)) != -1) {
                    out.write(buffer, 0, read);
                }
            }
            Ln.i("Saved clipboard image to gallery: " + dest.getAbsolutePath());
            return dest.getAbsolutePath();
        } catch (Exception e) {
            Ln.e("Failed to save clipboard image to gallery", e);
            return null;
        }
    }
}

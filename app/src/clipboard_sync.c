#include "clipboard_sync.h"

#include <windows.h>
#include <SDL3/SDL.h>
#include <libavutil/sha.h>
#include <libavutil/mem.h>
#include <stdlib.h>
#include <string.h>

#include "image_convert.h"
#include "control_msg.h"
#include "util/log.h"

#define SYNC_MAGIC 0x216CB001u
#define SYNC_DEVICES 1 // each identity has its own mapping, no device-count limit
#define TX_AUTO (UINT64_C(1) << 63)
#define TX_FAILED (UINT64_C(1) << 62)
#define IMAGE_LIMIT (256u * 1024u * 1024u)

enum transfer_state { IDLE, PENDING, DONE, FAILED };
struct device_state {
    uint8_t key[32];
    uint64_t received_revision;
    uint64_t transaction;
    uint64_t owner_created;
    uint64_t retry_after;
    uint32_t pc_sequence;
    uint32_t owner;
    uint32_t attempts;
    uint32_t state;
};
struct shared_state {
    uint32_t magic;
    uint64_t counter;
    uint64_t epoch;
    uint64_t copy_version;
    uint32_t copy_sequence;
    uint8_t copy_digest[32];
    struct device_state devices[SYNC_DEVICES];
};
struct clipboard_marker {
    uint32_t magic;
    uint32_t kind; // 1=text, 2=image
    uint64_t size;
    uint8_t source[32];
    uint8_t digest[32];
    char mime[64];
    uint64_t writer_window;
};

static HANDLE sync_mutex, sync_mapping;
static struct shared_state *shared;
static struct device_state *device;
static uint8_t device_key[32];
static uint64_t process_created;
static UINT marker_format, image_format;
static HWND clipboard_window;

static bool
digest(const char *mime, const void *data, size_t size, uint8_t out[32]) {
    struct AVSHA *sha = av_sha_alloc();
    if (!sha) {
        return false;
    }
    av_sha_init(sha, 256);
    av_sha_update(sha, (const uint8_t *) mime, strlen(mime) + 1);
    av_sha_update(sha, data, size);
    av_sha_final(sha, out);
    av_free(sha);
    return true;
}

static bool
lock_shared_for(DWORD timeout) {
    if (!sync_mutex || !shared) {
        return false;
    }
    DWORD r = WaitForSingleObject(sync_mutex, timeout);
    return r == WAIT_OBJECT_0 || r == WAIT_ABANDONED;
}

static bool
lock_shared(void) {
    return lock_shared_for(100);
}

static uint64_t
creation_time(HANDLE process) {
    FILETIME created, exited, kernel, user;
    if (!GetProcessTimes(process, &created, &exited, &kernel, &user)) {
        return 0;
    }
    return ((uint64_t) created.dwHighDateTime << 32) | created.dwLowDateTime;
}

static bool
owner_alive(const struct device_state *d) {
    HANDLE h = OpenProcess(SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION,
                            FALSE, d->owner);
    if (!h) {
        // An access failure is not evidence that a live sender has exited.
        return GetLastError() != ERROR_INVALID_PARAMETER;
    }
    bool alive = WaitForSingleObject(h, 0) == WAIT_TIMEOUT
              && creation_time(h) == d->owner_created;
    CloseHandle(h);
    return alive;
}

bool
sc_clipboard_sync_init(const char *serial) {
    const char *identity = getenv("SCEZ_EXPECT_SERIAL");
    if (!identity || !*identity) {
        identity = serial;
    }
    if (!digest("device", identity, strlen(identity), device_key)) {
        return false;
    }
    marker_format = RegisterClipboardFormatW(L"scrcpy-ez clipboard origin v2.2.16");
    image_format = RegisterClipboardFormatW(L"scrcpy-ez original image v2.2.16");
    // A real owner window is required by EmptyClipboard/SetClipboardData.
    clipboard_window = CreateWindowExW(0, L"STATIC", L"scrcpy-ez clipboard",
                                       0, 0, 0, 0, 0, HWND_MESSAGE, NULL,
                                       GetModuleHandleW(NULL), NULL);
    process_created = creation_time(GetCurrentProcess());
    sync_mutex = CreateMutexW(NULL, FALSE, L"Local\\scrcpy-ez.clipboard.2.2.16.mutex");
    wchar_t mapping_name[128] = L"Local\\scrcpy-ez.clipboard.2.2.16.";
    size_t prefix = wcslen(mapping_name);
    static const wchar_t hex[] = L"0123456789abcdef";
    for (unsigned i = 0; i < 32; ++i) {
        mapping_name[prefix + i * 2] = hex[device_key[i] >> 4];
        mapping_name[prefix + i * 2 + 1] = hex[device_key[i] & 15];
    }
    mapping_name[prefix + 64] = 0;
    sync_mapping = CreateFileMappingW(INVALID_HANDLE_VALUE, NULL, PAGE_READWRITE,
                                      0, 4096, // supervisor retains this across route switches
                                      mapping_name);
    if (!clipboard_window || !sync_mutex || !sync_mapping
            || !marker_format || !image_format) {
        sc_clipboard_sync_destroy();
        return false;
    }
    shared = MapViewOfFile(sync_mapping, FILE_MAP_ALL_ACCESS, 0, 0, sizeof(*shared));
    if (!lock_shared()) {
        sc_clipboard_sync_destroy();
        return false;
    }
    if (shared->magic != SYNC_MAGIC) {
        memset(shared, 0, sizeof(*shared));
        shared->magic = SYNC_MAGIC;
        LARGE_INTEGER now;
        QueryPerformanceCounter(&now);
        shared->epoch = (uint64_t) now.QuadPart ^ process_created;
    }
    for (unsigned i = 0; i < SYNC_DEVICES; ++i) {
        struct device_state *d = &shared->devices[i];
        if (!d->key[0] && !memcmp(d->key, (uint8_t[32]) {0}, 32)) {
            memcpy(d->key, device_key, 32);
        }
        if (!memcmp(d->key, device_key, 32)) {
            device = d;
            break;
        }
    }
    ReleaseMutex(sync_mutex);
    if (!device) {
        sc_clipboard_sync_destroy();
        return false;
    }
    return true;
}

void
sc_clipboard_sync_disconnected(void) {
    if (device && lock_shared_for(INFINITE)) {
        if (device->state == PENDING && device->owner == GetCurrentProcessId()
                && device->owner_created == process_created) {
            device->state = IDLE;
            device->owner = 0;
            LOGD("Clipboard transfer released after connection shutdown");
        }
        ReleaseMutex(sync_mutex);
    }
}

void
sc_clipboard_sync_destroy(void) {
    sc_clipboard_sync_disconnected();
    device = NULL;
    if (shared) { UnmapViewOfFile(shared); shared = NULL; }
    if (sync_mapping) { CloseHandle(sync_mapping); sync_mapping = NULL; }
    if (sync_mutex) { CloseHandle(sync_mutex); sync_mutex = NULL; }
    if (clipboard_window) { DestroyWindow(clipboard_window); clipboard_window = NULL; }
}

bool
sc_clipboard_sync_begin(uint32_t pc_sequence, uint64_t *transaction) {
    if (!device || !pc_sequence || !lock_shared()) {
        return false; // fail closed: never reintroduce an uncoordinated loop
    }
    bool start = false;
    if ((device->state == PENDING && owner_alive(device))
            || (device->pc_sequence == pc_sequence && device->state == DONE)) {
        goto end;
    }
    // Origin is a clipboard format, so it survives the process that received
    // it and is visible to new sessions and USB/Wi-Fi replacement processes.
    if (OpenClipboard(clipboard_window)) {
        HANDLE h = GetClipboardData(marker_format);
        const struct clipboard_marker *m = h && GlobalSize(h) >= sizeof(*m)
                                         ? GlobalLock(h) : NULL;
        bool local_source = m && m->magic == SYNC_MAGIC
                         && !memcmp(m->source, device_key, 32)
                         && (!GetClipboardOwner()
                             || GetClipboardOwner() == (HWND) (uintptr_t) m->writer_window);
        if (m) { GlobalUnlock(h); }
        CloseClipboard();
        if (local_source) { goto end; }
    } else {
        goto end; // retry when the clipboard is available
    }
    if (device->state == PENDING && owner_alive(device)) {
        // Do not let a later copy overtake an earlier in-flight transfer.
        goto end;
    }
    if (device->pc_sequence == pc_sequence && device->state == DONE) {
        goto end;
    }
    if (device->pc_sequence == pc_sequence && device->state == FAILED
            && (device->attempts >= 3 || GetTickCount64() < device->retry_after)) {
        goto end;
    }
    if (device->pc_sequence != pc_sequence) {
        device->attempts = 0;
    }
    device->pc_sequence = pc_sequence;
    device->state = PENDING;
    device->owner = GetCurrentProcessId();
    device->owner_created = process_created;
    device->transaction = TX_AUTO | (++shared->counter & (TX_FAILED - 1));
    ++device->attempts;
    *transaction = device->transaction;
    start = true;
end:
    ReleaseMutex(sync_mutex);
    return start;
}

bool
sc_clipboard_sync_needs_recovery(uint32_t pc_sequence) {
    if (!device || !lock_shared()) { return false; }
    bool retry = device->transaction && device->pc_sequence == pc_sequence
              && device->state != DONE;
    ReleaseMutex(sync_mutex);
    return retry;
}

bool
sc_clipboard_sync_stamp(struct sc_control_msg *msg) {
    const void *data;
    size_t size;
    const char *mime;
    if (msg->type == SC_CONTROL_MSG_TYPE_SET_CLIPBOARD) {
        data = msg->set_clipboard.text;
        size = strlen(data);
        mime = "text/plain";
    } else if (msg->type == SC_CONTROL_MSG_TYPE_SET_IMAGE_CLIPBOARD) {
        data = msg->set_image_clipboard.data;
        size = msg->set_image_clipboard.size;
        mime = msg->set_image_clipboard.mimetype;
    } else { return true; }
    uint8_t hash[32];
    DWORD seq = GetClipboardSequenceNumber();
    if (msg->clipboard_pc_sequence && msg->clipboard_pc_sequence != seq) {
        return false; // a new local copy superseded the snapshot being read
    }
    if (!digest(mime, data, size, hash) || !device || !lock_shared()) { return false; }
    if (msg->clipboard_pc_sequence
            && msg->clipboard_pc_sequence != GetClipboardSequenceNumber()) {
        ReleaseMutex(sync_mutex);
        return false;
    }
    if (seq != shared->copy_sequence || memcmp(shared->copy_digest, hash, 32)) {
        shared->copy_sequence = seq;
        memcpy(shared->copy_digest, hash, 32);
        ++shared->copy_version;
    }
    msg->clipboard_epoch = shared->epoch;
    msg->clipboard_version = shared->copy_version;
    msg->type = msg->type == SC_CONTROL_MSG_TYPE_SET_CLIPBOARD
              ? SC_CONTROL_MSG_TYPE_SET_CLIPBOARD_VERSIONED
              : SC_CONTROL_MSG_TYPE_SET_IMAGE_CLIPBOARD_VERSIONED;
    ReleaseMutex(sync_mutex);
    return true;
}

void
sc_clipboard_sync_failed(uint64_t transaction) {
    if (device && lock_shared_for(INFINITE)) {
        if (device->transaction == transaction && device->state == PENDING) {
            device->state = FAILED;
            device->retry_after = GetTickCount64() + 1000;
        }
        ReleaseMutex(sync_mutex);
    }
}

bool
sc_clipboard_sync_ack(uint64_t sequence) {
    if (!(sequence & TX_AUTO)) {
        return false;
    }
    if (device && lock_shared_for(INFINITE)) {
        if (device->transaction == (sequence & ~TX_FAILED)
                && device->state == PENDING) {
            device->state = sequence & TX_FAILED ? FAILED : DONE;
            device->retry_after = GetTickCount64() + 1000;
            LOGD("Clipboard transaction acknowledged (%s)",
                 sequence & TX_FAILED ? "failed" : "complete");
        }
        ReleaseMutex(sync_mutex);
    }
    return true;
}

static HGLOBAL
copy_global(const void *data, size_t size) {
    HGLOBAL h = GlobalAlloc(GMEM_MOVEABLE, size);
    if (!h) { return NULL; }
    void *p = GlobalLock(h);
    if (!p) { GlobalFree(h); return NULL; }
    memcpy(p, data, size);
    GlobalUnlock(h);
    return h;
}

static bool
open_clipboard(void) {
    for (unsigned i = 0; i < 5; ++i) {
        if (OpenClipboard(clipboard_window)) { return true; }
        SDL_Delay(5);
    }
    return false;
}

static bool
same_marker(const struct clipboard_marker *m) {
    HANDLE h = GetClipboardData(marker_format);
    if (!h || GlobalSize(h) < sizeof(*m)) { return false; }
    const struct clipboard_marker *current = GlobalLock(h);
    if (!current) { return false; }
    bool same = current->magic == SYNC_MAGIC && current->kind == m->kind
             && current->size == m->size
             && !memcmp(current->digest, m->digest, 32)
             && !memcmp(current->mime, m->mime, sizeof(m->mime));
    GlobalUnlock(h);
    return same;
}

static bool
same_text(HGLOBAL payload) {
    HANDLE current = GetClipboardData(CF_UNICODETEXT);
    if (!current || !payload) {
        return false;
    }
    const wchar_t *a = GlobalLock(current);
    const wchar_t *b = GlobalLock(payload);
    size_t na = GlobalSize(current) / sizeof(wchar_t);
    size_t nb = GlobalSize(payload) / sizeof(wchar_t);
    bool same = false;
    if (a && b) {
        size_t i = 0, j = 0;
        while (i < na && j < nb) {
            // SDL normalizes Windows CRLF to LF; compare without losing HTML
            // or other formats already supplied by the computer application.
            if (a[i] == L'\r' && i + 1 < na && a[i + 1] == L'\n') { ++i; }
            if (b[j] == L'\r' && j + 1 < nb && b[j + 1] == L'\n') { ++j; }
            if (a[i] != b[j]) { break; }
            if (!a[i]) { same = true; break; }
            ++i;
            ++j;
        }
    }
    if (a) { GlobalUnlock(current); }
    if (b) { GlobalUnlock(payload); }
    return same;
}

// Holding the short metadata lock serializes commits from every casting process.
// No network operation or image decoding is performed while this lock is held.
static bool
commit(struct clipboard_marker *marker, uint64_t revision, uint32_t expected,
       UINT format, HGLOBAL payload, HGLOBAL original, HGLOBAL png) {
    HGLOBAL provenance = copy_global(marker, sizeof(*marker));
    bool ok = false;
    if (!provenance || !device || !lock_shared()) { goto cleanup; }
    if (revision && revision < device->received_revision) {
        ReleaseMutex(sync_mutex);
        goto cleanup;
    }
    if (!open_clipboard()) {
        ReleaseMutex(sync_mutex);
        goto cleanup;
    }
    DWORD seq = GetClipboardSequenceNumber();
    bool same = same_marker(marker)
             || (format == CF_UNICODETEXT && same_text(payload));
    if (same || (expected == seq && EmptyClipboard()
            && SetClipboardData(format, payload))) {
        if (!same) {
            payload = NULL; // Windows owns each successful handle
            if (original && SetClipboardData(image_format, original)) { original = NULL; }
            if (png && SetClipboardData(RegisterClipboardFormatW(L"PNG"), png)) { png = NULL; }
            if (SetClipboardData(marker_format, provenance)) {
                provenance = NULL;
            } else {
                LOGW("Clipboard origin format could not be installed");
            }
            seq = GetClipboardSequenceNumber();
        }
        device->received_revision = revision ? revision : device->received_revision;
        // A receiver must not invalidate a live opposite-direction transfer.
        if (device->state != PENDING) {
            device->pc_sequence = seq;
            device->state = DONE;
        }
        ok = true;
    } else {
        LOGD("Device clipboard update discarded (newer computer copy or clipboard busy)");
    }
    CloseClipboard();
    ReleaseMutex(sync_mutex);
cleanup:
    if (payload) { GlobalFree(payload); }
    if (original) { GlobalFree(original); }
    if (png) { GlobalFree(png); }
    if (provenance) { GlobalFree(provenance); }
    return ok;
}

bool
sc_clipboard_sync_receive_text(const char *text, uint64_t revision, uint32_t expected) {
    struct clipboard_marker m = {.magic = SYNC_MAGIC, .kind = 1, .size = strlen(text)};
    m.writer_window = (uintptr_t) clipboard_window;
    memcpy(m.source, device_key, 32);
    strcpy(m.mime, "text/plain");
    if (!digest(m.mime, text, m.size, m.digest)) { return false; }
    int count = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, text, -1, NULL, 0);
    if (!count) { return false; }
    wchar_t *wide = malloc((size_t) count * sizeof(*wide));
    if (!wide) { return false; }
    MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, text, -1, wide, count);
    HGLOBAL payload = copy_global(wide, (size_t) count * sizeof(*wide));
    free(wide);
    return payload && commit(&m, revision, expected, CF_UNICODETEXT, payload, NULL, NULL);
}

bool
sc_clipboard_sync_receive_image(const char *mime, const uint8_t *data, size_t size,
                                uint64_t revision, uint32_t expected) {
    struct clipboard_marker m = {.magic = SYNC_MAGIC, .kind = 2, .size = size};
    m.writer_window = (uintptr_t) clipboard_window;
    if (!size || size > IMAGE_LIMIT || strlen(mime) >= sizeof(m.mime)) { return false; }
    strcpy(m.mime, mime);
    memcpy(m.source, device_key, 32);
    if (!digest(mime, data, size, m.digest)) { return false; }
    // Suppress identical fan-out before the expensive decode. commit() repeats
    // the check under the global lock to close a concurrent receiver race.
    if (open_clipboard()) {
        bool same = same_marker(&m);
        CloseClipboard();
        if (same) { return commit(&m, revision, expected, 0, NULL, NULL, NULL); }
    }
    uint8_t *pixels;
    unsigned width, height;
    if (!sc_image_to_bgra(data, size, mime, &pixels, &width, &height)) {
        LOGW("Device image cannot be rendered; computer clipboard retained");
        return false;
    }
    BITMAPV5HEADER header = {0};
    header.bV5Size = sizeof(header);
    header.bV5Width = width;
    header.bV5Height = -(LONG) height; // top-down BGRA, lossless
    header.bV5Planes = 1;
    header.bV5BitCount = 32;
    header.bV5Compression = BI_BITFIELDS;
    header.bV5SizeImage = width * height * 4;
    header.bV5RedMask = 0x00ff0000;
    header.bV5GreenMask = 0x0000ff00;
    header.bV5BlueMask = 0x000000ff;
    header.bV5AlphaMask = 0xff000000;
    header.bV5CSType = 0x73524742; // LCS_sRGB without a multicharacter constant
    HGLOBAL dib = GlobalAlloc(GMEM_MOVEABLE, sizeof(header) + header.bV5SizeImage);
    void *dst = dib ? GlobalLock(dib) : NULL;
    if (!dst) { if (dib) { GlobalFree(dib); } free(pixels); return false; }
    memcpy(dst, &header, sizeof(header));
    memcpy((uint8_t *) dst + sizeof(header), pixels, header.bV5SizeImage);
    GlobalUnlock(dib);
    free(pixels);
    HGLOBAL raw = GlobalAlloc(GMEM_MOVEABLE, sizeof(m) + size);
    dst = raw ? GlobalLock(raw) : NULL;
    if (!dst) { if (raw) { GlobalFree(raw); } GlobalFree(dib); return false; }
    memcpy(dst, &m, sizeof(m));
    memcpy((uint8_t *) dst + sizeof(m), data, size);
    GlobalUnlock(raw);
    HGLOBAL png = !strcmp(mime, "image/png") ? copy_global(data, size) : NULL;
    return commit(&m, revision, expected, CF_DIBV5, dib, raw, png);
}

bool
sc_clipboard_sync_get_image(uint8_t **data, size_t *size, char **mime,
                            size_t limit, bool *oversized) {
    bool ok = false;
    if (!open_clipboard()) { return false; }
    HANDLE h = GetClipboardData(image_format);
    size_t total = h ? GlobalSize(h) : 0;
    const struct clipboard_marker *m = total >= sizeof(*m) ? GlobalLock(h) : NULL;
    if (m && m->magic == SYNC_MAGIC && m->kind == 2 && m->size > 0
            && m->size <= IMAGE_LIMIT && m->size <= total - sizeof(*m)
            && memchr(m->mime, 0, sizeof(m->mime))) {
        if (limit && m->size > limit) {
            *oversized = true;
        } else {
            *data = malloc(m->size);
            *mime = strdup(m->mime);
            if (*data && *mime) {
                *size = m->size;
                memcpy(*data, (const uint8_t *) m + sizeof(*m), *size);
                ok = true;
            } else {
                free(*data);
                free(*mime);
            }
        }
    }
    if (m) { GlobalUnlock(h); }
    CloseClipboard();
    return ok;
}

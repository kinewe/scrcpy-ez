#include "input_manager.h"

#include <assert.h>
#include <stdlib.h>
#include <string.h>
#include <SDL3/SDL.h>

#include "android/input.h"
#include "android/keycodes.h"
#include "events.h"
#include "image_convert.h"
#include "clipboard_sync.h"
#include "input_events.h"
#include "screen.h"
#include "shortcut_mod.h"
#include "util/log.h"
#include "util/sdl.h"
#include "util/thread.h"

#ifdef _WIN32
#include <windows.h>
#endif

// Compress BMP clipboard images larger than this threshold to JPEG (quality 95)
// before sending, to keep control messages small. 16MB keeps the common
// case lossless (BMP sent as-is, converted to PNG by the server); only
// very large BMPs are compressed to stay within the control message limit
// (SC_CONTROL_MSG_MAX_SIZE = 256MB). Serialization allocates exactly the
// message size on demand, so large messages cost transient heap memory
// instead of a static 256MB buffer.
#define SC_IMAGE_CLIPBOARD_COMPRESS_THRESHOLD (192 * 1024 * 1024) // 192MB (lossless: only a safety net near the 256MB message limit)

// Interval of the Windows clipboard polling timer
#define SC_CLIPBOARD_WATCH_INTERVAL_MS 300

// On Windows, SDL3 does not send SDL_EVENT_CLIPBOARD_UPDATE when the clipboard
// is changed by another application (it is only checked when the window gains
// focus), so the clipboard sequence number is polled by a timer.
//
// Loop suppression: when the device clipboard is synchronized to the computer
// (reverse synchronization, set by the receiver tasks), the computer clipboard
// is modified, which would trigger a new synchronization back to the device
// (computer -> device -> computer -> ...). To prevent this infinite loop, the
// reverse synchronization records the Windows clipboard sequence number right
// after setting it; any clipboard update while the sequence number is still
// unchanged is ignored (the content is still the one set by the reverse
// synchronization). As soon as the user copies something else, the sequence
// number changes and the suppression is released.
//
// Only accessed from the main thread: recorded by the receiver clipboard tasks
// (which run on the main thread) and consumed by the input manager event loop.
#ifdef _WIN32
static uint32_t clipboard_reverse_sync_seq; // 0 = no reverse sync pending
// Last observed Windows clipboard sequence number; only accessed from the SDL
// timer thread. Stored globally (not in the input manager) so that the timer
// callback stays valid even after the input manager is destroyed.
static uint32_t clipboard_poll_sequence;
// Windows clipboard sequence number of the last content pushed to the device
// (by the automatic clipboard sync, a manual MOD+Shift+C, Ctrl+v, or a
// reverse synchronization). 0 = nothing pushed yet. Used to avoid pushing
// the same content twice: when Ctrl+v is pressed while the computer
// clipboard still matches the last pushed content, only the PASTE key is
// injected, without sending a new SET_CLIPBOARD/SET_IMAGE_CLIPBOARD message
// (which would create a duplicate clipboard entry on the device).
// Only accessed from the main thread.
static uint32_t clipboard_last_pushed_seq;
// Windows clipboard sequence number recorded at input manager startup;
// used to suppress the startup clipboard push when
// --no-clipboard-push-on-start is set (mode switch / reconnection: the
// startup SDL clipboard event must not push the content that was already
// present on the computer clipboard before scrcpy started).
static uint32_t clipboard_start_suppress_seq;
// Fingerprint of the last content pushed to the device. Some apps
// (WeChat) write the clipboard twice for a single copy (two different
// sequence numbers with identical content), so the seq-based dedup
// alone is not enough: if the content fingerprint matches the last
// pushed one, the update is skipped even though the sequence number
// changed.
static uint64_t clipboard_last_pushed_fp;
static bool clipboard_last_pushed_fp_valid;
// Alternate fingerprint of the last pushed content. On Windows, a copied
// image file can be read through two different paths with two different
// byte streams: the original file bytes (CF_HDROP) and the data SDL
// provides for "image/png" (which SDL re-encodes, not the original file
// bytes). A clipboard event may switch from one path to the other (e.g.
// when SDL holds the clipboard open), so both fingerprints are recorded
// and an update is deduplicated if it matches either of them.
static uint64_t clipboard_last_pushed_fp_alt;
static bool clipboard_last_pushed_fp_alt_valid;
#else
static bool clipboard_reverse_sync_pending;
#endif

void
sc_input_manager_mark_clipboard_reverse_sync(void) {
    assert(sc_thread_is_main());
#ifdef _WIN32
    clipboard_reverse_sync_seq = GetClipboardSequenceNumber();
    // The computer clipboard now contains exactly the device clipboard
    // content, so consider it as the last pushed content: a subsequent
    // Ctrl+v must only inject the PASTE key, without pushing the same
    // content back to the device (which would duplicate it).
    clipboard_last_pushed_seq = clipboard_reverse_sync_seq;
    // The device content is now on the computer clipboard: invalidate the
    // fingerprint (the reverse-sync seq check above prevents the loop).
    clipboard_last_pushed_fp_valid = false;
    clipboard_last_pushed_fp_alt_valid = false;
#else
    clipboard_reverse_sync_pending = true;
#endif
}

#ifdef _WIN32
static Uint32 SDLCALL
clipboard_watch_timer_cb(void *userdata, SDL_TimerID timer_id, Uint32 interval) {
    (void) userdata;
    (void) timer_id;

    DWORD seq = GetClipboardSequenceNumber();
    if (seq != clipboard_poll_sequence) {
        clipboard_poll_sequence = seq;
        // Post to the main thread (the timer callback runs on a SDL timer
        // thread, and SDL_PushEvent is thread-safe)
    }
    // A transaction may fail or its sending process may exit without changing
    // the clipboard sequence. Rechecking metadata permits bounded recovery.
    if (!sc_push_event(SC_EVENT_CLIPBOARD_CHANGED)) {
        LOGW("Could not post clipboard changed event");
    }

    return interval;
}
#endif

void
sc_input_manager_init(struct sc_input_manager *im,
                      const struct sc_input_manager_params *params) {
    // A processor must have ops initialized
    assert(!params->kp || params->kp->ops);
    assert(!params->mp || params->mp->ops);
    assert(!params->gp || params->gp->ops);

    im->controller = params->controller;
    im->fp = params->fp;
    im->screen = params->screen;
    im->kp = params->kp;
    im->mp = params->mp;
    im->gp = params->gp;
    im->camera = params->camera;
    im->screen_off = false;
    im->ctrl_power_pressed = false;

    im->mouse_bindings = params->mouse_bindings;
    im->legacy_paste = params->legacy_paste;
    im->clipboard_autosync = params->clipboard_autosync;
    im->clipboard_sync = params->clipboard_sync;
    im->suppress_start_push = !params->clipboard_push_on_start;

    im->sdl_shortcut_mods = sc_shortcut_mods_to_sdl(params->shortcut_mods);

    im->vfinger_down = false;
    im->vfinger_invert_x = false;
    im->vfinger_invert_y = false;

    im->mouse_buttons_state = 0;

    im->last_keycode = SDLK_UNKNOWN;
    im->last_mod = 0;
    im->key_repeat = 0;

    im->next_sequence = 1; // 0 is reserved for SC_SEQUENCE_INVALID

#ifdef _WIN32
    // Start polling the Windows clipboard sequence number to detect clipboard
    // changes from other applications (SDL3 does not report them on Windows)
    // The timer callback does not reference the input manager, so it stays
    // valid until SDL_Quit() removes all timers.
    clipboard_poll_sequence = GetClipboardSequenceNumber();
    clipboard_last_pushed_seq = 0; // nothing pushed yet
    clipboard_start_suppress_seq = GetClipboardSequenceNumber();
    clipboard_last_pushed_fp_valid = false;
    if (!SDL_AddTimer(SC_CLIPBOARD_WATCH_INTERVAL_MS,
                      clipboard_watch_timer_cb, NULL)) {
        LOGW("Could not start clipboard watch timer: %s", SDL_GetError());
    }
#endif

    im->disconnected = false;
}

static void
send_keycode(struct sc_input_manager *im, enum android_keycode keycode,
             enum sc_action action, const char *name) {
    assert(im->controller && im->kp && !im->camera);

    // send DOWN event
    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_INJECT_KEYCODE;
    msg.inject_keycode.action = action == SC_ACTION_DOWN
                              ? AKEY_EVENT_ACTION_DOWN
                              : AKEY_EVENT_ACTION_UP;
    msg.inject_keycode.keycode = keycode;
    msg.inject_keycode.metastate = 0;
    msg.inject_keycode.repeat = 0;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request 'inject %s'", name);
    }
}

static inline void
action_home(struct sc_input_manager *im, enum sc_action action) {
    send_keycode(im, AKEYCODE_HOME, action, "HOME");
}

static inline void
action_back(struct sc_input_manager *im, enum sc_action action) {
    send_keycode(im, AKEYCODE_BACK, action, "BACK");
}

static inline void
action_app_switch(struct sc_input_manager *im, enum sc_action action) {
    send_keycode(im, AKEYCODE_APP_SWITCH, action, "APP_SWITCH");
}

static void
save_clipboard_image_to_gallery(struct sc_input_manager *im) {
    // No argument: the server copies the most recent clipboard image
    // file (already cached for the device clipboard) into the gallery
    // and triggers a media scan.
    struct sc_control_msg msg = {
        .type = SC_CONTROL_MSG_TYPE_SAVE_CLIPBOARD_IMAGE_TO_GALLERY,
    };
    if (sc_controller_push_msg(im->controller, &msg)) {
        LOGI("Save clipboard image to gallery");
    } else {
        LOGW("Could not request 'save clipboard image to gallery'");
    }
}

static inline void
action_power(struct sc_input_manager *im, enum sc_action action) {
    send_keycode(im, AKEYCODE_POWER, action, "POWER");
}

static inline void
action_volume_up(struct sc_input_manager *im, enum sc_action action) {
    send_keycode(im, AKEYCODE_VOLUME_UP, action, "VOLUME_UP");
}

static inline void
action_volume_down(struct sc_input_manager *im, enum sc_action action) {
    send_keycode(im, AKEYCODE_VOLUME_DOWN, action, "VOLUME_DOWN");
}

static inline void
action_menu(struct sc_input_manager *im, enum sc_action action) {
    send_keycode(im, AKEYCODE_MENU, action, "MENU");
}

// turn the screen on if it was off, press BACK otherwise
// If the screen is off, it is turned on only on ACTION_DOWN
static void
press_back_or_turn_screen_on(struct sc_input_manager *im,
                             enum sc_action action) {
    assert(im->controller && im->kp && !im->camera);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_BACK_OR_SCREEN_ON;
    msg.back_or_screen_on.action = action == SC_ACTION_DOWN
                                 ? AKEY_EVENT_ACTION_DOWN
                                 : AKEY_EVENT_ACTION_UP;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request 'press back or turn screen on'");
    }
}

static void
expand_notification_panel(struct sc_input_manager *im) {
    assert(im->controller && !im->camera);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_EXPAND_NOTIFICATION_PANEL;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request 'expand notification panel'");
    }
}

static void
expand_settings_panel(struct sc_input_manager *im) {
    assert(im->controller && !im->camera);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_EXPAND_SETTINGS_PANEL;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request 'expand settings panel'");
    }
}

static void
collapse_panels(struct sc_input_manager *im) {
    assert(im->controller && !im->camera);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_COLLAPSE_PANELS;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request 'collapse notification panel'");
    }
}

static bool
get_device_clipboard(struct sc_input_manager *im, enum sc_copy_key copy_key) {
    assert(im->controller && im->kp && !im->camera);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_GET_CLIPBOARD;
    msg.get_clipboard.copy_key = copy_key;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request 'get device clipboard'");
        return false;
    }

    return true;
}

static bool
clipboard_data_matches(const char *mime_type, const void *data, size_t size) {
    static const struct {
        const char *mime;
        const uint8_t *magic;
        size_t magic_len;
    } signatures[] = {
        { "image/png",  (const uint8_t *) "\x89PNG", 4 },
        { "image/jpeg", (const uint8_t *) "\xFF\xD8\xFF", 3 },
        { "image/jpg",  (const uint8_t *) "\xFF\xD8\xFF", 3 },
        { "image/gif",  (const uint8_t *) "GIF8", 4 },
        { "image/webp", (const uint8_t *) "RIFF", 4 },
        { "image/bmp",  (const uint8_t *) "BM", 2 },
        { NULL, NULL, 0 }
    };

    if (!data || size < 4) {
        return false;
    }
    const uint8_t *bytes = data;
    for (size_t i = 0; signatures[i].mime; ++i) {
        if (strcmp(mime_type, signatures[i].mime) != 0) {
            continue;
        }
        if (size < signatures[i].magic_len
                || memcmp(bytes, signatures[i].magic, signatures[i].magic_len) != 0) {
            return false;
        }
        if (strcmp(mime_type, "image/webp") == 0) {
            // RIFF size WEBP
            return size >= 12 && memcmp(bytes + 8, "WEBP", 4) == 0;
        }
        return true;
    }
    // Unknown mime type: do not reject it, let the server handle it
    return true;
}


#ifdef _WIN32
static bool
clipboard_get_hdrop_first_file(wchar_t *path, size_t path_size) {
    // Real-world copies (Explorer, WeChat PC) may use delayed rendering for
    // CF_HDROP: the format is registered but the data is only produced when
    // requested, which may fail briefly right after the copy. Other apps may
    // also hold the clipboard open for a few milliseconds. Without retrying,
    // the HDROP path would be skipped and we would fall back to the SDL/DIB
    // path, which often contains a small thumbnail (e.g. 1620x1080) generated
    // by the source app instead of the original file.
    if (!IsClipboardFormatAvailable(CF_HDROP)) {
        // No HDROP at all (e.g. browser copy): do not wait.
        return false;
    }
    for (int attempt = 0; attempt < 12; attempt++) {
        if (!OpenClipboard(NULL)) {
            Sleep(50);
            continue;
        }
        bool ok = false;
        HANDLE hdrop = GetClipboardData(CF_HDROP);
        if (hdrop) {
            UINT count = DragQueryFileW((HDROP) hdrop, 0xFFFFFFFF, NULL, 0);
            if (count > 0) {
                UINT len = DragQueryFileW((HDROP) hdrop, 0, NULL, 0);
                if (len > 0 && (size_t) len < path_size) {
                    DragQueryFileW((HDROP) hdrop, 0, (LPWSTR) path, len + 1);
                    ok = true;
                }
            }
        }
        CloseClipboard();
        if (ok) {
            return true;
        }
        // Delayed rendering: the source app needs time to provide the data.
        Sleep(100);
    }
    return false;
}

static const char *
image_mime_from_extension(const wchar_t *path) {
    const wchar_t *dot = wcsrchr(path, L'.');
    if (!dot) {
        return NULL;
    }
    if (_wcsicmp(dot, L".gif") == 0) return "image/gif";
    if (_wcsicmp(dot, L".png") == 0) return "image/png";
    if (_wcsicmp(dot, L".jpg") == 0 || _wcsicmp(dot, L".jpeg") == 0) return "image/jpeg";
    if (_wcsicmp(dot, L".webp") == 0) return "image/webp";
    if (_wcsicmp(dot, L".bmp") == 0) return "image/bmp";
    if (_wcsicmp(dot, L".heic") == 0 || _wcsicmp(dot, L".heif") == 0) return "image/heic";
    return NULL;
}

// Detect the real image format from magic bytes, which has priority over
// the file extension: some apps (WeChat, QQ, browsers...) copy files with
// a misleading extension (e.g. a real GIF animation saved as ".jpg").
// Returns NULL if the magic bytes do not identify a known image format.
static const char *
image_mime_from_magic(const uint8_t *data, size_t size) {
    if (size >= 6 && (memcmp(data, "GIF87a", 6) == 0
                      || memcmp(data, "GIF89a", 6) == 0)) {
        return "image/gif";
    }
    if (size >= 8 && memcmp(data, "\x89PNG\r\n\x1a\n", 8) == 0) {
        return "image/png";
    }
    if (size >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF) {
        return "image/jpeg";
    }
    if (size >= 12 && memcmp(data, "RIFF", 4) == 0
            && memcmp(data + 8, "WEBP", 4) == 0) {
        return "image/webp";
    }
    if (size >= 2 && data[0] == 'B' && data[1] == 'M') {
        return "image/bmp";
    }
    return NULL;
}

// Forward declaration (defined after clipboard_get_hdrop_image).
static bool clipboard_has_hdrop_image(void);

// FNV-1a 64-bit hash.
static uint64_t
clipboard_fnv1a(const uint8_t *data, size_t size) {
    uint64_t h = 1469598103934665603ULL;
    for (size_t i = 0; i < size; i++) {
        h ^= data[i];
        h *= 1099511628211ULL;
    }
    return h;
}

// Fingerprint of the first HDROP image file: full file content hash (FNV-1a
// over the file bytes) XOR the file size. This is the SAME fingerprint as the
// one computed for SDL image/text data, so a clipboard event is deduplicated
// regardless of the path used to read the content (HDROP or SDL): when SDL
// holds the clipboard open, the HDROP path falls back to the SDL data path
// (which returns the same original file bytes), and both fingerprints must
// match to avoid a duplicate push.
static bool
clipboard_hdrop_fingerprint(uint64_t *out_fp) {
    wchar_t path[MAX_PATH] = {0};
    if (!clipboard_get_hdrop_first_file(path, sizeof(path))) {
        return false;
    }
    FILE *f = _wfopen(path, L"rb");
    if (!f) {
        return false;
    }
    uint64_t h = 1469598103934665603ULL;
    uint8_t buf[8192];
    size_t rd;
    uint64_t size = 0;
    while ((rd = fread(buf, 1, sizeof(buf), f)) > 0) {
        size += rd;
        for (size_t i = 0; i < rd; i++) {
            h ^= buf[i];
            h *= 1099511628211ULL;
        }
    }
    fclose(f);
    h ^= size;
    *out_fp = h;
    return true;
}

// Fingerprint of the SDL clipboard data: image MIME data first, then
// text. Used both for the current content fingerprint and for the
// alternate fingerprint of a HDROP push (SDL re-encodes the file, so
// this fingerprint differs from the raw file bytes fingerprint).
static bool
clipboard_sdl_data_fingerprint(uint64_t *out_fp) {
    const char *image_mime_types[] = {
        "image/png",
        "image/jpeg",
        "image/jpg",
        "image/bmp",
        "image/webp",
        "image/gif",
    };
    for (size_t i = 0;
         i < sizeof(image_mime_types) / sizeof(image_mime_types[0]); i++) {
        size_t size;
        void *sdldata = SDL_GetClipboardData(image_mime_types[i], &size);
        if (sdldata && size > 0) {
            uint64_t fp = clipboard_fnv1a(sdldata, size) ^ size;
            SDL_free(sdldata);
            *out_fp = fp;
            return true;
        }
    }
    char *text = SDL_GetClipboardText();
    if (text && *text) {
        size_t len = strlen(text);
        uint64_t fp = clipboard_fnv1a((const uint8_t *) text, len) ^ len;
        SDL_free(text);
        *out_fp = fp;
        return true;
    }
    return false;
}

// Fingerprint of the current clipboard content: HDROP image file first,
// then SDL image data, then text. Returns false if the content cannot
// be fingerprinted.
static bool
clipboard_current_fingerprint(uint64_t *out_fp) {
    if (clipboard_has_hdrop_image()) {
        return clipboard_hdrop_fingerprint(out_fp);
    }
    return clipboard_sdl_data_fingerprint(out_fp);
}

// Read the first image file from the CF_HDROP clipboard data (copy file in
// Explorer). The file bytes are sent as-is, keeping the original format
// (GIF animation, PNG, JPEG...) instead of the rasterized CF_DIB bitmap.
// Returns false if there is no HDROP data or the first file is not an image
// (in which case the caller falls back to the SDL image clipboard path; a
// non-image file must not be pushed, to avoid pushing a file path as text).
static bool
clipboard_get_hdrop_image(uint8_t **out_data, size_t *out_size,
                           const char **out_mime) {
    wchar_t path[MAX_PATH] = {0};
    if (!clipboard_get_hdrop_first_file(path, sizeof(path))) {
        return false;
    }
    FILE *f = _wfopen(path, L"rb");
    if (!f) {
        LOGW("Could not open HDROP image file");
        return false;
    }
    // Read the file header first: the magic bytes identify the real format
    // (a ".jpg" file containing GIF data must be pushed as image/gif). The
    // extension is only a fallback when the magic bytes are not recognized.
    uint8_t header[16];
    size_t hdr = fread(header, 1, sizeof(header), f);
    const char *mime = hdr >= 2 ? image_mime_from_magic(header, hdr) : NULL;
    if (!mime) {
        mime = image_mime_from_extension(path);
    }
    if (!mime) {
        LOGD("HDROP item is not an image file, not pushed");
        fclose(f);
        return false;
    }
    fseek(f, 0, SEEK_END);
    long sz = ftell(f);
    fseek(f, 0, SEEK_SET);
    if (sz <= 0) {
        fclose(f);
        return false;
    }
    uint8_t *data = malloc((size_t) sz);
    if (!data) {
        fclose(f);
        return false;
    }
    size_t rd = fread(data, 1, (size_t) sz, f);
    fclose(f);
    if (rd != (size_t) sz) {
        free(data);
        return false;
    }
    *out_data = data;
    *out_size = (size_t) sz;
    *out_mime = mime;
    return true;
}

// Lightweight check: is the first CF_HDROP item an image file? Used by the
// empty-clipboard check so that a copied image file is not dropped before
// the HDROP push path is reached.
static bool
clipboard_has_hdrop_image(void) {
    wchar_t path[MAX_PATH] = {0};
    if (!clipboard_get_hdrop_first_file(path, sizeof(path))) {
        return false;
    }
    // Magic bytes first (a GIF saved as ".jpg" must be detected as an
    // image), then fall back to the extension.
    FILE *f = _wfopen(path, L"rb");
    if (f) {
        uint8_t header[16];
        size_t hdr = fread(header, 1, sizeof(header), f);
        fclose(f);
        if (hdr >= 2 && image_mime_from_magic(header, hdr)) {
            return true;
        }
    }
    return image_mime_from_extension(path) != NULL;
}
#endif

#ifdef _WIN32
// Copying a file in Explorer puts only CF_HDROP on the clipboard: the SDL
// image path would see no image. If the first HDROP item is an image file,
// push its original bytes (preserving the format and GIF animation).
// Non-image files are not pushed. Returns true if pushed (or handled),
// false otherwise (caller falls back to SDL data).
static bool
clipboard_push_hdrop_image(struct sc_input_manager *im, bool paste,
                           uint64_t sequence) {
    uint32_t snapshot_sequence = GetClipboardSequenceNumber();
    const char *hdrop_mime = NULL;
    uint8_t *hdrop_data = NULL;
    size_t hdrop_size = 0;
    if (!clipboard_get_hdrop_image(&hdrop_data, &hdrop_size, &hdrop_mime)) {
        return false;
    }
    // Compute the fingerprints BEFORE pushing: the main one uses the file
    // bytes already read (no clipboard access), the alternate one (SDL
    // re-encoded data) is read now to avoid a clipboard-open race right
    // after the push (SDL may hold the clipboard open while posting its
    // own clipboard event).
    uint64_t hdrop_fp = clipboard_fnv1a(hdrop_data, hdrop_size) ^ hdrop_size;
    uint64_t sdl_alt = 0;
    bool sdl_alt_ok = clipboard_sdl_data_fingerprint(&sdl_alt);
    size_t mimetype_len = strlen(hdrop_mime);
    size_t msg_size = 34 + mimetype_len + hdrop_size;
    if (msg_size > SC_CONTROL_MSG_MAX_SIZE) {
        LOGW("HDROP image message too large: %u bytes, dropping", (unsigned) msg_size);
        free(hdrop_data);
        return false;
    }
    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_SET_IMAGE_CLIPBOARD;
    msg.set_image_clipboard.sequence = sequence;
    msg.clipboard_pc_sequence = snapshot_sequence;
    msg.set_image_clipboard.data = hdrop_data;
    msg.set_image_clipboard.size = hdrop_size;
    msg.set_image_clipboard.mimetype = strdup(hdrop_mime);
    if (!msg.set_image_clipboard.mimetype) {
        LOG_OOM();
        free(hdrop_data);
        return false;
    }
    msg.set_image_clipboard.paste = paste;
    bool success = sc_controller_push_msg(im->controller, &msg);
    if (success) {
        // On success, msg.data and msg.mimetype ownership is transferred to
        // the controller (it is freed by the controller thread after
        // serialization). Freeing them here would cause a use-after-free.
        clipboard_last_pushed_seq = GetClipboardSequenceNumber();
        clipboard_last_pushed_fp = hdrop_fp;
        clipboard_last_pushed_fp_valid = true;
        clipboard_last_pushed_fp_alt_valid = sdl_alt_ok;
        if (sdl_alt_ok) {
            clipboard_last_pushed_fp_alt = sdl_alt;
        }
        LOGI("Image clipboard: pushed HDROP file as %s (%u bytes)",
             hdrop_mime, (unsigned) hdrop_size);
        return true;
    }
    free(msg.set_image_clipboard.data);
    free(msg.set_image_clipboard.mimetype);
    return false;
}
#endif

bool
sc_input_manager_set_device_image_clipboard(struct sc_input_manager *im, bool paste,
                           uint64_t sequence) {
    assert(im->controller && im->kp && !im->camera);

    // Multi-step clipboard write (e.g. Explorer with a preview extension
    // writes a thumbnail bitmap first, then CF_HDROP within the same
    // sequence number): by now the HDROP file may have appeared, so
    // retry the HDROP path before pushing the (possibly thumbnail)
    // SDL bitmap data.
#ifdef _WIN32
    uint32_t snapshot_sequence = GetClipboardSequenceNumber();
    uint8_t *original;
    size_t original_size;
    char *original_mime;
    if (sc_clipboard_sync_get_image(&original, &original_size, &original_mime)) {
        struct sc_control_msg msg = {.type = SC_CONTROL_MSG_TYPE_SET_IMAGE_CLIPBOARD};
        msg.set_image_clipboard.sequence = sequence;
        msg.clipboard_pc_sequence = snapshot_sequence;
        msg.set_image_clipboard.data = original;
        msg.set_image_clipboard.size = original_size;
        msg.set_image_clipboard.mimetype = original_mime;
        msg.set_image_clipboard.paste = paste;
        if (sc_controller_push_msg(im->controller, &msg)) { return true; }
        free(original);
        free(original_mime);
        return false;
    }
    if (clipboard_push_hdrop_image(im, paste, sequence)) {
        return true;
    }
#endif

    // Try common image MIME types to check if image clipboard data exists
    const char* image_mime_types[] = {
        "image/png",
        "image/jpeg",
        "image/jpg",
        "image/gif",
        "image/webp",
        "image/bmp",
        NULL
    };

    for (int i = 0; image_mime_types[i]; i++) {
        const char* mime_type = image_mime_types[i];
        size_t size;
        void *img_data = SDL_GetClipboardData(mime_type, &size);
        if (img_data && size > 0) {
            // Verify that the data matches the claimed MIME type: some
            // applications register exotic clipboard formats under a misleading
            // name. If the data does not match, the server would not be able to
            // decode it and would fall back to an unreadable clipboard item.
            if (!clipboard_data_matches(mime_type, img_data, size)) {
                LOGW("Clipboard data does not match mime type \"%s\", skipping",
                     mime_type);
                SDL_free(img_data);
                continue;
            }
            uint8_t *data = img_data;
            uint8_t *compressed = NULL;
            // Fingerprints computed before pushing (see HDROP branch):
            // main = SDL data hash, alternate = HDROP file hash.
            uint64_t sdl_fp = clipboard_fnv1a(data, size) ^ size;
            uint64_t hdrop_alt = 0;
            bool hdrop_alt_ok = clipboard_hdrop_fingerprint(&hdrop_alt);

            // Compress large BMP images to JPEG to keep the control message small
            if (strcmp(mime_type, "image/bmp") == 0
                    && size > SC_IMAGE_CLIPBOARD_COMPRESS_THRESHOLD
                    && sc_image_bmp_to_jpeg(img_data, size, &compressed, &size)) {
                LOGI("Image clipboard: compressed BMP to JPEG (%u bytes)", (unsigned) size);
                mime_type = "image/jpeg";
                data = compressed;
            }

            size_t mimetype_len = strlen(mime_type);

            // Check if message exceeds max size
            size_t msg_size = 34 + mimetype_len + size;
            if (msg_size > SC_CONTROL_MSG_MAX_SIZE) {
                LOGW("Image clipboard message too large: %u bytes, dropping",
                     (unsigned) msg_size);
                free(compressed);
                SDL_free(img_data);
                // Report the drop as a failure so the caller may fall back
                // to the text clipboard (same behavior as the HDROP path)
                return false;
            }

            struct sc_control_msg msg;
            msg.type = SC_CONTROL_MSG_TYPE_SET_IMAGE_CLIPBOARD;
            msg.set_image_clipboard.sequence = sequence;
#ifdef _WIN32
            msg.clipboard_pc_sequence = snapshot_sequence;
#endif
            msg.set_image_clipboard.data = malloc(size);
            if (msg.set_image_clipboard.data) {
                memcpy(msg.set_image_clipboard.data, data, size);
                msg.set_image_clipboard.size = size;
                msg.set_image_clipboard.mimetype = strdup(mime_type);
                if (!msg.set_image_clipboard.mimetype) {
                    LOG_OOM();
                    free(msg.set_image_clipboard.data);
                    free(compressed);
                    SDL_free(img_data);
                    return false;
                }
                msg.set_image_clipboard.paste = paste;

                bool success = sc_controller_push_msg(im->controller, &msg);
                if (success) {
#ifdef _WIN32
                    clipboard_last_pushed_seq = GetClipboardSequenceNumber();
                    clipboard_last_pushed_fp = sdl_fp;
                    clipboard_last_pushed_fp_valid = true;
                    clipboard_last_pushed_fp_alt_valid = hdrop_alt_ok;
                    if (hdrop_alt_ok) {
                        clipboard_last_pushed_fp_alt = hdrop_alt;
                    }
#endif
                    free(compressed);
                    SDL_free(img_data);
                    return true;
                }
                free(msg.set_image_clipboard.data);
                free(msg.set_image_clipboard.mimetype);
            }
            free(compressed);
            SDL_free(img_data);
        }
    }

    // Return false since we can't actually call the SDL3 functions in this context
    return false;
}

static bool clipboard_has_image(void);

static bool
set_device_clipboard(struct sc_input_manager *im, bool paste,
                     uint64_t sequence) {
    assert(im->controller && im->kp && !im->camera);

    if (sc_input_manager_set_device_image_clipboard(im, paste, sequence)) {
        // Successfully sent image clipboard, return true
        return true;
    }

    // Failed image reads must not clear the device clipboard with empty text.
    if (clipboard_has_image()
#ifdef _WIN32
            || clipboard_has_hdrop_image()
#endif
            ) {
        return false;
    }

    // Fallback to text clipboard
#ifdef _WIN32
    uint32_t snapshot_sequence = GetClipboardSequenceNumber();
#endif
    char *text = SDL_GetClipboardText();
    if (!text) {
        LOGW("Could not get clipboard text: %s", SDL_GetError());
        return false;
    }

    char *text_dup = strdup(text);
    SDL_free(text);
    if (!text_dup) {
        LOGW("Could not strdup input text");
        return false;
    }

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_SET_CLIPBOARD;
    msg.set_clipboard.sequence = sequence;
#ifdef _WIN32
    msg.clipboard_pc_sequence = snapshot_sequence;
#endif
    msg.set_clipboard.text = text_dup;
    msg.set_clipboard.paste = paste;

    uint64_t text_fp = 0;
#ifdef _WIN32
    size_t text_len = strlen(text_dup);
    text_fp = clipboard_fnv1a((const uint8_t *) text_dup, text_len) ^ text_len;
#endif

    if (!sc_controller_push_msg(im->controller, &msg)) {
        free(text_dup);
        LOGW("Could not request 'set device clipboard'");
        return false;
    }

#ifdef _WIN32
    clipboard_last_pushed_seq = GetClipboardSequenceNumber();
    clipboard_last_pushed_fp = text_fp;
    clipboard_last_pushed_fp_valid = true;
    clipboard_last_pushed_fp_alt_valid = false;
#endif

    return true;
}

static bool
clipboard_has_image(void) {
    // Same MIME types as in sc_input_manager_set_device_image_clipboard()
    static const char *const image_mime_types[] = {
        "image/png",
        "image/jpeg",
        "image/jpg",
        "image/gif",
        "image/webp",
        "image/bmp",
        NULL
    };

    for (size_t i = 0; image_mime_types[i]; ++i) {
        if (SDL_HasClipboardData(image_mime_types[i])) {
            return true;
        }
    }
    return false;
}

static void
sc_input_manager_process_clipboard_update(struct sc_input_manager *im) {
    if (im->camera || !im->controller || !im->kp || im->disconnected) {
        return;
    }
    if (!im->clipboard_sync) {
        // Disabled by --no-clipboard-sync
        return;
    }

#ifdef _WIN32
    if (im->suppress_start_push) {
        // First clipboard event after startup with
        // --no-clipboard-push-on-start (reconnection / mode switch):
        // if the sequence number still matches the one recorded at
        // startup, this is the startup push event (content already
        // present before scrcpy started) and it must be suppressed.
        // If the sequence number changed, the user copied something
        // new right after startup: push it normally.
        if (GetClipboardSequenceNumber() == clipboard_start_suppress_seq
                && !sc_clipboard_sync_needs_recovery(clipboard_start_suppress_seq)) {
            return;
        }
        im->suppress_start_push = false;
    }
    char *startup_text = SDL_GetClipboardText();
    bool empty_snapshot = !startup_text || !*startup_text;
    SDL_free(startup_text);
    if (empty_snapshot && !clipboard_has_image() && !clipboard_has_hdrop_image()) {
        return;
    }
    uint32_t current_sequence = GetClipboardSequenceNumber();
    uint64_t transaction;
    if (!sc_clipboard_sync_begin(current_sequence, &transaction)) {
        return;
    }
    // Claim covers the snapshot read and queue insertion. A copy occurring
    // during this read is retried as a newer transaction after the ACK.
    if (!set_device_clipboard(im, false, transaction)) {
        sc_clipboard_sync_failed(transaction);
        LOGW("Clipboard transaction could not be queued");
    }
    return;
#else
    if (clipboard_reverse_sync_pending) {
        // The computer clipboard change comes from a reverse synchronization
        // (device -> computer): ignore it, otherwise the content would be
        // pushed back to the device, triggering an infinite loop.
        clipboard_reverse_sync_pending = false;
        LOGD("Clipboard update ignored (reverse synchronization)");
        return;
    }
#endif

    // Do not push an empty clipboard: it would clear the device clipboard
    // (e.g. when copying a file on the computer, the clipboard contains
    // neither text nor image).
    char *text = SDL_GetClipboardText();
    bool empty = !text || !*text;
    SDL_free(text);
    if (empty && !clipboard_has_image()
#ifdef _WIN32
            && !clipboard_has_hdrop_image()
#endif
            ) {
        LOGD("Computer clipboard is empty, nothing to synchronize");
        return;
    }



    // Synchronize the computer clipboard (text or image) to the device
    // clipboard without pasting (nopaste), so the user can paste it manually
    // with Ctrl+v (or a long press) in any app.
    if (!set_device_clipboard(im, false, SC_SEQUENCE_INVALID)) {
        LOGW("Could not synchronize computer clipboard to device");
    } else {
        LOGI("Computer clipboard synchronized to device");
    }
}

static void
set_display_power(struct sc_input_manager *im, bool on) {
    assert(im->controller && !im->camera);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_SET_DISPLAY_POWER;
    msg.set_display_power.on = on;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request 'set screen power mode'");
        return;
    }

    // Track the state locally so exit paths can restore the screen even if
    // the device-side cleanup process is not running.
    im->screen_off = !on;
    LOGI("Device screen turned %s", on ? "on" : "off");
}

bool
sc_input_manager_is_screen_off(const struct sc_input_manager *im) {
    return im->screen_off;
}

void
sc_input_manager_turn_screen_on(struct sc_input_manager *im) {
    if (im->screen_off) {
        set_display_power(im, true);
    }
}

static void
switch_fps_counter_state(struct sc_input_manager *im) {
    struct sc_fps_counter *fps_counter = &im->screen->fps_counter;

    // the started state can only be written from the current thread, so there
    // is no ToCToU issue
    if (sc_fps_counter_is_started(fps_counter)) {
        sc_fps_counter_stop(fps_counter);
    } else {
        sc_fps_counter_start(fps_counter);
        // Any error is already logged
    }
}

static void
clipboard_paste(struct sc_input_manager *im) {
    assert(im->controller && im->kp && !im->camera);

    char *text = SDL_GetClipboardText();
    if (!text) {
        LOGW("Could not get clipboard text: %s", SDL_GetError());
        return;
    }
    if (!*text) {
        // empty text
        SDL_free(text);
        return;
    }

    char *text_dup = strdup(text);
    SDL_free(text);
    if (!text_dup) {
        LOGW("Could not strdup input text");
        return;
    }

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_INJECT_TEXT;
    msg.inject_text.text = text_dup;
    if (!sc_controller_push_msg(im->controller, &msg)) {
        free(text_dup);
        LOGW("Could not request 'paste clipboard'");
    }
}

static void
rotate_device(struct sc_input_manager *im) {
    assert(im->controller && !im->camera);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_ROTATE_DEVICE;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request device rotation");
    }
}

static void
open_hard_keyboard_settings(struct sc_input_manager *im) {
    assert(im->controller && !im->camera);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_OPEN_HARD_KEYBOARD_SETTINGS;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request opening hard keyboard settings");
    }
}

static void
reset_video(struct sc_input_manager *im) {
    assert(im->controller);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_RESET_VIDEO;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request reset video");
    }
}

static void
camera_set_torch(struct sc_input_manager *im, bool on) {
    assert(im->controller && im->camera);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_CAMERA_SET_TORCH;
    msg.camera_set_torch.on = on;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request setting camera torch");
    }
}

static void
camera_zoom_in(struct sc_input_manager *im) {
    assert(im->controller && im->camera);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_CAMERA_ZOOM_IN;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request camera zoom in");
    }
}

static void
camera_zoom_out(struct sc_input_manager *im) {
    assert(im->controller && im->camera);

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_CAMERA_ZOOM_OUT;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request camera zoom out");
    }
}

static void
apply_orientation_transform(struct sc_input_manager *im,
                            enum sc_orientation transform) {
    struct sc_screen *screen = im->screen;
    enum sc_orientation new_orientation =
        sc_orientation_apply(screen->orientation, transform);
    sc_screen_set_orientation(screen, new_orientation);
}

static void
sc_input_manager_process_text_input(struct sc_input_manager *im,
                                    const SDL_TextInputEvent *event) {
    if (im->camera || !im->kp || im->screen->paused || im->disconnected) {
        return;
    }

    if (!im->kp->ops->process_text) {
        // The key processor does not support text input
        return;
    }

    uint16_t mod = sc_sdl_mod_normalize(SDL_GetModState());
    if (sc_shortcut_mods_is_shortcut_mod(im->sdl_shortcut_mods, mod)) {
        // A shortcut must never generate text events
        return;
    }

    struct sc_text_event evt = {
        .text = event->text,
    };

    im->kp->ops->process_text(im->kp, &evt);
}

static bool
simulate_virtual_finger(struct sc_input_manager *im,
                        enum android_motionevent_action action,
                        struct sc_point point) {
    bool up = action == AMOTION_EVENT_ACTION_UP;

    struct sc_control_msg msg;
    msg.type = SC_CONTROL_MSG_TYPE_INJECT_TOUCH_EVENT;
    msg.inject_touch_event.action = action;
    msg.inject_touch_event.position.screen_size = im->screen->frame_size;
    msg.inject_touch_event.position.point = point;
    msg.inject_touch_event.pointer_id = SC_POINTER_ID_VIRTUAL_FINGER;
    msg.inject_touch_event.pressure = up ? 0.0f : 1.0f;
    msg.inject_touch_event.action_button = 0;
    msg.inject_touch_event.buttons = 0;

    if (!sc_controller_push_msg(im->controller, &msg)) {
        LOGW("Could not request 'inject virtual finger event'");
        return false;
    }

    return true;
}

static struct sc_point
inverse_point(struct sc_point point, struct sc_size size,
              bool invert_x, bool invert_y) {
    if (invert_x) {
        point.x = size.width - point.x;
    }
    if (invert_y) {
        point.y = size.height - point.y;
    }
    return point;
}

static void
sc_input_manager_process_key(struct sc_input_manager *im,
                             const SDL_KeyboardEvent *event) {
    // some key events do not interact with the device, so process the event
    // even if control is disabled

    // controller is NULL if --no-control is requested
    bool control = im->controller;
    bool paused = im->screen->paused;
    bool video = im->screen->video;
    bool disconnected = im->disconnected;

    SDL_Keycode sdl_keycode = event->key;
    uint16_t mod = sc_sdl_mod_normalize(event->mod);
    bool down = event->type == SDL_EVENT_KEY_DOWN;
    bool ctrl = mod & SDL_KMOD_CTRL;
    bool shift = mod & SDL_KMOD_SHIFT;
    bool repeat = event->repeat;

    // Either the modifier includes a shortcut modifier, or the key
    // press/release is a modifier key.
    // The second condition is necessary to ignore the release of the modifier
    // key (because in this case mod is 0).
    uint16_t mods = im->sdl_shortcut_mods;
    bool is_shortcut = sc_shortcut_mods_is_shortcut_mod(mods, mod)
                    || sc_shortcut_mods_is_shortcut_key(mods, sdl_keycode);
    LOGD("KEYDBG: down=%d repeat=%d keycode=%d scancode=%d mod=0x%x mods=0x%x is_shortcut=%d ctrl=%d shift=%d",
         down, repeat, (int) sdl_keycode, (int) event->scancode, (unsigned) mod,
         (unsigned) mods, is_shortcut, ctrl, shift);

    if (down && !repeat && !disconnected) {
        if (sdl_keycode == im->last_keycode && mod == im->last_mod) {
            ++im->key_repeat;
        } else {
            im->key_repeat = 0;
            im->last_keycode = sdl_keycode;
            im->last_mod = mod;
        }
    }

    // Ctrl+G: save the current clipboard image to the device gallery.
    // Ctrl is not a MOD key (unlike Alt/Super), so without this check the
    // combination would be forwarded to the device as a regular key event.
    // Unlike MOD+Shift+G, Ctrl+letter combinations are never intercepted
    // by the Chinese input method (e.g. Ctrl+V works without switching
    // the keyboard layout), so this shortcut works with a Chinese IME
    // active.
    if (down && !repeat && ctrl && !shift
            && !(event->mod & SDL_KMOD_ALT) && !(event->mod & SDL_KMOD_GUI)
            && sdl_keycode == SDLK_G) {
        if (im->kp && !paused) {
            save_clipboard_image_to_gallery(im);
        }
        return;
    }

    // Ctrl+F: toggle the fps overlay visibility. Same interception pattern
    // as Ctrl+G: consumed by the client, never forwarded to the device, and
    // not blocked by a Chinese IME. F matches "fps" for easy recall.
    if (down && !repeat && ctrl && !shift
            && !(event->mod & SDL_KMOD_ALT) && !(event->mod & SDL_KMOD_GUI)
            && sdl_keycode == SDLK_F) {
        sc_screen_toggle_fps_overlay(im->screen);
        return;
    }

    // Ctrl+H: toggle the device screen power (screen off saves battery while
    // mirroring). Same interception pattern as Ctrl+G/F: never forwarded to
    // the device, works with a Chinese IME active.
    if (down && !repeat && ctrl && !shift
            && !(event->mod & SDL_KMOD_ALT) && !(event->mod & SDL_KMOD_GUI)
            && sdl_keycode == SDLK_H) {
        if (control && !im->camera && !disconnected && !paused) {
            set_display_power(im, im->screen_off);
        }
        return;
    }

    // Ctrl+P: one short physical POWER press. Consume repeats and key-up so
    // holding the shortcut cannot open the power menu or leak P to the app.
    if (sdl_keycode == SDLK_P && (im->ctrl_power_pressed
            || (ctrl && !shift && !(mod & SDL_KMOD_ALT)
                                 && !(mod & SDL_KMOD_GUI)))) {
        if (down && !repeat && !im->ctrl_power_pressed) {
            im->ctrl_power_pressed = true;
            if (control && im->kp && !im->camera && !disconnected && !paused) {
                action_power(im, SC_ACTION_DOWN);
                action_power(im, SC_ACTION_UP);
            }
        } else if (!down) {
            im->ctrl_power_pressed = false;
        }
        return;
    }

    // Ctrl+T: toggle the window always-on-top state. Same interception
    // pattern as Ctrl+G/F/H: never forwarded to the device, works with a
    // Chinese IME active. T matches "top" for easy recall. The bare T key
    // (without Ctrl) is already used in camera mode to toggle the torch, but
    // the Ctrl modifier keeps the two bindings distinct.
    if (down && !repeat && ctrl && !shift
            && !(event->mod & SDL_KMOD_ALT) && !(event->mod & SDL_KMOD_GUI)
            && sdl_keycode == SDLK_T) {
        sc_screen_toggle_always_on_top(im->screen);
        return;
    }

    // Shortcuts that do not involve the MOD key
    switch (sdl_keycode) {
        case SDLK_F11:
            if (video && !repeat && down) {
                bool alt = mod & SDL_KMOD_ALT;
                bool super = mod & SDL_KMOD_GUI;
                if (!ctrl && !shift && !alt && !super) {
                    sc_screen_toggle_fullscreen(im->screen);
                }
            }
            return;
    }

    if (is_shortcut) {
        enum sc_action action = down ? SC_ACTION_DOWN : SC_ACTION_UP;
        switch (sdl_keycode) {
            case SDLK_Z:
                if (video && down && !repeat) {
                    sc_screen_set_paused(im->screen, !shift);
                }
                return;
            case SDLK_DOWN:
                // Only capture if shift is set
                if (shift) {
                    if (video && !repeat && down) {
                        apply_orientation_transform(im,
                                                    SC_ORIENTATION_FLIP_180);
                    }
                    return;
                }
                break;
            case SDLK_UP:
                // Only capture if shift is set
                if (shift) {
                    if (video && !repeat && down) {
                        apply_orientation_transform(im, SC_ORIENTATION_FLIP_180);
                    }
                    return;
                }
                break;
            case SDLK_LEFT:
                if (video && !repeat && down) {
                    if (shift) {
                        apply_orientation_transform(im, SC_ORIENTATION_FLIP_0);
                    } else {
                        apply_orientation_transform(im, SC_ORIENTATION_270);
                    }
                }
                return;
            case SDLK_RIGHT:
                if (video && !repeat && down) {
                    if (shift) {
                        apply_orientation_transform(im, SC_ORIENTATION_FLIP_0);
                    } else {
                        apply_orientation_transform(im, SC_ORIENTATION_90);
                    }
                }
                return;
            case SDLK_F:
                if (video && !shift && !repeat && down) {
                    sc_screen_toggle_fullscreen(im->screen);
                }
                return;
            case SDLK_W:
                if (video && !shift && !repeat && down) {
                    sc_screen_resize_to_fit(im->screen);
                }
                return;
            case SDLK_G:
                LOGD("KEYDBG: first-switch SDLK_G shift=%d video=%d", shift, video);
                // MOD+G (no shift): resize to pixel-perfect. With shift held
                // (MOD+Shift+G), fall through to the save-to-gallery shortcut
                // handled in the device-control switch below.
                if (video && !shift && !repeat && down) {
                    sc_screen_resize_to_pixel_perfect(im->screen);
                    return;
                }
                break;
            case SDLK_I:
                if (video && !shift && !repeat && down) {
                    switch_fps_counter_state(im);
                }
                return;
            case SDLK_Q:
                sc_push_event(SDL_EVENT_QUIT);
                return;
        }

        if (disconnected) {
            // Only handle shortcuts that do not interact with the device (since
            // it is disconnected)
            return;
        }

        // Flatten conditions to avoid additional indentation levels
        if (control) {
            // Controls for all sources
            switch (sdl_keycode) {
                case SDLK_R:
                    // Only capture if shift is set
                    if (shift) {
                        if (!repeat && down && !paused) {
                            reset_video(im);
                        }
                        return;
                    }
                    break;
            }
        }

        if (control && !im->camera) {
            switch (sdl_keycode) {
                case SDLK_H:
                    if (im->kp && !shift && !repeat && !paused) {
                        action_home(im, action);
                    }
                    return;
                case SDLK_B: // fall-through
                case SDLK_BACKSPACE:
                    if (im->kp && !shift && !repeat && !paused) {
                        action_back(im, action);
                    }
                    return;
                case SDLK_S:
                    if (im->kp && !shift && !repeat && down && !paused) {
                        action_app_switch(im, action);
                    }
                    return;
                case SDLK_G:
                    LOGD("KEYDBG: second-switch SDLK_G shift=%d kp=%d paused=%d", shift, !!im->kp, paused);
                    // MOD+Shift+G: save the current clipboard image (the
                    // original file already cached in the device clipboard)
                    // to the device gallery. Not MOD+Shift+S: on Windows the
                    // default MOD is Alt or Super, and Alt+Shift (input
                    // language switch) / Super+Shift+S (Windows screenshot)
                    // collide with system shortcuts, while Ctrl+Shift+S is
                    // not a MOD combination at all (Ctrl is forwarded to the
                    // device).
                    if (im->kp && shift && !repeat && down && !paused) {
                        save_clipboard_image_to_gallery(im);
                    }
                    return;
                case SDLK_M:
                    if (im->kp && !shift && !repeat && !paused) {
                        action_menu(im, action);
                    }
                    return;
                case SDLK_P:
                    if (im->kp && !shift && !repeat && !paused) {
                        action_power(im, action);
                    }
                    return;
                case SDLK_O:
                    if (control && !repeat && down && !paused) {
                        bool on = shift;
                        set_display_power(im, on);
                    }
                    return;
                case SDLK_DOWN:
                    if (im->kp && !shift && !paused) {
                        // forward repeated events
                        action_volume_down(im, action);
                    }
                    return;
                case SDLK_UP:
                    if (im->kp && !shift && !paused) {
                        // forward repeated events
                        action_volume_up(im, action);
                    }
                    return;
                case SDLK_C:
                    if (im->kp && !repeat && down && !paused) {
                        if (shift) {
                            // MOD+Shift+C: copy the computer clipboard (image
                            // or text) to the device clipboard without pasting
                            set_device_clipboard(im, false, SC_SEQUENCE_INVALID);
                        } else {
                            get_device_clipboard(im, SC_COPY_KEY_COPY);
                        }
                    }
                    return;
                case SDLK_X:
                    if (im->kp && !shift && !repeat && down && !paused) {
                        get_device_clipboard(im, SC_COPY_KEY_CUT);
                    }
                    return;
                case SDLK_V:
                    if (im->kp && !repeat && down && !paused) {
                        if (shift || im->legacy_paste) {
                            // inject the text as input events
                            clipboard_paste(im);
                        } else {
                            // store the text in the device clipboard and paste,
                            // without requesting an acknowledgment
                            set_device_clipboard(im, true, SC_SEQUENCE_INVALID);
                        }
                    }
                    return;
                case SDLK_N:
                    if (!repeat && down && !paused) {
                        if (shift) {
                            collapse_panels(im);
                        } else if (im->key_repeat == 0) {
                            expand_notification_panel(im);
                        } else {
                            expand_settings_panel(im);
                        }
                    }
                    return;
                case SDLK_R:
                    if (!repeat && !shift && down && !paused) {
                        rotate_device(im);
                    }
                    return;
                case SDLK_K:
                    if (!shift && !repeat && down && !paused
                            && im->kp && im->kp->hid) {
                        // Only if the current keyboard is hid
                        open_hard_keyboard_settings(im);
                    }
                    return;
            }
        }

        if (control && im->camera) {
            switch (sdl_keycode) {
                case SDLK_T:
                    if (!repeat && down) {
                        camera_set_torch(im, !shift);
                    }
                    return;
                case SDLK_DOWN:
                    if (!shift && down && !paused) {
                        // forward repeated events
                        camera_zoom_out(im);
                    }
                    return;
                case SDLK_UP:
                    if (!shift && down && !paused) {
                        // forward repeated events
                        camera_zoom_in(im);
                    }
                    return;
            }
        }

        return;
    }

    if (!im->kp || paused) {
        return;
    }

    assert(!im->camera);

    uint64_t ack_to_wait = SC_SEQUENCE_INVALID;
    bool is_ctrl_v = ctrl && !shift && sdl_keycode == SDLK_V && down && !repeat;
    if (im->clipboard_autosync && is_ctrl_v) {
        if (im->legacy_paste) {
            // inject the text as input events
            clipboard_paste(im);
            return;
        }

        // If the automatic clipboard sync is enabled and the computer
        // clipboard still contains the content that was already pushed to
        // the device, do not push it again: the device clipboard already
        // has the latest content, only the PASTE key must be injected.
        // Pushing again would create a duplicate clipboard entry on the
        // device (e.g. two files in the clipboard history for one copy).
        bool push = true;
#ifdef _WIN32
        if (im->clipboard_sync) {
            uint32_t seq = GetClipboardSequenceNumber();
#ifdef _WIN32
            // Fingerprint check first: WeChat writes the clipboard twice
            // (two different sequence numbers with identical content), so
            // the automatic sync dedup advances the sequence number without
            // pushing. Comparing only the sequence number would wrongly
            // conclude "not pushed yet" and push the same content again.
            if (clipboard_last_pushed_fp_valid) {
                uint64_t fp;
                if (clipboard_current_fingerprint(&fp)
                        && fp == clipboard_last_pushed_fp) {
                    LOGD("Clipboard already pushed to device (fingerprint match), only injecting PASTE");
                    push = false;
                }
            }
#endif
            if (push && clipboard_last_pushed_seq
                    && seq == clipboard_last_pushed_seq) {
                LOGD("Clipboard already pushed to device, only injecting PASTE");
                push = false;
            }
        }
#endif

        uint64_t sequence = SC_SEQUENCE_INVALID;
        if (push) {
            // Request an acknowledgement only if necessary
            sequence = im->kp->async_paste ? im->next_sequence
                                           : SC_SEQUENCE_INVALID;

            // Synchronize the computer clipboard to the device clipboard
            // before sending Ctrl+v, to allow seamless copy-paste. This
            // covers the case where the automatic sync is disabled
            // (--no-clipboard-sync) or the clipboard changed but the
            // automatic sync did not trigger yet.
            bool ok = set_device_clipboard(im, false, sequence);
            if (!ok) {
                LOGW("Clipboard could not be synchronized, Ctrl+v not injected");
                return;
            }

            if (im->kp->async_paste) {
                // The key processor must wait for this ack before injecting
                // Ctrl+v
                ack_to_wait = sequence;
                // Increment only when the request succeeded
                ++im->next_sequence;
            }
        }
    }

    enum sc_keycode keycode = sc_keycode_from_sdl(sdl_keycode);
    // Accept SC_KEYCODE_UNKNOWN, as long as the physical scancode is known

    enum sc_scancode scancode = sc_scancode_from_sdl(event->scancode);
    if (scancode == SC_SCANCODE_UNKNOWN) {
        return;
    }

    struct sc_key_event evt = {
        .action = sc_action_from_sdl_keyboard_type(event->type),
        .keycode = keycode,
        .scancode = scancode,
        .repeat = event->repeat,
        .mods_state = sc_mods_state_from_sdl(mod),
    };

    assert(im->kp->ops->process_key);
    im->kp->ops->process_key(im->kp, &evt, ack_to_wait);
}

static struct sc_position
sc_input_manager_get_position(struct sc_input_manager *im, int32_t x,
                                                           int32_t y) {
    if (im->mp->relative_mode) {
        // No absolute position
        return (struct sc_position) {
            .screen_size = {0, 0},
            .point = {0, 0},
        };
    }

    return (struct sc_position) {
        .screen_size = im->screen->frame_size,
        .point = sc_screen_convert_window_to_frame_coords(im->screen, x, y),
    };
}

static void
sc_input_manager_process_mouse_motion(struct sc_input_manager *im,
                                      const SDL_MouseMotionEvent *event) {
    if (im->camera || !im->mp || im->screen->paused || im->disconnected) {
        return;
    }

    if (event->which == SDL_TOUCH_MOUSEID) {
        // simulated from touch events, so it's a duplicate
        return;
    }

    struct sc_mouse_motion_event evt = {
        .position = sc_input_manager_get_position(im, event->x, event->y),
        .pointer_id = im->vfinger_down ? SC_POINTER_ID_GENERIC_FINGER
                                       : SC_POINTER_ID_MOUSE,
        .xrel = event->xrel,
        .yrel = event->yrel,
        .buttons_state = im->mouse_buttons_state,
    };

    assert(im->mp->ops->process_mouse_motion);
    im->mp->ops->process_mouse_motion(im->mp, &evt);

    // vfinger must never be used in relative mode
    assert(!im->mp->relative_mode || !im->vfinger_down);

    if (im->vfinger_down) {
        assert(!im->mp->relative_mode); // assert one more time
        struct sc_point mouse =
           sc_screen_convert_window_to_frame_coords(im->screen, event->x,
                                                    event->y);
        struct sc_point vfinger = inverse_point(mouse, im->screen->frame_size,
                                                im->vfinger_invert_x,
                                                im->vfinger_invert_y);
        simulate_virtual_finger(im, AMOTION_EVENT_ACTION_MOVE, vfinger);
    }
}

static void
sc_input_manager_process_touch(struct sc_input_manager *im,
                               const SDL_TouchFingerEvent *event) {
    if (im->camera || !im->mp || im->screen->paused || im->disconnected) {
        return;
    }

    if (!im->mp->ops->process_touch) {
        // The mouse processor does not support touch events
        return;
    }

    struct sc_size window_size = sc_sdl_get_window_size(im->screen->window);

    // SDL touch event coordinates are normalized in the range [0; 1]
    int32_t x = event->x * (int32_t) window_size.width;
    int32_t y = event->y * (int32_t) window_size.height;

    struct sc_touch_event evt = {
        .position = {
            .screen_size = im->screen->frame_size,
            .point =
                sc_screen_convert_window_to_frame_coords(im->screen, x, y),
        },
        .action = sc_touch_action_from_sdl(event->type),
        .pointer_id = event->fingerID,
        .pressure = event->pressure,
    };

    im->mp->ops->process_touch(im->mp, &evt);
}

static enum sc_mouse_binding
sc_input_manager_get_binding(const struct sc_mouse_binding_set *bindings,
                             uint8_t sdl_button) {
    switch (sdl_button) {
        case SDL_BUTTON_LEFT:
            return SC_MOUSE_BINDING_CLICK;
        case SDL_BUTTON_RIGHT:
            return bindings->right_click;
        case SDL_BUTTON_MIDDLE:
            return bindings->middle_click;
        case SDL_BUTTON_X1:
            return bindings->click4;
        case SDL_BUTTON_X2:
            return bindings->click5;
        default:
            return SC_MOUSE_BINDING_DISABLED;
    }
}

static void
sc_input_manager_process_mouse_button(struct sc_input_manager *im,
                                      const SDL_MouseButtonEvent *event) {
    // some mouse events do not interact with the device, so process the event
    // even if control is disabled

    if (im->camera || im->disconnected) {
        return;
    }

    if (event->which == SDL_TOUCH_MOUSEID) {
        // simulated from touch events, so it's a duplicate
        return;
    }

    bool control = im->controller;
    bool paused = im->screen->paused;
    bool down = event->type == SDL_EVENT_MOUSE_BUTTON_DOWN;

    enum sc_mouse_button button = sc_mouse_button_from_sdl(event->button);
    if (button == SC_MOUSE_BUTTON_UNKNOWN) {
        return;
    }

    if (!down) {
        // Mark the button as released
        im->mouse_buttons_state &= ~button;
    }

    SDL_Keymod keymod = SDL_GetModState();
    bool ctrl_pressed = keymod & SDL_KMOD_CTRL;
    bool shift_pressed = keymod & SDL_KMOD_SHIFT;

    if (control && !paused) {
        enum sc_action action = down ? SC_ACTION_DOWN : SC_ACTION_UP;

        struct sc_mouse_binding_set *bindings = !shift_pressed
                                              ? &im->mouse_bindings.pri
                                              : &im->mouse_bindings.sec;
        enum sc_mouse_binding binding =
            sc_input_manager_get_binding(bindings, event->button);
        assert(binding != SC_MOUSE_BINDING_AUTO);
        switch (binding) {
            case SC_MOUSE_BINDING_DISABLED:
                // ignore click
                return;
            case SC_MOUSE_BINDING_BACK:
                if (im->kp) {
                    press_back_or_turn_screen_on(im, action);
                }
                return;
            case SC_MOUSE_BINDING_HOME:
                if (im->kp) {
                    action_home(im, action);
                }
                return;
            case SC_MOUSE_BINDING_APP_SWITCH:
                if (im->kp) {
                    action_app_switch(im, action);
                }
                return;
            case SC_MOUSE_BINDING_EXPAND_NOTIFICATION_PANEL:
                if (down) {
                    if (event->clicks < 2) {
                        expand_notification_panel(im);
                    } else {
                        expand_settings_panel(im);
                    }
                }
                return;
            default:
                assert(binding == SC_MOUSE_BINDING_CLICK);
                break;
        }
    }

    // double-click on black borders resizes to fit the device screen
    bool video = im->screen->video;
    bool mouse_relative_mode = im->mp && im->mp->relative_mode;
    if (video && !mouse_relative_mode && event->button == SDL_BUTTON_LEFT
            && event->clicks == 2) {
        int32_t x = event->x;
        int32_t y = event->y;
        SDL_FRect *r = &im->screen->rect;
        bool outside = x < r->x || x >= r->x + r->w
                    || y < r->y || y >= r->y + r->h;
        if (outside) {
            if (down) {
                sc_screen_resize_to_fit(im->screen);
            }
            return;
        }
    }

    if (!im->mp || paused) {
        return;
    }

    if (down) {
        // Mark the button as pressed
        im->mouse_buttons_state |= button;
    }

    bool change_vfinger = event->button == SDL_BUTTON_LEFT &&
            ((down && !im->vfinger_down && (ctrl_pressed || shift_pressed)) ||
             (!down && im->vfinger_down));
    bool use_finger = im->vfinger_down || change_vfinger;

    struct sc_mouse_click_event evt = {
        .position = sc_input_manager_get_position(im, event->x, event->y),
        .action = sc_action_from_sdl_mousebutton_type(event->type),
        .button = button,
        .pointer_id = use_finger ? SC_POINTER_ID_GENERIC_FINGER
                                 : SC_POINTER_ID_MOUSE,
        .buttons_state = im->mouse_buttons_state,
    };

    assert(im->mp->ops->process_mouse_click);
    im->mp->ops->process_mouse_click(im->mp, &evt);

    if (im->mp->relative_mode) {
        assert(!im->vfinger_down); // vfinger must not be used in relative mode
        // No pinch-to-zoom simulation
        return;
    }

    // Pinch-to-zoom, rotate and tilt simulation.
    //
    // If Ctrl is hold when the left-click button is pressed, then
    // pinch-to-zoom mode is enabled: on every mouse event until the left-click
    // button is released, an additional "virtual finger" event is generated,
    // having a position inverted through the center of the screen.
    //
    // In other words, the center of the rotation/scaling is the center of the
    // screen.
    //
    // To simulate a vertical tilt gesture (a vertical slide with two fingers),
    // Shift can be used instead of Ctrl. The "virtual finger" has a position
    // inverted with respect to the vertical axis of symmetry in the middle of
    // the screen.
    //
    // To simulate a horizontal tilt gesture (a horizontal slide with two
    // fingers), Ctrl+Shift can be used. The "virtual finger" has a position
    // inverted with respect to the horizontal axis of symmetry in the middle
    // of the screen. It is expected to be less frequently used, that's why the
    // one-mod shortcuts are assigned to rotation and vertical tilt.
    if (change_vfinger) {
        struct sc_point mouse =
            sc_screen_convert_window_to_frame_coords(im->screen, event->x,
                                                                 event->y);
        if (down) {
            // Ctrl  Shift     invert_x  invert_y
            // ----  ----- ==> --------  --------
            //   0     0           0         0      -
            //   0     1           1         0      vertical tilt
            //   1     0           1         1      rotate
            //   1     1           0         1      horizontal tilt
            im->vfinger_invert_x = ctrl_pressed ^ shift_pressed;
            im->vfinger_invert_y = ctrl_pressed;
        }
        struct sc_point vfinger = inverse_point(mouse, im->screen->frame_size,
                                                im->vfinger_invert_x,
                                                im->vfinger_invert_y);
        enum android_motionevent_action action = down
                                               ? AMOTION_EVENT_ACTION_DOWN
                                               : AMOTION_EVENT_ACTION_UP;
        if (!simulate_virtual_finger(im, action, vfinger)) {
            return;
        }
        im->vfinger_down = down;
    }
}

static void
sc_input_manager_process_mouse_wheel(struct sc_input_manager *im,
                                     const SDL_MouseWheelEvent *event) {
    if (im->camera || !im->kp || im->screen->paused || im->disconnected) {
        return;
    }

    if (!im->mp->ops->process_mouse_scroll) {
        // The mouse processor does not support scroll events
        return;
    }

    // mouse_x and mouse_y are expressed in pixels relative to the window
    float mouse_x;
    float mouse_y;
    uint32_t buttons = SDL_GetMouseState(&mouse_x, &mouse_y);
    (void) buttons; // Actual buttons are tracked manually to ignore shortcuts

    struct sc_mouse_scroll_event evt = {
        .position = sc_input_manager_get_position(im, mouse_x, mouse_y),
        .hscroll = event->x,
        .vscroll = event->y,
        .buttons_state = im->mouse_buttons_state,
    };

    im->mp->ops->process_mouse_scroll(im->mp, &evt);
}

static void
sc_input_manager_process_gamepad_device(struct sc_input_manager *im,
                                       const SDL_GamepadDeviceEvent *event) {
    // Handle device added or removed even if paused

    if (im->camera || !im->gp || im->disconnected) {
        return;
    }

    if (event->type == SDL_EVENT_GAMEPAD_ADDED) {
        SDL_Gamepad *sdl_gamepad = SDL_OpenGamepad(event->which);
        if (!sdl_gamepad) {
            LOGW("Could not open gamepad: %s", SDL_GetError());
            return;
        }

        SDL_Joystick *joystick = SDL_GetGamepadJoystick(sdl_gamepad);
        if (!joystick) {
            LOGW("Could not get gamepad joystick: %s", SDL_GetError());
            SDL_CloseGamepad(sdl_gamepad);
            return;
        }

        struct sc_gamepad_device_event evt = {
            .gamepad_id = SDL_GetJoystickID(joystick),
        };
        im->gp->ops->process_gamepad_added(im->gp, &evt);
    } else if (event->type == SDL_EVENT_GAMEPAD_REMOVED) {
        SDL_JoystickID id = event->which;

        SDL_Gamepad *sdl_gamepad = SDL_GetGamepadFromID(id);
        if (sdl_gamepad) {
            SDL_CloseGamepad(sdl_gamepad);
        } else {
            LOGW("Unknown gamepad device removed");
        }

        struct sc_gamepad_device_event evt = {
            .gamepad_id = id,
        };
        im->gp->ops->process_gamepad_removed(im->gp, &evt);
    } else {
        // Nothing to do
        return;
    }
}

static void
sc_input_manager_process_gamepad_axis(struct sc_input_manager *im,
                                      const SDL_GamepadAxisEvent *event) {
    if (im->camera || !im->gp || im->screen->paused || im->disconnected) {
        return;
    }

    enum sc_gamepad_axis axis = sc_gamepad_axis_from_sdl(event->axis);
    if (axis == SC_GAMEPAD_AXIS_UNKNOWN) {
        return;
    }

    struct sc_gamepad_axis_event evt = {
        .gamepad_id = event->which,
        .axis = axis,
        .value = event->value,
    };
    im->gp->ops->process_gamepad_axis(im->gp, &evt);
}

static void
sc_input_manager_process_gamepad_button(struct sc_input_manager *im,
                                       const SDL_GamepadButtonEvent *event) {
    if (im->camera || !im->gp || im->screen->paused || im->disconnected) {
        return;
    }

    enum sc_gamepad_button button = sc_gamepad_button_from_sdl(event->button);
    if (button == SC_GAMEPAD_BUTTON_UNKNOWN) {
        return;
    }

    struct sc_gamepad_button_event evt = {
        .gamepad_id = event->which,
        .action = sc_action_from_sdl_gamepad_button_type(event->type),
        .button = button,
    };
    im->gp->ops->process_gamepad_button(im->gp, &evt);
}

static bool
is_apk(const char *file) {
    const char *ext = strrchr(file, '.');
    return ext && !strcmp(ext, ".apk");
}

static void
sc_input_manager_process_file(struct sc_input_manager *im,
                              const SDL_DropEvent *event) {
    if (im->camera || !im->controller || im->disconnected) {
        return;
    }

    assert(event->type == SDL_EVENT_DROP_FILE);
    char *file = strdup(event->data);
    if (!file) {
        LOG_OOM();
        return;
    }

    enum sc_file_pusher_action action;
    if (is_apk(file)) {
        action = SC_FILE_PUSHER_ACTION_INSTALL_APK;
    } else {
        action = SC_FILE_PUSHER_ACTION_PUSH_FILE;
    }
    bool ok = sc_file_pusher_request(im->fp, action, file);
    if (!ok) {
        free(file);
    }
}

static void
sc_input_manager_on_device_disconnected(struct sc_input_manager *im) {
    im->disconnected = true;

    struct sc_fps_counter *fps_counter = &im->screen->fps_counter;
    if (sc_fps_counter_is_started(fps_counter)) {
        sc_fps_counter_stop(fps_counter);
    }
}

void
sc_input_manager_handle_event(struct sc_input_manager *im,
                              const SDL_Event *event) {
    switch (event->type) {
        case SDL_EVENT_TEXT_INPUT:
            sc_input_manager_process_text_input(im, &event->text);
            break;
        case SDL_EVENT_KEY_DOWN:
        case SDL_EVENT_KEY_UP:
            sc_input_manager_process_key(im, &event->key);
            break;
        case SDL_EVENT_MOUSE_MOTION:
            sc_input_manager_process_mouse_motion(im, &event->motion);
            break;
        case SDL_EVENT_MOUSE_WHEEL:
            sc_input_manager_process_mouse_wheel(im, &event->wheel);
            break;
        case SDL_EVENT_MOUSE_BUTTON_DOWN:
        case SDL_EVENT_MOUSE_BUTTON_UP:
            sc_input_manager_process_mouse_button(im, &event->button);
            break;
        case SDL_EVENT_FINGER_MOTION:
        case SDL_EVENT_FINGER_DOWN:
        case SDL_EVENT_FINGER_UP:
            sc_input_manager_process_touch(im, &event->tfinger);
            break;
        case SDL_EVENT_GAMEPAD_ADDED:
        case SDL_EVENT_GAMEPAD_REMOVED:
            sc_input_manager_process_gamepad_device(im, &event->gdevice);
            break;
        case SDL_EVENT_GAMEPAD_AXIS_MOTION:
            sc_input_manager_process_gamepad_axis(im, &event->gaxis);
            break;
        case SDL_EVENT_GAMEPAD_BUTTON_DOWN:
        case SDL_EVENT_GAMEPAD_BUTTON_UP:
            sc_input_manager_process_gamepad_button(im, &event->gbutton);
            break;
        case SDL_EVENT_DROP_FILE:
            sc_input_manager_process_file(im, &event->drop);
            break;
        case SDL_EVENT_CLIPBOARD_UPDATE:
            sc_input_manager_process_clipboard_update(im);
            break;
        case SC_EVENT_CLIPBOARD_CHANGED:
            sc_input_manager_process_clipboard_update(im);
            break;
        case SC_EVENT_DEVICE_DISCONNECTED:
            sc_input_manager_on_device_disconnected(im);
            break;
    }
}

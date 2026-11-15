#include "perspective.h"
#include "local_preview.h"
#include "perspective_color.h"
#include "perspective_key.h"
#include "perspective_locators.h"
#include "perspective_test_control.h"
#include "events.h"
#include <string.h>
#include <stdlib.h>
#include <math.h>
#include <SDL3/SDL.h>
#include <libavcodec/avcodec.h>
#include <libavutil/opt.h>
#include <libavutil/hwcontext.h>
#include <libavutil/pixdesc.h>
#include <libswscale/swscale.h>
#include "util/binary.h"
#include "util/log.h"
#include "util/tick.h"

#ifdef _WIN32
#include <windows.h>
#include "perspective_notice.h"
#include "desktop_capture.h"
#define CAMERA_WIDTH 1920
#define CAMERA_HEIGHT 1080
#define CAMERA_BITRATE_NATIVE 8000000
#define CAMERA_BITRATE_COMPATIBILITY 16000000
static bool set_capture_affinity(struct sc_perspective *p, bool exclude);

static void
request_calibration_locked(struct sc_perspective *p) {
    p->handoff_deadline = 0;
    p->alignment_valid = false;
    p->calibration_region_valid = false;
    atomic_store(&p->calibration_requested, true);
}

static void
reuse_handoff_alignment_locked(struct sc_perspective *p) {
    p->handoff_deadline = 0;
    atomic_store(&p->calibration_requested, false);
    atomic_store(&p->keyframe, true);
    LOGI("Perspective alignment reused: scanner geometry verified across scene transition");
}

/* Hot-start cache key: virtual camera format + observed app-window frame +
 * display orientation. WeChat version/layout changes show up as a geometry
 * change or fail the one-frame cache verification, which forces a real
 * calibration instead of trusting stale data. */
static void
perspective_cache_key(struct sc_perspective *p, char *out, size_t n) {
    snprintf(out, n, "v1:%dx%d:%dx%d:%u", CAMERA_WIDTH, CAMERA_HEIGHT,
             p->observed_width, p->observed_height, p->orientation);
}

/* Look up this geometry in SCEZ_PERSPECTIVE_CALIB ("key|m0,m1,..;key|..."). */
static void
perspective_cache_lookup(struct sc_perspective *p) {
    char key[64];
    perspective_cache_key(p, key, sizeof(key));
    const char *cur = p->cache_env;
    while (cur && *cur) {
        const char *sep = strchr(cur, '|');
        if (!sep) { break; }
        size_t klen = (size_t) (sep - cur);
        if (klen == strlen(key) && !strncmp(cur, key, klen)) {
            double vals[6] = {0};
            const char *v = sep + 1;
            int got = 0;
            while (got < 6 && v && *v) {
                char *end = NULL;
                vals[got] = strtod(v, &end);
                if (end == v) { break; }
                ++got;
                v = end;
                if (*v == ',') { ++v; } else { break; }
            }
            if (got == 6) {
                memcpy(p->cache_map, vals, sizeof(vals));
                memcpy(p->map, vals, sizeof(vals));
                p->alignment_valid = true;
                p->cache_hit = true;
                atomic_store(&p->keyframe, true);
                snprintf(p->cache_key, sizeof(p->cache_key), "%s", key);
                LOGI("Perspective cache candidate: %s", key);
            }
            return;
        }
        const char *next = strchr(cur, ';');
        cur = next ? next + 1 : NULL;
    }
}
/* GUI writes this file when the virtual display's top activity is really
 * WeChat's BaseScanUI. It is a pure page-readiness signal (no WeChat state
 * change); frame content remains the final calibration-readiness check. */
static bool
perspective_scanner_page_ready(struct sc_perspective *p) {
    if (!p->ready_file[0]) { return false; }
    DWORD attr = GetFileAttributesA(p->ready_file);
    return attr != INVALID_FILE_ATTRIBUTES;
}

static bool
send_packet(struct sc_perspective *p, const uint8_t *data, uint32_t size,
            uint64_t sequence) {
    uint8_t header[12];
    sc_write32be(header, size);
    sc_write64be(header + 4, sequence);
    return net_send_all(p->socket, header, sizeof(header)) == sizeof(header)
        && (!size || net_send_all(p->socket, data, size) == size);
}

static int
receive_state(void *userdata) {
    struct sc_perspective *p = userdata;
    uint8_t state;
    while (!atomic_load(&p->stopped)
            && net_recv_all(p->socket, &state, 1) == 1) {
        if (state == 4) {
            atomic_store(&p->evicted, true);
            sc_push_event(SC_EVENT_PERSPECTIVE_STATE);
            LOGI("Perspective camera evicted; projection remains active");
            if (!camera_notice_show((HWND) p->hwnd)) { LOGW("Could not display camera occupancy notice"); }
            continue;
        }
        if (state > 3) { break; }
        if (state && !set_capture_affinity(p,true)) { break; }
        if (!state) { set_capture_affinity(p,false); }
        if (state) { atomic_store(&p->keyframe, true); }
        if (state == 3) {
            sc_mutex_lock(&p->mutex);
            sc_tick now = sc_tick_now();
            p->calibration_not_before = now + SC_TICK_FROM_MS(650);
            if (p->handoff_deadline > now && !p->handoff_restarted
                    && !atomic_load(&p->compatibility_key)) {
                p->handoff_restarted = true;
                if (p->handoff_confirmed) {
                    reuse_handoff_alignment_locked(p);
                } else {
                    // Keep the measured map until the scene reports its geometry.
                    atomic_store(&p->calibration_requested, true);
                }
            } else {
                request_calibration_locked(p);
            }
            atomic_store(&p->evicted, false);
            sc_mutex_unlock(&p->mutex);
        }
        if (state && !atomic_load(&p->active)) {
            atomic_fetch_add(&p->camera_cycle, 1);
        }
        bool previous_active = atomic_exchange(&p->active, state != 0);
        if (previous_active != (state != 0)) {
            LOGI("SCRCPY_EZ_PERSPECTIVE_ACTIVE=%d",state != 0);
        }
        if (state == 3 || previous_active != (state != 0)) {
            sc_push_event(SC_EVENT_PERSPECTIVE_STATE);
        }
    }
    atomic_store(&p->stopped, true);
    if(atomic_exchange(&p->active,false)){LOGI("SCRCPY_EZ_PERSPECTIVE_ACTIVE=0");}
    set_capture_affinity(p,false);
    sc_push_event(SC_EVENT_PERSPECTIVE_STATE);
    return 0;
}

static unsigned
camera_bitrate(const struct sc_perspective *p) {
    // Compatibility estimates opacity from the returning key. Preserve more
    // chroma precision when desktop content and the backing share this feed.
    return atomic_load(&p->compatibility_key) ? CAMERA_BITRATE_COMPATIBILITY
                                              : CAMERA_BITRATE_NATIVE;
}

static AVCodecContext *
open_encoder(struct sc_desktop_capture *capture, unsigned fps, unsigned bitrate) {
    if(getenv("SCEZ_PERSPECTIVE_TEST_CONTROL")){av_log_set_level(AV_LOG_VERBOSE);}
    const char *names[] = {"h264_nvenc", "h264_mf"};
    for (unsigned i = 0; i < 2; ++i) {
        const AVCodec *codec = avcodec_find_encoder_by_name(names[i]);
        if (!codec) { continue; }
        AVCodecContext *ctx = avcodec_alloc_context3(codec);
        if (!ctx) { return NULL; }
        ctx->width = CAMERA_WIDTH;
        ctx->height = CAMERA_HEIGHT;
        ctx->pix_fmt = i == 0 ? AV_PIX_FMT_D3D11 : AV_PIX_FMT_NV12;
        if (i == 0) { ctx->hw_frames_ctx = sc_desktop_capture_ref_frames(capture); }
        ctx->time_base = (AVRational) {1, fps};
        ctx->framerate = (AVRational) {fps, 1};
        ctx->bit_rate = bitrate;
        ctx->rc_max_rate = bitrate;
        ctx->rc_buffer_size = bitrate/10;
        ctx->gop_size = fps;
        ctx->colorspace = AVCOL_SPC_BT709; ctx->color_range = AVCOL_RANGE_MPEG;
        ctx->color_primaries = AVCOL_PRI_BT709; ctx->color_trc = AVCOL_TRC_BT709;
        ctx->max_b_frames = 0;
        ctx->flags |= AV_CODEC_FLAG_LOW_DELAY;
        AVDictionary *opts = NULL;
        if (i == 0) {
            av_dict_set(&opts, "preset", "p1", 0);
            av_dict_set(&opts, "tune", "ull", 0);
            av_dict_set(&opts, "rc", "cbr", 0);
            av_dict_set(&opts, "zerolatency", "1", 0);
            av_dict_set(&opts, "delay", "0", 0);
            av_dict_set(&opts, "forced-idr", "1", 0);
        } else {
            av_dict_set(&opts, "rate_control", "cbr", 0);
            av_dict_set(&opts, "scenario", "display_remoting", 0);
        }
        int ret = avcodec_open2(ctx, codec, &opts);
        av_dict_free(&opts);
        if (ret >= 0) {
            LOGI("Perspective camera: %s, %dx%d, %u fps, %s", names[i],
                 CAMERA_WIDTH, CAMERA_HEIGHT, fps, i == 0 ? "D3D11 texture input" : "NV12 download fallback");
            LOGI("Perspective camera input bitrate: %u",bitrate);
            return ctx;
        }
        avcodec_free_context(&ctx);
    }
    return NULL;
}

static bool
desktop_available(void) {
    HDESK input = OpenInputDesktop(0, FALSE, DESKTOP_READOBJECTS);
    if (!input) { return false; }
    wchar_t name[128];
    DWORD needed;
    bool available = GetUserObjectInformationW(input, UOI_NAME, name, sizeof(name), &needed)
                  && !lstrcmpiW(name, L"Default");
    CloseDesktop(input);
    return available;
}

static void
orient_mapping(double m[6], unsigned orientation) {
    double points[3][2] = {{m[4], m[5]}, {m[4]+m[0], m[5]+m[2]}, {m[4]+m[1], m[5]+m[3]}};
    for (int i = 0; i < 3; ++i) {
        double x = points[i][0], y = points[i][1];
        if (orientation & 4) { x = 1 - x; }
        switch (orientation & 3) {
            case 1: points[i][0] = 1-y; points[i][1] = x; break;
            case 2: points[i][0] = 1-x; points[i][1] = 1-y; break;
            case 3: points[i][0] = y; points[i][1] = 1-x; break;
            default: points[i][0] = x; points[i][1] = y; break;
        }
    }
    m[0] = points[1][0]-points[0][0]; m[1] = points[2][0]-points[0][0];
    m[2] = points[1][1]-points[0][1]; m[3] = points[2][1]-points[0][1];
    m[4] = points[0][0]; m[5] = points[0][1];
}

static void
capture_keep_awake(bool enabled) {
    // Windows tracks this request per thread. Only the capture thread changes
    // it, and an idle camera channel must not keep the display or system awake.
    EXECUTION_STATE flags = ES_CONTINUOUS;
    if (enabled) {
        flags |= ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED;
    }
    if (SetThreadExecutionState(flags)) {
        LOGI("Perspective camera sleep prevention %s", enabled ? "active" : "released");
    } else {
        LOGW("Could not %s perspective camera sleep prevention",
             enabled ? "request" : "release");
    }
}

static int
capture_screen(void *userdata) {
    struct sc_perspective *p = userdata;
    typedef HANDLE (WINAPI *dpi_fn)(HANDLE);
    dpi_fn set_dpi = (dpi_fn)(void *)GetProcAddress(GetModuleHandleW(L"user32.dll"), "SetThreadDpiAwarenessContext");
    if (set_dpi) { set_dpi(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2); }
    AVCodecContext *encoder = NULL;
    AVFrame *frame = av_frame_alloc(), *software = av_frame_alloc();
    AVPacket *packet = av_packet_alloc();
    struct sc_desktop_capture *source = NULL;
    uint64_t sequence = 0;
    bool awake_requested = false;
    bool failed = !frame || !software || !packet;
    const char *metrics_path = getenv("SCEZ_PERSPECTIVE_METRICS");
    FILE *metrics = metrics_path ? fopen(metrics_path, "w") : NULL;
    if (metrics) { fputs("sequence,begin_us,render_end_us,sent_end_us,mode,x,y,width,height\n", metrics); }
    uint8_t config[12];
    sc_write32be(config, 0x455a5643);
    sc_write32be(config + 4, CAMERA_WIDTH);
    sc_write32be(config + 8, CAMERA_HEIGHT);
    if (failed || net_send_all(p->socket, config, sizeof(config)) != sizeof(config)) { failed = true; goto end; }
    LOGI("Perspective capture thread started");
    sc_tick next = sc_tick_now(), heartbeat = 0, desktop_check = 0;
    bool available = true, was_active = false;
    unsigned test_mode=0,last_photo=0;sc_tick test_check=0;
    unsigned test_bitrate = 0;
    const char *test_rate = getenv("SCEZ_PERSPECTIVE_TEST_BITRATE");
    if (test_rate) {
        char *end;
        unsigned long value = strtoul(test_rate, &end, 10);
        if (!*end && value >= 1000000 && value <= 60000000) {
            test_bitrate = value;
        }
    }
    while (!atomic_load(&p->stopped)) {
        sc_tick now = sc_tick_now();
        if (now - desktop_check > SC_TICK_FROM_MS(500)) {
            bool current = desktop_available();
            if (current != available) {
                LOGI("Perspective desktop %s", current ? "resumed" : "unavailable; sending black frames");
                sc_desktop_capture_suspend(source);
                available = current;
            }
            desktop_check = now;
        }
        if (!atomic_load(&p->active)) {
            if (was_active) { sc_desktop_capture_suspend(source); }
            if (awake_requested) {
                capture_keep_awake(false);
                awake_requested = false;
            }
            was_active = false;
            if (now - heartbeat > SC_TICK_FROM_MS(500)) {
                if (!send_packet(p, NULL, 0, sequence)) { failed = true; break; }
                heartbeat = now;
            }
            SDL_DelayNS(SC_TICK_TO_NS(SC_TICK_FROM_MS(15)));
            next = sc_tick_now(); continue;
        }
        if (!awake_requested) {
            capture_keep_awake(true);
            awake_requested = true;
        }
        if (!source) {
            source = sc_desktop_capture_create(CAMERA_WIDTH, CAMERA_HEIGHT, p->fps);
            if (!source) { failed = true; break; }
            unsigned rate = test_bitrate ? test_bitrate : camera_bitrate(p);
            encoder = open_encoder(source, p->fps, rate);
            if (!encoder) { failed = true; break; }
            // Initialization/consent may take most of a calibration window.
            // Start its deadline only after the producer can actually send.
            now = sc_tick_now(); next = now;
        }
        bool keyframe = !was_active || atomic_exchange(&p->keyframe, false);
        if(now-test_check>SC_TICK_FROM_MS(100)){
            unsigned current=sc_perspective_test_mode();test_check=now;
            if(current!=test_mode){test_mode=current;keyframe=true;LOGI("PERSPECTIVE_TEST input_mode=%u",test_mode);}
            unsigned rate = sc_perspective_test_bitrate();
            if (!rate) { rate = test_bitrate ? test_bitrate : camera_bitrate(p); }
            if (rate != encoder->bit_rate && !strcmp(encoder->codec->name, "h264_nvenc")) {
                encoder->bit_rate = encoder->rc_max_rate = rate;
                encoder->rc_buffer_size = rate / 10;
                keyframe = true;
                LOGI("Perspective camera input bitrate requested: %u", rate);
            }
        }
        was_active = true;
        double x, y, w, h, mapping[6];
        bool calibrating, awaiting_calibration;unsigned calibration_pass;
        sc_mutex_lock(&p->mutex);
        x=p->x; y=p->y; w=p->width; h=p->height;
        if (p->scanner_waiting_ready) {
            bool ready = perspective_scanner_page_ready(p);
            if (ready || now >= p->scanner_ready_deadline) {
                p->scanner_waiting_ready = false;
                p->scanner_ready_seen = true;
                p->calibration_retry_at = 0;
                atomic_store(&p->calibration_requested, true);
                LOGI(ready ? "Perspective calibration: scanner page ready; retry"
                           : "Perspective calibration: scanner page wait timeout; retry");
            }
        }
        if (!p->calibrating && !p->alignment_valid && p->scanner_alignment
                && p->calibration_retry_at && now >= p->calibration_retry_at
                && p->calibration_retry_count < 2
                && !atomic_load(&p->compatibility_key)) {
            // The previous attempt saw no calibration field at all (a stale
            // WeChat page was still showing). Retry once the page may have
            // settled instead of walking the wide/compact legacy ladder.
            p->calibration_retry_at = 0;
            atomic_store(&p->calibration_requested, true);
        }
        if (atomic_load(&p->calibration_requested) && now-p->last_resize > SC_TICK_FROM_MS(400)
                && now >= p->calibration_not_before) {
            p->handoff_deadline = 0;
            p->alignment_valid = false;
            p->calibration_region_valid = false;
            atomic_store(&p->calibration_requested, false);
            p->calibrating = true;
            p->calibration_field_seen = false;
            p->calibration_started = now;
            if (!p->cache_hit && !p->cache_reported && p->cache_env[0]
                    && !atomic_load(&p->compatibility_key)) {
                perspective_cache_lookup(p);
            }
            // Scanner fast path: one frame carries an id-coded 4x3 marker grid,
            // so one round-trip is enough for any crop/orientation. Legacy
            // four-corner passes remain as fallback (and for compatibility mode).
            bool fast_grid = p->scanner_alignment && !atomic_load(&p->compatibility_key);
            {
                static int legacy_mode = -1;
                if (legacy_mode < 0) {
                    const char *mode = getenv("SCEZ_LOCATOR_MODE");
                    legacy_mode = mode && !strcmp(mode, "legacy");
                }
                if (legacy_mode) { fast_grid = false; }
            }
            // The landscape scanner can crop the virtual sensor's outside
            // quarters completely. Use four points close to the optical center.
            bool centered = p->scanner_alignment && !atomic_load(&p->compatibility_key)
                         && p->observed_width > p->observed_height;
            if (fast_grid) {
                p->calibration_pass = SC_LOCATORS_ID_GRID;
                // A cache hit already has a proven map; only give the markers
                // a short window to confirm it. A long verification window
                // keeps the calibration cover up and delays native-alpha's
                // scene transition (the 1.5s attempt amplified K80's ~9s
                // decision when its preview was slow to show markers).
                p->calibration_deadline = now + SC_TICK_FROM_MS(p->cache_hit ? 600 : 1500);
            } else {
                p->calibration_pass = centered ? SC_LOCATORS_CENTER : SC_LOCATORS_WIDE;
                p->calibration_deadline = now + SC_TICK_FROM_MS(3000);
            }
            p->candidate_count = 0;
            LOGI("Perspective calibration start pass=%u cache=%d observed=%dx%d",
                 (unsigned) p->calibration_pass, p->cache_hit ? 1 : 0,
                 p->observed_width, p->observed_height);
            if (p->key_probe != SC_KEY_PROBE_DISABLED) {
                p->key_probe = SC_KEY_PROBE_IDLE;
                p->key_channel = sc_perspective_key_channel();
            }
        }
        if (p->calibrating && p->calibration_pass == SC_LOCATORS_ID_GRID
                && !p->calibration_field_seen
                && now - p->calibration_started > SC_TICK_FROM_MS(600)) {
            // Fast fail for the stale-page case: with no green calibration
            // field at all there is nothing for wide/compact passes to find.
            p->calibration_deadline = now;
        }
        if (p->calibrating && now >= p->calibration_deadline) {
            if (p->cache_hit && !p->cache_verified) {
                // The geometry key already matched a proven map. If the marker
                // field never becomes visible (page still loading, or the
                // native-alpha scene is taking over), keep the cached map
                // instead of walking the legacy fallback ladder: that ladder
                // delayed native alpha by ~6s and could end in compatibility
                // color-key mode. A future session will verify again.
                p->calibrating = false;
                keyframe = true;
                p->calibration_retry_count = 0;
                p->calibration_retry_at = 0;
                p->scanner_waiting_ready = false;
                LOGI("Perspective calibration: cached map kept (verification skipped)");
            } else if (p->calibration_pass == SC_LOCATORS_ID_GRID
                    && !p->calibration_field_seen) {
                // No green calibration field in any frame: the app page is
                // still stale/blank, so wide/compact retries cannot help.
                // Release the calibration cover immediately and retry once
                // the page has had time to settle.
                p->calibrating = false;
                keyframe = true;
                if (p->ready_file[0] && !p->scanner_ready_seen) {
                    // GUI monitors the virtual display's top activity; wait for
                    // its "BaseScanUI is foreground" file instead of a blind
                    // delay, then retry calibration immediately.
                    p->scanner_waiting_ready = true;
                    p->scanner_ready_deadline = now + SC_TICK_FROM_MS(5000);
                    LOGI("Perspective calibration: no field yet; waiting for scanner page ready");
                } else if (p->calibration_retry_count < 2) {
                    ++p->calibration_retry_count;
                    p->calibration_retry_at = now + SC_TICK_FROM_MS(900);
                    LOGI("Perspective calibration: no field yet; retry %u/2",
                         p->calibration_retry_count);
                } else {
                    LOGW("Perspective calibration: no field after retries, skipping legacy passes");
                }
            } else if (p->calibration_pass == SC_LOCATORS_ID_GRID) {
                if (p->candidate_count > 0) {
                    // A first frame already solved; give the confirmation frame
                    // a little more time before degrading to legacy passes.
                    p->calibration_deadline = now + SC_TICK_FROM_MS(1200);
                } else {
                    p->calibration_pass = SC_LOCATORS_WIDE;
                    p->calibration_deadline = now + SC_TICK_FROM_MS(2500);
                    keyframe = true;
                    LOGI("Perspective calibration fallback: legacy wide locators");
                }
            } else if (p->calibration_pass == SC_LOCATORS_WIDE && p->candidate_count == 0) {
                p->calibration_pass = SC_LOCATORS_COMPACT;
                p->calibration_deadline = now + SC_TICK_FROM_MS(2500);
                keyframe = true;
                LOGI("Perspective calibration retry: compact locators for cropped previews");
            }else{p->calibrating=false; LOGW("Perspective camera: automatic preview alignment unavailable");}
        }
        calibrating=p->calibrating; awaiting_calibration=atomic_load(&p->calibration_requested);
        calibration_pass=p->calibration_pass;
        memcpy(mapping,p->map,sizeof(mapping)); orient_mapping(mapping,p->orientation);
        if (p->key_probe == SC_KEY_PROBE_REQUESTED) {
            p->key_probe = SC_KEY_PROBE_ACTIVE;
            p->key_probe_deadline = now + SC_KEY_PROBE_TIME;
            keyframe = true;
        } else if (p->key_probe == SC_KEY_PROBE_ACTIVE
                && now >= p->key_probe_deadline) {
            p->key_probe = SC_KEY_PROBE_DONE;
            keyframe = true;
            LOGI("Perspective key probe timed out, using default backing");
        } else if (p->key_probe == SC_KEY_PROBE_VERIFY
                && now >= p->key_probe_deadline) {
            p->key_probe = SC_KEY_PROBE_DONE;
            p->key_channel = sc_perspective_key_channel();
            keyframe = true;
            LOGI("Perspective key could not be measured, using default backing");
        }
        bool probing = p->key_probe == SC_KEY_PROBE_ACTIVE;
        unsigned key_channel = p->key_channel;
        sc_mutex_unlock(&p->mutex);
        POINT origin={0,0}; ClientToScreen((HWND)p->hwnd,&origin);
        unsigned mode = 0;
        if (calibrating) {
            mode = calibration_pass == SC_LOCATORS_ID_GRID ? SC_DESKTOP_CAPTURE_ID_GRID
                 : calibration_pass == SC_LOCATORS_CENTER ? SC_DESKTOP_CAPTURE_CENTER_LOCATORS
                 : calibration_pass == SC_LOCATORS_COMPACT ? 4 : 1;
        }
        if (!available || !IsWindowVisible((HWND)p->hwnd) || IsIconic((HWND)p->hwnd) || w<=1 || h<=1 || awaiting_calibration) {
            mode=2;
            if (!available || IsIconic((HWND)p->hwnd)) { sc_desktop_capture_suspend(source); }
        }
        if (mode == 0 && sequence % p->input_period != 0) { mode = 3; }
        if(!calibrating&&!awaiting_calibration&&available&&w>1&&h>1&&IsWindowVisible((HWND)p->hwnd)&&!IsIconic((HWND)p->hwnd)){
            // Diagnostic fields: key, desktop, black, gray, white, dark red.
            if (probing) mode = SC_DESKTOP_CAPTURE_KEY_PROBE;
            const unsigned modes[]={0,3,0,2,6,5,7};
            if(test_mode)mode=modes[test_mode];
            unsigned photo=atomic_load(&p->photo_phase);
            if(photo!=last_photo){last_photo=photo;keyframe=true;}
            if(photo>0&&photo<=4){const unsigned fields[]={0,3,2,5,6};mode=fields[photo];}
        }
        if (!sc_desktop_capture_render(source,frame,origin.x+x,origin.y+y,w,h,
                                       mapping,mode,key_channel)) { failed=true; break; }
        if (calibrating && sequence % 60 == 0) {
            LOGI("Perspective capture rendered seq=%llu mode=%u", (unsigned long long) sequence, mode);
        }
        if (mode == 0) { local_preview_capture_geometry(origin.x+x,origin.y+y,w,h); }
        sc_tick rendered = sc_tick_now();
        AVFrame *input = frame;
        if (encoder->pix_fmt != AV_PIX_FMT_D3D11) {
            av_frame_unref(software);
            if (av_hwframe_transfer_data(software,frame,0)<0 || av_frame_copy_props(software,frame)<0) { failed=true; break; }
            input=software;
        }
        input->pts=sequence++;
        input->pict_type=keyframe ? AV_PICTURE_TYPE_I : AV_PICTURE_TYPE_NONE;
        if (avcodec_send_frame(encoder,input)<0) { failed=true; break; }
        int ret;
        while ((ret=avcodec_receive_packet(encoder,packet))>=0) {
            uint64_t pts=packet->pts>=0 ? (uint64_t)packet->pts : sequence-1;
            bool ok=packet->size>0 && packet->size<=8*1024*1024 && send_packet(p,packet->data,packet->size,pts);
            av_packet_unref(packet); if (!ok) { failed=true; break; }
        }
        if (failed || (ret!=AVERROR(EAGAIN) && ret!=AVERROR_EOF)) { failed=true; break; }
        if (metrics) {
            fprintf(metrics,"%llu,%lld,%lld,%lld,%u,%.3f,%.3f,%.3f,%.3f\n",(unsigned long long)(sequence-1),(long long)now,
                (long long)rendered,(long long)sc_tick_now(),mode,origin.x+x,origin.y+y,w,h);
            if (sequence % p->fps == 0) { fflush(metrics); }
        }
        next += SC_TICK_FROM_MS(1000)/p->fps;
        now=sc_tick_now();
        if (next>now) { SDL_DelayNS(SC_TICK_TO_NS(next-now)); } else { next=now; }
    }
end:
    LOGI("Perspective capture thread ended failed=%d stopped=%d",
         failed ? 1 : 0, atomic_load(&p->stopped) ? 1 : 0);
    if (awake_requested) { capture_keep_awake(false); }
    if (failed && !atomic_load(&p->stopped)) { LOGE("Perspective camera input stopped; closing this session safely"); }
    atomic_store(&p->stopped,true); net_interrupt(p->socket);
    av_packet_free(&packet); av_frame_free(&frame); av_frame_free(&software); avcodec_free_context(&encoder);
    sc_desktop_capture_destroy(source);
    if (metrics) { fclose(metrics); }
    return failed ? 1 : 0;
}

static bool
set_capture_affinity(struct sc_perspective *p, bool exclude) {
    typedef BOOL (WINAPI *get_fn)(HWND, DWORD *);
    typedef BOOL (WINAPI *set_fn)(HWND, DWORD);
    HMODULE user=GetModuleHandleW(L"user32.dll");
    get_fn get=(get_fn)(void *)GetProcAddress(user,"GetWindowDisplayAffinity");
    set_fn set=(set_fn)(void *)GetProcAddress(user,"SetWindowDisplayAffinity");
    if (!set || !get) { return false; }
    if (exclude) {
        if(p->affinity_changed){return true;}
        DWORD previous=0;
        if (!get((HWND)p->hwnd,&previous) || !set((HWND)p->hwnd,0x11)) {
            LOGE("Perspective window exclusion failed: %lu",(unsigned long)GetLastError()); return false;
        }
        p->previous_affinity=previous; p->affinity_changed=true;
    } else if (p->affinity_changed) {
        set((HWND)p->hwnd,p->previous_affinity); p->affinity_changed=false;
    }
    return true;
}

#endif

bool
sc_perspective_start(struct sc_perspective *p, sc_socket socket, void *hwnd,
                     unsigned input_period, bool scanner_alignment) {
    memset(p, 0, sizeof(*p));
#ifdef _WIN32
    p->socket = socket;
    p->hwnd = hwnd; p->fps = 30; p->input_period = input_period;
    p->scanner_alignment = scanner_alignment;
    const char *cache_env = getenv("SCEZ_PERSPECTIVE_CALIB");
    if (cache_env && cache_env[0]) {
        snprintf(p->cache_env, sizeof(p->cache_env), "%s", cache_env);
    }
    const char *ready_env = getenv("SCEZ_SCANNER_PAGE_READY");
    if (ready_env && ready_env[0]) {
        snprintf(p->ready_file, sizeof(p->ready_file), "%s", ready_env);
    }
    if (input_period != 60 && input_period != 30 && input_period != 5 && input_period != 3) { return false; }
    LOGI("Local perspective: %.1f desktop fps, 30fps camera transport; GPU composition active", 30.0 / input_period);
    DWORD timeout = 2000;
    setsockopt(socket->socket, SOL_SOCKET, SO_SNDTIMEO, (const char *) &timeout, sizeof(timeout));
    atomic_init(&p->stopped, false);
    atomic_init(&p->active, false);
    atomic_init(&p->camera_cycle, 0);
    atomic_init(&p->evicted, false);
    atomic_init(&p->compatibility_key, sc_perspective_color_key_enabled());
    atomic_init(&p->keyframe, true);
    atomic_init(&p->calibration_requested, false);
    atomic_init(&p->photo_phase, 0);
    p->key_channel = sc_perspective_key_channel();
    if (getenv("SCEZ_PERSPECTIVE_KEY_COLOR")
            || getenv("SCEZ_PERSPECTIVE_TEST_CONTROL")) {
        p->key_probe = SC_KEY_PROBE_DISABLED;
    }
    p->map[0] = 1; p->map[3] = 1;
    if (!sc_mutex_init(&p->mutex)) {
        set_capture_affinity(p,false); return false;
    }
    if (!sc_thread_create(&p->receiver, receive_state, "camera-state", p)) {
        sc_mutex_destroy(&p->mutex); set_capture_affinity(p,false);
        return false;
    }
    if (!sc_thread_create(&p->thread, capture_screen, "camera-input", p)) {
        atomic_store(&p->stopped, true); net_interrupt(socket);
        sc_thread_join(&p->receiver, NULL); sc_mutex_destroy(&p->mutex); set_capture_affinity(p,false);
        return false;
    }
    p->started = true;
    return true;
#else
    (void) socket; (void) hwnd; (void) input_period; (void) scanner_alignment;
    LOGE("Perspective camera requires Windows");
    return false;
#endif
}

void
sc_perspective_begin_handoff(struct sc_perspective *p, uint32_t epoch) {
#ifdef _WIN32
    sc_mutex_lock(&p->mutex);
    if (p->handoff_epoch != epoch) {
        p->handoff_epoch = epoch;
        // Only a map measured before the first covered frame can carry over.
        p->handoff_deadline = 0;
        p->handoff_confirmed = false;
        p->handoff_restarted = false;
        if (p->alignment_valid && !p->calibrating
                && !atomic_load(&p->calibration_requested)
                && !atomic_load(&p->compatibility_key)) {
            p->handoff_deadline = sc_tick_now() + SC_TICK_FROM_SEC(3);
        }
    }
    sc_mutex_unlock(&p->mutex);
#else
    (void) p; (void) epoch;
#endif
}

void
sc_perspective_confirm_handoff(struct sc_perspective *p, uint32_t epoch) {
#ifdef _WIN32
    if (!epoch) {
        return;
    }
    sc_mutex_lock(&p->mutex);
    if (p->handoff_epoch == epoch && p->handoff_deadline > sc_tick_now()
            && !atomic_load(&p->compatibility_key)) {
        p->handoff_confirmed = true;
        if (p->handoff_restarted) {
            reuse_handoff_alignment_locked(p);
        }
    }
    sc_mutex_unlock(&p->mutex);
#else
    (void) p; (void) epoch;
#endif
}

void
sc_perspective_update(struct sc_perspective *p, double x, double y,
                       double width, double height, unsigned orientation) {
    if (!p->started) { return; }
    sc_mutex_lock(&p->mutex);
    if (fabs(p->width - width) > 1 || fabs(p->height - height) > 1) {
        p->last_resize = sc_tick_now();
    }
    p->x = x; p->y = y; p->width = width; p->height = height;
    p->orientation = orientation;
    sc_mutex_unlock(&p->mutex);
}

void
sc_perspective_stop(struct sc_perspective *p) {
    if (!p->started || p->threads_joined) { return; }
    atomic_store(&p->stopped, true); net_interrupt(p->socket);
    sc_thread_join(&p->thread, NULL);
    sc_thread_join(&p->receiver, NULL);
    p->threads_joined = true;
#ifdef _WIN32
    camera_notice_close();
    set_capture_affinity(p,false);
#endif
}

void
sc_perspective_destroy(struct sc_perspective *p) {
    if (!p->started) { return; }
    sc_perspective_stop(p);
    sc_mutex_destroy(&p->mutex);
    p->started = false;
}

static void
sc_perspective_region_update(struct sc_perspective *p, const AVFrame *frame,
        const int field_bounds[4], int step) {
    int x = (field_bounds[0] > 0 ? field_bounds[0] - 1 : 0) * step;
    int y = (field_bounds[1] > 0 ? field_bounds[1] - 1 : 0) * step;
    int right = (field_bounds[2] + 2) * step;
    int bottom = (field_bounds[3] + 2) * step;
    if (right > frame->width) { right = frame->width; }
    if (bottom > frame->height) { bottom = frame->height; }
    sc_mutex_lock(&p->mutex);
    p->calibration_region[0] = (double) x / frame->width;
    p->calibration_region[1] = (double) y / frame->height;
    p->calibration_region[2] = (double) (right - x) / frame->width;
    p->calibration_region[3] = (double) (bottom - y) / frame->height;
    p->calibration_region_valid = true;
    sc_mutex_unlock(&p->mutex);
}

/* Candidate maps are accepted faster for the id-grid fast path: one good
 * frame is already a full affine solution; a second consecutive frame
 * confirms it. Legacy four-corner passes keep the historic 4-frame rule. */
static void
sc_perspective_offer_map(struct sc_perspective *p, const double m[6]) {
    sc_mutex_lock(&p->mutex);
    if (p->calibrating) {
        if (p->cache_hit && !p->cache_verified) {
            double diff = 0;
            for (int i = 0; i < 6; ++i) {
                double d = fabs(p->cache_map[i] - m[i]);
                if (d > diff) { diff = d; }
            }
            if (diff <= .02) {
                p->cache_verified = true;
                LOGI("Perspective cache verified: %s", p->cache_key);
            } else {
                p->cache_hit = false;
                LOGI("Perspective cache rejected: %s (diff=%.3f)", p->cache_key, diff);
            }
        }
        bool stable = p->candidate_count > 0;
        for (int i = 0; i < 6; ++i) {
            if (fabs(p->candidate_map[i] - m[i]) > .012) { stable = false; }
        }
        if (!stable) {
            memcpy(p->candidate_map, m, sizeof(double) * 6);
            p->candidate_count = 1; p->candidate_since = sc_tick_now();
        } else {
            ++p->candidate_count;
            for (int i = 0; i < 6; ++i) { p->candidate_map[i] = .7 * p->candidate_map[i] + .3 * m[i]; }
            unsigned need = p->calibration_pass == SC_LOCATORS_ID_GRID ? 2 : 4;
            sc_tick settle = SC_TICK_FROM_MS(p->calibration_pass == SC_LOCATORS_ID_GRID ? 120 : 200);
            if (p->candidate_count >= need && sc_tick_now() - p->candidate_since >= settle) {
                memcpy(p->map, p->candidate_map, sizeof(double) * 6);
                p->calibrating = false;
                p->alignment_valid = true;
                p->calibration_retry_count = 0;
                p->calibration_retry_at = 0;
                p->scanner_waiting_ready = false;
                atomic_store(&p->keyframe, true);
                LOGI("Perspective aligned: %.3f %.3f %.3f %.3f %.3f %.3f",
                     p->map[0], p->map[1], p->map[2], p->map[3], p->map[4], p->map[5]);
                if (p->scanner_alignment && !p->cache_reported) {
                    char key[64];
                    perspective_cache_key(p, key, sizeof(key));
                    p->cache_reported = true;
                    LOGI("SCRCPY_EZ_PERSPECTIVE_CALIB key=%s map=%.6f,%.6f,%.6f,%.6f,%.6f,%.6f rms=0.00", key,
                         p->map[0], p->map[1], p->map[2], p->map[3], p->map[4], p->map[5]);
                }
            }
        }
    }
    sc_mutex_unlock(&p->mutex);
}

/* Solve u,v -> x,y affine least squares for a subset of points. */
static bool
sc_perspective_fit_affine(const double *u, const double *v,
        const double *x, const double *y, const int *index, int n, double m[6]) {
    double su=0, sv=0, suu=0, svv=0, suv=0, sx=0, sy=0, sux=0, svx=0, suy=0, svy=0;
    for (int k = 0; k < n; ++k) {
        int i = index[k];
        su += u[i]; sv += v[i];
        suu += u[i] * u[i]; svv += v[i] * v[i]; suv += u[i] * v[i];
        sx += x[i]; sy += y[i];
        sux += u[i] * x[i]; svx += v[i] * x[i];
        suy += u[i] * y[i]; svy += v[i] * y[i];
    }
    double a[3][3] = {{suu, suv, su}, {suv, svv, sv}, {su, sv, (double) n}};
    double bx[3] = {sux, svx, sx}, by[3] = {suy, svy, sy};
    /* Gaussian elimination with partial pivoting, twice on the same matrix. */
    double inv[3][3];
    for (int r = 0; r < 3; ++r) {
        for (int c = 0; c < 3; ++c) { inv[r][c] = 0; }
        inv[r][r] = 1;
    }
    double a2[3][3];
    memcpy(a2, a, sizeof(a));
    for (int col = 0; col < 3; ++col) {
        int pivot = col;
        for (int r = col + 1; r < 3; ++r) {
            if (fabs(a2[r][col]) > fabs(a2[pivot][col])) { pivot = r; }
        }
        if (fabs(a2[pivot][col]) < 1e-9) { return false; }
        if (pivot != col) {
            for (int c = 0; c < 3; ++c) {
                double t = a2[col][c]; a2[col][c] = a2[pivot][c]; a2[pivot][c] = t;
                t = inv[col][c]; inv[col][c] = inv[pivot][c]; inv[pivot][c] = t;
            }
        }
        double d = a2[col][col];
        for (int c = 0; c < 3; ++c) { a2[col][c] /= d; inv[col][c] /= d; }
        for (int r = 0; r < 3; ++r) {
            if (r == col) { continue; }
            double f = a2[r][col];
            if (f == 0) { continue; }
            for (int c = 0; c < 3; ++c) {
                a2[r][c] -= f * a2[col][c];
                inv[r][c] -= f * inv[col][c];
            }
        }
    }
    double cx[3], cy[3];
    for (int r = 0; r < 3; ++r) {
        cx[r] = inv[r][0] * bx[0] + inv[r][1] * bx[1] + inv[r][2] * bx[2];
        cy[r] = inv[r][0] * by[0] + inv[r][1] * by[1] + inv[r][2] * by[2];
    }
    m[0] = cx[0]; m[1] = cx[1]; m[4] = cx[2];
    m[2] = cy[0]; m[3] = cy[1]; m[5] = cy[2];
    for (int i = 0; i < 6; ++i) { if (!isfinite(m[i])) { return false; } }
    return true;
}

/* Robust affine fit for the id grid: the palette decoding may occasionally
 * confuse one marker's inner colour (or a UI pixel may look like a marker),
 * so run a small deterministic RANSAC over three-point subsets, then refit
 * and trim on the winning inlier set. */
static bool
sc_perspective_fit_grid(const double *u, const double *v,
        const double *x, const double *y, int n, int frame_w, int frame_h,
        double m[6], int *inliers_out, double *rms_out) {
    if (n < 4) { return false; }
    int best[SC_ID_GRID_CELLS];
    int best_count = 0;
    int index[3];
    for (int a = 0; a < n; ++a) {
        for (int b = a + 1; b < n; ++b) {
            for (int c = b + 1; c < n; ++c) {
                double area = fabs((u[b] - u[a]) * (v[c] - v[a])
                                 - (u[c] - u[a]) * (v[b] - v[a]));
                if (area < 1e-4) { continue; }
                index[0] = a; index[1] = b; index[2] = c;
                double mm[6];
                if (!sc_perspective_fit_affine(u, v, x, y, index, 3, mm)) { continue; }
                if (fabs(mm[0] * mm[3] - mm[1] * mm[2]) < .01) { continue; }
                int kept = 0;
                int keep[SC_ID_GRID_CELLS];
                for (int k = 0; k < n; ++k) {
                    double px = (mm[0] * u[k] + mm[1] * v[k] + mm[4] - x[k]) * frame_w;
                    double py = (mm[2] * u[k] + mm[3] * v[k] + mm[5] - y[k]) * frame_h;
                    if (sqrt(px * px + py * py) <= 2.5) { keep[kept++] = k; }
                }
                if (kept > best_count) {
                    best_count = kept;
                    memcpy(best, keep, sizeof(int) * (size_t) kept);
                }
            }
        }
    }
    if (best_count < 4) { return false; }
    double mm[6];
    if (!sc_perspective_fit_affine(u, v, x, y, best, best_count, mm)) { return false; }
    int kept = 0;
    int keep[SC_ID_GRID_CELLS];
    for (int k = 0; k < best_count; ++k) {
        int i = best[k];
        double px = (mm[0] * u[i] + mm[1] * v[i] + mm[4] - x[i]) * frame_w;
        double py = (mm[2] * u[i] + mm[3] * v[i] + mm[5] - y[i]) * frame_h;
        if (sqrt(px * px + py * py) <= 2.0) { keep[kept++] = i; }
    }
    if (kept >= 4 && sc_perspective_fit_affine(u, v, x, y, keep, kept, mm)) {
        best_count = kept;
        memcpy(best, keep, sizeof(int) * (size_t) kept);
        if (!sc_perspective_fit_affine(u, v, x, y, best, best_count, mm)) { return false; }
    }
    double sum = 0;
    for (int k = 0; k < best_count; ++k) {
        int i = best[k];
        double px = (mm[0] * u[i] + mm[1] * v[i] + mm[4] - x[i]) * frame_w;
        double py = (mm[2] * u[i] + mm[3] * v[i] + mm[5] - y[i]) * frame_h;
        double err = sqrt(px * px + py * py);
        if (err > 3.0) { return false; }
        sum += err * err;
    }
    memcpy(m, mm, sizeof(mm));
    *inliers_out = best_count;
    *rms_out = sqrt(sum / best_count);
    return true;
}

struct sc_id_component {
    int color;
    int area;
    int cx, cy;   /* bbox centre in downsampled coordinates */
    int left, top, right, bottom;
    double sum_x, sum_y;
};

/* Sample the inner colour inside a two-tone marker. */
static int
sc_id_grid_inner_at(const uint8_t *labels, int w, int h, int cx, int cy, int outer) {
    int votes[4] = {0};
    for (int dy = -2; dy <= 2; ++dy) {
        int y = cy + dy;
        if (y < 0 || y >= h) { continue; }
        for (int dx = -2; dx <= 2; ++dx) {
            int x = cx + dx;
            if (x < 0 || x >= w) { continue; }
            int label = labels[y * w + x];
            if (label <= 3 && label != outer) { ++votes[label]; }
        }
    }
    int best = -1, best_n = 0;
    for (int i = 0; i < 4; ++i) {
        if (votes[i] > best_n) { best = i; best_n = votes[i]; }
    }
    return best_n >= 2 ? best : -1;
}

/* One-frame id-coded grid detection (scanner fast path).
 * Every marker carries a unique (outer, inner) palette pair, so a single
 * frame yields source-UV <-> preview-pixel correspondences for whichever
 * markers survive the preview crop. The affine fit then covers exactly the
 * visible region, independent of landscape/portrait/rotation. */
static void
observe_id_grid(struct sc_perspective *p, const AVFrame *frame, const uint8_t *labels,
        int w, int h, int step, const int field_bounds[4]) {
    size_t count = (size_t) w * h;
    int green = 0;
    for (size_t i = 0; i < count; ++i) { if (labels[i] == 4) { ++green; } }
    if ((size_t) green <= count / 8) { return; }
    sc_perspective_region_update(p, frame, field_bounds, step);

    struct sc_id_component *components = calloc(count / 8 + 16, sizeof(*components));
    int *queue = malloc(count * sizeof(*queue));
    uint8_t *visited = calloc(count, 1);
    if (!components || !queue || !visited) { free(components); free(queue); free(visited); return; }
    int component_count = 0;
    int component_cap = (int) (count / 8 + 16);
    for (int y = 0; y < h; ++y) {
        for (int x = 0; x < w; ++x) {
            size_t start = (size_t) y * w + x;
            int color = labels[start];
            if (color > 3 || visited[start]) { continue; }
            size_t begin = 0, end = 1;
            queue[0] = (int) start; visited[start] = 1;
            double sum_x = 0, sum_y = 0;
            int left = x, right = x, top = y, bottom = y;
            while (begin < end) {
                int item = queue[begin++], ix = item % w, iy = item / w;
                sum_x += ix; sum_y += iy;
                if (ix < left) { left = ix; } if (ix > right) { right = ix; }
                if (iy < top) { top = iy; } if (iy > bottom) { bottom = iy; }
                int adjacent[] = {ix > 0 ? item - 1 : -1, ix + 1 < w ? item + 1 : -1,
                                  iy > 0 ? item - w : -1, iy + 1 < h ? item + w : -1};
                for (int k = 0; k < 4; ++k) {
                    int next = adjacent[k];
                    if (next >= 0 && !visited[next] && labels[next] == color) {
                        visited[next] = 1; queue[end++] = next;
                    }
                }
            }
            if ((int) end < 16) { continue; }
            if (component_count >= component_cap) { continue; }
            struct sc_id_component *c = &components[component_count++];
            c->color = color; c->area = (int) end; c->sum_x = sum_x; c->sum_y = sum_y;
            c->left = left; c->top = top; c->right = right; c->bottom = bottom;
            c->cx = (left + right) / 2; c->cy = (top + bottom) / 2;
        }
    }
    free(visited); free(queue);
    if (component_count <= 0) { free(components); return; }

    double u[SC_ID_GRID_CELLS], v[SC_ID_GRID_CELLS];
    double px[SC_ID_GRID_CELLS], py[SC_ID_GRID_CELLS];
    int area[SC_ID_GRID_CELLS] = {0};
    int markers = 0;
    for (int i = 0; i < component_count; ++i) {
        struct sc_id_component *c = &components[i];
        int inner = sc_id_grid_inner_at(labels, w, h, c->cx, c->cy, c->color);
        if (inner < 0) { continue; }
        int cell = sc_id_grid_cell_for(c->color, inner);
        if (cell < 0) { continue; }
        if (c->area <= area[cell]) { continue; }
        area[cell] = c->area;
        u[cell] = sc_id_grid_u(cell);
        v[cell] = sc_id_grid_v(cell);
        px[cell] = (c->sum_x / c->area * step + .5) / frame->width;
        py[cell] = (c->sum_y / c->area * step + .5) / frame->height;
    }
    double src_u[SC_ID_GRID_CELLS], src_v[SC_ID_GRID_CELLS], obs_x[SC_ID_GRID_CELLS], obs_y[SC_ID_GRID_CELLS];
    int used_cell[SC_ID_GRID_CELLS];
    int used = 0;
    for (int cell = 0; cell < SC_ID_GRID_CELLS; ++cell) {
        if (!area[cell]) { continue; }
        used_cell[used] = cell;
        src_u[used] = u[cell]; src_v[used] = v[cell];
        obs_x[used] = px[cell]; obs_y[used] = py[cell];
        ++used;
    }
    static unsigned grid_raw_logs;
    static bool grid_debug_saved;
    const char *debug_dir = getenv("SCEZ_KEY_DEBUG_DIR");
    if (debug_dir && !grid_debug_saved && used >= 4) {
        grid_debug_saved = true;
        SDL_Surface *surface = SDL_CreateSurface(frame->width, frame->height, SDL_PIXELFORMAT_RGBA32);
        struct SwsContext *sws = surface ? sws_getContext(frame->width, frame->height,
                AV_PIX_FMT_YUV420P, frame->width, frame->height, AV_PIX_FMT_RGBA,
                SWS_BILINEAR, NULL, NULL, NULL) : NULL;
        if (sws) {
            sc_perspective_configure_rgb(sws, frame);
            uint8_t *dst[4] = {surface->pixels};
            int stride[4] = {surface->pitch};
            sws_scale(sws, (const uint8_t *const *)frame->data, frame->linesize,
                      0, frame->height, dst, stride);
            char path[1024];
            snprintf(path, sizeof(path), "%s/calibration-grid.png", debug_dir);
            SDL_SavePNG(surface, path);
            sws_freeContext(sws);
        }
        SDL_DestroySurface(surface);
    }
    if (used < 4) {
        if (grid_raw_logs++ < 4) {
            LOGI("Perspective grid: markers=%d (need >=4)", used);
            for (int i = 0; i < used; ++i) {
                LOGI("  grid marker cell=%d uv=%.3f,%.3f obs=%.3f,%.3f",
                     used_cell[i], src_u[i], src_v[i], obs_x[i], obs_y[i]);
            }
        }
        free(components); return;
    }
    double m[6];
    int inliers = 0;
    double rms = 0;
    if (!sc_perspective_fit_grid(src_u, src_v, obs_x, obs_y, used,
            frame->width, frame->height, m, &inliers, &rms)
            || inliers < 4 || rms > 1.5) {
        if (grid_raw_logs++ < 4) {
            LOGI("Perspective grid: markers=%d inliers=%d rms=%.2fpx (rejected)", used, inliers, rms);
            for (int i = 0; i < used; ++i) {
                LOGI("  grid marker cell=%d uv=%.3f,%.3f obs=%.3f,%.3f",
                     used_cell[i], src_u[i], src_v[i], obs_x[i], obs_y[i]);
            }
        }
        free(components); return;
    }
    double min_u = 1, max_u = 0, min_v = 1, max_v = 0;
    for (int i = 0; i < SC_ID_GRID_CELLS; ++i) {
        if (!area[i]) { continue; }
        if (u[i] < min_u) { min_u = u[i]; } if (u[i] > max_u) { max_u = u[i]; }
        if (v[i] < min_v) { min_v = v[i]; } if (v[i] > max_v) { max_v = v[i]; }
    }
    double span_u = max_u - min_u, span_v = max_v - min_v;
    if (fabs(m[0] * m[3] - m[1] * m[2]) < .01 || span_u < .15 || span_v < .15) {
        free(components); return;
    }
    for (int i = 0; i < 6; ++i) {
        if (!isfinite(m[i]) || fabs(m[i]) > 6) { free(components); return; }
    }
    static unsigned grid_ok_logs;
    if (grid_ok_logs++ < 8) {
        LOGI("Perspective grid: markers=%d inliers=%d rms=%.2fpx span=%.2fx%.2f",
             used, inliers, rms, span_u, span_v);
    }
    static unsigned grid_raw_ok_logs;
    if (grid_raw_ok_logs++ < 3) {
        for (int i = 0; i < used; ++i) {
            LOGI("  grid ok cell=%d uv=%.3f,%.3f obs=%.3f,%.3f",
                 used_cell[i], src_u[i], src_v[i], obs_x[i], obs_y[i]);
        }
    }
    free(components);
    sc_perspective_offer_map(p, m);
}

static void
observe_software(struct sc_perspective *p, const AVFrame *frame) {
    sc_mutex_lock(&p->mutex);
    if (p->observed_width != frame->width || p->observed_height != frame->height) {
        p->observed_width = frame->width; p->observed_height = frame->height;
        request_calibration_locked(p);
    }
    bool calibrating = p->calibrating;unsigned calibration_pass=p->calibration_pass;
    sc_mutex_unlock(&p->mutex);
    if (!calibrating) { return; }
    int step = (frame->width > frame->height ? frame->width : frame->height) / 320;
    if (step < 1) { step = 1; }
    int w = frame->width / step, h = frame->height / step;
    size_t count = (size_t) w * h;
    uint8_t *labels = malloc(count);
    int *queue = malloc(count * sizeof(*queue));
    if (!labels || !queue) { free(labels); free(queue); return; }
    int field_bounds[4] = {w, h, 0, 0};
    int green_count = 0;
    struct sc_perspective_yuv_coefficients colors=sc_perspective_yuv_coefficients(frame);
    for (int y = 0; y < h; ++y) {
        for (int x = 0; x < w; ++x) {
            int fx = x * step, fy = y * step;
            int yy = frame->data[0][fy * frame->linesize[0] + fx];
            int u = frame->data[1][(fy / 2) * frame->linesize[1] + fx / 2];
            int v = frame->data[2][(fy / 2) * frame->linesize[2] + fx / 2];
            int r,g,b;sc_perspective_yuv_rgb(&colors,yy,u,v,&r,&g,&b);
            // Native scanners may dim the camera outside their scan box.
            // Chroma ratios preserve the same four labels under that dimming.
            int color = r > 72 && r > 1.8*g && r > 1.8*b ? 0
                    : b > 72 && b > 1.8*r && b > 1.8*g ? 1
                    : r > 72 && g > 72 && r < 1.6*g && g < 1.6*r && b < .55*fmin(r,g) ? 2
                    : r > 72 && b > 72 && r < 1.6*b && b < 1.6*r && g < .55*fmin(r,b) ? 3
                    : g > 72 && g > 1.8*r && g > 1.8*b ? 4 : 255;
            labels[y * w + x] = color;
            if (color == 4) {
                ++green_count;
                if (x < field_bounds[0]) { field_bounds[0] = x; }
                if (y < field_bounds[1]) { field_bounds[1] = y; }
                if (x > field_bounds[2]) { field_bounds[2] = x; }
                if (y > field_bounds[3]) { field_bounds[3] = y; }
            }
        }
    }
    if ((size_t) green_count > count / 8 && !p->calibration_field_seen) {
        sc_mutex_lock(&p->mutex);
        p->calibration_field_seen = true;
        sc_mutex_unlock(&p->mutex);
    }
    if (calibration_pass == SC_LOCATORS_ID_GRID) {
        observe_id_grid(p, frame, labels, w, h, step, field_bounds);
        free(labels); free(queue);
        return;
    }
    int largest[5] = {0};
    double centers[5][2] = {{0}};
    for (size_t index = 0; index < count; ++index) {
        int color = labels[index];
        if (color > 4) { continue; }
        size_t begin = 0, end = 1;
        queue[0] = index; labels[index] = 255;
        double sum_x = 0, sum_y = 0;
        while (begin < end) {
            int item = queue[begin++], x = item % w, y = item / w;
            sum_x += x; sum_y += y;
            int adjacent[] = {x > 0 ? item - 1 : -1, x + 1 < w ? item + 1 : -1,
                              y > 0 ? item - w : -1, y + 1 < h ? item + w : -1};
            for (int k = 0; k < 4; ++k) {
                int next = adjacent[k];
                if (next >= 0 && labels[next] == color) {
                    labels[next] = 255; queue[end++] = next;
                }
            }
        }
        if ((int) end > largest[color]) {
            largest[color] = end;
            centers[color][0] = (sum_x / end * step + .5) / frame->width;
            centers[color][1] = (sum_y / end * step + .5) / frame->height;
        }
    }
    free(labels); free(queue);
    static bool debug_calibration_saved;
    const char *debug_calibration_dir=getenv("SCEZ_KEY_DEBUG_DIR");
    if(debug_calibration_dir&&!debug_calibration_saved&&largest[0]>16){
      debug_calibration_saved=true;
      SDL_Surface *surface=SDL_CreateSurface(frame->width,frame->height,SDL_PIXELFORMAT_RGBA32);
      struct SwsContext *sws=surface?sws_getContext(frame->width,frame->height,AV_PIX_FMT_YUV420P,frame->width,frame->height,AV_PIX_FMT_RGBA,SWS_BILINEAR,NULL,NULL,NULL):NULL;
      if(sws){sc_perspective_configure_rgb(sws,frame);uint8_t *dst[4]={surface->pixels};int stride[4]={surface->pitch};sws_scale(sws,(const uint8_t*const*)frame->data,frame->linesize,0,frame->height,dst,stride);char path[1024];snprintf(path,sizeof(path),"%s/calibration.png",debug_calibration_dir);SDL_SavePNG(surface,path);sws_freeContext(sws);}
      SDL_DestroySurface(surface);
    }
    static unsigned debug_observations;
    if(getenv("SCEZ_KEY_DEBUG_DIR")&&(largest[4]||largest[0]||largest[1])&&debug_observations++<16){
      LOGI("CALIBRATION_DIAGNOSTIC labels=%d,%d,%d,%d field=%d centers=%.3f,%.3f %.3f,%.3f %.3f,%.3f %.3f,%.3f",largest[0],largest[1],largest[2],largest[3],largest[4],centers[0][0],centers[0][1],centers[1][0],centers[1][1],centers[2][0],centers[2][1],centers[3][0],centers[3][1]);
    }
    // A separate solid overlay uses the visible field's rectangle. No
    // per-pixel calibration matte or foreground/background color separation.
    if ((size_t) largest[4] > count / 8) {
        // Include narrow disconnected fringes at the preview boundary too.
        // The large connected field above prevents isolated green UI icons
        // from triggering a mask on an otherwise non-calibration frame.
        int x = (field_bounds[0] > 0 ? field_bounds[0] - 1 : 0) * step;
        int y = (field_bounds[1] > 0 ? field_bounds[1] - 1 : 0) * step;
        int right = (field_bounds[2] + 2) * step;
        int bottom = (field_bounds[3] + 2) * step;
        if (right > frame->width) { right = frame->width; }
        if (bottom > frame->height) { bottom = frame->height; }
        sc_mutex_lock(&p->mutex);
        p->calibration_region[0] = (double) x / frame->width;
        p->calibration_region[1] = (double) y / frame->height;
        p->calibration_region[2] = (double) (right - x) / frame->width;
        p->calibration_region[3] = (double) (bottom - y) / frame->height;
        p->calibration_region_valid = true;
        sc_mutex_unlock(&p->mutex);
    }
    for (int i = 0; i < 4; ++i) { if (largest[i] < 16) { return; } }
    for (int i = 0; i < 2; ++i) {
        if (fabs(centers[3][i] - centers[1][i] - centers[2][i] + centers[0][i]) > .012) { return; }
    }
    double m[6];
    if (calibration_pass == SC_LOCATORS_CENTER && (size_t) largest[4] <= count / 8) {
        return;
    }
    struct sc_locator_grid grid = sc_locator_grid_for(calibration_pass);
    double span_x = grid.right - grid.left;
    double span_y = grid.bottom - grid.top;
    m[0] = (centers[1][0] - centers[0][0]) / span_x;
    m[1] = (centers[2][0] - centers[0][0]) / span_y;
    m[2] = (centers[1][1] - centers[0][1]) / span_x;
    m[3] = (centers[2][1] - centers[0][1]) / span_y;
    m[4] = centers[0][0] - grid.left * m[0] - grid.top * m[1];
    m[5] = centers[0][1] - grid.left * m[2] - grid.top * m[3];
    if (fabs(m[0] * m[3] - m[1] * m[2]) < .05) { return; }
    for (int i = 0; i < 6; ++i) { if (!isfinite(m[i]) || fabs(m[i]) > 4) { return; } }
    sc_perspective_offer_map(p, m);
}

void
sc_perspective_observe(struct sc_perspective *p, const AVFrame *frame) {
    if (!p->started || atomic_load(&p->stopped)) { return; }
    sc_mutex_lock(&p->mutex);
    if (p->observed_width != frame->width || p->observed_height != frame->height) {
        p->observed_width = frame->width; p->observed_height = frame->height;
        request_calibration_locked(p);
    }
    bool needed = p->calibrating;
    sc_mutex_unlock(&p->mutex);
    const char *debug = getenv("SCEZ_PERSPECTIVE_DEBUG_IMAGE");
    static sc_tick last_debug;
    bool dump = debug && sc_tick_now() - last_debug > SC_TICK_FROM_MS(1000);
    if (!needed && !dump) { return; }
    AVFrame *download = NULL, *converted = NULL;
    const AVFrame *software = frame;
    const AVPixFmtDescriptor *desc = av_pix_fmt_desc_get(frame->format);
    if (desc && (desc->flags & AV_PIX_FMT_FLAG_HWACCEL)) {
        download = av_frame_alloc();
        if (!download || av_hwframe_transfer_data(download, frame, 0) < 0
                || av_frame_copy_props(download,frame)<0) { goto done; }
        software = download;
    }
    if (software->format != AV_PIX_FMT_YUV420P) {
        converted = av_frame_alloc();
        if (!converted) { goto done; }
        converted->format = AV_PIX_FMT_YUV420P;
        converted->width = frame->width; converted->height = frame->height;
        if (av_frame_get_buffer(converted, 32) < 0
                || av_frame_copy_props(converted,software)<0) { goto done; }
        struct SwsContext *convert = sws_getContext(frame->width, frame->height, software->format,
            frame->width, frame->height, AV_PIX_FMT_YUV420P, SWS_POINT, NULL, NULL, NULL);
        if (!convert) { goto done; }
        sws_scale(convert, (const uint8_t *const *) software->data, software->linesize,
                  0, frame->height, converted->data, converted->linesize);
        sws_freeContext(convert);
        software = converted;
    }
    if (needed) { observe_software(p, software); }
    if (dump) {
        // Explicit opt-in diagnostic for local frame inspection during testing.
        FILE *f = fopen(debug, "wb");
        if (f) {
            fprintf(f, "P5\n%d %d\n255\n", software->width, software->height);
            for (int y = 0; y < software->height; ++y) {
                fwrite(software->data[0] + y * software->linesize[0], 1, software->width, f);
            }
            fclose(f);
        }
        last_debug = sc_tick_now();
    }
done:
    av_frame_free(&converted); av_frame_free(&download);
}

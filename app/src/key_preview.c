#include "gpu_matte.h"
#include "key_preview.h"
#include "perspective_locators.h"

#include <libswscale/swscale.h>
#include <string.h>

#include "key_reference.h"
#include "perspective_color.h"
#include "util/log.h"
#include "util/tick.h"

#define KEY_PLATE_FRAMES 6
#define KEY_PLATE_SUBSAMPLES 3

struct key_plate_cell {
    float sum, low, high;
    unsigned count;
};

static struct {
    struct gpu_matte *matte;
    struct SwsContext *sws;
    AVFrame *rgba;
    SDL_Texture *input, *output;
    ID3D11ShaderResourceView *source;
    int width, height;
    int64_t pts;
    enum AVColorSpace color_space;
    enum AVColorRange color_range;
    bool have_pts, have_reference, frame_keyed;
    sc_tick last_key;
    float reference[3];
    struct sc_key_backing backing;
    struct key_plate_cell plate[EZ_KEY_PLATE_SIZE];
    unsigned plate_frames;
    unsigned channel;
    sc_tick probe_deadline, probe_first_frame;
    unsigned probe_frames;
    float probe_risk[3];
} preview;

bool
sc_key_preview_is_composited(SDL_Texture *texture) {
    return texture && texture == preview.output && preview.last_key;
}

void
sc_key_preview_destroy(void) {
    SDL_DestroyTexture(preview.output);
    if (preview.source) {
        ID3D11ShaderResourceView_Release(preview.source);
    }
    SDL_DestroyTexture(preview.input);
    gpu_matte_destroy(preview.matte);
    sws_freeContext(preview.sws);
    av_frame_free(&preview.rgba);
    memset(&preview, 0, sizeof(preview));
}

static bool
init_preview(SDL_Renderer *renderer, int width, int height) {
    ID3D11Device *device = SDL_GetPointerProperty(SDL_GetRendererProperties(renderer),
                              SDL_PROP_RENDERER_D3D11_DEVICE_POINTER, NULL);
    if (!device) {
        return false;
    }
    preview.rgba = av_frame_alloc();
    if (!preview.rgba) {
        return false;
    }
    preview.rgba->format = AV_PIX_FMT_RGBA;
    preview.rgba->width = width;
    preview.rgba->height = height;
    if (av_frame_get_buffer(preview.rgba, 32) < 0) {
        return false;
    }
    preview.sws = sws_getContext(width, height, AV_PIX_FMT_YUV420P, width, height,
                                AV_PIX_FMT_RGBA, SWS_FAST_BILINEAR, NULL, NULL, NULL);
    preview.input = SDL_CreateTexture(renderer, SDL_PIXELFORMAT_RGBA32,
                                      SDL_TEXTUREACCESS_STREAMING, width, height);
    if (!preview.sws || !preview.input) {
        return false;
    }
    ID3D11Texture2D *input = SDL_GetPointerProperty(SDL_GetTextureProperties(preview.input),
                                  SDL_PROP_TEXTURE_D3D11_TEXTURE_POINTER, NULL);
    if (!input || FAILED(ID3D11Device_CreateShaderResourceView(device,
                         (ID3D11Resource *) input, NULL, &preview.source))) {
        return false;
    }
    preview.matte = gpu_matte_create(device, width, height);
    gpu_matte_set_soft_key(preview.matte);
    if (!preview.matte) {
        return false;
    }
    SDL_PropertiesID props = SDL_CreateProperties();
    SDL_SetNumberProperty(props, SDL_PROP_TEXTURE_CREATE_FORMAT_NUMBER, SDL_PIXELFORMAT_RGBA32);
    SDL_SetNumberProperty(props, SDL_PROP_TEXTURE_CREATE_ACCESS_NUMBER, SDL_TEXTUREACCESS_STATIC);
    SDL_SetNumberProperty(props, SDL_PROP_TEXTURE_CREATE_WIDTH_NUMBER, width);
    SDL_SetNumberProperty(props, SDL_PROP_TEXTURE_CREATE_HEIGHT_NUMBER, height);
    SDL_SetPointerProperty(props, SDL_PROP_TEXTURE_CREATE_D3D11_TEXTURE_POINTER,
                           gpu_matte_texture(preview.matte));
    preview.output = SDL_CreateTextureWithProperties(renderer, props);
    SDL_DestroyProperties(props);
    return preview.output && SDL_SetTextureBlendMode(preview.output, SDL_BLENDMODE_NONE);
}

static void
probe_key(struct sc_perspective *p, const float region[4], sc_tick deadline,
          sc_tick now) {
    if (preview.probe_deadline != deadline) {
        preview.probe_deadline = deadline;
        preview.probe_first_frame = 0;
        preview.probe_frames = 0;
        memset(preview.probe_risk, 0, sizeof(preview.probe_risk));
    }
    const AVFrame *rgba = preview.rgba;
    int left = (int) (region[0] * rgba->width);
    int top = (int) (region[1] * rgba->height);
    int right = (int) ((region[0] + region[2]) * rgba->width);
    int bottom = (int) ((region[1] + region[3]) * rgba->height);
    struct sc_key_probe_samples samples = {0};
    // Rotate the sample grid to catch one-pixel strokes during warm-up.
    int offset_x = preview.probe_frames & 1;
    int offset_y = (preview.probe_frames >> 1) & 1;
    for (int y = MAX(0, top + 2) + offset_y; y < MIN(rgba->height, bottom - 2); y += 2) {
        const uint8_t *row = rgba->data[0] + y * rgba->linesize[0];
        for (int x = MAX(0, left + 2) + offset_x; x < MIN(rgba->width, right - 2); x += 2) {
            sc_key_probe_sample(&samples, row + x * 4);
        }
    }
    if (!sc_key_probe_has_field(&samples)) {
        return;
    }
    for (unsigned c = 0; c < 3; ++c) {
        float risk = sc_key_probe_risk(&samples, c);
        preview.probe_risk[c] = MAX(preview.probe_risk[c], risk);
    }
    if (!preview.probe_frames++) {
        preview.probe_first_frame = now;
    }
    if (preview.probe_frames < 5
            || now - preview.probe_first_frame < SC_TICK_FROM_MS(400)) {
        return;
    }
    unsigned channel = sc_key_probe_choose(preview.probe_risk);
    sc_mutex_lock(&p->mutex);
    bool accepted = p->key_probe == SC_KEY_PROBE_ACTIVE
                 && p->key_probe_deadline == deadline
                 && !atomic_load(&p->calibration_requested);
    if (accepted) {
        p->key_channel = channel;
        p->key_probe = channel == 2 ? SC_KEY_PROBE_DONE : SC_KEY_PROBE_VERIFY;
        p->key_probe_deadline = now + SC_KEY_PROBE_TIME;
        atomic_store(&p->keyframe, true);
        LOGI("Perspective key selected from preview: %s (risk %.2f/%.2f/%.2f)",
             channel == 0 ? "red" : channel == 1 ? "green" : "blue",
             preview.probe_risk[0], preview.probe_risk[1], preview.probe_risk[2]);
    }
    sc_mutex_unlock(&p->mutex);
}

static bool
collect_key(const AVFrame *rgba, const float region[4], unsigned channel,
            const struct sc_key_backing *reference,
            struct sc_key_samples *samples) {
    int left = (int) (region[0] * rgba->width);
    int top = (int) (region[1] * rgba->height);
    int right = (int) ((region[0] + region[2]) * rgba->width);
    int bottom = (int) ((region[1] + region[3]) * rgba->height);
    int inset_x = (right - left) / 12, inset_y = (bottom - top) / 12;
    unsigned peak = sc_perspective_key_strength(channel) * 255 + 16;
    unsigned tested = 0, backing = 0;
    for (int y = MAX(0, top + inset_y); y < MIN(rgba->height, bottom - inset_y); y += 8) {
        const uint8_t *row = rgba->data[0] + y * rgba->linesize[0];
        for (int x = MAX(0, left + inset_x); x < MIN(rgba->width, right - inset_x); x += 8) {
            const uint8_t *pixel = row + x * 4;
            ++tested;
            if (reference) {
                backing += sc_key_matches_backing(reference, pixel, channel);
            } else if (pixel[channel] <= peak) {
                sc_key_sample_for_channel(samples, pixel, channel);
            }
        }
    }
    if (!reference) {
        backing = samples->total;
    }
    return tested >= 16 && backing > tested / 2;
}

static void
learn_key_plate(const float region[4], unsigned channel) {
    const AVFrame *rgba = preview.rgba;
    const int left = (int) (region[0] * rgba->width);
    const int top = (int) (region[1] * rgba->height);
    const int right = (int) ((region[0] + region[2]) * rgba->width);
    const int bottom = (int) ((region[1] + region[3]) * rgba->height);
    const unsigned a = (channel + 1) % 3, b = (channel + 2) % 3;
    const float scale = 1.f / (255 * sc_key_chroma(preview.reference, channel));
    const int grid_w = EZ_KEY_PLATE_WIDTH * KEY_PLATE_SUBSAMPLES;
    const int grid_h = EZ_KEY_PLATE_HEIGHT * KEY_PLATE_SUBSAMPLES;

    for (int row = 0; row < EZ_KEY_PLATE_HEIGHT; ++row) {
        for (int col = 0; col < EZ_KEY_PLATE_WIDTH; ++col) {
            float sum = 0, low = 1, high = 0;
            unsigned count = 0;
            for (int dy = 0; dy < KEY_PLATE_SUBSAMPLES; ++dy) {
                int gy = row * KEY_PLATE_SUBSAMPLES + dy;
                int y = (2 * gy + 1) * rgba->height / (2 * grid_h);
                if (y < top || y >= bottom) continue;
                const uint8_t *line = rgba->data[0] + y * rgba->linesize[0];
                for (int dx = 0; dx < KEY_PLATE_SUBSAMPLES; ++dx) {
                    int gx = col * KEY_PLATE_SUBSAMPLES + dx;
                    int x = (2 * gx + 1) * rgba->width / (2 * grid_w);
                    if (x < left || x >= right) continue;
                    const uint8_t *rgb = line + x * 4;
                    if (!sc_key_matches_backing(&preview.backing, rgb, channel)) continue;
                    float t = MIN(1.f, (rgb[channel] - MIN(rgb[a], rgb[b])) * scale);
                    low = MIN(low, t);
                    high = MAX(high, t);
                    sum += t;
                    ++count;
                }
            }
            // Skip cells on a strong boundary or mostly covered by UI.
            if (count < 5 || high - low > .06f) continue;
            float value = sum / count;
            struct key_plate_cell *cell = &preview.plate[row * EZ_KEY_PLATE_WIDTH + col];
            cell->low = cell->count ? MIN(cell->low, value) : value;
            cell->high = MAX(cell->high, value);
            cell->sum += value;
            ++cell->count;
        }
    }
    if (++preview.plate_frames != KEY_PLATE_FRAMES) return;

    float plate[EZ_KEY_PLATE_SIZE] = {0};
    unsigned valid = 0;
    for (unsigned i = 0; i < EZ_KEY_PLATE_SIZE; ++i) {
        const struct key_plate_cell *cell = &preview.plate[i];
        if (cell->count >= 4 && cell->high - cell->low <= .065f) {
            plate[i] = (cell->sum - cell->low - cell->high) / (cell->count - 2);
            ++valid;
        }
    }
    if (valid) {
        gpu_matte_set_key_plate(preview.matte, plate);
    }
    LOGD("Perspective key spatial reference: %u/%u cells", valid, EZ_KEY_PLATE_SIZE);
}

static bool
has_alignment_locators(const AVFrame *rgba, const double map[6],
                       enum sc_locator_layout layout) {
    struct sc_locator_grid grid = sc_locator_grid_for(layout);
    // Green backing can also match a queued alignment frame. Check the four
    // calibrated locations before letting that frame reveal the preview.
    static const unsigned colors[] = {1, 4, 3, 5}; // red, blue, yellow, magenta
    unsigned matching = 0;
    for (unsigned i = 0; i < 4; ++i) {
        double u = i % 2 ? grid.right : grid.left;
        double v = i < 2 ? grid.top : grid.bottom;
        int x = (map[0] * u + map[1] * v + map[4]) * rgba->width;
        int y = (map[2] * u + map[3] * v + map[5]) * rgba->height;
        if (x < 0 || x >= rgba->width || y < 0 || y >= rgba->height) {
            continue;
        }
        const uint8_t *pixel = rgba->data[0] + y * rgba->linesize[0] + x * 4;
        unsigned peak = MAX(pixel[0], MAX(pixel[1], pixel[2]));
        unsigned color = 0;
        for (unsigned c = 0; c < 3; ++c) {
            if (pixel[c] * 3 > peak * 2) {
                color |= 1u << c;
            }
        }
        matching += peak > 72 && color == colors[i];
    }
    return matching >= 3;
}

SDL_Texture *
sc_key_preview_texture(SDL_Renderer *renderer, struct sc_perspective *p,
                       const AVFrame *frame, SDL_Texture *fallback) {
    bool active = atomic_load(&p->active) && !atomic_load(&p->stopped)
               && !atomic_load(&p->evicted);
    float region[4];
    double map[6];
    sc_mutex_lock(&p->mutex);
    bool ready = p->calibration_region_valid && p->candidate_count >= 4
              && !p->calibrating && !atomic_load(&p->calibration_requested);
    for (unsigned i = 0; i < 4; ++i) {
        region[i] = p->calibration_region[i];
    }
    memcpy(map, p->map, sizeof(map));
    enum sc_locator_layout layout = p->calibration_pass;
    if (active && ready && frame && frame->format == AV_PIX_FMT_YUV420P
            && p->key_probe == SC_KEY_PROBE_IDLE) {
        p->key_probe = SC_KEY_PROBE_REQUESTED;
    }
    enum sc_key_probe_state probe = p->key_probe;
    sc_tick deadline = p->key_probe_deadline;
    unsigned channel = p->key_channel;
    sc_mutex_unlock(&p->mutex);
    if (!active || !ready || !frame || frame->format != AV_PIX_FMT_YUV420P) {
        preview.have_pts = preview.have_reference = preview.frame_keyed = false;
        preview.last_key = 0;
        return fallback;
    }
    if (preview.width != frame->width || preview.height != frame->height) {
        sc_key_preview_destroy();
        bool initialized = init_preview(renderer, frame->width, frame->height);
        if (!initialized) {
            LOGW("Perspective color key renderer unavailable: %s", SDL_GetError());
            sc_key_preview_destroy();
        }
        preview.width = frame->width;
        preview.height = frame->height;
    }
    if (!preview.output) {
        return fallback;
    }
    if (probe == SC_KEY_PROBE_REQUESTED || channel != preview.channel) {
        preview.have_pts = preview.have_reference = preview.frame_keyed = false;
        preview.last_key = 0;
        preview.channel = channel;
        gpu_matte_set_key_channel(preview.matte, channel);
    }
    if (probe == SC_KEY_PROBE_REQUESTED) {
        return fallback;
    }
    sc_tick now = sc_tick_now();
    if (frame->pts == AV_NOPTS_VALUE || !preview.have_pts || preview.pts != frame->pts) {
        if (!preview.have_pts || preview.color_space != frame->colorspace
                              || preview.color_range != frame->color_range) {
            if (sc_perspective_configure_rgb(preview.sws, frame) < 0) {
                return fallback;
            }
            preview.color_space = frame->colorspace;
            preview.color_range = frame->color_range;
            preview.have_reference = false;
        }
        preview.pts = frame->pts;
        preview.have_pts = true;
        preview.frame_keyed = false;
        struct sc_key_samples samples = {0};
        bool converted = sws_scale(preview.sws, (const uint8_t *const *) frame->data,
                                   frame->linesize, 0, frame->height, preview.rgba->data,
                                   preview.rgba->linesize) == frame->height;
        if (probe == SC_KEY_PROBE_ACTIVE) {
            preview.have_reference = false;
            preview.last_key = 0;
            if (converted && now >= deadline - SC_KEY_PROBE_TIME + SC_TICK_FROM_MS(150)) {
                probe_key(p, region, deadline, now);
            }
            return fallback;
        }
        const struct sc_key_backing *backing = preview.have_reference ? &preview.backing : NULL;
        if (converted && collect_key(preview.rgba, region, channel, backing, &samples)
                && (channel != 1 || preview.have_reference
                                 || !has_alignment_locators(preview.rgba, map, layout))) {
            if (!preview.have_reference && sc_key_reference(&samples, preview.reference)) {
                if (sc_key_chroma(preview.reference, channel) > .1f) {
                    sc_key_backing_init(&preview.backing, &samples, preview.reference, channel);
                    preview.have_reference = collect_key(preview.rgba, region, channel,
                                                         &preview.backing, NULL);
                    if (preview.have_reference) {
                        memset(preview.plate, 0, sizeof(preview.plate));
                        preview.plate_frames = 0;
                        gpu_matte_set_key_plate(preview.matte, NULL);
                    }
                }
            }
            if (preview.have_reference
                    && SDL_UpdateTexture(preview.input, NULL, preview.rgba->data[0],
                                         preview.rgba->linesize[0])
                    && SDL_FlushRenderer(renderer)) {
                // Flush invalidates SDL's blend cache; reset the actual state too.
                ID3D11Device *device = SDL_GetPointerProperty(SDL_GetRendererProperties(renderer),
                                          SDL_PROP_RENDERER_D3D11_DEVICE_POINTER, NULL);
                ID3D11DeviceContext *context;
                ID3D11Device_GetImmediateContext(device, &context);
                ID3D11DeviceContext_OMSetBlendState(context, NULL, NULL, 0xffffffff);
                ID3D11DeviceContext_Release(context);
                gpu_matte_set_camera_region(preview.matte, region, true);
                const float inverse[6] = {1, 0, 0, 1, 0, 0};
                double gpu_us;
                preview.frame_keyed = gpu_matte_render(preview.matte, preview.source,
                                                       inverse, preview.reference, &gpu_us);
                if (preview.frame_keyed) {
                    if (preview.plate_frames < KEY_PLATE_FRAMES) {
                        learn_key_plate(region, channel);
                    }
                    if (probe == SC_KEY_PROBE_VERIFY) {
                        sc_mutex_lock(&p->mutex);
                        if (p->key_probe == SC_KEY_PROBE_VERIFY
                                && p->key_probe_deadline == deadline) {
                            p->key_probe = SC_KEY_PROBE_DONE;
                        }
                        sc_mutex_unlock(&p->mutex);
                    }
                    if (!preview.last_key) {
                        LOGI("Perspective color key active: #%02X%02X%02X",
                             (unsigned) (preview.reference[0] * 255 + .5f),
                             (unsigned) (preview.reference[1] * 255 + .5f),
                             (unsigned) (preview.reference[2] * 255 + .5f));
                    }
                    preview.last_key = now;
                }
            }
        }
    }
    // Desktop frames are still sent to the phone for real QR recognition.
    bool recent = preview.last_key && now - preview.last_key < SC_TICK_FROM_MS(350);
    return preview.frame_keyed || recent ? preview.output : fallback;
}

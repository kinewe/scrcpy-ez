/* Inverse6 hybrid acceptance test (D3D11 WARP, 64x64).
 *
 * Covers INVERSE6_DESIGN.md section 6:
 *  a) LEARN learns the keyed video path's own background while a light patch
 *     sweeps over a dim 76-alpha background that also carries a static
 *     3/255 keying-bias band (video alpha 73 where native is 76); ready/
 *     learned-frame counters, reset and re-learn.
 *  b) LIVE inside the motion envelope: background is byte-identical to native
 *     (bias canceled), the bright light body equals the single-key V (+-1),
 *     the faint halo is preserved as native + (V - model), isolated speckles
 *     are suppressed to exact native and a 3x3 faint blob survives.
 *  c) motion mask 0 outside the envelope, protected component regions and the
 *     camera exterior all stay byte-identical to native (camera outside is
 *     strictly >; a pixel exactly on the camera edge stays inside).
 *  d) LIVE never updates the model: after the light leaves, the whole frame is
 *     native again, and re-lighting the same spot keeps the exact same bytes.
 *  e) state OFF is still the unchanged single-pass path (the normal-path
 *     model/fixture checks below), and switching OFF after inverse use is
 *     byte-identical to the OFF output captured before the inverse resources
 *     even existed.
 *
 * Expected pixel values are derived by rendering the same input frame once in
 * state OFF with the native reference cleared: that output is the single-key V.
 * The only tolerances used are +-1 LSB (shader float vs UNORM rounding) and the
 * few per-pixel fixture deviations explicitly noted next to the assertions.
 */
#include "gpu_matte.h"
#include "native_ui.h"
#include <assert.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define W 64
#define H 64
#define PITCH (W * 4)

static int fails;
#define CHECK(condition) do { \
    if (!(condition)) { printf("FAIL %d: %s\n", __LINE__, #condition); ++fails; } \
    else printf("ok %d: %s\n", __LINE__, #condition); \
} while (0)

static ID3D11Device *device;
static ID3D11DeviceContext *context;
static ID3D11Texture2D *input_texture, *read_texture;
static ID3D11ShaderResourceView *input_view;
static const float test_inverse[6] = {1, 0, 0, 1, 0, 0};
static const float test_background[3] = {0, 1, 0};
static uint8_t native_frame[W * H * 4];
static uint8_t motion_frame[W * H * 4];
static float native_regions[32][4];

static void model_checks(void) {
    SDL_Surface *frame=SDL_CreateSurface(4,4,SDL_PIXELFORMAT_RGBA32);
    SDL_Surface *base=SDL_CreateSurface(4,4,SDL_PIXELFORMAT_RGBA32);assert(frame&&base);
    float rects[32][4]={{0,0,.5f,1}};
    SDL_FillSurfaceRect(frame,NULL,SDL_MapSurfaceRGBA(frame,0,0,0,76));
    sc_native_ui_model(base,frame,rects,1,true);
    SDL_FillSurfaceRect(frame,NULL,SDL_MapSurfaceRGBA(frame,0,0,128,180));
    sc_native_ui_model(base,frame,rects,1,false);
    uint8_t *left=(uint8_t*)base->pixels,*right=left+3*4;
    assert(left[2]==128&&left[3]==180);assert(right[2]==0&&right[3]==76);
    SDL_Surface *motion=SDL_CreateSurface(4,4,SDL_PIXELFORMAT_RGBA32);assert(motion);
    SDL_FillSurfaceRect(frame,NULL,SDL_MapSurfaceRGBA(frame,0,0,0,76));
    sc_native_ui_inverse_model(base,motion,frame,rects,1,true,true);
    uint8_t *animated=(uint8_t*)frame->pixels+2*4;animated[2]=128;animated[3]=180;
    uint8_t *component=(uint8_t*)frame->pixels;component[1]=255;component[3]=255;
    sc_native_ui_inverse_model(base,motion,frame,rects,1,false,true);
    assert(((uint8_t*)motion->pixels)[0]==0);
    assert(((uint8_t*)motion->pixels)[2*4]==255);
    assert(((uint8_t*)motion->pixels)[3*4]==0);
    /* Protected components are cleaned with the same min-alpha rule: a component
     * frame with HIGHER alpha must not overwrite an already darker base sample
     * (otherwise sweep light freezes into static UI and leaves a ghost). */
    assert(((uint8_t*)base->pixels)[1]==0);
    assert(((uint8_t*)base->pixels)[3]==180);
    assert(((uint8_t*)base->pixels)[2*4+3]==76);
    /* A darker component sample must still refine the base. */
    uint8_t *dim=(uint8_t*)frame->pixels;
    dim[0]=0;dim[1]=0;dim[2]=64;dim[3]=100;
    sc_native_ui_inverse_model(base,motion,frame,rects,1,false,true);
    assert(((uint8_t*)base->pixels)[3]==100);
    assert(((uint8_t*)base->pixels)[2]==64);
    SDL_FillSurfaceRect(frame,NULL,SDL_MapSurfaceRGBA(frame,0,0,0,76));
    assert(sc_native_ui_inverse_freeze(motion,rects,1));
    assert(((uint8_t*)motion->pixels)[3*4]==255); // Fill unsampled sweep holes once.
    assert(((uint8_t*)motion->pixels)[0]==0); // Static components stay protected.
    SDL_Surface *before=SDL_DuplicateSurface(motion);assert(before);
    SDL_FillSurfaceRect(frame,NULL,SDL_MapSurfaceRGBA(frame,0,64,0,180));
    sc_native_ui_inverse_model(base,motion,frame,rects,1,false,false);
    for(int y=0;y<motion->h;++y)assert(!memcmp((uint8_t*)motion->pixels+y*motion->pitch,(uint8_t*)before->pixels+y*before->pitch,motion->w*4));
    SDL_DestroySurface(before);
    SDL_DestroySurface(motion);
    SDL_DestroySurface(base);SDL_DestroySurface(frame);
    frame=SDL_CreateSurface(8,8,SDL_PIXELFORMAT_RGBA32);base=SDL_CreateSurface(8,8,SDL_PIXELFORMAT_RGBA32);
    motion=SDL_CreateSurface(8,8,SDL_PIXELFORMAT_RGBA32);assert(frame&&base&&motion);
    SDL_FillSurfaceRect(frame,NULL,SDL_MapSurfaceRGBA(frame,0,0,0,76));
    sc_native_ui_inverse_model(base,motion,frame,rects,0,true,true);
    uint8_t *moving=(uint8_t*)frame->pixels+3*frame->pitch+3*4;moving[2]=128;moving[3]=180;
    sc_native_ui_inverse_model(base,motion,frame,rects,0,false,true);
    assert(sc_native_ui_inverse_freeze(motion,rects,0));
    assert(((uint8_t*)motion->pixels)[7*motion->pitch+7*4]==0);
    moving=(uint8_t*)frame->pixels+7*frame->pitch+7*4;moving[2]=128;moving[3]=180;
    sc_native_ui_inverse_model(base,motion,frame,rects,0,false,false);
    assert(((uint8_t*)motion->pixels)[7*motion->pitch+7*4]==0); // New changes cannot grow a frozen envelope.
    SDL_DestroySurface(motion);SDL_DestroySurface(base);SDL_DestroySurface(frame);
    /* Camera-preview region mask: the whole calibrated region is cleaned, the
     * protected component stays native, everything outside keeps latest PNG. */
    SDL_Surface *region_mask=SDL_CreateSurface(16,8,SDL_PIXELFORMAT_RGBA32);assert(region_mask);
    float camera_region[4]={.25f,.25f,.5f,.5f};
    float component_rects[32][4]={{.3f,.3f,.2f,.2f}};
    sc_native_ui_inverse_region_mask(region_mask,camera_region,component_rects,1);
    const uint8_t *rm=(const uint8_t*)region_mask->pixels;
    assert(rm[2*region_mask->pitch+8*4]==255);   /* inside region, not a component */
    assert(rm[1*region_mask->pitch+1*4]==0);     /* outside the camera region */
    assert(rm[3*region_mask->pitch+7*4]==0);     /* component rect inside the region */
    SDL_DestroySurface(region_mask);
    char path[]="native-ui-regions-test.bin";FILE *f=fopen(path,"wb");assert(f);
    uint32_t header[3]={400,400,1};int32_t box[4]={10,20,30,40};
    fwrite("EVS1",1,4,f);fwrite(header,4,3,f);fwrite(box,4,4,f);fclose(f);
    _putenv_s("SCEZ_NATIVE_UI_REGIONS_FILE",path);
    assert(sc_native_ui_regions(16,16,rects)==1);
    assert(fabsf(rects[0][0]-.025f)<.0001f&&fabsf(rects[0][3]-.1f)<.0001f);
    assert(sc_native_ui_regions(16,32,rects)==0);
    f=fopen(path,"wb");assert(f);fwrite("bad",1,3,f);fclose(f);
    assert(sc_native_ui_regions(16,16,rects)==0);remove(path);_putenv_s("SCEZ_NATIVE_UI_REGIONS_FILE","");
}

static int close4(const uint8_t *p, int r, int g, int b, int a, int tol) {
    return abs((int) p[0] - r) <= tol && abs((int) p[1] - g) <= tol &&
           abs((int) p[2] - b) <= tol && abs((int) p[3] - a) <= tol;
}

static void print_pixel(const char *what, int x, int y, const uint8_t *got, const uint8_t *want) {
    printf("FAIL %s (%d,%d) got %d,%d,%d,%d want %d,%d,%d,%d\n", what, x, y,
           got[0], got[1], got[2], got[3], want[0], want[1], want[2], want[3]);
    ++fails;
}

static void expect_native_rect(const char *what, const uint8_t *out, int x0, int y0, int x1, int y1) {
    for (int y = y0; y < y1; ++y) for (int x = x0; x < x1; ++x) {
        const uint8_t *p = out + (y * W + x) * 4, *n = native_frame + (y * W + x) * 4;
        if (memcmp(p, n, 4)) { print_pixel(what, x, y, p, n); return; }
    }
    printf("ok native-exact %s\n", what);
}

static void expect_close_rect(const char *what, const uint8_t *out, const uint8_t *want,
                              int x0, int y0, int x1, int y1, int tol) {
    for (int y = y0; y < y1; ++y) for (int x = x0; x < x1; ++x) {
        const uint8_t *p = out + (y * W + x) * 4, *w = want + (y * W + x) * 4;
        if (abs((int) p[0] - (int) w[0]) > tol || abs((int) p[1] - (int) w[1]) > tol ||
            abs((int) p[2] - (int) w[2]) > tol || abs((int) p[3] - (int) w[3]) > tol) {
            print_pixel(what, x, y, p, w); return;
        }
    }
    printf("ok close %s (tol %d)\n", what, tol);
}

static void expect_rgba_rect(const char *what, const uint8_t *out, int x0, int y0, int x1, int y1,
                             int r, int g, int b, int a, int tol) {
    uint8_t want[4] = {(uint8_t) r, (uint8_t) g, (uint8_t) b, (uint8_t) a};
    for (int y = y0; y < y1; ++y) for (int x = x0; x < x1; ++x) {
        const uint8_t *p = out + (y * W + x) * 4;
        if (!close4(p, r, g, b, a, tol)) { print_pixel(what, x, y, p, want); return; }
    }
    printf("ok rgba %s\n", what);
}

static void d3d_init(void) {
    assert(SUCCEEDED(D3D11CreateDevice(NULL, D3D_DRIVER_TYPE_WARP, NULL, 0, NULL, 0,
                                       D3D11_SDK_VERSION, &device, NULL, &context)));
    uint8_t initial[W * H * 4] = {0};
    D3D11_TEXTURE2D_DESC d = {0};
    d.Width = W; d.Height = H; d.MipLevels = d.ArraySize = 1;
    d.Format = DXGI_FORMAT_R8G8B8A8_UNORM; d.SampleDesc.Count = 1;
    d.BindFlags = D3D11_BIND_SHADER_RESOURCE;
    D3D11_SUBRESOURCE_DATA data = {initial, PITCH, 0};
    assert(SUCCEEDED(ID3D11Device_CreateTexture2D(device, &d, &data, &input_texture)));
    assert(SUCCEEDED(ID3D11Device_CreateShaderResourceView(device, (ID3D11Resource *) input_texture, NULL, &input_view)));
    d.Usage = D3D11_USAGE_STAGING; d.BindFlags = 0; d.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
    assert(SUCCEEDED(ID3D11Device_CreateTexture2D(device, &d, NULL, &read_texture)));
}

static void d3d_destroy(void) {
    ID3D11ShaderResourceView_Release(input_view);
    ID3D11Texture2D_Release(input_texture);
    ID3D11Texture2D_Release(read_texture);
    ID3D11DeviceContext_Release(context);
    ID3D11Device_Release(device);
}

/* Renders one frame in whatever inverse state the matte is currently in. */
static void render(struct gpu_matte *m, const uint8_t *src, uint8_t *dst) {
    ID3D11DeviceContext_UpdateSubresource(context, (ID3D11Resource *) input_texture, 0, NULL, src, PITCH, 0);
    double ignored = 0;
    assert(gpu_matte_render(m, input_view, test_inverse, test_background, &ignored));
    ID3D11DeviceContext_CopyResource(context, (ID3D11Resource *) read_texture,
                                     (ID3D11Resource *) gpu_matte_texture(m));
    D3D11_MAPPED_SUBRESOURCE map;
    assert(SUCCEEDED(ID3D11DeviceContext_Map(context, (ID3D11Resource *) read_texture, 0,
                                             D3D11_MAP_READ, 0, &map)));
    for (int y = 0; y < H; ++y) memcpy(dst + y * PITCH, (const uint8_t *) map.pData + y * map.RowPitch, PITCH);
    ID3D11DeviceContext_Unmap(context, (ID3D11Resource *) read_texture, 0);
}

/* Normal single-pass path with a native reference and no motion mask (the
 * pre-inverse hybrid behavior); inverse6 must leave this byte-identical. */
static void normal_path_checks(void) {
    uint8_t source[W * H * 4], model[W * H * 4], out[W * H * 4];
    memset(model, 0, sizeof(model));
    for (int y = 0; y < H; ++y) for (int x = 0; x < W; ++x) {
        unsigned i = (y * W + x) * 4;
        source[i] = source[i + 2] = 0; source[i + 1] = 255; source[i + 3] = 255;
        if (y >= 24 && y < 32) { source[i + 1] = 127; source[i + 2] = 128; }
        if (y >= 8 && y < 12) { source[i] = 20; source[i + 1] = 230; source[i + 2] = 40; }
        if (y >= 40 && y < 48) { model[i + 1] = 255; model[i + 3] = 255; }
        if (y >= 50 && y < 56) { source[i + 1] = (uint8_t) (179 + (x % 5) - 2); model[i + 3] = 76; }
        if (y >= 16 && y < 24) { source[i + 1] = 150; model[i + 3] = 76; }
    }
    struct gpu_matte *m = gpu_matte_create(device, W, H);
    assert(m);
    float regions[32][4] = {{0, 40.f / 64, 1, 8.f / 64}};
    assert(gpu_matte_set_native_reference(m, model, PITCH, regions, 1));
    render(m, source, out);
    expect_rgba_rect("normal single-pass keyed light", out, 0, 24, W, 32, 0, 0, 128, 128, 1);
    expect_rgba_rect("normal single-pass opaque native UI", out, 0, 40, W, 48, 0, 255, 0, 255, 0);
    expect_rgba_rect("normal single-pass keyed bias returns native", out, 0, 50, W, 56, 0, 0, 0, 76, 0);
    float camera[4] = {0, 0, 1, .625f};
    gpu_matte_set_camera_region(m, camera, true);
    render(m, source, out);
    expect_rgba_rect("normal camera exterior single-key fallback", out, 0, 42, W, 43, 0, 255, 0, 255, 0);
    gpu_matte_set_camera_region(m, NULL, false);
    assert(gpu_matte_set_native_reference(m, NULL, 0, NULL, 0));
    render(m, source, out);
    expect_rgba_rect("normal single-key fallback", out, 0, 42, W, 43, 0, 0, 0, 1, 1);
    gpu_matte_destroy(m);
}

/* ------------------------- inverse6 fixture ------------------------- */

/* Premultiplied UI over transparent before the dynamic light is added:
 * dim 76/255 scrim everywhere and a static 73/255 keying-bias band (video alpha
 * three LSB below the 76-alpha native PNG). The video path carries no
 * component UI: the native PNG owns the protected components. */
static void ui_rgba(int x, int y, float v[4]) {
    (void) x;
    v[0] = v[1] = v[2] = 0; v[3] = 76.f / 255;
    if (y >= 44 && y < 48) v[3] = 73.f / 255;
}
/* The bright scanner body: premultiplied RGB over alpha 0.8. */
static void add_core(float v[4]) { v[0] = .05f; v[1] = .05f; v[2] = .6f; v[3] = .8f; }
/* The soft halo: only ~0.052 extra alpha over the 76/255 scrim. */
static void add_halo(float v[4]) { v[0] = .03f; v[1] = .03f; v[2] = .06f; v[3] = .35f; }

/* Encode the green-keyed video pixel whose single-key recovery is V. */
static void encode(const float v[4], uint8_t *dst) {
    for (int c = 0; c < 3; ++c) {
        float cf = v[c] + (1.f - v[3]) * (c == 1 ? 1.f : 0.f);
        int i = (int) lroundf(cf * 255.f);
        dst[c] = (uint8_t) (i < 0 ? 0 : i > 255 ? 255 : i);
    }
    dst[3] = 255;
}
/* mode >= 0: LEARN sweep frame (light core/halo shifted down by mode rows).
 * mode == -1: LIVE frame with the light at a new position, a faint halo, one
 *             isolated speckle and a 3x3 faint blob.
 * mode <= -2: background only. */
static void fill_frame(uint8_t *src, int mode) {
    for (int y = 0; y < H; ++y) for (int x = 0; x < W; ++x) {
        float v[4];
        ui_rgba(x, y, v);
        if (mode >= 0) {
            if (y >= 24 + mode && y < 32 + mode) add_core(v);
            if (y >= 33 + mode && y < 37 + mode) add_halo(v);
        } else if (mode == -1) {
            if (y >= 27 && y < 35) add_core(v);
            if (y >= 36 && y < 40) add_halo(v);
            if (y == 45 && x == 45) v[3] = 76.f / 255 + .02f;          /* isolated speckle */
            if (y >= 48 && y < 51 && x >= 20 && x < 23) {              /* 3x3 faint blob */
                v[0] = .01f; v[1] = .01f; v[2] = .02f; v[3] = 76.f / 255 + .02f;
            }
        }
        encode(v, src + (y * W + x) * 4);
    }
}
/* LIVE frame plus bright pixels where output must be suppressed:
 * - rows 4..7: protected component region (native opaque white),
 * - rows 10..18: inside camera but motion mask 0 (outside sweep envelope),
 * - rows 27..34 are already bright and cross the y=32 camera boundary. */
static void fill_edges(uint8_t *src) {
    for (int y = 0; y < H; ++y) for (int x = 0; x < W; ++x) {
        float v[4];
        ui_rgba(x, y, v);
        if (y >= 27 && y < 35) add_core(v);
        if (y >= 36 && y < 40) add_halo(v);
        if (y >= 4 && y < 8 && x >= 20 && x < 41) add_core(v);
        if (y >= 10 && y < 19 && x >= 8 && x < 25) add_core(v);
        encode(v, src + (y * W + x) * 4);
    }
}

static void build_native_frame(void) {
    for (int y = 0; y < H; ++y) for (int x = 0; x < W; ++x) {
        uint8_t *p = native_frame + (y * W + x) * 4;
        p[0] = p[1] = p[2] = 0; p[3] = 76;
        if (y >= 4 && y < 8) { p[0] = p[1] = p[2] = 255; p[3] = 255; }     /* protected component */
        if (y >= 58 && y < 62 && x >= 4 && x < 20) { p[0] = 153; p[1] = 51; p[2] = 51; p[3] = 255; }
    }
    memset(motion_frame, 0, sizeof(motion_frame));
    for (int y = 0; y < H; ++y) for (int x = 0; x < W; ++x)
        motion_frame[(y * W + x) * 4] = (y >= 10 && y < 19) ? 0 : 255;
    memset(native_regions, 0, sizeof(native_regions));
    native_regions[0][0] = 0; native_regions[0][1] = 4.f / H;
    native_regions[0][2] = 1; native_regions[0][3] = 4.f / H;
}

static void set_off(struct gpu_matte *m) {
    gpu_matte_set_inverse_state(m, EZ_GPU_MATTE_INVERSE_OFF);
    assert(gpu_matte_set_native_reference(m, NULL, 0, NULL, 0));
    gpu_matte_set_camera_region(m, NULL, false);
}

static void set_live(struct gpu_matte *m) {
    gpu_matte_set_inverse_state(m, EZ_GPU_MATTE_INVERSE_LIVE);
    assert(gpu_matte_set_native_reference(m, native_frame, PITCH, native_regions, 1));
    assert(gpu_matte_set_native_motion(m, motion_frame, PITCH));
    gpu_matte_set_camera_region(m, NULL, false);
}

static void inverse6_checks(void) {
    uint8_t src[W * H * 4], out[W * H * 4];
    uint8_t vbg[W * H * 4], vlive[W * H * 4], vedges[W * H * 4], vframe0[W * H * 4];
    uint8_t offbase[W * H * 4], live_first[W * H * 4], live_again[W * H * 4];
    build_native_frame();
    struct gpu_matte *m = gpu_matte_create(device, W, H);
    assert(m);
    CHECK(!gpu_matte_inverse_ready(m));
    CHECK(gpu_matte_inverse_learned_frames(m) == 0);

    /* OFF baseline before the inverse resources exist: the unchanged
     * single-pass composition with native+motion bound (old hybrid path). */
    assert(gpu_matte_set_native_reference(m, native_frame, PITCH, native_regions, 1));
    assert(gpu_matte_set_native_motion(m, motion_frame, PITCH));
    fill_frame(src, -2);
    render(m, src, offbase);

    /* Single-key V of each frame: state OFF with no native reference. */
    set_off(m);
    fill_frame(src, -2);
    render(m, src, vbg);
    fill_frame(src, -1);
    render(m, src, vlive);
    fill_frame(src, 0);
    render(m, src, vframe0);
    CHECK(close4(vbg + (20 * W + 32) * 4, 0, 0, 0, 76, 1));
    CHECK(vbg[(45 * W + 32) * 4 + 3] == 73);      /* bias band: video 73 vs native 76 */
    CHECK(native_frame[(45 * W + 32) * 4 + 3] == 76);
    CHECK(close4(vlive + (30 * W + 32) * 4, 13, 13, 153, 204, 2));   /* bright body */
    CHECK(close4(vlive + (38 * W + 32) * 4, 8, 8, 15, 90, 2));       /* faint halo */
    CHECK(close4(vlive + (49 * W + 21) * 4, 3, 3, 5, 81, 2));        /* 3x3 blob */
    CHECK(close4(vlive + (45 * W + 45) * 4, 0, 0, 0, 81, 2));        /* isolated speckle */
    CHECK(close4(vframe0 + (28 * W + 32) * 4, 13, 13, 153, 204, 2)); /* learn-frame core */

    /* (a) LEARN: sweep a light patch over the dim background + bias band. */
    gpu_matte_reset_video_model(m);
    CHECK(!gpu_matte_inverse_ready(m));
    CHECK(gpu_matte_inverse_learned_frames(m) == 0);
    gpu_matte_set_inverse_state(m, EZ_GPU_MATTE_INVERSE_LEARN);
    fill_frame(src, 0);
    render(m, src, out);
    CHECK(gpu_matte_inverse_learned_frames(m) == 1);
    CHECK(!gpu_matte_inverse_ready(m));
    expect_close_rect("LEARN frame publishes the single-key V", out, vframe0, 0, 0, W, H, 1);
    for (int i = 1; i < 40; ++i) {
        fill_frame(src, (i % 8) * 3);
        render(m, src, out);
    }
    CHECK(gpu_matte_inverse_learned_frames(m) == 40);
    CHECK(gpu_matte_inverse_ready(m));

    /* Reset; LIVE before the model is ready must fall back to the single-key V. */
    gpu_matte_reset_video_model(m);
    CHECK(!gpu_matte_inverse_ready(m));
    CHECK(gpu_matte_inverse_learned_frames(m) == 0);
    set_live(m);
    fill_frame(src, -1);
    render(m, src, out);
    CHECK(!gpu_matte_inverse_ready(m));
    CHECK(gpu_matte_inverse_learned_frames(m) == 0);
    expect_close_rect("LIVE before ready falls back to V", out, vlive, 0, 0, W, H, 1);

    /* Clean re-learn on background-only frames (uniform model). */
    gpu_matte_reset_video_model(m);
    gpu_matte_set_inverse_state(m, EZ_GPU_MATTE_INVERSE_LEARN);
    fill_frame(src, -2);
    for (int i = 0; i < 32; ++i) render(m, src, out);
    CHECK(gpu_matte_inverse_learned_frames(m) == 32);
    CHECK(gpu_matte_inverse_ready(m));

    /* (b) LIVE inside the motion envelope. */
    set_live(m);
    fill_frame(src, -1);
    render(m, src, live_first);
    expect_native_rect("LIVE background above the light core", live_first, 2, 20, 62, 27);
    expect_native_rect("LIVE background between halo and bias band", live_first, 2, 40, 62, 44);
    expect_native_rect("LIVE learned keying-bias band", live_first, 2, 44, 62, 47);
    expect_native_rect("LIVE background below the blob", live_first, 2, 52, 62, 57);
    expect_close_rect("LIVE bright body equals single-key V", live_first, vlive, 30, 29, 34, 32, 1);
    CHECK(live_first[(30 * W + 32) * 4 + 3] > native_frame[(30 * W + 32) * 4 + 3]);
    expect_close_rect("LIVE faint halo preserved as n+(V-model)", live_first, vlive, 30, 36, 34, 39, 1);
    CHECK(live_first[(38 * W + 32) * 4 + 3] > native_frame[(38 * W + 32) * 4 + 3]);
    CHECK(memcmp(live_first + (45 * W + 45) * 4, native_frame + (45 * W + 45) * 4, 4) == 0);
    expect_close_rect("LIVE 3x3 faint blob center", live_first, vlive, 21, 49, 22, 50, 1);
    CHECK(live_first[(49 * W + 21) * 4 + 3] > native_frame[(49 * W + 21) * 4 + 3]);
    CHECK(gpu_matte_inverse_learned_frames(m) == 32);

    /* (d) LIVE never updates the model: light leaves -> exact native. */
    fill_frame(src, -2);
    render(m, src, out);
    expect_native_rect("LIVE background-only frame equals native", out, 0, 0, W, H);
    CHECK(gpu_matte_inverse_learned_frames(m) == 32);
    CHECK(gpu_matte_inverse_ready(m));
    fill_frame(src, -1);
    render(m, src, live_again);
    CHECK(memcmp(live_again, live_first, W * H * 4) == 0);   /* re-lighting is not lost */

    /* (c) Protected region, motion envelope and camera exterior. */
    set_off(m);
    fill_edges(src);
    render(m, src, vedges);
    CHECK(close4(vedges + (32 * W + 32) * 4, 13, 13, 153, 204, 2));  /* camera boundary light */
    CHECK(close4(vedges + (14 * W + 16) * 4, 13, 13, 153, 204, 2));  /* envelope light */
    CHECK(close4(vedges + (5 * W + 30) * 4, 13, 13, 153, 204, 2));   /* protected region light */
    set_live(m);
    float camera[4] = {0, 0, 1, .5f};
    gpu_matte_set_camera_region(m, camera, true);
    fill_edges(src);
    render(m, src, out);
    expect_native_rect("camera exterior keeps native", out, 0, 33, W, H);
    expect_close_rect("camera boundary q==camera.y+h stays inside", out, vedges, 0, 32, W, 33, 1);
    expect_native_rect("motion envelope 0 suppresses bright light", out, 8, 10, 25, 19);
    expect_native_rect("protected component region keeps native", out, 0, 4, W, 8);
    gpu_matte_set_camera_region(m, NULL, false);

    /* LIVE fallbacks: missing motion or native reference publishes V. */
    CHECK(gpu_matte_set_native_motion(m, NULL, 0));
    fill_frame(src, -1);
    render(m, src, out);
    expect_close_rect("LIVE without motion falls back to V", out, vlive, 0, 0, W, H, 1);
    CHECK(gpu_matte_set_native_motion(m, motion_frame, PITCH));
    CHECK(gpu_matte_set_native_reference(m, NULL, 0, NULL, 0));
    fill_frame(src, -1);
    render(m, src, out);
    expect_close_rect("LIVE without native reference falls back to V", out, vlive, 0, 0, W, H, 1);

    /* (e) OFF after inverse use is byte-identical to the pre-inverse baseline. */
    CHECK(gpu_matte_set_native_reference(m, native_frame, PITCH, native_regions, 1));
    CHECK(gpu_matte_set_native_motion(m, motion_frame, PITCH));
    gpu_matte_set_inverse_state(m, EZ_GPU_MATTE_INVERSE_OFF);
    fill_frame(src, -2);
    render(m, src, out);
    CHECK(memcmp(out, offbase, W * H * 4) == 0);
    CHECK(gpu_matte_inverse_learned_frames(m) == 32);
    CHECK(gpu_matte_inverse_ready(m));

    /* Reset + relearn after the live session (section 6 item 7). */
    gpu_matte_reset_video_model(m);
    CHECK(!gpu_matte_inverse_ready(m));
    gpu_matte_set_inverse_state(m, EZ_GPU_MATTE_INVERSE_LEARN);
    fill_frame(src, -2);
    render(m, src, out);
    CHECK(gpu_matte_inverse_learned_frames(m) == 1);
    CHECK(!gpu_matte_inverse_ready(m));
    for (int i = 0; i < 31; ++i) {
        fill_frame(src, (i % 8) * 3);
        render(m, src, out);
    }
    CHECK(gpu_matte_inverse_learned_frames(m) == 32);
    CHECK(gpu_matte_inverse_ready(m));

    /* State OFF without native reference still returns the single-key V. */
    gpu_matte_set_inverse_state(m, EZ_GPU_MATTE_INVERSE_OFF);
    assert(gpu_matte_set_native_reference(m, NULL, 0, NULL, 0));
    fill_frame(src, -1);
    render(m, src, out);
    expect_close_rect("OFF single-key fallback returns V", out, vlive, 0, 0, W, H, 1);

    gpu_matte_destroy(m);
}

int main(void) {
    model_checks();
    _putenv_s("SCEZ_PERSPECTIVE_KEY_COLOR", "green");
    d3d_init();
    normal_path_checks();
    inverse6_checks();
    d3d_destroy();
    _putenv_s("SCEZ_PERSPECTIVE_KEY_COLOR", "");
    if (fails) {
        printf("hybrid: FAILED %d checks\n", fails);
        return 1;
    }
    puts("hybrid: normal single-pass, inverse6 learn/ready/reset, byte-exact native background with keying bias, single-key V body, preserved faint halo, speckle suppression, blob survival, envelope/region/camera isolation, frozen model and OFF fallback passed");
    return 0;
}

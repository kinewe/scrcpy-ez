// Monitor WGC -> GPU affine sampling -> NV12. No desktop readback or manual
// reconstruction of foreign windows, desktop icons, or wallpaper.
#define COBJMACROS
#include <windows.h>
#include <d3d11.h>
#include <d3dcompiler.h>
#include <dxgi.h>
#include <roapi.h>
#include <winstring.h>
#include <inspectable.h>
#include <math.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <libavutil/hwcontext.h>
#include <libavutil/hwcontext_d3d11va.h>
#include "desktop_capture.h"
#include "perspective_locators.h"
#include "util/log.h"

// Small WinRT ABI boundary, as in the previous capture backend.
#define METHOD(o, n, t) ((t)(*(void ***)(o))[n])
typedef HRESULT (STDMETHODCALLTYPE *method0)(void *);
typedef HRESULT (STDMETHODCALLTYPE *method_out)(void *, void **);
typedef HRESULT (STDMETHODCALLTYPE *method_size)(void *, void *);
typedef HRESULT (STDMETHODCALLTYPE *method_bool)(void *, BYTE);
typedef struct { INT32 width, height; } capture_size;
typedef HRESULT (STDMETHODCALLTYPE *method_create)(void *, void *, INT32, INT32, capture_size, void **);
typedef HRESULT (STDMETHODCALLTYPE *method_recreate)(void *, void *, INT32, INT32, capture_size);
typedef HRESULT (STDMETHODCALLTYPE *method_session)(void *, void *, void **);
typedef HRESULT (STDMETHODCALLTYPE *method_monitor)(void *, HMONITOR, REFIID, void **);
typedef HRESULT (STDMETHODCALLTYPE *method_interface)(void *, REFIID, void **);
#define SC_MAX_CAPTURE_MONITORS 8
struct monitor_source {
    HMONITOR monitor;
    void *item, *pool, *session;
    ID3D11Texture2D *texture;
    ID3D11ShaderResourceView *view;
    capture_size size;
    UINT width, height;
    ULONGLONG retry_after;
    bool ready;
};
struct sc_desktop_capture {
    AVBufferRef *device_ref, *frames_ref;
    ID3D11Device *device; // borrowed from device_ref
    ID3D11DeviceContext *context;
    ID3D11VideoDevice *video_device;
    ID3D11VideoContext *video_context;
    ID3D11VideoProcessorEnumerator *enumerator;
    ID3D11VideoProcessor *processor;
    ID3D11VideoProcessorInputView *input_view;
    ID3D11Texture2D *target;
    ID3D11RenderTargetView *target_view;
    ID3D11VertexShader *vertex_shader;
    ID3D11PixelShader *pixel_shader;
    ID3D11SamplerState *sampler;
    ID3D11Buffer *constants;
    void *winrt_device, *item_factory, *pool_factory;
    GUID item_iid, closable_iid, dxgi_access_iid;
    struct monitor_source sources[SC_MAX_CAPTURE_MONITORS];
    unsigned width, height;
    bool ro_initialized;
    bool borderless_allowed;
};
struct render_constants { float row_x[4], row_y[4], monitor[4], options[4], clip[4]; };
#define SC_STRINGIFY_LITERAL_(x) #x
#define SC_STRINGIFY_LITERAL(x) SC_STRINGIFY_LITERAL_(x)
static const char shader_source[] =
    "Texture2D image:register(t0); SamplerState sampling:register(s0);"
    "cbuffer Geometry:register(b0){float4 rowX;float4 rowY;float4 monitor;float4 options;float4 clip;}"
    "struct V{float4 pos:SV_POSITION;float2 uv:TEXCOORD0;};"
    "V vs(uint id:SV_VertexID){V o;float2 p=float2((id<<1)&2,id&2);"
    "o.pos=float4(p.x*2-1,1-p.y*2,0,1);o.uv=p;return o;}"
    "float4 ps(V i):SV_TARGET{"
    "if(options.z>9.5){float2 q=floor(i.uv*float2(4,3));float2 c=(q+float2(.5,.5))/float2(4,3);"
    "int cell=(int)q.x+(int)q.y*4;int o=cell-(cell/4)*4;int inr=(o+cell/4+1)-(o+cell/4+1)/4*4;"
    "float3 oc=(o==0)?float3(1,0,0):((o==1)?float3(0,0,1):((o==2)?float3(1,1,0):float3(1,0,1)));"
    "float3 ic=(inr==0)?float3(1,0,0):((inr==1)?float3(0,0,1):((inr==2)?float3(1,1,0):float3(1,0,1)));"
    "float2 d=abs(i.uv-c);if(d.x<.045&&d.y<.045){return (d.x<.018&&d.y<.018)?float4(ic,1):float4(oc,1);}return float4(0,1,0,1);}"
    "if(options.z>8.5){bool right=i.uv.x>=.5,bottom=i.uv.y>=.5;"
    "float lo=" SC_STRINGIFY_LITERAL(SC_CENTER_LOCATOR_LOW) ",hi=" SC_STRINGIFY_LITERAL(SC_CENTER_LOCATOR_HIGH) ";"
    "float2 center=float2(right?hi:lo,bottom?hi:lo);"
    "if(any(abs(i.uv-center)>=.038))return float4(0,1,0,1);"
    "return bottom?(right?float4(1,0,1,1):float4(1,1,0,1)):(right?float4(0,0,1,1):float4(1,0,0,1));}"
    "if(options.z>7.5)return float4(.25,.25,.25,1);"
    "if(options.z>6.5)return float4(.34,0,0,1);"
    "if(options.z>5.5)return float4(.5,.5,.5,1);"
    "if(options.z>4.5)return float4(1,1,1,1);"
    "if(options.z>2.5&&options.z<3.5)return options.w<.5?float4(.34,0,0,1):(options.w<1.5?float4(0,1,0,1):float4(0,0,1,1));"
    "if(options.z>1.5&&options.z<2.5)return float4(0,0,0,1);"
    "if(options.z>.5){float2 q=i.uv*options.xy;bool compact=options.z>3.5;bool right=q.x>=options.x*(compact?.175:.5);"
    "bool bottom=q.y>=options.y*.5;float2 center=options.xy*float2(compact?(right?.25:.1):(right?.75:.25),bottom?.75:.25);"
    "if(abs(q.x-center.x)>=options.x/(compact?48:16)||abs(q.y-center.y)>=options.y/16)return float4(0,1,0,1);"
    "return bottom?(right?float4(1,0,1,1):float4(1,1,0,1)):(right?float4(0,0,1,1):float4(1,0,0,1));}"
    "float2 p=float2(dot(rowX.xy,i.uv)+rowX.z,dot(rowY.xy,i.uv)+rowY.z);"
    "if(any(p<clip.xy)||any(p>=clip.xy+clip.zw))discard;"
    "float2 uv=(p-monitor.xy)*monitor.zw;"
    "if(any(uv<0)||any(uv>=1))discard;return float4(image.Sample(sampling,uv).rgb,1);}";
#undef SC_STRINGIFY_LITERAL
#undef SC_STRINGIFY_LITERAL_

static GUID guid(const wchar_t *text) { GUID id; CLSIDFromString(text, &id); return id; }
static void release(void *o) { if (o) { IUnknown_Release((IUnknown *)o); } }
static HRESULT query(void *o, const GUID *id, void **out) { return IUnknown_QueryInterface((IUnknown *)o, id, out); }
static void close_object(struct sc_desktop_capture *c, void *o) {
    void *closable = NULL;
    if (o && SUCCEEDED(query(o, &c->closable_iid, &closable))) {
        METHOD(closable, 6, method0)(closable); release(closable);
    }
}
static HRESULT factory(const wchar_t *name, const GUID *id, void **out) {
    HSTRING text; HRESULT hr = WindowsCreateString(name, (UINT32)wcslen(name), &text);
    if (FAILED(hr)) { return hr; }
    hr = RoGetActivationFactory(text, id, out); WindowsDeleteString(text); return hr;
}

// Same public consent/property path used by OBS. Denied or unavailable access
// leaves the OS indicator intact; capture itself remains usable.
static bool request_borderless(void) {
    GUID access_id=guid(L"{743ed370-06ec-5040-a58a-901f0f757095}");
    GUID info_id=guid(L"{00000036-0000-0000-c000-000000000046}");
    void *access=NULL,*operation=NULL,*info=NULL;
    typedef HRESULT (STDMETHODCALLTYPE *request_fn)(void *,INT32,void **);
    typedef HRESULT (STDMETHODCALLTYPE *status_fn)(void *,INT32 *);
    HRESULT hr=factory(L"Windows.Graphics.Capture.GraphicsCaptureAccess",&access_id,&access);
    bool allowed=false;
    if (FAILED(hr)) { goto end; }
    hr=METHOD(access,6,request_fn)(access,0,&operation); // Borderless
    if (FAILED(hr) || FAILED(query(operation,&info_id,&info))) { goto end; }
    ULONGLONG deadline=GetTickCount64()+2000;
    INT32 status=0,result=-1;
    while (SUCCEEDED(hr=METHOD(info,7,status_fn)(info,&status)) && status==0 && GetTickCount64()<deadline) { Sleep(5); }
    if (SUCCEEDED(hr) && status==1 && SUCCEEDED(METHOD(operation,8,status_fn)(operation,&result))) {
        allowed=result==4; // AppCapabilityAccessStatus.Allowed
        LOGI("Perspective borderless capture permission: %s (status=%d)",allowed?"allowed":"unavailable",result);
    } else {
        METHOD(info,9,method0)(info); // bounded optional request
        LOGW("Perspective borderless capture request unavailable; OS indicator retained");
    }
end:
    release(info);release(operation);release(access);return allowed;
}
static void close_source(struct sc_desktop_capture *c, struct monitor_source *s) {
    close_object(c, s->session); close_object(c, s->pool);
    release(s->session); release(s->pool); release(s->item); release(s->view); release(s->texture);
    memset(s, 0, sizeof(*s));
}
void sc_desktop_capture_suspend(struct sc_desktop_capture *c) {
    if (c) { for (int i=0; i<SC_MAX_CAPTURE_MONITORS; ++i) { close_source(c, &c->sources[i]); } }
}
void sc_desktop_capture_destroy(struct sc_desktop_capture *c) {
    if (!c) { return; }
    sc_desktop_capture_suspend(c);
    if (c->context) { ID3D11DeviceContext_ClearState(c->context); ID3D11DeviceContext_Flush(c->context); }
    release(c->input_view); release(c->processor); release(c->enumerator);
    release(c->video_context); release(c->video_device);
    release(c->vertex_shader); release(c->pixel_shader); release(c->sampler); release(c->constants);
    release(c->target_view); release(c->target);
    release(c->winrt_device); release(c->item_factory); release(c->pool_factory);
    av_buffer_unref(&c->frames_ref); av_buffer_unref(&c->device_ref);
    if (c->ro_initialized) { RoUninitialize(); }
    free(c);
}
AVBufferRef *sc_desktop_capture_ref_frames(struct sc_desktop_capture *c) { return av_buffer_ref(c->frames_ref); }
static bool create_shaders(struct sc_desktop_capture *c) {
    ID3DBlob *vs=NULL, *ps=NULL, *error=NULL;
    HRESULT hr=D3DCompile(shader_source, sizeof(shader_source)-1, "ez-monitor-affine", NULL, NULL,
        "vs", "vs_4_0", D3DCOMPILE_ENABLE_STRICTNESS|D3DCOMPILE_OPTIMIZATION_LEVEL3, 0, &vs, &error);
    if (FAILED(hr)) { goto end; }
    release(error); error=NULL;
    hr=D3DCompile(shader_source, sizeof(shader_source)-1, "ez-monitor-affine", NULL, NULL,
        "ps", "ps_4_0", D3DCOMPILE_ENABLE_STRICTNESS|D3DCOMPILE_OPTIMIZATION_LEVEL3, 0, &ps, &error);
    if (FAILED(hr)) { goto end; }
    hr=ID3D11Device_CreateVertexShader(c->device, ID3D10Blob_GetBufferPointer(vs), ID3D10Blob_GetBufferSize(vs), NULL, &c->vertex_shader);
    if (FAILED(hr)) { goto end; }
    hr=ID3D11Device_CreatePixelShader(c->device, ID3D10Blob_GetBufferPointer(ps), ID3D10Blob_GetBufferSize(ps), NULL, &c->pixel_shader);
end:
    if (FAILED(hr)) { LOGE("Perspective GPU shader: 0x%08lx %s", (unsigned long)hr,
        error ? (const char *)ID3D10Blob_GetBufferPointer(error) : ""); }
    release(vs); release(ps); release(error); return SUCCEEDED(hr);
}

struct sc_desktop_capture *sc_desktop_capture_create(unsigned width, unsigned height, unsigned fps) {
    struct sc_desktop_capture *c=calloc(1,sizeof(*c));
    if (!c) { return NULL; }
    c->width=width; c->height=height;
    HRESULT hr=RoInitialize(RO_INIT_MULTITHREADED);
    if (FAILED(hr)) { goto fail; }
    c->ro_initialized=true;
    c->borderless_allowed=request_borderless();
    c->item_iid=guid(L"{79c3f95b-31f7-4ec2-a464-632ef5d30760}");
    c->closable_iid=guid(L"{30d5a829-7fa4-4026-83bb-d75bae4ea99e}");
    c->dxgi_access_iid=guid(L"{a9b3d012-3df2-4ee3-b8d1-8695f457d3c1}");
    GUID item_factory=guid(L"{3628e81b-3cac-4c60-b7f4-23ce0e0c3356}");
    GUID pool_factory=guid(L"{589b103f-6bbc-5df5-a991-02e28b3b66d5}");
    hr=factory(L"Windows.Graphics.Capture.GraphicsCaptureItem",&item_factory,&c->item_factory); if (FAILED(hr)) { goto fail; }
    hr=factory(L"Windows.Graphics.Capture.Direct3D11CaptureFramePool",&pool_factory,&c->pool_factory); if (FAILED(hr)) { goto fail; }
    c->device_ref=av_hwdevice_ctx_alloc(AV_HWDEVICE_TYPE_D3D11VA);
    if (!c->device_ref) { hr=E_OUTOFMEMORY; goto fail; }
    AVHWDeviceContext *dc=(void *)c->device_ref->data; AVD3D11VADeviceContext *hw=dc->hwctx;
    const D3D_FEATURE_LEVEL levels[]={D3D_FEATURE_LEVEL_11_0};
    hr=D3D11CreateDevice(NULL,D3D_DRIVER_TYPE_HARDWARE,NULL,D3D11_CREATE_DEVICE_BGRA_SUPPORT|D3D11_CREATE_DEVICE_VIDEO_SUPPORT,
        levels,1,D3D11_SDK_VERSION,&hw->device,NULL,NULL);
    if (FAILED(hr)) { goto fail; }
    if (av_hwdevice_ctx_init(c->device_ref)<0) { hr=E_FAIL; goto fail; }
    c->device=hw->device; c->context=hw->device_context;
    GUID multithread_id=guid(L"{9b7e4e00-342c-4106-a19f-4f2704f689f0}"); void *multithread=NULL;
    if (SUCCEEDED(query(c->context,&multithread_id,&multithread))) {
        typedef BOOL (STDMETHODCALLTYPE *protect_fn)(void *,BOOL);
        METHOD(multithread,5,protect_fn)(multithread,TRUE); release(multithread);
    }
    IDXGIDevice *dxgi=NULL; GUID dxgi_id=guid(L"{54ec77fa-1377-44e6-8c32-88fd5f44c84c}");
    hr=query(c->device,&dxgi_id,(void **)&dxgi); if (FAILED(hr)) { goto fail; }
    typedef HRESULT (WINAPI *create_device_fn)(IDXGIDevice *,void **);
    create_device_fn create_device=(create_device_fn)(void *)GetProcAddress(GetModuleHandleW(L"d3d11.dll"),"CreateDirect3D11DeviceFromDXGIDevice");
    hr=create_device ? create_device(dxgi,&c->winrt_device) : E_NOINTERFACE; release(dxgi); if (FAILED(hr)) { goto fail; }
    c->frames_ref=av_hwframe_ctx_alloc(c->device_ref);
    if (!c->frames_ref) { hr=E_OUTOFMEMORY; goto fail; }
    AVHWFramesContext *frames=(void *)c->frames_ref->data;
    frames->format=AV_PIX_FMT_D3D11; frames->sw_format=AV_PIX_FMT_NV12;
    frames->width=width; frames->height=height; frames->initial_pool_size=0; // NV12 render-target arrays are not supported by every driver.
    ((AVD3D11VAFramesContext *)frames->hwctx)->BindFlags=D3D11_BIND_RENDER_TARGET;
    if (av_hwframe_ctx_init(c->frames_ref)<0) { hr=E_FAIL; goto fail; }
    D3D11_TEXTURE2D_DESC td={0}; td.Width=width; td.Height=height; td.MipLevels=1; td.ArraySize=1;
    td.Format=DXGI_FORMAT_B8G8R8A8_UNORM; td.SampleDesc.Count=1; td.Usage=D3D11_USAGE_DEFAULT;
    td.BindFlags=D3D11_BIND_RENDER_TARGET|D3D11_BIND_SHADER_RESOURCE;
    hr=ID3D11Device_CreateTexture2D(c->device,&td,NULL,&c->target); if (FAILED(hr)) { goto fail; }
    hr=ID3D11Device_CreateRenderTargetView(c->device,(ID3D11Resource *)c->target,NULL,&c->target_view);
    if (FAILED(hr)) { goto fail; }
    if (!create_shaders(c)) { hr=E_FAIL; goto fail; }
    D3D11_SAMPLER_DESC sd={0}; sd.Filter=D3D11_FILTER_MIN_MAG_MIP_LINEAR;
    sd.AddressU=sd.AddressV=sd.AddressW=D3D11_TEXTURE_ADDRESS_CLAMP; sd.MaxLOD=D3D11_FLOAT32_MAX;
    hr=ID3D11Device_CreateSamplerState(c->device,&sd,&c->sampler); if (FAILED(hr)) { goto fail; }
    D3D11_BUFFER_DESC bd={0}; bd.ByteWidth=sizeof(struct render_constants); bd.Usage=D3D11_USAGE_DEFAULT; bd.BindFlags=D3D11_BIND_CONSTANT_BUFFER;
    hr=ID3D11Device_CreateBuffer(c->device,&bd,NULL,&c->constants); if (FAILED(hr)) { goto fail; }
    GUID video_device_id=guid(L"{10ec4d5b-975a-4689-b9e4-d0aac30fe333}");
    GUID video_context_id=guid(L"{61f21c45-3c0e-4a74-9cea-67100d9ad5e4}");
    hr=query(c->device,&video_device_id,(void **)&c->video_device); if (FAILED(hr)) { goto fail; }
    hr=query(c->context,&video_context_id,(void **)&c->video_context); if (FAILED(hr)) { goto fail; }
    D3D11_VIDEO_PROCESSOR_CONTENT_DESC vd={0}; vd.InputFrameFormat=D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE;
    vd.InputFrameRate.Numerator=vd.OutputFrameRate.Numerator=fps; vd.InputFrameRate.Denominator=vd.OutputFrameRate.Denominator=1;
    vd.InputWidth=vd.OutputWidth=width; vd.InputHeight=vd.OutputHeight=height; vd.Usage=D3D11_VIDEO_USAGE_PLAYBACK_NORMAL;
    hr=ID3D11VideoDevice_CreateVideoProcessorEnumerator(c->video_device,&vd,&c->enumerator); if (FAILED(hr)) { goto fail; }
    hr=ID3D11VideoDevice_CreateVideoProcessor(c->video_device,c->enumerator,0,&c->processor); if (FAILED(hr)) { goto fail; }
    D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC iv={0}; iv.ViewDimension=D3D11_VPIV_DIMENSION_TEXTURE2D;
    hr=ID3D11VideoDevice_CreateVideoProcessorInputView(c->video_device,(ID3D11Resource *)c->target,c->enumerator,&iv,&c->input_view);
    if (FAILED(hr)) { goto fail; }
    D3D11_VIDEO_PROCESSOR_COLOR_SPACE in={0},out={0};
    in.Nominal_Range=D3D11_VIDEO_PROCESSOR_NOMINAL_RANGE_0_255;
    out.YCbCr_Matrix=1; out.Nominal_Range=D3D11_VIDEO_PROCESSOR_NOMINAL_RANGE_16_235;
    ID3D11VideoContext_VideoProcessorSetStreamColorSpace(c->video_context,c->processor,0,&in);
    ID3D11VideoContext_VideoProcessorSetOutputColorSpace(c->video_context,c->processor,&out);
    ID3D11VideoContext_VideoProcessorSetStreamAutoProcessingMode(c->video_context,c->processor,0,FALSE);
    ID3D11VideoContext_VideoProcessorSetStreamFrameFormat(c->video_context,c->processor,0,D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE);
    RECT full={0,0,width,height};
    ID3D11VideoContext_VideoProcessorSetStreamSourceRect(c->video_context,c->processor,0,TRUE,&full);
    ID3D11VideoContext_VideoProcessorSetStreamDestRect(c->video_context,c->processor,0,TRUE,&full);
    ID3D11VideoContext_VideoProcessorSetOutputTargetRect(c->video_context,c->processor,TRUE,&full);
    LOGI("Perspective capture: monitor WGC, GPU affine sampling and NV12 conversion"); return c;
fail:
    LOGE("Perspective GPU capture initialization failed: 0x%08lx",(unsigned long)hr);
    sc_desktop_capture_destroy(c); return NULL;
}

static bool open_source(struct sc_desktop_capture *c,struct monitor_source *s,HMONITOR monitor) {
    HRESULT hr=METHOD(c->item_factory,4,method_monitor)(c->item_factory,monitor,&c->item_iid,&s->item);
    if (FAILED(hr)) { goto fail; }
    hr=METHOD(s->item,7,method_size)(s->item,&s->size);
    if (FAILED(hr) || s->size.width<1 || s->size.height<1) { goto fail; }
    hr=METHOD(c->pool_factory,6,method_create)(c->pool_factory,c->winrt_device,87,2,s->size,&s->pool); if (FAILED(hr)) { goto fail; }
    hr=METHOD(s->pool,10,method_session)(s->pool,s->item,&s->session); if (FAILED(hr)) { goto fail; }
    void *option=NULL; GUID cursor=guid(L"{2c39ae40-7d2e-5044-804e-8b6799d4cf9e}");
    if (SUCCEEDED(query(s->session,&cursor,&option))) { METHOD(option,7,method_bool)(option,0); release(option); }
    GUID border=guid(L"{f2cdd966-22ae-5ea1-9596-3a289344c3be}"); option=NULL;
    if (c->borderless_allowed && SUCCEEDED(query(s->session,&border,&option))) {
        HRESULT border_hr=METHOD(option,7,method_bool)(option,0);
        LOGI("Perspective capture border disabled: 0x%08lx",(unsigned long)border_hr);
        release(option);
    }
    hr=METHOD(s->session,6,method0)(s->session); if (FAILED(hr)) { goto fail; }
    s->monitor=monitor; return true;
fail:
    LOGW("Perspective monitor capture unavailable: 0x%08lx",(unsigned long)hr);
    close_source(c,s); s->monitor=monitor; s->retry_after=GetTickCount64()+2000; return false;
}
static bool update_source(struct sc_desktop_capture *c,struct monitor_source *s) {
    void *frame=NULL,*surface=NULL,*access=NULL; ID3D11Texture2D *texture=NULL;
    HRESULT hr=METHOD(s->pool,7,method_out)(s->pool,&frame);
    if (FAILED(hr)) {
        // A closed pool must be recreated, for example after a display topology
        // change. Keep retrying at a bounded rate rather than freezing forever.
        HMONITOR monitor=s->monitor;
        close_source(c,s); s->monitor=monitor; s->retry_after=GetTickCount64()+2000;
        LOGW("Perspective monitor capture reset: 0x%08lx",(unsigned long)hr);
        return false;
    }
    if (!frame) { return s->ready; }
    for (int i=0;i<2;++i) {
        void *next=NULL; if (FAILED(METHOD(s->pool,7,method_out)(s->pool,&next)) || !next) { break; }
        close_object(c,frame); release(frame); frame=next;
    }
    capture_size size={0}; METHOD(frame,8,method_size)(frame,&size);
    if (size.width!=s->size.width || size.height!=s->size.height) {
        close_object(c,frame); release(frame); s->ready=false;
        if (size.width>0 && size.height>0 && SUCCEEDED(METHOD(s->pool,6,method_recreate)(s->pool,c->winrt_device,87,2,size))) { s->size=size; }
        return false;
    }
    hr=METHOD(frame,6,method_out)(frame,&surface); if (FAILED(hr)) { goto end; }
    hr=query(surface,&c->dxgi_access_iid,&access); if (FAILED(hr)) { goto end; }
    GUID texture_id=guid(L"{6f15aaf2-d208-4e89-9ab4-489535d34f9c}");
    hr=METHOD(access,3,method_interface)(access,&texture_id,(void **)&texture); if (FAILED(hr)) { goto end; }
    D3D11_TEXTURE2D_DESC td; ID3D11Texture2D_GetDesc(texture,&td);
    if (!s->texture || !s->view || td.Width!=s->width || td.Height!=s->height) {
        release(s->view); s->view=NULL; release(s->texture); s->texture=NULL; s->ready=false;
        td.Usage=D3D11_USAGE_DEFAULT; td.BindFlags=D3D11_BIND_SHADER_RESOURCE; td.CPUAccessFlags=0; td.MiscFlags=0;
        hr=ID3D11Device_CreateTexture2D(c->device,&td,NULL,&s->texture); if (FAILED(hr)) { goto end; }
        hr=ID3D11Device_CreateShaderResourceView(c->device,(ID3D11Resource *)s->texture,NULL,&s->view); if (FAILED(hr)) { goto end; }
        s->width=td.Width; s->height=td.Height;
    }
    ID3D11DeviceContext_CopyResource(c->context,(ID3D11Resource *)s->texture,(ID3D11Resource *)texture); s->ready=true;
end:
    release(texture); release(access); release(surface); close_object(c,frame); release(frame); return SUCCEEDED(hr) && s->ready;
}

struct draw_monitors { struct sc_desktop_capture *c; RECT bounds; struct render_constants constants; };
static BOOL CALLBACK draw_monitor(HMONITOR monitor,HDC dc,LPRECT bounds,LPARAM userdata) {
    (void)dc; struct draw_monitors *draw=(void *)userdata; RECT intersection;
    if (!IntersectRect(&intersection,bounds,&draw->bounds)) { return TRUE; }
    struct sc_desktop_capture *c=draw->c; struct monitor_source *s=NULL;
    for (int i=0;i<SC_MAX_CAPTURE_MONITORS;++i) { if (c->sources[i].monitor==monitor) { s=&c->sources[i]; break; } }
    if (!s) { for (int i=0;i<SC_MAX_CAPTURE_MONITORS;++i) { if (!c->sources[i].monitor) { s=&c->sources[i]; break; } } }
    if (!s) { return TRUE; }
    if (!s->pool && GetTickCount64()>=s->retry_after) { open_source(c,s,monitor); }
    if (!s->pool || !update_source(c,s)) { return TRUE; }
    struct render_constants constants=draw->constants;
    constants.monitor[0]=bounds->left; constants.monitor[1]=bounds->top;
    constants.monitor[2]=1.f/(bounds->right-bounds->left); constants.monitor[3]=1.f/(bounds->bottom-bounds->top);
    ID3D11DeviceContext_UpdateSubresource(c->context,(ID3D11Resource *)c->constants,0,NULL,&constants,0,0);
    ID3D11DeviceContext_PSSetShaderResources(c->context,0,1,&s->view); ID3D11DeviceContext_Draw(c->context,3,0);
    ID3D11ShaderResourceView *empty=NULL; ID3D11DeviceContext_PSSetShaderResources(c->context,0,1,&empty); return TRUE;
}
bool sc_desktop_capture_render(struct sc_desktop_capture *c,AVFrame *frame,
        double x,double y,double width,double height,const double m[6],
        unsigned mode,unsigned key_channel) {
    if (!c || FAILED(ID3D11Device_GetDeviceRemovedReason(c->device))) { return false; }
    av_frame_unref(frame); if (av_hwframe_get_buffer(c->frames_ref,frame,0)<0) { return false; }
    ID3D11DeviceContext_OMSetRenderTargets(c->context,1,&c->target_view,NULL);
    const float black[4]={0,0,0,1}; ID3D11DeviceContext_ClearRenderTargetView(c->context,c->target_view,black);
    D3D11_VIEWPORT viewport={0,0,c->width,c->height,0,1}; ID3D11DeviceContext_RSSetViewports(c->context,1,&viewport);
    ID3D11DeviceContext_IASetInputLayout(c->context,NULL);
    ID3D11DeviceContext_IASetPrimitiveTopology(c->context,D3D11_PRIMITIVE_TOPOLOGY_TRIANGLELIST);
    ID3D11DeviceContext_VSSetShader(c->context,c->vertex_shader,NULL,0); ID3D11DeviceContext_PSSetShader(c->context,c->pixel_shader,NULL,0);
    ID3D11DeviceContext_PSSetSamplers(c->context,0,1,&c->sampler); ID3D11DeviceContext_PSSetConstantBuffers(c->context,0,1,&c->constants);
    struct draw_monitors draw={0}; draw.c=c;
    draw.constants.row_x[0]=m[0]*width; draw.constants.row_x[1]=m[1]*width; draw.constants.row_x[2]=x+m[4]*width;
    draw.constants.row_y[0]=m[2]*height; draw.constants.row_y[1]=m[3]*height; draw.constants.row_y[2]=y+m[5]*height;
    draw.constants.options[0]=c->width; draw.constants.options[1]=c->height; draw.constants.options[2]=mode;
    draw.constants.options[3]=(float)key_channel;
    draw.constants.clip[0]=x;draw.constants.clip[1]=y;draw.constants.clip[2]=width;draw.constants.clip[3]=height;
    double xs[4]={x+m[4]*width,x+(m[4]+m[0])*width,x+(m[4]+m[1])*width,x+(m[4]+m[0]+m[1])*width};
    double ys[4]={y+m[5]*height,y+(m[5]+m[2])*height,y+(m[5]+m[3])*height,y+(m[5]+m[2]+m[3])*height};
    double left=xs[0],right=xs[0],top=ys[0],bottom=ys[0];
    for (int i=1;i<4;++i) { left=fmin(left,xs[i]);right=fmax(right,xs[i]);top=fmin(top,ys[i]);bottom=fmax(bottom,ys[i]); }
    draw.bounds=(RECT){(LONG)floor(left),(LONG)floor(top),(LONG)ceil(right),(LONG)ceil(bottom)};
    RECT visible={(LONG)floor(x),(LONG)floor(y),(LONG)ceil(x+width),(LONG)ceil(y+height)};
    IntersectRect(&draw.bounds,&draw.bounds,&visible);
    if (mode==0) { EnumDisplayMonitors(NULL,NULL,draw_monitor,(LPARAM)&draw); }
    if (mode != 0 && mode != 2) {
        ID3D11DeviceContext_UpdateSubresource(c->context,(ID3D11Resource *)c->constants,0,NULL,&draw.constants,0,0);
        ID3D11DeviceContext_Draw(c->context,3,0);
    }
    ID3D11DeviceContext_OMSetRenderTargets(c->context,0,NULL,NULL);
    D3D11_VIDEO_PROCESSOR_OUTPUT_VIEW_DESC ov={0}; ov.ViewDimension=D3D11_VPOV_DIMENSION_TEXTURE2D;
    ov.Texture2D.MipSlice=0;
    ID3D11VideoProcessorOutputView *output_view=NULL;
    HRESULT hr=ID3D11VideoDevice_CreateVideoProcessorOutputView(c->video_device,(ID3D11Resource *)frame->data[0],c->enumerator,&ov,&output_view);
    if (FAILED(hr)) { LOGE("Perspective NV12 output view: 0x%08lx",(unsigned long)hr); return false; }
    D3D11_VIDEO_PROCESSOR_STREAM stream={0}; stream.Enable=TRUE; stream.pInputSurface=c->input_view;
    hr=ID3D11VideoContext_VideoProcessorBlt(c->video_context,c->processor,output_view,0,1,&stream);
    release(output_view); ID3D11DeviceContext_Flush(c->context);
    frame->color_range=AVCOL_RANGE_MPEG; frame->colorspace=AVCOL_SPC_BT709;
    frame->color_primaries=AVCOL_PRI_BT709; frame->color_trc=AVCOL_TRC_BT709;
    if (FAILED(hr)) { LOGE("Perspective NV12 conversion: 0x%08lx",(unsigned long)hr); }
    return SUCCEEDED(hr);
}

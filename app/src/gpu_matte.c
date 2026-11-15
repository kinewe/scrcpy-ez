#include "gpu_matte.h"
#include <d3dcompiler.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "perspective_key.h"
/* Compute writes a separate RGBA texture; graphics state is preserved.
 *
 * Inverse6: the normal single-pass composition (below) is untouched. For the
 * inverse experiment the same source is compiled once more with INVERSE_VIDEO:
 * that variant additionally writes the keyed UI result V to a second UAV so a
 * temporal learner can model V's own static background and a compositor can
 * add only the (V - model) dynamic increment on top of native pixels. No
 * color/channel classification is involved, so it is not tied to one app. */
static const char shader[]=
 "Texture2D<float4> image:register(t0);Texture2D<float4> reference:register(t1);Texture2D<float4> nativeUi:register(t2);Texture2D<float4> nativeMotion:register(t3);RWTexture2D<float4> result:register(u0);\n"
 "#ifdef INVERSE_VIDEO\n"
 "RWTexture2D<float4> inverseVideo:register(u1);\n"
 "#endif\n"
 "cbuffer Params:register(b0){float4 rowU;float4 rowV;float4 background;float4 camera;float4 flat;float4 badges[16];float4 plate[512];float4 referenceOptions;float4 nativeRegions[32];}"
 "float plane(int2 p){p=clamp(p,int2(0,0),int2(31,63));uint i=p.y*32+p.x;return plate[i/4][i%4];}"
 "float2 plate_sample(float2 pixel,float2 size){"
 "float2 gp=(pixel+.5)/size*float2(32,64)-.5;int2 cell=(int2)floor(gp);float2 f=frac(gp);"
 "float4 v=float4(plane(cell),plane(cell+int2(1,0)),plane(cell+int2(0,1)),plane(cell+1));"
 "float4 weights=float4((1-f.x)*(1-f.y),f.x*(1-f.y),(1-f.x)*f.y,f.x*f.y)*step(.001,v);"
 "float confidence=dot(weights,1);"
 "float lo=min(min(v.x,v.y),min(v.z,v.w)),hi=max(max(v.x,v.y),max(v.z,v.w));"
 "return float2(confidence>.5?dot(v,weights)/confidence:0,lo>=.94&&hi-lo<=.025?1:0);}"
 "float transmission(float3 rgb){float d=referenceOptions.y<.5?rgb.r-min(rgb.g,rgb.b):"
 "(referenceOptions.y<1.5?rgb.g-min(rgb.r,rgb.b):rgb.b-min(rgb.r,rgb.g));return saturate(d*background.w);}"
 "float max3(float3 rgb){return max(rgb.r,max(rgb.g,rgb.b));}"
 "float min3(float3 rgb){return min(rgb.r,min(rgb.g,rgb.b));}"
 "float other_difference(float3 rgb){return abs(referenceOptions.y<.5?rgb.g-rgb.b:"
 "(referenceOptions.y<1.5?rgb.r-rgb.b:rgb.r-rgb.g));}"
 "float white_stroke(float3 rgb){float low=min3(rgb);"
 "return smoothstep(.60,.86,low)*(1-smoothstep(.10,.24,max3(rgb)-low));}"
 "[numthreads(16,16,1)]void main(uint3 id:SV_DispatchThreadID){"
 "uint w,h;result.GetDimensions(w,h);if(id.x>=w||id.y>=h)return;"
 "float4 c=image[id.xy];float2 pixel=float2(id.xy);"
 "for(int i=0;i<(int)rowU.w;i++){float4 b=badges[i*2];float r=badges[i*2+1].x;"
 "float2 d=pixel-b.xy;if(dot(d,d)<=r*r){int x=clamp((int)(b.x+r+8),0,(int)w-1);c=image[int2(x,id.y)];}}"
 "for(int j=0;j<(int)rowU.w;j++){float4 b=badges[j*2];float r=badges[j*2+1].x;"
 "float2 d=pixel-b.zw;if(dot(d,d)<=r*r){int2 source=int2(round(b.xy+d));"
 "if(all(source>=0)&&source.x<(int)w&&source.y<(int)h)c=image[source];}}"
 "float2 q=float2(id.xy)/float2(w,h);"
 "float2 uv=float2(dot(rowU.xy,q)+rowU.z,dot(rowV.xy,q)+rowV.z);"
 /* Affine coefficients can land exactly on a camera edge. Allow <.024 pixel
  * at 2400 px to make CPU/GPU fused arithmetic choose the same edge. */
 "bool outside=rowV.w>.5?(any(q<camera.xy)||any(q>camera.xy+camera.zw)):(any(uv<-.00001)||any(uv>1.00001));"
 "if(outside){\n"
 "#ifdef INVERSE_VIDEO\n"
 /* Outside the camera region the keyed video path carries no live overlay. */
 "inverseVideo[id.xy]=float4(0,0,0,0);\n"
 "#endif\n"
 "float4 o=referenceOptions.z>1.5?nativeUi[id.xy]:float4(c.rgb,1);if(referenceOptions.z>.5){for(int r=0;r<(int)referenceOptions.w;r++){float4 rect=nativeRegions[r];"
 "if(all(q>=rect.xy)&&all(q<rect.xy+rect.zw))o=nativeUi[id.xy];}}result[id.xy]=o;return;}"
 /* A multi-background reference can disambiguate opaque blue UI from blue
  * camera backing. Only use it while the observed pixel still agrees with
  * that static UI; moving scan effects and changed controls use the live path. */
 "float4 model=0;float referenceWeight=0;if(referenceOptions.x>.5){model=reference[id.xy];"
 "float3 expected=model.rgb+(1-model.a)*background.rgb;float3 error=abs(c.rgb-expected);"
 "if(all(model.rgb<=model.a+.004)){referenceWeight=1-smoothstep(.015,.06,max(error.r,max(error.g,error.b)));"
 "model.a=max(model.a,1.0/255);}}"
 "float t=transmission(c.rgb);"
 "float3 foreground=saturate(c.rgb-t*background.rgb);"
 /* Average only neighboring pixels that could be the same dimmed backing.
  * Keep chromatic and bright details, and avoid crossing the camera boundary. */
 "if(flat.w<0){"
 "float neutral=1-smoothstep(.012,.045,max(foreground.r,max(foreground.g,foreground.b)));"
 "if(neutral>0){"
 "float sum=t*2,weight=2;"
 "int2 offsets[4]={int2(-2,0),int2(2,0),int2(0,-2),int2(0,2)};"
 "[unroll]for(int n=0;n<4;n++){int2 xy=int2(id.xy)+offsets[n];"
 "float2 nq=float2(xy)/float2(w,h);"
 "if(all(xy>=0)&&xy.x<(int)w&&xy.y<(int)h&&all(nq>=camera.xy)&&all(nq<=camera.xy+camera.zw)){"
 "float3 rgb=image[xy].rgb;float nt=transmission(rgb);float3 delta=abs(rgb-c.rgb);"
 "float k=1-smoothstep(.012,.035,max(abs(nt-t),max(delta.r,max(delta.g,delta.b))));"
 "sum+=k*nt;weight+=k;}}"
 "float local=lerp(t,sum/weight,neutral);"
 /* Trust the spatial reference only close to the live sample, with a soft
  * transition so moving scrims and scan edges retain their own opacity. */
 "if(flat.w< -1.5){float2 plateRef=plate_sample(pixel,float2(w,h));float level=plateRef.x;"
 "float distance=max(abs(level-t),abs(level-local));"
 /* On a measured clear plane, allow the same small number of decoded color
  * steps for each key strength. A weak key otherwise magnifies those steps. */
 "float lo=.016,hi=.045;if(plateRef.y>.5){float quantum=background.w/255;"
 "lo=max(lo,min(.04,3*quantum));hi=max(hi,min(.08,6*quantum));}"
 "float strength=level>0?neutral*(1-smoothstep(lo,hi,distance)):0;"
 "local=lerp(local,level,strength);foreground*=1-strength*plateRef.y;}t=local;}"
 "}else{"
 /* Stabilize only near-black key-plane pixels. White/green scan graphics and
  * native controls keep their original alpha and detail. */
 "float distance=1;float level=t;for(int k=0;k<(int)flat.w;k++){float d=abs(t-flat[k]);if(d<distance){distance=d;level=flat[k];}}"
 "float spatial=plate_sample(pixel,float2(w,h)).x;if(spatial>0){level=spatial;distance=abs(t-level);}"
 "float strength=(1-smoothstep(.012,.045,max(foreground.r,max(foreground.g,foreground.b))))*(1-smoothstep(.022,.06,distance));"
 "t=lerp(t,level,strength);foreground*=1-strength;"
 "}"
 "float a=1-t;if(flat.w<0){"
 "float clean=1-smoothstep(.012,.045,max(foreground.r,max(foreground.g,foreground.b)));"
 "float knee=smoothstep(1.0/255,6.0/255,a);"
 "a=lerp(a,lerp(1.0/255,a,knee),clean);"
 "}else if(round(a*255)<4)a=1.0/255;"
 "float3 color=min(foreground,a.xxx);\n"
 /* Subsampled chroma can put the backing color on the wrong side of a thin
  * white edge. Recover that edge from luminance where an adjacent white
  * stroke and the observed color both support it. Give the edge a small dark
  * underlay so the stroke still reads against a bright desktop. */
 "#ifndef INVERSE_VIDEO\n"
 "if(flat.w<0&&rowV.w>.5&&rowU.w==0&&referenceOptions.x<.5&&referenceOptions.z<.5"
 "&&a<.98&&min3(c.rgb)>min3(background.rgb)+.016&&max3(foreground)-min3(foreground)<.19){"
 "float support=0;int radius=min(w,h)>=1152?2:1;"
 "int2 offsets[4]={int2(-1,0),int2(1,0),int2(0,-1),int2(0,1)};"
 "[unroll]for(int n=0;n<4;n++){int2 p=int2(id.xy)+radius*offsets[n];"
 "float2 pos=float2(p)/float2(w,h);"
 "if(all(p>=0)&&p.x<(int)w&&p.y<(int)h&&all(pos>=camera.xy)&&all(pos<=camera.xy+camera.zw))"
 "support=max(support,white_stroke(image[p].rgb));}"
 "if(support>0){"
 "float level=flat.w< -1.5?plate_sample(pixel,float2(w,h)).x:1;"
 "float3 base=background.rgb*(level>0?level:1);"
 "float3 luma=float3(.2126,.7152,.0722);float y0=dot(base,luma);"
 "float edge=saturate((dot(c.rgb,luma)-y0)/max(.01,1-y0));"
 "float3 predicted=lerp(base,float3(1,1,1),edge);"
 "float3 error=predicted-c.rgb;"
 "float strength=support*smoothstep(.02,.07,edge)*(1-smoothstep(.025,.06,other_difference(error)))"
 "*(1-smoothstep(.04,.14,max3(abs(error))));"
 "float repair=strength*(1-smoothstep(.74,.98,a));"
 "color=lerp(color,edge.xxx,repair);a=lerp(a,edge,repair);"
 "a+=(1-a)*.27*strength;}}\n"
 "#endif\n"
 "float4 output=lerp(float4(color,a),model,referenceWeight);\n"
 "#ifdef INVERSE_VIDEO\n"
 /* The inverse composite pass consumes the raw keyed result V (premultiplied
  * UI over transparent) and decides generically which part is dynamic. */
 "inverseVideo[id.xy]=output;result[id.xy]=output;return;\n"
 "#endif\n"
 "if(referenceOptions.z>.5){float4 n=nativeUi[id.xy];float3 expected=n.rgb+(1-n.a)*background.rgb;float3 difference=abs(c.rgb-expected);"
 "float weight=1-smoothstep(.018,.055,max(difference.r,max(difference.g,difference.b)));"
 "for(int r=0;r<(int)referenceOptions.w;r++){float4 rect=nativeRegions[r];if(all(q>=rect.xy)&&all(q<rect.xy+rect.zw))weight=1;}"
 "output=lerp(output,n,weight);}result[id.xy]=output;} ";
/* Inverse6 only: temporal background model of V plus the difference-layer
 * compositor. Both entry points share the same cbuffer layout as the main
 * shader, so no extra parameter plumbing is needed for camera geometry. */
static const char inverse_shader[]=
 "Texture2D<float4> video:register(t0);Texture2D<float4> model:register(t1);Texture2D<float4> nativeUi:register(t2);Texture2D<float4> nativeMotion:register(t3);RWTexture2D<float4> output:register(u0);"
 "cbuffer Params:register(b0){float4 rowU;float4 rowV;float4 background;float4 camera;float4 flat;float4 badges[16];float4 plate[512];float4 referenceOptions;float4 nativeRegions[32];}"
 "cbuffer Inverse:register(b1){float4 options;}"
 "float kernel(int k){return k==0?6:(abs(k)==1?4:1);}"
 /* Learn V without its animated component: the least opaque sample is the
  * static background because the overlay only ever adds alpha/color. A small
  * spatial pre-average suppresses per-pixel codec noise before the minimum. */
 "[numthreads(16,16,1)]void learn(uint3 id:SV_DispatchThreadID){"
 "uint w,h;output.GetDimensions(w,h);if(id.x>=w||id.y>=h)return;"
 "float4 v=0;[unroll]for(int dy=-1;dy<=1;dy++){[unroll]for(int dx=-1;dx<=1;dx++){"
 "int2 p=clamp(int2(id.xy)+int2(dx,dy),int2(0,0),int2((int)w-1,(int)h-1));v+=video[p];}}v*=1.0/9.0;"
 "if(options.x>.5){output[id.xy]=v;return;}"
 "float4 m=model[id.xy];float4 o=m;"
 "if(v.a<m.a-0.5/255)o=v;else if(v.a>m.a+0.5/255)o=m;else o=float4(min(v.rgb,m.rgb),m.a);"
 "output[id.xy]=o;}"
 /* Live: native pixels are authoritative; only the video increment over its
  * learned background is added back, with 5x5 spatial support so per-pixel
  * codec noise cannot dial the blend up and down (the inverse2-4 grain). */
 "[numthreads(16,16,1)]void compose(uint3 id:SV_DispatchThreadID){"
 "uint w,h;output.GetDimensions(w,h);if(id.x>=w||id.y>=h)return;"
 "float2 q=float2(id.xy)/float2(w,h);"
 "float2 uv=float2(dot(rowU.xy,q)+rowU.z,dot(rowV.xy,q)+rowV.z);"
 "bool outside=rowV.w>.5?(any(q<camera.xy)||any(q>camera.xy+camera.zw)):(any(uv<-.00001)||any(uv>1.00001));"
 "float4 n=nativeUi[id.xy];if(outside){output[id.xy]=n;return;}"
 "float4 v=video[id.xy];float4 m=model[id.xy];float4 diff=max(v-m,0);"
 /* Color-only residual suppression: below ~3/255 added alpha the difference is
  * pure codec/keying chroma noise, which otherwise paints a fixed-position blue
  * ghost that survives the output clamp. Real halo/body pixels keep their color. */
 "if(diff.a<.012)diff.rgb=0;"
 "float support=0;[unroll]for(int dy=-2;dy<=2;dy++){[unroll]for(int dx=-2;dx<=2;dx++){"
 "int2 p=clamp(int2(id.xy)+int2(dx,dy),int2(0,0),int2((int)w-1,(int)h-1));"
 "float4 vv=max(video[p]-model[p],0);"
 "support+=max(max(vv.r,vv.g),max(vv.b,vv.a))*kernel(dx)*kernel(dy);}}"
 "float active=smoothstep(options.x,options.y,support/256)*step(.5,nativeMotion[id.xy].r);"
 "float4 o=saturate(n+diff*active);o.rgb=min(o.rgb,o.a.xxx);"
 "output[id.xy]=o;}";
#define EZ_INVERSE_LEARN_FRAMES 32
#define EZ_INVERSE_LO .0035f
#define EZ_INVERSE_HI .015f
struct stamp {ID3D11Query *disjoint,*begin,*end;bool pending;};
struct gpu_matte {ID3D11DeviceContext *context;ID3D11ComputeShader *shader;
 ID3D11Texture2D *texture;ID3D11UnorderedAccessView *uav;ID3D11Buffer *constants;
 ID3D11Texture2D *reference_texture;ID3D11ShaderResourceView *reference_view;
 ID3D11Texture2D *native_texture;ID3D11ShaderResourceView *native_view;float native_regions[32][4];unsigned native_region_count;
 ID3D11Texture2D *motion_texture;ID3D11ShaderResourceView *motion_view;
 ID3D11ComputeShader *shader_inverse,*learn_shader,*compose_shader;
 ID3D11Texture2D *video_texture;ID3D11ShaderResourceView *video_view;ID3D11UnorderedAccessView *video_uav;
 ID3D11Texture2D *model_texture[2];ID3D11ShaderResourceView *model_view[2];ID3D11UnorderedAccessView *model_uav[2];
 ID3D11Buffer *inverse_options;
 int inverse_state;bool inverse_ready,inverse_learn_first;unsigned inverse_frames,model_index;
 unsigned w,h,next,key_channel;struct stamp stamps[4];double last_us;
 unsigned badge_count;struct ez_badge badges[EZ_MAX_BADGES];
 bool have_camera_region;float camera_region[4];float key_levels[3];unsigned key_level_count;
 bool soft_key,have_key_plate;float key_plate[EZ_KEY_PLATE_SIZE];};
static void rel(void *p){if(p)IUnknown_Release((IUnknown*)p);}
void gpu_matte_destroy(struct gpu_matte *m){
 if(!m)return;
 for(unsigned i=0;i<4;++i){rel(m->stamps[i].disjoint);rel(m->stamps[i].begin);rel(m->stamps[i].end);}
 rel(m->shader_inverse);rel(m->learn_shader);rel(m->compose_shader);
 rel(m->video_view);rel(m->video_uav);rel(m->video_texture);
 for(unsigned i=0;i<2;++i){rel(m->model_view[i]);rel(m->model_uav[i]);rel(m->model_texture[i]);}
 rel(m->inverse_options);
 rel(m->motion_view);rel(m->motion_texture);rel(m->native_view);rel(m->native_texture);rel(m->reference_view);rel(m->reference_texture);rel(m->constants);rel(m->uav);rel(m->texture);rel(m->shader);rel(m->context);free(m);
}
static ID3D11Texture2D *make_rgba_texture(struct gpu_matte *m,ID3D11ShaderResourceView **view,ID3D11UnorderedAccessView **uav){
 ID3D11Device *device=NULL;ID3D11Texture2D_GetDevice(m->texture,&device);if(!device)return NULL;
 D3D11_TEXTURE2D_DESC d={0};d.Width=m->w;d.Height=m->h;d.MipLevels=1;d.ArraySize=1;
 d.Format=DXGI_FORMAT_R8G8B8A8_UNORM;d.SampleDesc.Count=1;d.BindFlags=D3D11_BIND_SHADER_RESOURCE|D3D11_BIND_UNORDERED_ACCESS;
 ID3D11Texture2D *texture=NULL;HRESULT hr=ID3D11Device_CreateTexture2D(device,&d,NULL,&texture);
 if(SUCCEEDED(hr)&&view)hr=ID3D11Device_CreateShaderResourceView(device,(ID3D11Resource*)texture,NULL,view);
 if(SUCCEEDED(hr)&&uav)hr=ID3D11Device_CreateUnorderedAccessView(device,(ID3D11Resource*)texture,NULL,uav);
 ID3D11Device_Release(device);
 if(FAILED(hr)){rel(texture);if(view)rel(*view);if(uav)rel(*uav);if(view)*view=NULL;if(uav)*uav=NULL;return NULL;}
 return texture;
}
static ID3D11ComputeShader *compile_cs(struct gpu_matte *m,const char *source,size_t size,const char *entry,bool inverse_macro){
 ID3D11Device *device=NULL;ID3D11Texture2D_GetDevice(m->texture,&device);if(!device)return NULL;
 D3D10_SHADER_MACRO define[2]={{"INVERSE_VIDEO","1"},{NULL,NULL}};
 ID3DBlob *code=NULL,*errors=NULL;
 HRESULT hr=D3DCompile(source,size,"ez-local-matte",inverse_macro?define:NULL,NULL,entry,"cs_5_0",D3DCOMPILE_OPTIMIZATION_LEVEL3,0,&code,&errors);
 if(FAILED(hr)){fprintf(stderr,"MATTE_SHADER_FAILED %s 0x%lx %s\n",entry,(unsigned long)hr,errors?(char*)ID3D10Blob_GetBufferPointer(errors):"");rel(code);rel(errors);ID3D11Device_Release(device);return NULL;}
 ID3D11ComputeShader *shader=NULL;
 hr=ID3D11Device_CreateComputeShader(device,ID3D10Blob_GetBufferPointer(code),ID3D10Blob_GetBufferSize(code),NULL,&shader);
 rel(code);rel(errors);ID3D11Device_Release(device);
 if(FAILED(hr)){fprintf(stderr,"MATTE_SHADER_CREATE_FAILED %s 0x%lx\n",entry,(unsigned long)hr);return NULL;}
 return shader;
}
/* Created lazily: only the inverse experiment asks for the extra 3 textures,
 * so the production single-pass path keeps its memory and binding behavior. */
static bool inverse_create(struct gpu_matte *m){
 if(m->inverse_options)return true;
 ID3D11Device *device=NULL;ID3D11Texture2D_GetDevice(m->texture,&device);if(!device)return false;
 m->shader_inverse=compile_cs(m,shader,sizeof(shader)-1,"main",true);
 m->learn_shader=compile_cs(m,inverse_shader,sizeof(inverse_shader)-1,"learn",false);
 m->compose_shader=compile_cs(m,inverse_shader,sizeof(inverse_shader)-1,"compose",false);
 m->video_texture=make_rgba_texture(m,&m->video_view,&m->video_uav);
 for(unsigned i=0;i<2;++i)m->model_texture[i]=make_rgba_texture(m,&m->model_view[i],&m->model_uav[i]);
 D3D11_BUFFER_DESC bd={0};bd.ByteWidth=16;bd.Usage=D3D11_USAGE_DEFAULT;bd.BindFlags=D3D11_BIND_CONSTANT_BUFFER;
 HRESULT hr=ID3D11Device_CreateBuffer(device,&bd,NULL,&m->inverse_options);
 ID3D11Device_Release(device);
 if(!m->shader_inverse||!m->learn_shader||!m->compose_shader||!m->video_texture||!m->model_texture[0]||!m->model_texture[1]||FAILED(hr)){
  fprintf(stderr,"MATTE_INVERSE_INIT_FAILED\n");
  rel(m->shader_inverse);m->shader_inverse=NULL;rel(m->learn_shader);m->learn_shader=NULL;rel(m->compose_shader);m->compose_shader=NULL;
  rel(m->video_view);rel(m->video_uav);rel(m->video_texture);m->video_view=NULL;m->video_uav=NULL;m->video_texture=NULL;
  for(unsigned i=0;i<2;++i){rel(m->model_view[i]);rel(m->model_uav[i]);rel(m->model_texture[i]);m->model_view[i]=NULL;m->model_uav[i]=NULL;m->model_texture[i]=NULL;}
  rel(m->inverse_options);m->inverse_options=NULL;return false;
 }
 m->model_index=0;m->inverse_frames=0;m->inverse_ready=false;m->inverse_learn_first=true;
 fprintf(stderr,"MATTE_INVERSE_READY %ux%u\n",m->w,m->h);
 return true;
}
void gpu_matte_set_inverse_state(struct gpu_matte *m,int state){
 if(!m)return;
 if(state<0||state>EZ_GPU_MATTE_INVERSE_LIVE)state=EZ_GPU_MATTE_INVERSE_OFF;
 if(state!=EZ_GPU_MATTE_INVERSE_OFF&&!m->inverse_options&&!inverse_create(m))state=EZ_GPU_MATTE_INVERSE_OFF;
 m->inverse_state=state;
}
void gpu_matte_reset_video_model(struct gpu_matte *m){
 if(!m)return;
 m->inverse_ready=false;m->inverse_frames=0;m->inverse_learn_first=true;m->model_index=0;
}
bool gpu_matte_inverse_ready(struct gpu_matte *m){return m&&m->inverse_ready;}
unsigned gpu_matte_inverse_learned_frames(struct gpu_matte *m){return m?m->inverse_frames:0;}
static void inverse_learn_pass(struct gpu_matte *m){
 ID3D11DeviceContext *c=m->context;
 ID3D11ShaderResourceView *views[4]={m->video_view,m->model_view[m->model_index],NULL,NULL};
 ID3D11UnorderedAccessView *none[2]={NULL,NULL};
 ID3D11UnorderedAccessView *target=m->model_uav[m->model_index^1];
 ID3D11DeviceContext_CSSetUnorderedAccessViews(c,0,2,none,NULL);
 ID3D11DeviceContext_CSSetShaderResources(c,0,4,views);
 ID3D11DeviceContext_CSSetUnorderedAccessViews(c,0,1,&target,NULL);
 ID3D11DeviceContext_CSSetShader(c,m->learn_shader,NULL,0);
 float options[4]={m->inverse_learn_first?1.f:0.f,0,0,0};
 ID3D11DeviceContext_UpdateSubresource(c,(ID3D11Resource*)m->inverse_options,0,NULL,options,0,0);
 ID3D11DeviceContext_CSSetConstantBuffers(c,0,1,&m->constants);
 ID3D11DeviceContext_CSSetConstantBuffers(c,1,1,&m->inverse_options);
 ID3D11DeviceContext_Dispatch(c,(m->w+15)/16,(m->h+15)/16,1);
 m->model_index^=1;m->inverse_learn_first=false;++m->inverse_frames;
 if(m->inverse_frames>=EZ_INVERSE_LEARN_FRAMES)m->inverse_ready=true;
}
static void inverse_compose_pass(struct gpu_matte *m){
 ID3D11DeviceContext *c=m->context;
 ID3D11ShaderResourceView *views[4]={m->video_view,m->model_view[m->model_index],m->native_view,m->motion_view};
 ID3D11UnorderedAccessView *none[2]={NULL,NULL};
 ID3D11DeviceContext_CSSetUnorderedAccessViews(c,0,2,none,NULL);
 ID3D11UnorderedAccessView *target=m->uav;
 ID3D11DeviceContext_CSSetShaderResources(c,0,4,views);
 ID3D11DeviceContext_CSSetUnorderedAccessViews(c,0,1,&target,NULL);
 ID3D11DeviceContext_CSSetShader(c,m->compose_shader,NULL,0);
 float options[4]={EZ_INVERSE_LO,EZ_INVERSE_HI,0,0};
 ID3D11DeviceContext_UpdateSubresource(c,(ID3D11Resource*)m->inverse_options,0,NULL,options,0,0);
 ID3D11DeviceContext_CSSetConstantBuffers(c,0,1,&m->constants);
 ID3D11DeviceContext_CSSetConstantBuffers(c,1,1,&m->inverse_options);
 ID3D11DeviceContext_Dispatch(c,(m->w+15)/16,(m->h+15)/16,1);
}
struct gpu_matte *gpu_matte_create(ID3D11Device *device,unsigned w,unsigned h){
 struct gpu_matte *m=calloc(1,sizeof(*m));if(!m)return NULL;
 m->key_channel=sc_perspective_key_channel();
 m->w=w;m->h=h;m->last_us=-1;ID3D11Device_GetImmediateContext(device,&m->context);
 ID3DBlob *code=NULL,*errors=NULL;
 HRESULT hr=D3DCompile(shader,sizeof(shader)-1,"ez-local-matte",NULL,NULL,"main","cs_5_0",D3DCOMPILE_OPTIMIZATION_LEVEL3,0,&code,&errors);
 if(FAILED(hr)){fprintf(stderr,"MATTE_SHADER_FAILED 0x%lx %s\n",(unsigned long)hr,errors?(char*)ID3D10Blob_GetBufferPointer(errors):"");rel(code);rel(errors);goto fail;}
 hr=ID3D11Device_CreateComputeShader(device,ID3D10Blob_GetBufferPointer(code),ID3D10Blob_GetBufferSize(code),NULL,&m->shader);
 rel(code);rel(errors);if(FAILED(hr))goto fail;
 D3D11_TEXTURE2D_DESC td={0};td.Width=w;td.Height=h;td.MipLevels=1;td.ArraySize=1;
 td.Format=DXGI_FORMAT_R8G8B8A8_UNORM;td.SampleDesc.Count=1;td.BindFlags=D3D11_BIND_SHADER_RESOURCE|D3D11_BIND_UNORDERED_ACCESS;
 hr=ID3D11Device_CreateTexture2D(device,&td,NULL,&m->texture);if(FAILED(hr))goto fail;
 hr=ID3D11Device_CreateUnorderedAccessView(device,(ID3D11Resource*)m->texture,NULL,&m->uav);if(FAILED(hr))goto fail;
 D3D11_BUFFER_DESC bd={0};bd.ByteWidth=96+sizeof(m->badges)+sizeof(m->key_plate)+sizeof(m->native_regions);bd.Usage=D3D11_USAGE_DEFAULT;bd.BindFlags=D3D11_BIND_CONSTANT_BUFFER;
 hr=ID3D11Device_CreateBuffer(device,&bd,NULL,&m->constants);if(FAILED(hr))goto fail;
 for(unsigned i=0;i<4;++i){
   D3D11_QUERY_DESC q={D3D11_QUERY_TIMESTAMP_DISJOINT,0};
   if(FAILED(ID3D11Device_CreateQuery(device,&q,&m->stamps[i].disjoint)))goto fail;
   q.Query=D3D11_QUERY_TIMESTAMP;
   if(FAILED(ID3D11Device_CreateQuery(device,&q,&m->stamps[i].begin)))goto fail;
   if(FAILED(ID3D11Device_CreateQuery(device,&q,&m->stamps[i].end)))goto fail;
 }
 return m;
fail:fprintf(stderr,"MATTE_INIT_FAILED 0x%lx\n",(unsigned long)hr);gpu_matte_destroy(m);return NULL;
}
ID3D11Texture2D *gpu_matte_texture(struct gpu_matte *m){return m->texture;}
void gpu_matte_set_badges(struct gpu_matte *m,const struct ez_badge *badges,unsigned count){
 if(!m)return;
 m->badge_count=count<=EZ_MAX_BADGES?count:0;
 if(m->badge_count)memcpy(m->badges,badges,m->badge_count*sizeof(*badges));
}
void gpu_matte_set_camera_region(struct gpu_matte *m,const float region[4],bool valid){
 if(!m)return;m->have_camera_region=valid;
 if(valid)memcpy(m->camera_region,region,sizeof(m->camera_region));
}
void gpu_matte_set_key_levels(struct gpu_matte *m,const float levels[3],unsigned count){
 if(!m)return;m->key_level_count=count<=3?count:0;
 if(m->key_level_count)memcpy(m->key_levels,levels,m->key_level_count*sizeof(*levels));
}
void gpu_matte_set_soft_key(struct gpu_matte *m){
 if(m)m->soft_key=true;
}
void gpu_matte_set_key_channel(struct gpu_matte *m, unsigned channel) {
    m->key_channel = channel;
}
void gpu_matte_set_key_plate(struct gpu_matte *m,const float plate[EZ_KEY_PLATE_SIZE]){
 if(!m)return;
 m->have_key_plate=plate!=NULL;
 if(plate)memcpy(m->key_plate,plate,sizeof(m->key_plate));
 else memset(m->key_plate,0,sizeof(m->key_plate));
}
bool gpu_matte_set_reference(struct gpu_matte *m,const uint8_t *rgba,unsigned pitch){
 if(!m)return false;
 if(!rgba){rel(m->reference_view);rel(m->reference_texture);m->reference_view=NULL;m->reference_texture=NULL;return true;}
 if(pitch<m->w*4)return false;
 if(!m->reference_texture){
   ID3D11Device *device=NULL;ID3D11Texture2D_GetDevice(m->texture,&device);
   D3D11_TEXTURE2D_DESC d={0};d.Width=m->w;d.Height=m->h;d.MipLevels=d.ArraySize=1;
   d.Format=DXGI_FORMAT_R8G8B8A8_UNORM;d.SampleDesc.Count=1;d.BindFlags=D3D11_BIND_SHADER_RESOURCE;
   HRESULT hr=ID3D11Device_CreateTexture2D(device,&d,NULL,&m->reference_texture);
   if(SUCCEEDED(hr))hr=ID3D11Device_CreateShaderResourceView(device,(ID3D11Resource*)m->reference_texture,NULL,&m->reference_view);
   rel(device);if(FAILED(hr)){gpu_matte_set_reference(m,NULL,0);return false;}
 }
 ID3D11DeviceContext_UpdateSubresource(m->context,(ID3D11Resource*)m->reference_texture,0,NULL,rgba,pitch,0);return true;
}
bool gpu_matte_set_native_reference(struct gpu_matte *m,const uint8_t *rgba,unsigned pitch,const float regions[32][4],unsigned count){
 if(!m)return false;
 if(!rgba){rel(m->native_view);rel(m->native_texture);m->native_view=NULL;m->native_texture=NULL;m->native_region_count=0;gpu_matte_set_native_motion(m,NULL,0);return true;}
 if(pitch<m->w*4||count>32||!regions)return false;
 if(!m->native_texture){
   ID3D11Device *device=NULL;ID3D11Texture2D_GetDevice(m->texture,&device);
   D3D11_TEXTURE2D_DESC d={0};d.Width=m->w;d.Height=m->h;d.MipLevels=d.ArraySize=1;
   d.Format=DXGI_FORMAT_R8G8B8A8_UNORM;d.SampleDesc.Count=1;d.BindFlags=D3D11_BIND_SHADER_RESOURCE;
   HRESULT hr=ID3D11Device_CreateTexture2D(device,&d,NULL,&m->native_texture);
   if(SUCCEEDED(hr))hr=ID3D11Device_CreateShaderResourceView(device,(ID3D11Resource*)m->native_texture,NULL,&m->native_view);
   rel(device);if(FAILED(hr)){gpu_matte_set_native_reference(m,NULL,0,NULL,0);return false;}
 }
 memcpy(m->native_regions,regions,count*sizeof(regions[0]));m->native_region_count=count;
 ID3D11DeviceContext_UpdateSubresource(m->context,(ID3D11Resource*)m->native_texture,0,NULL,rgba,pitch,0);return true;
}
bool gpu_matte_set_native_motion(struct gpu_matte *m,const uint8_t *rgba,unsigned pitch){
 if(!m)return false;
 if(!rgba){rel(m->motion_view);rel(m->motion_texture);m->motion_view=NULL;m->motion_texture=NULL;return true;}
 if(pitch<m->w*4)return false;
 if(!m->motion_texture){
   ID3D11Device *device=NULL;ID3D11Texture2D_GetDevice(m->texture,&device);
   D3D11_TEXTURE2D_DESC d={0};d.Width=m->w;d.Height=m->h;d.MipLevels=d.ArraySize=1;
   d.Format=DXGI_FORMAT_R8G8B8A8_UNORM;d.SampleDesc.Count=1;d.BindFlags=D3D11_BIND_SHADER_RESOURCE;
   HRESULT hr=ID3D11Device_CreateTexture2D(device,&d,NULL,&m->motion_texture);
   if(SUCCEEDED(hr))hr=ID3D11Device_CreateShaderResourceView(device,(ID3D11Resource*)m->motion_texture,NULL,&m->motion_view);
   rel(device);if(FAILED(hr)){gpu_matte_set_native_motion(m,NULL,0);return false;}
 }
 ID3D11DeviceContext_UpdateSubresource(m->context,(ID3D11Resource*)m->motion_texture,0,NULL,rgba,pitch,0);return true;
}
bool gpu_matte_render(struct gpu_matte *m,ID3D11ShaderResourceView *source,const float inverse[6],const float bg[3],double *gpu_us){
 if(!m||!source)return false;
 bool use_inverse=m->inverse_state!=EZ_GPU_MATTE_INVERSE_OFF&&m->inverse_options;
 ID3D11DeviceContext *c=m->context;
 for(unsigned i=0;i<4;++i){struct stamp *s=&m->stamps[i];if(!s->pending)continue;
   D3D11_QUERY_DATA_TIMESTAMP_DISJOINT d;UINT64 a,b;
   if(ID3D11DeviceContext_GetData(c,(ID3D11Asynchronous*)s->disjoint,&d,sizeof(d),D3D11_ASYNC_GETDATA_DONOTFLUSH)==S_OK&&
      ID3D11DeviceContext_GetData(c,(ID3D11Asynchronous*)s->begin,&a,sizeof(a),D3D11_ASYNC_GETDATA_DONOTFLUSH)==S_OK&&
      ID3D11DeviceContext_GetData(c,(ID3D11Asynchronous*)s->end,&b,sizeof(b),D3D11_ASYNC_GETDATA_DONOTFLUSH)==S_OK){
      if(!d.Disjoint&&d.Frequency)m->last_us=(double)(b-a)*1000000/d.Frequency;
      s->pending=false;
   }
 }
 float params[24+EZ_MAX_BADGES*8+EZ_KEY_PLATE_SIZE+128]={inverse[0],inverse[1],inverse[4],(float)m->badge_count,inverse[2],inverse[3],inverse[5],m->have_camera_region?1:0,bg[0],bg[1],bg[2],0};
 memcpy(params+12,m->camera_region,sizeof(m->camera_region));
 memcpy(params+16,m->key_levels,sizeof(m->key_levels));
 params[19]=m->soft_key?(m->have_key_plate?-2.f:-1.f):(float)m->key_level_count;
 if(m->badge_count)memcpy(params+20,m->badges,m->badge_count*sizeof(*m->badges));
 memcpy(params+20+EZ_MAX_BADGES*8,m->key_plate,sizeof(m->key_plate));
 params[20+EZ_MAX_BADGES*8+EZ_KEY_PLATE_SIZE]=m->reference_view?1:0;
 params[21+EZ_MAX_BADGES*8+EZ_KEY_PLATE_SIZE]=(float)m->key_channel;
 params[22+EZ_MAX_BADGES*8+EZ_KEY_PLATE_SIZE]=m->native_view?(m->motion_view?2:1):0;
 params[23+EZ_MAX_BADGES*8+EZ_KEY_PLATE_SIZE]=(float)m->native_region_count;
 memcpy(params+24+EZ_MAX_BADGES*8+EZ_KEY_PLATE_SIZE,m->native_regions,sizeof(m->native_regions));
 float delta=sc_key_chroma(bg,m->key_channel);if(delta<.1f)return false;params[11]=1/delta;
 ID3D11DeviceContext_UpdateSubresource(c,(ID3D11Resource*)m->constants,0,NULL,params,0,0);
 ID3D11ShaderResourceView *previous_ps=NULL,*previous_cs[4]={0};ID3D11ComputeShader *previous_shader=NULL;
 ID3D11UnorderedAccessView *previous_uavs[2]={0};ID3D11Buffer *previous_cb=NULL,*previous_cb1=NULL;
 ID3D11DeviceContext_PSGetShaderResources(c,0,1,&previous_ps);
 ID3D11DeviceContext_CSGetShaderResources(c,0,4,previous_cs);
 ID3D11DeviceContext_CSGetShader(c,&previous_shader,NULL,NULL);
 ID3D11DeviceContext_CSGetUnorderedAccessViews(c,0,use_inverse?2:1,previous_uavs);
 ID3D11DeviceContext_CSGetConstantBuffers(c,0,1,&previous_cb);
 if(use_inverse)ID3D11DeviceContext_CSGetConstantBuffers(c,1,1,&previous_cb1);
 ID3D11ShaderResourceView *none=NULL;ID3D11DeviceContext_PSSetShaderResources(c,0,1,&none);
 ID3D11ComputeShader *active_shader=use_inverse?m->shader_inverse:m->shader;
 ID3D11DeviceContext_CSSetShader(c,active_shader,NULL,0);
 ID3D11ShaderResourceView *views[4]={source,m->reference_view,m->native_view,m->motion_view};ID3D11DeviceContext_CSSetShaderResources(c,0,4,views);
 if(use_inverse){ID3D11UnorderedAccessView *uavs[2]={m->uav,m->video_uav};ID3D11DeviceContext_CSSetUnorderedAccessViews(c,0,2,uavs,NULL);}
 else ID3D11DeviceContext_CSSetUnorderedAccessViews(c,0,1,&m->uav,NULL);
 ID3D11DeviceContext_CSSetConstantBuffers(c,0,1,&m->constants);
 struct stamp *stamp=&m->stamps[m->next++%4];bool time=!stamp->pending;
 if(time){ID3D11DeviceContext_Begin(c,(ID3D11Asynchronous*)stamp->disjoint);ID3D11DeviceContext_End(c,(ID3D11Asynchronous*)stamp->begin);}
 ID3D11DeviceContext_Dispatch(c,(m->w+15)/16,(m->h+15)/16,1);
 if(time){ID3D11DeviceContext_End(c,(ID3D11Asynchronous*)stamp->end);ID3D11DeviceContext_End(c,(ID3D11Asynchronous*)stamp->disjoint);stamp->pending=true;}
 if(use_inverse){
   if(m->inverse_state==EZ_GPU_MATTE_INVERSE_LEARN)inverse_learn_pass(m);
   else if(m->inverse_state==EZ_GPU_MATTE_INVERSE_LIVE&&m->inverse_ready&&m->native_view&&m->motion_view)inverse_compose_pass(m);
 }
 ID3D11UnorderedAccessView *no_uav=NULL;ID3D11DeviceContext_CSSetUnorderedAccessViews(c,0,1,&no_uav,NULL);
 if(use_inverse){ID3D11UnorderedAccessView *no_uavs[2]={NULL,NULL};ID3D11DeviceContext_CSSetUnorderedAccessViews(c,0,2,no_uavs,NULL);}
 ID3D11DeviceContext_CSSetShaderResources(c,0,4,previous_cs);
 ID3D11DeviceContext_CSSetConstantBuffers(c,0,1,&previous_cb);
 if(use_inverse)ID3D11DeviceContext_CSSetConstantBuffers(c,1,1,&previous_cb1);
 ID3D11DeviceContext_CSSetShader(c,previous_shader,NULL,0);
 ID3D11DeviceContext_CSSetUnorderedAccessViews(c,0,use_inverse?2:1,previous_uavs,NULL);
 ID3D11DeviceContext_PSSetShaderResources(c,0,1,&previous_ps);
 rel(previous_ps);rel(previous_cs[0]);rel(previous_cs[1]);rel(previous_cs[2]);rel(previous_cs[3]);rel(previous_shader);rel(previous_uavs[0]);rel(previous_uavs[1]);rel(previous_cb);rel(previous_cb1);
 if(gpu_us)*gpu_us=m->last_us;
 return true;
}

#include "gpu_matte.h"
#include "key_reference.h"
#include <assert.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>

/* Known foreground + alpha, composited over each backing. This checks the
 * actual D3D shader, including a moving blue glow not present in a reference.
 * A blue-on-blue negative control must fail; green should preserve that glow.
 * It does not imply that arbitrary green UI can be recovered over green. */
static double check_color(ID3D11Device *device,ID3D11DeviceContext *context,unsigned key,bool soft) {
    const char *name=key==0?"red":key==1?"green":"blue";
    float strength=sc_perspective_key_strength(key);
    uint8_t pixels[64*64*4];
    struct sc_key_samples samples={0};
    for(unsigned y=0;y<64;++y)for(unsigned x=0;x<64;++x){
        float a=0,f[3]={0};
        if(y>=8&&y<24){a=.1f+.8f*x/63;f[2]=a;}
        if(y>=24&&y<32){a=.5f;f[0]=f[1]=f[2]=.5f;}
        if(y>=32&&y<36)a=.25f+.4f*x/63;
        if(y>=36&&y<40)a=.4f;
        if(y>=40&&y<44){a=1;f[key==0?1:0]=1;}
        if(y>=44&&y<48){a=.1f+.8f*x/63;f[1]=a;}
        if(y>=48){a=1;f[0]=f[1]=f[2]=1;}
        unsigned i=(y*64+x)*4;
        for(unsigned c=0;c<3;++c)pixels[i+c]=(uint8_t)lroundf(255*(f[c]+(c==key?strength*(1-a):0)));
        pixels[i+3]=255;
        sc_key_sample_for_channel(&samples,pixels+i,key);
    }
    float backing[3]={0};assert(sc_key_reference(&samples,backing));
    assert(fabsf(backing[key]-strength)<.005f);
    float levels[3];assert(sc_key_flat_levels_for_channel(&samples,backing,levels,key)>=2);
    D3D11_TEXTURE2D_DESC d={0};d.Width=d.Height=64;d.MipLevels=d.ArraySize=1;
    d.Format=DXGI_FORMAT_R8G8B8A8_UNORM;d.SampleDesc.Count=1;d.BindFlags=D3D11_BIND_SHADER_RESOURCE;
    D3D11_SUBRESOURCE_DATA initial={pixels,64*4,0};ID3D11Texture2D *input=NULL,*readback=NULL;
    ID3D11ShaderResourceView *view=NULL;
    assert(SUCCEEDED(ID3D11Device_CreateTexture2D(device,&d,&initial,&input)));
    assert(SUCCEEDED(ID3D11Device_CreateShaderResourceView(device,(ID3D11Resource*)input,NULL,&view)));
    d.Usage=D3D11_USAGE_STAGING;d.BindFlags=0;d.CPUAccessFlags=D3D11_CPU_ACCESS_READ;
    assert(SUCCEEDED(ID3D11Device_CreateTexture2D(device,&d,NULL,&readback)));
    struct gpu_matte *m=gpu_matte_create(device,64,64);assert(m);
    gpu_matte_set_key_channel(m,key);
    const float flat_levels[3]={1.f,.60f,.50f};
    gpu_matte_set_key_levels(m,flat_levels,3);
    if(soft){
        const float camera[4]={0,0,1,1};
        gpu_matte_set_camera_region(m,camera,true);
        gpu_matte_set_soft_key(m);
    }
    const float inverse[6]={1,0,0,1,0,0};double us;
    assert(gpu_matte_render(m,view,inverse,backing,&us));
    ID3D11DeviceContext_CopyResource(context,(ID3D11Resource*)readback,(ID3D11Resource*)gpu_matte_texture(m));
    D3D11_MAPPED_SUBRESOURCE mapped;
    assert(SUCCEEDED(ID3D11DeviceContext_Map(context,(ID3D11Resource*)readback,0,D3D11_MAP_READ,0,&mapped)));
    double error=0,ramp_error=0;
    for(unsigned y=0;y<64;++y)for(unsigned x=0;x<64;++x){
        const uint8_t *p=(const uint8_t*)mapped.pData+y*mapped.RowPitch+x*4;
        if(y<8)assert(p[3]<=2&&p[0]==0&&p[1]==0&&p[2]==0);
        if(y>=8&&y<24){
            int expected=(int)lroundf(255*(.1f+.8f*x/63));
            error+=abs((int)p[2]-expected);
            if(key!=2){assert(abs((int)p[2]-expected)<=1);assert(abs((int)p[3]-expected)<=2);assert(p[0]<=1&&p[1]==0);}
            else assert(p[2]==0&&p[3]<=2);
        }
        if(y>=24&&y<32)for(int c=0;c<4;++c)assert(abs((int)p[c]-128)<=1);
        if(y>=32&&y<36){
            int expected=(int)lroundf(255*(.25f+.4f*x/63));
            int alpha_error=abs((int)p[3]-expected);
            if(soft)assert(alpha_error<=3);
            ramp_error+=alpha_error;
            assert(p[0]==0&&p[1]==0&&p[2]==0);
        }
        if(y>=36&&y<40){assert(abs((int)p[3]-102)<=1);assert(p[0]==0&&p[1]==0&&p[2]==0);}
        if(y>=40&&y<44)assert(p[key==0?1:0]==255&&p[2]==0&&p[3]==255);
        if(y>=44&&y<48){
            int expected=(int)lroundf(255*(.1f+.8f*x/63));
            if(key!=1){assert(abs((int)p[1]-expected)<=1);assert(abs((int)p[3]-expected)<=2);}
            else assert(p[1]==0&&p[3]<=2);
        }
        if(y>=48)for(int c=0;c<4;++c)assert(p[c]==255);
    }
    printf("%s %s blue-glow mean error %.3f/255; clear field, white control, black scrim and opaque UI checked\n",
           name,soft?"soft":"legacy",error/(16*64));
    ID3D11DeviceContext_Unmap(context,(ID3D11Resource*)readback,0);
    gpu_matte_destroy(m);ID3D11ShaderResourceView_Release(view);
    ID3D11Texture2D_Release(input);ID3D11Texture2D_Release(readback);
    return ramp_error/(4*64);
}
int main(void) {
    assert(!_putenv_s("SCEZ_PERSPECTIVE_KEY_COLOR",""));
    assert(sc_perspective_key_channel()==2);
    ID3D11Device *device=NULL;ID3D11DeviceContext *context=NULL;
    assert(SUCCEEDED(D3D11CreateDevice(NULL,D3D_DRIVER_TYPE_WARP,NULL,0,NULL,0,D3D11_SDK_VERSION,&device,NULL,&context)));
    for(unsigned key=0;key<3;++key){
        double legacy=check_color(device,context,key,false);
        double soft=check_color(device,context,key,true);
        printf("key %u opacity ramp mean error %.3f -> %.3f/255\n",key,legacy,soft);
        assert(soft<legacy);
    }
    ID3D11DeviceContext_Release(context);ID3D11Device_Release(device);
    return 0;
}

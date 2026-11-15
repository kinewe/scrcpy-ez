#include "gpu_matte.h"
#include <assert.h>
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

int main(void) {
    ID3D11Device *device = NULL; ID3D11DeviceContext *context = NULL;
    assert(SUCCEEDED(D3D11CreateDevice(NULL,D3D_DRIVER_TYPE_WARP,NULL,0,NULL,0,D3D11_SDK_VERSION,&device,NULL,&context)));
    uint8_t pixels[64*64*4];
    for(unsigned i=0;i<64*64;++i){pixels[i*4]=0;pixels[i*4+1]=0;pixels[i*4+2]=255;pixels[i*4+3]=255;}
    unsigned white=(16*64+32)*4,green=(30*64+32)*4;
    pixels[white]=pixels[white+1]=255;
    pixels[green+1]=255;pixels[green+2]=0;
    D3D11_TEXTURE2D_DESC d={0};d.Width=d.Height=64;d.MipLevels=d.ArraySize=1;
    d.Format=DXGI_FORMAT_R8G8B8A8_UNORM;d.SampleDesc.Count=1;d.BindFlags=D3D11_BIND_SHADER_RESOURCE;
    D3D11_SUBRESOURCE_DATA data={pixels,64*4,0};ID3D11Texture2D *input=NULL,*readback=NULL;
    ID3D11ShaderResourceView *view=NULL;
    assert(SUCCEEDED(ID3D11Device_CreateTexture2D(device,&d,&data,&input)));
    assert(SUCCEEDED(ID3D11Device_CreateShaderResourceView(device,(ID3D11Resource*)input,NULL,&view)));
    d.Usage=D3D11_USAGE_STAGING;d.BindFlags=0;d.CPUAccessFlags=D3D11_CPU_ACCESS_READ;
    assert(SUCCEEDED(ID3D11Device_CreateTexture2D(device,&d,NULL,&readback)));
    struct gpu_matte *m=gpu_matte_create(device,64,64);assert(m);
    const float inverse[6]={1,0,0,1,0,-.02f},blue[3]={0,0,1},region[4]={0,0,1,.8125f};
    D3D11_MAPPED_SUBRESOURCE mapped;
    #define AT(x,y) ((uint8_t*)mapped.pData+(y)*mapped.RowPitch+(x)*4)
    double ignored;
    assert(gpu_matte_render(m,view,inverse,blue,&ignored));
    ID3D11DeviceContext_CopyResource(context,(ID3D11Resource*)readback,(ID3D11Resource*)gpu_matte_texture(m));
    assert(SUCCEEDED(ID3D11DeviceContext_Map(context,(ID3D11Resource*)readback,0,D3D11_MAP_READ,0,&mapped)));
    assert(AT(32,0)[3]==255); // reproduce the affine-edge blue stripe
    ID3D11DeviceContext_Unmap(context,(ID3D11Resource*)readback,0);
    gpu_matte_set_camera_region(m,region,true);
    assert(gpu_matte_render(m,view,inverse,blue,&ignored));
    ID3D11DeviceContext_CopyResource(context,(ID3D11Resource*)readback,(ID3D11Resource*)gpu_matte_texture(m));
    assert(SUCCEEDED(ID3D11DeviceContext_Map(context,(ID3D11Resource*)readback,0,D3D11_MAP_READ,0,&mapped)));
    assert(AT(32,0)[3]<=2); // real visible camera area includes the first rows
    assert(AT(32,60)[3]==255&&AT(32,60)[2]==255); // blue UI outside camera
    assert(AT(32,16)[3]==255&&AT(32,16)[0]==255); // white native control
    assert(AT(32,30)[3]==255&&AT(32,30)[1]==255); // native green scan animation
    ID3D11DeviceContext_Unmap(context,(ID3D11Resource*)readback,0);
    // Model the measured +/- few-code-value variation of a compressed flat
    // blue field. Its native 40% black scrim must become spatially uniform.
    for(unsigned y=4;y<50;++y)for(unsigned x=0;x<64;++x){
        if(x==32&&(y==16||y==30))continue;
        unsigned i=(y*64+x)*4;pixels[i]=2;pixels[i+1]=7;pixels[i+2]=150+x%7;
    }
    for(unsigned x=0;x<64;++x)pixels[(21*64+x)*4+2]=140+x/2;
    ID3D11DeviceContext_UpdateSubresource(context,(ID3D11Resource*)input,0,NULL,pixels,64*4,0);
    const float levels[3]={1,.6f,0},reference[3]={0,15.f/255,1};
    gpu_matte_set_key_levels(m,levels,2);
    assert(gpu_matte_render(m,view,inverse,reference,&ignored));
    ID3D11DeviceContext_CopyResource(context,(ID3D11Resource*)readback,(ID3D11Resource*)gpu_matte_texture(m));
    assert(SUCCEEDED(ID3D11DeviceContext_Map(context,(ID3D11Resource*)readback,0,D3D11_MAP_READ,0,&mapped)));
    for(unsigned x=0;x<64;++x){assert(AT(x,20)[3]>=101&&AT(x,20)[3]<=103);assert(AT(x,20)[0]==0&&AT(x,20)[1]==0&&AT(x,20)[2]==0);}
    for(unsigned x=1;x<64;++x){int delta=(int)AT(x,21)[3]-(int)AT(x-1,21)[3];assert(delta>=-4&&delta<=4);}
    assert(AT(32,16)[3]==255&&AT(32,16)[0]==255);
    assert(AT(32,30)[3]==255&&AT(32,30)[1]==255);
    ID3D11DeviceContext_Unmap(context,(ID3D11Resource*)readback,0);
    float plate[EZ_KEY_PLATE_SIZE];
    for(unsigned y=0;y<EZ_KEY_PLATE_HEIGHT;++y)for(unsigned x=0;x<EZ_KEY_PLATE_WIDTH;++x)
        plate[y*EZ_KEY_PLATE_WIDTH+x]=.4f+.2f*y/(EZ_KEY_PLATE_HEIGHT-1);
    for(unsigned y=4;y<50;++y)for(unsigned x=0;x<64;++x){
        if(x==32&&(y==16||y==30))continue;
        float base=.4f+.2f*y/63;unsigned i=(y*64+x)*4;
        pixels[i]=2;pixels[i+1]=(unsigned)(15*base);pixels[i+2]=(unsigned)(255*base)+2+(x%5)-2;
    }
    ID3D11DeviceContext_UpdateSubresource(context,(ID3D11Resource*)input,0,NULL,pixels,64*4,0);
    gpu_matte_set_key_plate(m,plate);
    assert(gpu_matte_render(m,view,inverse,reference,&ignored));
    ID3D11DeviceContext_CopyResource(context,(ID3D11Resource*)readback,(ID3D11Resource*)gpu_matte_texture(m));
    assert(SUCCEEDED(ID3D11DeviceContext_Map(context,(ID3D11Resource*)readback,0,D3D11_MAP_READ,0,&mapped)));
    for(unsigned y=4;y<50;++y)for(unsigned x=0;x<64;++x){
        if(x==32&&(y==16||y==30))continue;
        int expected=(int)(255*(1-(.4f+.2f*y/63))+.5f);
        int delta=(int)AT(x,y)[3]-expected;assert(delta>=-1&&delta<=1);
        assert(AT(x,y)[0]==0&&AT(x,y)[1]==0&&AT(x,y)[2]==0);
    }
    assert(AT(32,16)[3]==255&&AT(32,16)[0]==255);
    assert(AT(32,30)[3]==255&&AT(32,30)[1]==255);
    ID3D11DeviceContext_Unmap(context,(ID3D11Resource*)readback,0);
    // Static blue UI is indistinguishable from backing in a single image.
    // A known multi-background matte preserves it; a new green scan line at
    // the same pixel must immediately use the live path rather than freeze.
    uint8_t reference_pixels[64*64*4];
    for(unsigned i=0;i<64*64;++i){reference_pixels[i*4]=255;reference_pixels[i*4+1]=0;reference_pixels[i*4+2]=255;reference_pixels[i*4+3]=0;}
    unsigned control=(20*64+32)*4;
    reference_pixels[control]=0;reference_pixels[control+1]=136;reference_pixels[control+2]=255;reference_pixels[control+3]=255;
    pixels[control]=0;pixels[control+1]=136;pixels[control+2]=255;
    assert(!gpu_matte_set_reference(m,reference_pixels,1));
    assert(gpu_matte_set_reference(m,reference_pixels,64*4));
    ID3D11DeviceContext_UpdateSubresource(context,(ID3D11Resource*)input,0,NULL,pixels,64*4,0);
    assert(gpu_matte_render(m,view,inverse,blue,&ignored));
    ID3D11DeviceContext_CopyResource(context,(ID3D11Resource*)readback,(ID3D11Resource*)gpu_matte_texture(m));
    assert(SUCCEEDED(ID3D11DeviceContext_Map(context,(ID3D11Resource*)readback,0,D3D11_MAP_READ,0,&mapped)));
    assert(AT(32,20)[1]==136&&AT(32,20)[2]==255&&AT(32,20)[3]==255);
    ID3D11DeviceContext_Unmap(context,(ID3D11Resource*)readback,0);
    pixels[control+1]=255;pixels[control+2]=0;
    ID3D11DeviceContext_UpdateSubresource(context,(ID3D11Resource*)input,0,NULL,pixels,64*4,0);
    assert(gpu_matte_render(m,view,inverse,blue,&ignored));
    ID3D11DeviceContext_CopyResource(context,(ID3D11Resource*)readback,(ID3D11Resource*)gpu_matte_texture(m));
    assert(SUCCEEDED(ID3D11DeviceContext_Map(context,(ID3D11Resource*)readback,0,D3D11_MAP_READ,0,&mapped)));
    assert(AT(32,20)[1]==255&&AT(32,20)[2]==0&&AT(32,20)[3]==255);
    ID3D11DeviceContext_Unmap(context,(ID3D11Resource*)readback,0);
    assert(gpu_matte_set_reference(m,NULL,0));

    // Alternate coherent codec blocks and horizontal stripes across a stable
    // dimmed background. The soft spatial reference must absorb the noise
    // while keeping continuous alpha and allowing a moving scan line through.
    const float full_region[4] = {0, 0, 1, 1};
    gpu_matte_set_camera_region(m, full_region, true);
    gpu_matte_set_soft_key(m);
    for (unsigned y = 0; y < EZ_KEY_PLATE_HEIGHT; ++y) {
        for (unsigned x = 0; x < EZ_KEY_PLATE_WIDTH; ++x) {
            float t = y < 48 ? .6f : .75f - .4f * (2 * x + .5f) / 63;
            plate[y * EZ_KEY_PLATE_WIDTH + x] = t;
        }
    }
    unsigned jitter[2] = {0};
    unsigned tested = 0;
    uint8_t previous[64 * 64];
    for (unsigned spatial = 0; spatial < 2; ++spatial) {
        gpu_matte_set_key_plate(m, spatial ? plate : NULL);
        for (unsigned phase = 0; phase < 2; ++phase) {
            unsigned scan_y = phase ? 30 : 16;
            for (unsigned y = 0; y < 64; ++y) {
                for (unsigned x = 0; x < 64; ++x) {
                    uint8_t *rgb = pixels + (y * 64 + x) * 4;
                    rgb[0] = rgb[1] = 0;
                    int noise = ((x / 8 + y / 4 + phase) & 1) ? -4 : 4;
                    rgb[2] = y < 48 ? 153 + noise : lroundf(255 * (.75f - .4f * x / 63));
                    if (y == scan_y && x > 10 && x < 54) {
                        rgb[1] = 153;
                        rgb[2] = 102;
                    }
                    rgb[3] = 255;
                }
            }
            ID3D11DeviceContext_UpdateSubresource(context,(ID3D11Resource*)input,0,NULL,pixels,64*4,0);
            assert(gpu_matte_render(m,view,inverse,blue,&ignored));
            ID3D11DeviceContext_CopyResource(context,(ID3D11Resource*)readback,(ID3D11Resource*)gpu_matte_texture(m));
            assert(SUCCEEDED(ID3D11DeviceContext_Map(context,(ID3D11Resource*)readback,0,D3D11_MAP_READ,0,&mapped)));
            assert(AT(32,scan_y)[1] == 153 && AT(32,scan_y)[3] == 153);
            for (unsigned y = 4; y < 44; ++y) {
                if (y == 16 || y == 30) continue;
                for (unsigned x = 0; x < 64; ++x) {
                    unsigned index = y * 64 + x;
                    if (phase) {
                        jitter[spatial] += abs((int)AT(x,y)[3] - previous[index]);
                        if (!spatial) ++tested;
                    } else {
                        previous[index] = AT(x,y)[3];
                    }
                }
            }
            for (unsigned x = 0; x < 64; ++x) {
                int expected = lroundf(255 * (.25f + .4f * x / 63));
                assert(abs((int)AT(x,57)[3] - expected) <= 2);
            }
            ID3D11DeviceContext_Unmap(context,(ID3D11Resource*)readback,0);
        }
    }
    assert(tested && jitter[0] > 4 * tested && jitter[1] < tested);
    printf("soft spatial backing: mean frame-to-frame alpha change %.3f -> %.3f/255\n",
           (double)jitter[0] / tested, (double)jitter[1] / tested);

    // A weak backing can alternate between adjacent chroma bins in whole
    // blocks. The measured clear plane stays steady while white text has
    // enough edge contrast on a bright background and colored motion is live.
    const float red[3] = {81.f / 255, 0, 0};
    gpu_matte_set_key_channel(m, 0);
    for (unsigned i = 0; i < EZ_KEY_PLATE_SIZE; ++i) plate[i] = 1;
    gpu_matte_set_key_plate(m, plate);
    for (unsigned phase = 0; phase < 2; ++phase) {
        unsigned scan_y = phase ? 30 : 24;
        for (unsigned y = 0; y < 64; ++y) {
            for (unsigned x = 0; x < 64; ++x) {
                uint8_t *rgb = pixels + (y * 64 + x) * 4;
                rgb[0] = ((x / 8 + y / 8 + phase) & 1) ? 77 : 83;
                rgb[1] = rgb[2] = 0;
                if (x == 32 && y >= 10 && y <= 15) rgb[0] = rgb[1] = rgb[2] = 255;
                if (x == 31 && y >= 10 && y <= 15) { rgb[0] = 116; rgb[1] = rgb[2] = 51; }
                if (y == scan_y) { rgb[0] = 30; rgb[1] = 153; rgb[2] = 40; }
                rgb[3] = 255;
            }
        }
        ID3D11DeviceContext_UpdateSubresource(context,(ID3D11Resource*)input,0,NULL,pixels,64*4,0);
        assert(gpu_matte_render(m,view,inverse,red,&ignored));
        ID3D11DeviceContext_CopyResource(context,(ID3D11Resource*)readback,(ID3D11Resource*)gpu_matte_texture(m));
        assert(SUCCEEDED(ID3D11DeviceContext_Map(context,(ID3D11Resource*)readback,0,D3D11_MAP_READ,0,&mapped)));
        for (unsigned y = 35; y < 60; ++y) {
            for (unsigned x = 0; x < 64; ++x) {
                assert(AT(x,y)[0] <= 1 && AT(x,y)[3] <= 2);
            }
        }
        assert(AT(32,12)[0] == 255 && AT(32,12)[3] == 255);
        assert(abs((int)AT(31,12)[0] - 51) <= 2 && AT(31,12)[3] >= 95 && AT(31,12)[3] <= 112);
        assert(AT(33,12)[3] <= 2);
        assert(AT(29,12)[3] <= 2);
        // A green scan stroke at this opacity is distinct from key-colored noise.
        assert(AT(31,scan_y)[1] == 153 && AT(31,scan_y)[3] == 255);
        assert(AT(31,phase ? 24 : 30)[3] <= 2);
        ID3D11DeviceContext_Unmap(context,(ID3D11Resource*)readback,0);
    }
    gpu_matte_destroy(m);ID3D11ShaderResourceView_Release(view);
    ID3D11Texture2D_Release(input);ID3D11Texture2D_Release(readback);
    ID3D11DeviceContext_Release(context);ID3D11Device_Release(device);
    puts("preview bounds: top stripe removed; outside blue UI, white controls and green scan effect preserved");
    return 0;
}

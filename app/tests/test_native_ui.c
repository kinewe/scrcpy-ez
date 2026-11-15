#include "native_ui.h"
#include <assert.h>
#include <stdio.h>
#include <direct.h>
static void save(const char *path,unsigned alpha){
    SDL_Surface *s=SDL_CreateSurface(16,32,SDL_PIXELFORMAT_RGBA32);assert(s);
    SDL_FillSurfaceRect(s,NULL,SDL_MapSurfaceRGBA(s,20,80,200,alpha));assert(SDL_SavePNG(s,path));SDL_DestroySurface(s);
}
int main(void){
    assert(SDL_Init(SDL_INIT_VIDEO));SDL_Window *w=SDL_CreateWindow("native UI alpha checks",16,32,SDL_WINDOW_HIDDEN);assert(w);
    SDL_Renderer *r=SDL_CreateRenderer(w,"direct3d11");assert(r);
    char path[1024];assert(_getcwd(path,sizeof(path)-64));strcat(path,"/native-ui-test.png");remove(path);
    assert(!_putenv_s("SCEZ_NATIVE_UI_FRAME_FILE",path));struct sc_native_ui ui={0};sc_native_ui_init(&ui,16,32);
    assert(!sc_native_ui_pick(&ui,r,sc_tick_now()));save(path,128);SDL_Texture *t=NULL;
    for(int i=0;i<100&&!t;++i){SDL_Delay(20);t=sc_native_ui_pick(&ui,r,sc_tick_now());}assert(t);
    SDL_Texture *target=SDL_CreateTexture(r,SDL_PIXELFORMAT_RGBA32,SDL_TEXTUREACCESS_TARGET,16,32);assert(target);
    assert(SDL_SetRenderTarget(r,target));assert(SDL_RenderTexture(r,t,NULL,NULL));
    SDL_Surface *read=SDL_RenderReadPixels(r,NULL);assert(read);SDL_Surface *rgba=SDL_ConvertSurface(read,SDL_PIXELFORMAT_RGBA32);assert(rgba);
    const uint8_t *p=rgba->pixels;assert(p[0]==10&&p[1]==40&&p[2]==100&&p[3]==128);
    SDL_DestroySurface(rgba);SDL_DestroySurface(read);SDL_SetRenderTarget(r,NULL);
    assert(!sc_native_ui_pick(&ui,r,sc_tick_now()+SC_TICK_FROM_MS(5100)));
    remove(path);SDL_Delay(100);assert(!sc_native_ui_pick(&ui,r,sc_tick_now()));
    save(path,255);SDL_Delay(120);assert(!sc_native_ui_pick(&ui,r,sc_tick_now()));assert(!sc_native_ui_decode(path,16,32));
    save(path,76);assert(!sc_native_ui_decode(path,32,16));SDL_Delay(120);assert(sc_native_ui_pick(&ui,r,sc_tick_now()));
    sc_native_ui_destroy(&ui);remove(path);_putenv_s("SCEZ_NATIVE_UI_FRAME_FILE","");
    SDL_DestroyTexture(target);SDL_DestroyRenderer(r);SDL_DestroyWindow(w);SDL_Quit();
    puts("native UI: premultiplied RGBA readback, no-source/stale/deleted/opaque/aspect fallbacks and recovery passed");return 0;
}

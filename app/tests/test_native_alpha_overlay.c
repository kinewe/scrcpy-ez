/* Verify the actual RC23 SDL/D3D11 overlay, including faint and opaque alpha. */
#include <assert.h>
#include <stdio.h>
#include <string.h>
#include "local_preview.h"
#include "native_source.h"
#include "key_preview.h"
static SDL_Surface *next_frame;
static bool valid=true,enabled,failed,released;
static uint32_t scene_epoch;
static SDL_Texture *key_texture;
void sc_native_source_set_enabled(bool on){enabled=on;}
void sc_native_source_set_warming(bool on){(void)on;}
bool sc_native_source_valid(void){return valid;}
bool sc_native_source_failed(void){return failed;}
bool sc_native_source_release(void){return released;}
uint32_t sc_native_source_scene_epoch(void){return scene_epoch;}
SDL_Texture *sc_key_preview_texture(SDL_Renderer *r,struct sc_perspective *p,const AVFrame *f,SDL_Texture *fallback){
    (void)r;(void)p;(void)f;return key_texture?key_texture:fallback;
}
bool sc_key_preview_is_composited(SDL_Texture *t){return t==key_texture;}
void sc_key_preview_destroy(void){}
sc_tick sc_tick_now(void){return SC_TICK_FROM_NS(SDL_GetTicksNS());}
SDL_Surface *sc_native_source_take(uint64_t *generation){SDL_Surface *s=next_frame;next_frame=NULL;*generation=1;return s;}
void sc_native_source_presented(void){}
void sc_mutex_lock(sc_mutex *m){SDL_LockMutex(m->mutex);}
void sc_mutex_unlock(sc_mutex *m){SDL_UnlockMutex(m->mutex);}
int main(void){
    assert(SDL_Init(SDL_INIT_VIDEO));
    SDL_PropertiesID props=SDL_CreateProperties();
    SDL_SetNumberProperty(props,SDL_PROP_WINDOW_CREATE_WIDTH_NUMBER,32);
    SDL_SetNumberProperty(props,SDL_PROP_WINDOW_CREATE_HEIGHT_NUMBER,32);
    SDL_SetBooleanProperty(props,SDL_PROP_WINDOW_CREATE_TRANSPARENT_BOOLEAN,true);
    SDL_SetBooleanProperty(props,SDL_PROP_WINDOW_CREATE_HIDDEN_BOOLEAN,true);
    SDL_Window *window=SDL_CreateWindowWithProperties(props);SDL_DestroyProperties(props);assert(window);
    SDL_Renderer *renderer=SDL_CreateRenderer(window,"direct3d11");assert(renderer);
    struct sc_perspective p={0};p.started=true;p.mutex.mutex=SDL_CreateMutex();assert(p.mutex.mutex);
    atomic_init(&p.active,true);atomic_init(&p.stopped,false);atomic_init(&p.evicted,false);atomic_init(&p.calibration_requested,false);
    AVFrame frame={0};frame.width=32;frame.height=32;
    next_frame=SDL_CreateSurface(32,32,SDL_PIXELFORMAT_RGBA32);assert(next_frame);
    unsigned alphas[]={0,1,2,76,128,209,254,255};
    for(int y=0;y<32;y++)for(int x=0;x<32;x++){uint8_t *q=(uint8_t*)next_frame->pixels+y*next_frame->pitch+x*4;q[0]=240;q[1]=120;q[2]=60;q[3]=alphas[x%8];}
    SDL_Texture *texture=local_preview_texture(renderer,&p,&frame,NULL);assert(enabled&&texture&&local_preview_is_composited(texture));
    SDL_SetRenderDrawColor(renderer,0,0,0,0);assert(SDL_RenderClear(renderer));assert(SDL_RenderTexture(renderer,texture,NULL,NULL));
    SDL_Surface *read=SDL_RenderReadPixels(renderer,NULL);assert(read);SDL_Surface *rgba=SDL_ConvertSurface(read,SDL_PIXELFORMAT_RGBA32);assert(rgba);
    unsigned checked=0;
    for(int y=0;y<32;y++)for(int x=0;x<32;x++){
        const uint8_t *q=(const uint8_t*)rgba->pixels+y*rgba->pitch+x*4;unsigned a=alphas[x%8];
        unsigned expected[]={ (240*a+127)/255,(120*a+127)/255,(60*a+127)/255,a };
        for(unsigned c=0;c<4;c++)if(q[c]!=expected[c]){fprintf(stderr,"pixel %d,%d channel %u got %u expected %u\n",x,y,c,q[c],expected[c]);return 2;}
        checked++;
    }
    SDL_DestroySurface(rgba);SDL_DestroySurface(read);
    /* Already-premultiplied source bytes must survive without a second multiplication. */
    next_frame=SDL_CreateSurface(32,32,SDL_PIXELFORMAT_RGBA32);assert(next_frame);
    SDL_SetBooleanProperty(SDL_GetSurfaceProperties(next_frame),"ez.native.premultiplied",true);
    for(int y=0;y<32;y++)for(int x=0;x<32;x++){uint8_t *q=(uint8_t*)next_frame->pixels+y*next_frame->pitch+x*4;unsigned a=alphas[x%8];q[0]=(240*a+127)/255;q[1]=(120*a+127)/255;q[2]=(60*a+127)/255;q[3]=a;}
    texture=local_preview_texture(renderer,&p,&frame,NULL);assert(texture);
    assert(SDL_RenderClear(renderer));assert(SDL_RenderTexture(renderer,texture,NULL,NULL));read=SDL_RenderReadPixels(renderer,NULL);assert(read);rgba=SDL_ConvertSurface(read,SDL_PIXELFORMAT_RGBA32);assert(rgba);
    for(int y=0;y<32;y++)for(int x=0;x<32;x++){uint8_t *q=(uint8_t*)rgba->pixels+y*rgba->pitch+x*4;unsigned a=alphas[x%8];assert(q[0]==(240*a+127)/255&&q[1]==(120*a+127)/255&&q[2]==(60*a+127)/255&&q[3]==a);checked++;}
    SDL_DestroySurface(rgba);SDL_DestroySurface(read);
    valid=false;assert(local_preview_texture(renderer,&p,&frame,NULL)==NULL);
    valid=true;next_frame=SDL_CreateSurface(32,16,SDL_PIXELFORMAT_RGBA32);assert(next_frame);
    assert(local_preview_texture(renderer,&p,&frame,NULL)==NULL);assert(enabled);
    scene_epoch=1;
    atomic_store(&p.active,false);assert(local_preview_texture(renderer,&p,&frame,NULL)==NULL);assert(enabled);
    scene_epoch=0;
    atomic_store(&p.active,false);assert(local_preview_texture(renderer,&p,&frame,NULL)==NULL);assert(!enabled);
    atomic_store(&p.active,true);failed=true;valid=false;key_texture=texture;
    assert(local_preview_texture(renderer,&p,&frame,NULL)==NULL);
    released=true;
    assert(local_preview_texture(renderer,&p,&frame,NULL)==key_texture);
    local_preview_destroy();SDL_DestroyMutex(p.mutex.mutex);SDL_DestroyRenderer(renderer);SDL_DestroyWindow(window);SDL_Quit();
    printf("PASS native-alpha D3D11 overlay: %u exact RGBA pixels; stale source and geometry rejected\n",checked);return 0;
}

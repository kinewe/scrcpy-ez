#include "native_source.h"
#ifdef _WIN32
#include "util/net.h"
#include <windows.h>
#include <stdatomic.h>
#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <zlib.h>
#include "native_alpha_codec.h"
#include "adb/adb.h"
#include "events.h"
#include "util/file.h"
#include "util/intr.h"
#include "util/process.h"
#include "util/log.h"
#include "util/net.h"
#include "util/tick.h"
#define NA_MAX_BYTES (64u*1024u*1024u)
#define NA_HEADER 40
struct native_source {
    bool started,enabled,warming,valid;
    atomic_bool stop,event_pending;
    struct sc_intr intr;
    SDL_Mutex *mutex,*command_mutex;
    SDL_Thread *receiver,*launcher;
    sc_socket socket;
    sc_pid process;
    char serial[128],package[128],name[96],jar[128],classpath[160];
    uint16_t port;
    uint32_t epoch,sequence,min_epoch;
    uint64_t generation,frames;
    sc_tick alive;
    SDL_Surface *pending;
    sc_tick presented_at,last_presented;
    unsigned presented_count;
    int presented_fps;
};
static struct native_source ns;
static uint32_t be32(const uint8_t *p){return (uint32_t)p[0]<<24|(uint32_t)p[1]<<16|(uint32_t)p[2]<<8|p[3];}
static void wake_renderer(void){
    if(!atomic_exchange(&ns.event_pending,true)&&!sc_push_event(SC_EVENT_NATIVE_ALPHA))atomic_store(&ns.event_pending,false);
}
void sc_native_source_ack_event(void){if(ns.started)atomic_store(&ns.event_pending,false);}
void sc_native_source_presented(void){
    if(!ns.started)return;sc_tick now=sc_tick_now();SDL_LockMutex(ns.mutex);
    if(!ns.presented_at)ns.presented_at=now;
    ++ns.presented_count;ns.last_presented=now;
    if(now-ns.presented_at>=SC_TICK_FROM_SEC(1)){ns.presented_fps=(int)((uint64_t)ns.presented_count*SC_TICK_FREQ/(now-ns.presented_at));ns.presented_count=0;ns.presented_at=now;}
    SDL_UnlockMutex(ns.mutex);
}
int sc_native_source_fps(void){if(!ns.started)return -1;SDL_LockMutex(ns.mutex);int fps=ns.enabled&&ns.valid?(sc_tick_now()-ns.last_presented<SC_TICK_FROM_SEC(2)?ns.presented_fps:0):-1;SDL_UnlockMutex(ns.mutex);return fps;}
static void revoke(void){
    SDL_LockMutex(ns.mutex);ns.valid=false;SDL_DestroySurface(ns.pending);ns.pending=NULL;++ns.generation;SDL_UnlockMutex(ns.mutex);wake_renderer();
}
static bool read_utf(sc_socket fd,char *out,size_t cap){
    uint8_t b[2];ssize_t got=net_recv_all(fd,b,2);if(got!=2)return false;unsigned n=(unsigned)b[0]<<8|b[1];
    if(n>=cap){LOGW("NATIVE_ALPHA UTF length=%u cap=%u",n,(unsigned)cap);return false;}
    got=net_recv_all(fd,out,n);if(got!=(ssize_t)n){LOGW("NATIVE_ALPHA UTF body bytes=%d expected=%u",(int)got,n);return false;}
    out[n]=0;return true;
}
static bool command(uint8_t value){
    SDL_LockMutex(ns.command_mutex);sc_socket fd=ns.socket;bool ok=fd!=SC_SOCKET_NONE&&net_send_all(fd,&value,1)==1;SDL_UnlockMutex(ns.command_mutex);return ok;
}
void sc_native_source_set_enabled(bool enabled){
    if(!ns.started)return;
    SDL_LockMutex(ns.mutex);bool changed=ns.enabled!=enabled;ns.enabled=enabled;bool warming=ns.warming;if(changed&&enabled){ns.min_epoch=ns.epoch+1;ns.valid=false;}SDL_UnlockMutex(ns.mutex);
    if(changed){command(enabled?1:warming?3:2);if(!enabled)revoke();}
}
void sc_native_source_set_warming(bool warming){
    if(!ns.started)return;
    SDL_LockMutex(ns.mutex);bool changed=ns.warming!=warming;ns.warming=warming;bool enabled=ns.enabled;SDL_UnlockMutex(ns.mutex);
    if(changed&&!enabled)command(warming?3:2);
}
bool sc_native_source_active(void){return ns.started;}
bool sc_native_source_valid(void){
    if(!ns.started)return false;
    SDL_LockMutex(ns.mutex);bool valid=ns.enabled&&ns.valid&&sc_tick_now()-ns.alive<SC_TICK_FROM_MS(2000);SDL_UnlockMutex(ns.mutex);return valid;
}
SDL_Surface *sc_native_source_take(uint64_t *generation){
    if(!ns.started)return NULL;
    SDL_LockMutex(ns.mutex);SDL_Surface *s=ns.pending;ns.pending=NULL;*generation=ns.generation;SDL_UnlockMutex(ns.mutex);return s;
}
static int receive_frames(void *unused){
    (void)unused;sc_socket fd=SC_SOCKET_NONE;bool connected=false;struct na_decoder decoder={0};sc_tick rate_at=sc_tick_now();uint64_t rate_frames=0;
    char magic[80]={0},pkg[128]={0};
    for(unsigned attempt=0;attempt<80&&!atomic_load(&ns.stop);++attempt){
        fd=net_socket();if(fd==SC_SOCKET_NONE)break;
        SDL_LockMutex(ns.command_mutex);ns.socket=fd;SDL_UnlockMutex(ns.command_mutex);
        if(net_connect(fd,IPV4_LOCALHOST,ns.port)&&read_utf(fd,magic,sizeof(magic))&&read_utf(fd,pkg,sizeof(pkg))){connected=true;break;}
        SDL_LockMutex(ns.command_mutex);if(ns.socket==fd){ns.socket=SC_SOCKET_NONE;net_close(fd);}SDL_UnlockMutex(ns.command_mutex);
        fd=SC_SOCKET_NONE;SDL_Delay(250);
    }
    if(!connected){LOGW("NATIVE_ALPHA device handshake timed out");return 0;}
    if(strcmp(magic,"EZ_NATIVE_ALPHA_RGBA_V2")||strcmp(pkg,ns.package)){
        LOGW("NATIVE_ALPHA invalid handshake magic='%s' package='%s' expected='%s'",magic,pkg,ns.package);goto end;
    }
    LOGI("NATIVE_ALPHA connected: lossless RGBA, no PNG/calibration/inverse learner");
    SDL_LockMutex(ns.mutex);bool enabled=ns.enabled,warming=ns.warming;SDL_UnlockMutex(ns.mutex);command(enabled?1:warming?3:2);
    while(!atomic_load(&ns.stop)){
        uint8_t header[NA_HEADER];if(net_recv_all(fd,header,sizeof(header))!=sizeof(header))break;
        uint32_t type=be32(header),bytes=be32(header+4),sequence=be32(header+8),epoch=be32(header+12);
        uint32_t w=be32(header+16),h=be32(header+20),crc=be32(header+32),flags=be32(header+36);
        if(type!=1){
            if(bytes||type<2||type>4)break;
            if(type==2||type==4){decoder.valid=false;SDL_LockMutex(ns.mutex);if(epoch>ns.epoch)ns.epoch=epoch;SDL_UnlockMutex(ns.mutex);revoke();}
            else{SDL_LockMutex(ns.mutex);ns.alive=sc_tick_now();SDL_UnlockMutex(ns.mutex);}
            continue;
        }
        uint64_t raw_size=(uint64_t)w*h*4;
        if(!w||!h||w>16384||h>16384||raw_size>NA_MAX_BYTES||!bytes||bytes>NA_MAX_BYTES||flags<1||flags>3){LOGW("NATIVE_ALPHA invalid packet dimensions/encoding");break;}
        uint8_t *packed=malloc(bytes);SDL_Surface *surface=SDL_CreateSurface(w,h,SDL_PIXELFORMAT_RGBA32);
        if(!packed||!surface){free(packed);SDL_DestroySurface(surface);break;}
        if(net_recv_all(fd,packed,bytes)!=(ssize_t)bytes){free(packed);SDL_DestroySurface(surface);break;}
        size_t capacity=(size_t)raw_size+4+(size_t)h*8;
        uint8_t *raw=malloc(capacity);uLongf decoded=(uLongf)capacity;
        bool ok=raw&&uncompress(raw,&decoded,packed,bytes)==Z_OK&&na_decode(&decoder,flags,epoch,w,h,raw,decoded,crc);
        free(packed);
        if(!ok){free(raw);SDL_DestroySurface(surface);decoder.valid=false;revoke();if(!command(4))break;LOGW("NATIVE_ALPHA invalid patch; requested full frame");continue;}
        for(uint32_t y=0;y<h;++y)memcpy((uint8_t*)surface->pixels+y*surface->pitch,decoder.rgba+(size_t)y*w*4,(size_t)w*4);
        SDL_SetBooleanProperty(SDL_GetSurfaceProperties(surface),"ez.native.premultiplied",decoder.premultiplied);
        free(raw);SDL_LockMutex(ns.mutex);bool accept=ns.enabled&&epoch>=ns.min_epoch&&(epoch>ns.epoch||(epoch==ns.epoch&&sequence>ns.sequence));
        if(accept){SDL_DestroySurface(ns.pending);ns.pending=surface;surface=NULL;ns.epoch=epoch;ns.sequence=sequence;ns.valid=true;ns.alive=sc_tick_now();++ns.generation;++ns.frames;}
        uint64_t count=ns.frames;SDL_UnlockMutex(ns.mutex);SDL_DestroySurface(surface);
        if(accept){wake_renderer();++rate_frames;sc_tick at=sc_tick_now();if(count==1||at-rate_at>=SC_TICK_FROM_SEC(2)){LOGI("NATIVE_ALPHA frame=%" PRIu64 " fps=%.1f size=%ux%u seq=%u epoch=%u",count,(double)rate_frames*SC_TICK_FREQ/(at-rate_at),w,h,sequence,epoch);rate_frames=0;rate_at=at;}}
    }
end:free(decoder.rgba);revoke();return 0;
}
static int launch_probe(void *unused){
    (void)unused;
    char *helper=sc_file_get_local_path("scrcpy-ez.exe");
    const char *worker[]={helper,"--native-alpha-worker",sc_adb_get_executable(),ns.serial,ns.classpath,ns.package,ns.name,NULL};
    const char *direct[]={sc_adb_get_executable(),"-s",ns.serial,"shell",ns.classpath,"app_process","/","com.genymobile.scrcpy.NativeAlphaProbe",ns.package,ns.name,NULL};
    const char *const *cmd=helper&&sc_file_is_regular(helper)?worker:direct;
    sc_pid pid=SC_PROCESS_NONE;sc_pipe output=NULL;
    enum sc_process_result result=sc_process_execute_p(cmd,&pid,SC_PROCESS_NO_STDERR,NULL,&output,NULL);free(helper);
    if(result!=SC_PROCESS_SUCCESS)return 0;
    SDL_LockMutex(ns.mutex);ns.process=pid;SDL_UnlockMutex(ns.mutex);
    if(atomic_load(&ns.stop))sc_process_terminate(pid);
    char buffer[1024],line[65536];size_t used=0;ssize_t n;
    while((n=sc_pipe_read(output,buffer,sizeof(buffer)))>0)for(ssize_t i=0;i<n;++i){
        if(buffer[i]=='\n'){line[used]=0;if(used)LOGI("NATIVE_ALPHA device: %s",line);used=0;}
        else if(used+1<sizeof(line))line[used++]=buffer[i];
    }
    sc_pipe_close(output);SDL_LockMutex(ns.mutex);ns.process=SC_PROCESS_NONE;SDL_UnlockMutex(ns.mutex);sc_process_wait(pid,true);return 0;
}
static uint16_t allocate_forward(void){
    char remote[128];snprintf(remote,sizeof(remote),"localabstract:%s",ns.name);
    const char *args[]={sc_adb_get_executable(),"-s",ns.serial,"forward","tcp:0",remote,NULL};
    sc_pid pid;sc_pipe output=NULL;
    if(sc_process_execute_p(args,&pid,SC_PROCESS_NO_STDERR,NULL,&output,NULL)!=SC_PROCESS_SUCCESS)return 0;
    char reply[24];ssize_t size=sc_pipe_read(output,reply,sizeof(reply)-1);sc_pipe_close(output);sc_exit_code code=sc_process_wait(pid,true);
    if(code||size<=0)return 0;reply[size]=0;char *end;unsigned long port=strtoul(reply,&end,10);
    if(end==reply||port==0||port>65535)return 0;return (uint16_t)port;
}
bool sc_native_source_start(const char *serial,const char *package){
    if(ns.started)return true;
    if(!serial||!*serial)return false;
    memset(&ns,0,sizeof(ns));atomic_init(&ns.stop,false);atomic_init(&ns.event_pending,false);
    ns.socket=SC_SOCKET_NONE;ns.process=SC_PROCESS_NONE;ns.mutex=SDL_CreateMutex();ns.command_mutex=SDL_CreateMutex();
    if(!ns.mutex||!ns.command_mutex)goto fail_mutex;
    snprintf(ns.serial,sizeof(ns.serial),"%s",serial);snprintf(ns.package,sizeof(ns.package),"%s",package&&*package?package:"com.tencent.wework");
    snprintf(ns.name,sizeof(ns.name),"ez_native_alpha_%lu",(unsigned long)GetCurrentProcessId());
    snprintf(ns.jar,sizeof(ns.jar),"/data/local/tmp/ez-native-alpha-%lu.jar",(unsigned long)GetCurrentProcessId());
    snprintf(ns.classpath,sizeof(ns.classpath),"CLASSPATH=%s",ns.jar);sc_intr_init(&ns.intr);
    ns.port=allocate_forward(); // adb atomically allocates a free port; never rebind another session.
    if(!ns.port)goto fail_intr;
    const char *server=getenv("SCRCPY_SERVER_PATH");char *portable_server=NULL;
    if(!server||!*server){portable_server=sc_file_get_local_path("scrcpy-server");server=portable_server;}
    bool pushed=server&&sc_adb_push(&ns.intr,ns.serial,server,ns.jar,SC_ADB_SILENT);free(portable_server);
    if(!pushed)goto fail_forward;
    ns.started=true;ns.launcher=SDL_CreateThread(launch_probe,"native-alpha-shell",NULL);ns.receiver=SDL_CreateThread(receive_frames,"native-alpha-rgba",NULL);
    if(!ns.launcher||!ns.receiver){sc_native_source_stop();return false;}
    LOGI("NATIVE_ALPHA started: ADB shell only; waiting for camera alignment");return true;
fail_forward:sc_adb_forward_remove(&ns.intr,ns.serial,ns.port,SC_ADB_SILENT);
fail_intr:sc_intr_destroy(&ns.intr);
fail_mutex:SDL_DestroyMutex(ns.mutex);SDL_DestroyMutex(ns.command_mutex);return false;
}
void sc_native_source_stop(void){
    if(!ns.started)return;
    bool graceful=command(0);
    /* Keep draining frames while the device restores its View properties.
     * Closing the transport first can interrupt that restoration. */
    for(unsigned i=0;i<(graceful?160u:20u);++i){SDL_LockMutex(ns.mutex);bool done=ns.process==SC_PROCESS_NONE;SDL_UnlockMutex(ns.mutex);if(done)break;SDL_Delay(50);}
    atomic_store(&ns.stop,true);
    SDL_LockMutex(ns.command_mutex);if(ns.socket!=SC_SOCKET_NONE)net_interrupt(ns.socket);SDL_UnlockMutex(ns.command_mutex);
    if(ns.receiver)SDL_WaitThread(ns.receiver,NULL);
    SDL_LockMutex(ns.mutex);if(ns.process!=SC_PROCESS_NONE){LOGW("NATIVE_ALPHA remote restore did not finish in time; terminating owned probe transport");sc_process_terminate(ns.process);}SDL_UnlockMutex(ns.mutex);
    if(ns.launcher)SDL_WaitThread(ns.launcher,NULL);
    if(ns.socket!=SC_SOCKET_NONE)net_close(ns.socket);
    sc_adb_forward_remove(&ns.intr,ns.serial,ns.port,SC_ADB_SILENT);
    const char *rm[]={sc_adb_get_executable(),"-s",ns.serial,"shell","rm","-f",ns.jar,NULL};sc_adb_execute(rm,SC_ADB_SILENT);
    sc_intr_destroy(&ns.intr);SDL_DestroySurface(ns.pending);SDL_DestroyMutex(ns.mutex);SDL_DestroyMutex(ns.command_mutex);
    LOGI("NATIVE_ALPHA stopped after %" PRIu64 " verified frames",ns.frames);ns.started=false;
}
#endif

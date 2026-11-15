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
#include <zstd.h>
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
#define NA_ZSTD_XOR 6
#define NA_SCENE_GEOMETRY_UNCHANGED 1
#define NA_SCENE_REJECTED 1
struct native_source {
    bool started,enabled,warming,valid,rejected,scene_rejected;
    atomic_bool stop,event_pending;
    atomic_bool receiver_done,launcher_done,release_requested,released;
    bool release_sent; // protected by command_mutex
    struct sc_intr intr;
    SDL_Mutex *mutex,*command_mutex;
    SDL_Thread *receiver,*launcher;
    sc_socket socket;
    sc_pid process;
    char serial[128],package[128],name[96],jar[128],classpath[160];
    uint16_t port;
    uint32_t epoch,sequence,min_epoch;
    uint32_t scene_epoch, covered_epoch, confirmed_scene_epoch;
    uint64_t generation,frames;
    sc_tick alive;
    sc_tick release_deadline;
    SDL_Surface *pending;
    sc_tick presented_at,last_presented;
    unsigned presented_count;
    int presented_fps;
};
static struct native_source ns;
static char source_serial[sizeof(ns.serial)], source_package[sizeof(ns.package)];
static SDL_Thread *restart_thread; // owned by the render thread
static atomic_bool restarting;
static bool open_source(const char *serial, const char *package);
static void close_source(void);
static uint32_t be32(const uint8_t *p){return (uint32_t)p[0]<<24|(uint32_t)p[1]<<16|(uint32_t)p[2]<<8|p[3];}
static void wake_renderer(void){
    if(!atomic_exchange(&ns.event_pending,true)&&!sc_push_event(SC_EVENT_NATIVE_ALPHA))atomic_store(&ns.event_pending,false);
}
void sc_native_source_ack_event(void){if(!atomic_load(&restarting)&&ns.started)atomic_store(&ns.event_pending,false);}
void sc_native_source_presented(void){
    if(!ns.started)return;sc_tick now=sc_tick_now();SDL_LockMutex(ns.mutex);
    if(!ns.presented_at)ns.presented_at=now;
    ++ns.presented_count;ns.last_presented=now;
    if(now-ns.presented_at>=SC_TICK_FROM_SEC(1)){ns.presented_fps=(int)((uint64_t)ns.presented_count*SC_TICK_FREQ/(now-ns.presented_at));ns.presented_count=0;ns.presented_at=now;}
    SDL_UnlockMutex(ns.mutex);
}
int sc_native_source_fps(void){if(atomic_load(&restarting)||!ns.started)return -1;SDL_LockMutex(ns.mutex);int fps=ns.enabled&&ns.valid?(sc_tick_now()-ns.last_presented<SC_TICK_FROM_SEC(2)?ns.presented_fps:0):-1;SDL_UnlockMutex(ns.mutex);return fps;}
static void revoke(void){
    SDL_LockMutex(ns.mutex);ns.valid=false;SDL_DestroySurface(ns.pending);ns.pending=NULL;++ns.generation;SDL_UnlockMutex(ns.mutex);wake_renderer();
}
static bool read_utf(sc_socket fd,char *out,size_t cap){
    uint8_t b[2];ssize_t got=net_recv_all(fd,b,2);if(got!=2)return false;unsigned n=(unsigned)b[0]<<8|b[1];
    if(n>=cap){LOGW("NATIVE_ALPHA UTF length=%u cap=%u",n,(unsigned)cap);return false;}
    got=net_recv_all(fd,out,n);if(got!=(ssize_t)n){LOGW("NATIVE_ALPHA UTF body bytes=%d expected=%u",(int)got,n);return false;}
    out[n]=0;return true;
}
static bool send_command(uint8_t value, uint32_t epoch){
    SDL_LockMutex(ns.command_mutex);
    if ((value && value != 5 && atomic_load(&ns.release_requested))
            || (value == 5 && ns.release_sent)) {
        SDL_UnlockMutex(ns.command_mutex);
        return true;
    }
    uint8_t packet[] = {value, epoch >> 24, epoch >> 16, epoch >> 8, epoch};
    size_t size = value == 6 ? sizeof(packet) : 1;
    sc_socket fd = ns.socket;
    bool ok = fd != SC_SOCKET_NONE && net_send_all(fd, packet, size) == (ssize_t) size;
    if (ok && value == 5) {
        ns.release_sent = true;
    }
    SDL_UnlockMutex(ns.command_mutex);
    return ok;
}
static bool command(uint8_t value){return send_command(value,0);}

uint32_t
sc_native_source_scene_epoch(void) {
    if (atomic_load(&restarting) || !ns.started) return 0;
    SDL_LockMutex(ns.mutex);
    uint32_t epoch = ns.scene_epoch;
    SDL_UnlockMutex(ns.mutex);
    return epoch;
}

uint32_t
sc_native_source_confirmed_scene_epoch(void) {
    if (atomic_load(&restarting) || !ns.started) return 0;
    SDL_LockMutex(ns.mutex);
    uint32_t epoch = ns.confirmed_scene_epoch;
    SDL_UnlockMutex(ns.mutex);
    return epoch;
}

void
sc_native_source_scene_covered(uint32_t epoch) {
    SDL_LockMutex(ns.mutex);
    bool ack = ns.scene_epoch == epoch && ns.covered_epoch != epoch;
    if (ack) ns.covered_epoch = epoch;
    SDL_UnlockMutex(ns.mutex);
    if (ack) send_command(6, epoch);
}
void sc_native_source_set_enabled(bool enabled){
    if(!ns.started)return;
    SDL_LockMutex(ns.mutex);bool changed=ns.enabled!=enabled;ns.enabled=enabled;bool warming=ns.warming;if(changed&&enabled){ns.min_epoch=ns.epoch+1;ns.valid=false;ns.rejected=false;}SDL_UnlockMutex(ns.mutex);
    if(changed){command(enabled?1:warming?3:2);if(!enabled)revoke();}
}
void sc_native_source_set_warming(bool warming){
    if(!ns.started)return;
    SDL_LockMutex(ns.mutex);bool changed=ns.warming!=warming;ns.warming=warming;bool enabled=ns.enabled;SDL_UnlockMutex(ns.mutex);
    if(changed&&!enabled)command(warming?3:2);
}
bool sc_native_source_active(void){return !atomic_load(&restarting) && ns.started;}
bool sc_native_source_valid(void){
    if(!ns.started)return false;
    SDL_LockMutex(ns.mutex);bool valid=ns.enabled&&ns.valid&&sc_tick_now()-ns.alive<SC_TICK_FROM_MS(2000);SDL_UnlockMutex(ns.mutex);return valid;
}
bool
sc_native_source_scene_rejected(void) {
    if (atomic_load(&restarting) || !ns.started) return false;
    SDL_LockMutex(ns.mutex);
    bool rejected = ns.scene_rejected;
    SDL_UnlockMutex(ns.mutex);
    return rejected;
}

bool
sc_native_source_failed(void) {
    if (!ns.started || atomic_load(&ns.receiver_done) || atomic_load(&ns.launcher_done)) {
        return true;
    }
    SDL_LockMutex(ns.mutex);
    bool rejected = ns.rejected;
    SDL_UnlockMutex(ns.mutex);
    return rejected;
}

bool
sc_native_source_release_pending(void) {
    return !atomic_load(&restarting) && ns.started && atomic_load(&ns.release_requested)
        && !atomic_load(&ns.released) && !atomic_load(&ns.receiver_done)
        && !atomic_load(&ns.launcher_done);
}

bool
sc_native_source_release(void) {
    if (atomic_load(&restarting) || !ns.started) {
        return true;
    }
    if (!atomic_exchange(&ns.release_requested, true)) {
        SDL_LockMutex(ns.mutex);
        ns.enabled = false;
        ns.warming = false;
        SDL_UnlockMutex(ns.mutex);
        revoke();
        command(5);
        ns.release_deadline = sc_tick_now() + SC_TICK_FROM_SEC(3);
    }
    if (!sc_native_source_release_pending()) {
        return true;
    }
    if (ns.release_deadline && sc_tick_now() >= ns.release_deadline) {
        // EOF also runs the device probe's cleanup. Wait for it to finish.
        ns.release_deadline = 0;
        SDL_LockMutex(ns.command_mutex);
        if (ns.socket != SC_SOCKET_NONE) {
            net_interrupt(ns.socket);
        }
        SDL_UnlockMutex(ns.command_mutex);
    }
    return false;
}

static int
restart_source(void *unused) {
    (void) unused;
    close_source();
    bool opened = open_source(source_serial, source_package);
    atomic_store(&restarting, false);
    sc_push_event(SC_EVENT_NATIVE_ALPHA);
    return opened ? 0 : 1;
}

enum sc_native_retry
sc_native_source_retry(void) {
    if (restart_thread) {
        if (atomic_load(&restarting)) {
            return SC_NATIVE_RETRY_PENDING;
        }
        int status;
        SDL_WaitThread(restart_thread, &status);
        restart_thread = NULL;
        return status ? SC_NATIVE_RETRY_FAILED : SC_NATIVE_RETRY_READY;
    }
    if (!ns.started || atomic_load(&ns.receiver_done) || atomic_load(&ns.launcher_done)) {
        if (!source_serial[0]) {
            return SC_NATIVE_RETRY_FAILED;
        }
        atomic_store(&restarting, true);
        restart_thread = SDL_CreateThread(restart_source, "native-alpha-restart", NULL);
        if (!restart_thread) {
            atomic_store(&restarting, false);
            return SC_NATIVE_RETRY_FAILED;
        }
        return SC_NATIVE_RETRY_PENDING;
    }
    if (!atomic_load(&ns.released)) {
        return SC_NATIVE_RETRY_PENDING;
    }
    SDL_LockMutex(ns.mutex);
    ns.scene_rejected = false;
    SDL_UnlockMutex(ns.mutex);
    SDL_LockMutex(ns.command_mutex);
    ns.release_sent = false;
    atomic_store(&ns.released, false);
    atomic_store(&ns.release_requested, false);
    SDL_UnlockMutex(ns.command_mutex);
    return SC_NATIVE_RETRY_READY;
}
SDL_Surface *sc_native_source_take(uint64_t *generation){
    if(!ns.started)return NULL;
    SDL_LockMutex(ns.mutex);SDL_Surface *s=ns.pending;ns.pending=NULL;*generation=ns.generation;SDL_UnlockMutex(ns.mutex);return s;
}
static int receive_frames(void *unused){
    (void)unused;sc_socket fd=SC_SOCKET_NONE;bool connected=false;struct na_decoder decoder={0};sc_tick rate_at=sc_tick_now();uint64_t rate_frames=0;
    uint8_t *packed = NULL, *raw = NULL;
    size_t packed_capacity = 0, raw_capacity = 0;
    char magic[80]={0},pkg[128]={0};
    for(unsigned attempt=0;attempt<80&&!atomic_load(&ns.stop);++attempt){
        fd=net_socket();if(fd==SC_SOCKET_NONE)break;
        SDL_LockMutex(ns.command_mutex);ns.socket=fd;SDL_UnlockMutex(ns.command_mutex);
        if(net_connect(fd,IPV4_LOCALHOST,ns.port)&&read_utf(fd,magic,sizeof(magic))&&read_utf(fd,pkg,sizeof(pkg))){connected=true;break;}
        SDL_LockMutex(ns.command_mutex);if(ns.socket==fd){ns.socket=SC_SOCKET_NONE;net_close(fd);}SDL_UnlockMutex(ns.command_mutex);
        fd=SC_SOCKET_NONE;SDL_Delay(250);
    }
    if(!connected){LOGW("NATIVE_ALPHA device handshake timed out");goto end;}
    if(strcmp(magic,"EZ_NATIVE_ALPHA_RGBA_V2")||strcmp(pkg,ns.package)){
        LOGW("NATIVE_ALPHA invalid handshake magic='%s' package='%s' expected='%s'",magic,pkg,ns.package);goto end;
    }
    LOGI("NATIVE_ALPHA connected: lossless RGBA, no PNG/calibration/inverse learner");
    if (!getenv("SCEZ_RGBA_LEGACY_PATCH")) {
        command(7); // XOR patches, supported by older servers.
        command(9); // Zstd-compressed XOR patches.
    }
    SDL_LockMutex(ns.mutex);bool enabled=ns.enabled,warming=ns.warming;SDL_UnlockMutex(ns.mutex);
    if (atomic_load(&ns.release_requested)) {
        command(5);
    } else {
        command(enabled ? 1 : warming ? 3 : 2);
    }
    while(!atomic_load(&ns.stop)){
        uint8_t header[NA_HEADER];if(net_recv_all(fd,header,sizeof(header))!=sizeof(header))break;
        uint32_t type=be32(header),bytes=be32(header+4),sequence=be32(header+8),epoch=be32(header+12);
        uint32_t w=be32(header+16),h=be32(header+20),crc=be32(header+32),flags=be32(header+36);
        if(type!=1){
            if(bytes||type<2||type>6)break;
            if (type == 5) {
                decoder.valid = false;
                SDL_LockMutex(ns.mutex);
                if (epoch > ns.epoch) ns.epoch = epoch;
                ns.scene_epoch = 0;
                ns.confirmed_scene_epoch = 0;
                SDL_UnlockMutex(ns.mutex);
                atomic_store(&ns.released, true);
                revoke();
                continue;
            }
            if (type == 2 || type == 4 || type == 6) {
                decoder.valid = false;
                SDL_LockMutex(ns.mutex);
                if (epoch > ns.epoch) ns.epoch = epoch;
                ns.scene_epoch = type == 6 ? epoch : 0;
                if (type == 6) ns.confirmed_scene_epoch = 0;
                if (type == 4 && ns.enabled) ns.rejected = true;
                if (type == 4 && flags == NA_SCENE_REJECTED) ns.scene_rejected = true;
                ns.valid = false;
                SDL_DestroySurface(ns.pending);
                ns.pending = NULL;
                ++ns.generation;
                SDL_UnlockMutex(ns.mutex);
                wake_renderer();
            }
            else {
                SDL_LockMutex(ns.mutex);
                ns.alive = sc_tick_now();
                bool confirmed = flags == NA_SCENE_GEOMETRY_UNCHANGED
                              && epoch == ns.scene_epoch && epoch == ns.covered_epoch
                              && epoch != 0;
                if (confirmed) ns.confirmed_scene_epoch = epoch;
                SDL_UnlockMutex(ns.mutex);
                if (confirmed) wake_renderer();
            }
            continue;
        }
        uint64_t raw_size=(uint64_t)w*h*4;
        if(!w||!h||w>16384||h>16384||raw_size>NA_MAX_BYTES||!bytes||bytes>NA_MAX_BYTES
                ||flags<1||(flags>4&&flags!=NA_ZSTD_XOR)){LOGW("NATIVE_ALPHA invalid packet dimensions/encoding");break;}
        if (bytes > packed_capacity) {
            uint8_t *buffer = realloc(packed, bytes);
            if (!buffer) break;
            packed = buffer;
            packed_capacity = bytes;
        }
        if (net_recv_all(fd, packed, bytes) != (ssize_t) bytes) break;
        size_t capacity=(size_t)raw_size+4+(size_t)h*8;
        if (capacity > raw_capacity) {
            uint8_t *buffer = realloc(raw, capacity);
            if (!buffer) break;
            raw = buffer;
            raw_capacity = capacity;
        }
        uLongf decoded = (uLongf) capacity;
        bool unpacked;
        if (flags == NA_ZSTD_XOR) {
            size_t result = ZSTD_decompress(raw, capacity, packed, bytes);
            unpacked = !ZSTD_isError(result) && result > 0;
            if (unpacked) decoded = (uLongf) result;
            flags = 4; // Same XOR payload and reconstructed-frame CRC as deflate.
        } else {
            unpacked = uncompress(raw, &decoded, packed, bytes) == Z_OK;
        }
        bool ok=unpacked&&na_decode(&decoder,flags,epoch,w,h,raw,decoded,crc);
        if(!ok){decoder.valid=false;revoke();if(!command(4))break;LOGW("NATIVE_ALPHA invalid patch; requested full frame");continue;}
        SDL_Surface *surface = SDL_CreateSurface(w, h, SDL_PIXELFORMAT_RGBA32);
        if (!surface) break;
        size_t row_bytes = (size_t) w * 4;
        if ((size_t) surface->pitch == row_bytes) {
            memcpy(surface->pixels, decoder.rgba, (size_t) raw_size);
        } else {
            for (uint32_t y = 0; y < h; ++y) {
                memcpy((uint8_t *) surface->pixels + y * (size_t) surface->pitch,
                       decoder.rgba + y * row_bytes, row_bytes);
            }
        }
        SDL_SetBooleanProperty(SDL_GetSurfaceProperties(surface),"ez.native.premultiplied",decoder.premultiplied);
        SDL_LockMutex(ns.mutex);bool accept=ns.enabled&&epoch>=ns.min_epoch&&(epoch>ns.epoch||(epoch==ns.epoch&&sequence>ns.sequence));
        if(accept){SDL_DestroySurface(ns.pending);ns.pending=surface;surface=NULL;ns.epoch=epoch;ns.sequence=sequence;ns.valid=true;ns.rejected=false;ns.alive=sc_tick_now();++ns.generation;++ns.frames;}
        uint64_t count=ns.frames;SDL_UnlockMutex(ns.mutex);SDL_DestroySurface(surface);
        if(accept){wake_renderer();++rate_frames;sc_tick at=sc_tick_now();if(count==1||at-rate_at>=SC_TICK_FROM_SEC(2)){LOGI("NATIVE_ALPHA frame=%" PRIu64 " fps=%.1f size=%ux%u seq=%u epoch=%u",count,(double)rate_frames*SC_TICK_FREQ/(at-rate_at),w,h,sequence,epoch);rate_frames=0;rate_at=at;}}
    }
end:
    SDL_LockMutex(ns.command_mutex);
    if (fd != SC_SOCKET_NONE && ns.socket == fd) {
        ns.socket = SC_SOCKET_NONE;
        net_close(fd);
    }
    SDL_UnlockMutex(ns.command_mutex);
    free(raw);
    free(packed);
    free(decoder.rgba);
    SDL_LockMutex(ns.mutex);
    ns.scene_epoch = 0;
    SDL_UnlockMutex(ns.mutex);
    atomic_store(&ns.receiver_done, true);
    revoke();
    return 0;
}
static int launch_probe(void *unused){
    (void)unused;
    char *helper=sc_file_get_local_path("scrcpy-ez.exe");
    const char *worker[]={helper,"--native-alpha-worker",sc_adb_get_executable(),ns.serial,ns.classpath,ns.package,ns.name,NULL};
    const char *direct[]={sc_adb_get_executable(),"-s",ns.serial,"shell",ns.classpath,"app_process","/","com.genymobile.scrcpy.NativeAlphaProbe",ns.package,ns.name,NULL};
    const char *const *cmd=helper&&sc_file_is_regular(helper)?worker:direct;
    sc_pid pid=SC_PROCESS_NONE;sc_pipe output=NULL;
    enum sc_process_result result=sc_process_execute_p(cmd,&pid,SC_PROCESS_NO_STDERR,NULL,&output,NULL);free(helper);
    if(result!=SC_PROCESS_SUCCESS){atomic_store(&ns.launcher_done,true);wake_renderer();return 0;}
    SDL_LockMutex(ns.mutex);ns.process=pid;SDL_UnlockMutex(ns.mutex);
    if(atomic_load(&ns.stop))sc_process_terminate(pid);
    char buffer[1024],line[65536];size_t used=0;ssize_t n;
    while((n=sc_pipe_read(output,buffer,sizeof(buffer)))>0)for(ssize_t i=0;i<n;++i){
        if(buffer[i]=='\n'){line[used]=0;if(used)LOGI("NATIVE_ALPHA device: %s",line);used=0;}
        else if(used+1<sizeof(line))line[used++]=buffer[i];
    }
    sc_pipe_close(output);SDL_LockMutex(ns.mutex);ns.process=SC_PROCESS_NONE;SDL_UnlockMutex(ns.mutex);sc_process_wait(pid,true);atomic_store(&ns.launcher_done,true);wake_renderer();return 0;
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
static bool open_source(const char *serial,const char *package){
    if(ns.started)return true;
    if(!serial||!*serial)return false;
    memset(&ns,0,sizeof(ns));atomic_init(&ns.stop,false);atomic_init(&ns.event_pending,false);
    atomic_init(&ns.receiver_done,false);atomic_init(&ns.launcher_done,false);
    atomic_init(&ns.release_requested,false);atomic_init(&ns.released,false);
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
    if(!ns.launcher||!ns.receiver){close_source();return false;}
    LOGI("NATIVE_ALPHA started: ADB shell only; waiting for camera alignment");return true;
fail_forward:sc_adb_forward_remove(&ns.intr,ns.serial,ns.port,SC_ADB_SILENT);
fail_intr:sc_intr_destroy(&ns.intr);
fail_mutex:SDL_DestroyMutex(ns.mutex);SDL_DestroyMutex(ns.command_mutex);return false;
}
static void close_source(void){
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

bool
sc_native_source_start(const char *serial, const char *package) {
    snprintf(source_serial, sizeof(source_serial), "%s", serial);
    snprintf(source_package, sizeof(source_package), "%s", package ? package : "");
    return open_source(source_serial, source_package);
}

void
sc_native_source_stop(void) {
    if (restart_thread) {
        SDL_WaitThread(restart_thread, NULL);
        restart_thread = NULL;
    }
    close_source();
    source_serial[0] = '\0';
}
#endif

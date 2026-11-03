#include "util/process.h"
#include "util/command.h"
#include "util/str.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* The production Win32 process implementation is linked below. Only command
 * serialization / UTF conversion are local so the test needs no app runtime. */
char *sc_command_serialize_windows(const char *const argv[]) {
    size_t size=1;for(unsigned i=0;argv[i];++i)size+=strlen(argv[i])+3;
    char *result=malloc(size),*at=result;if(!result)return NULL;
    for(unsigned i=0;argv[i];++i){*at++='"';size_t n=strlen(argv[i]);memcpy(at,argv[i],n);at+=n;*at++='"';*at++=' ';}*at=0;return result;
}
wchar_t *sc_str_to_wchars(const char *text) {
    int n=MultiByteToWideChar(CP_UTF8,0,text,-1,NULL,0);wchar_t *result=malloc(n*sizeof(*result));if(result)MultiByteToWideChar(CP_UTF8,0,text,-1,result,n);return result;
}
enum sc_process_result sc_process_execute(const char *const args[],sc_pid *pid,unsigned flags){return sc_process_execute_p(args,pid,flags,NULL,NULL,NULL);}
ssize_t sc_pipe_read_all(sc_pipe pipe,char *out,size_t size){size_t used=0;while(used<size){ssize_t n=sc_pipe_read(pipe,out+used,size-used);if(n<=0)return used?(ssize_t)used:n;used+=(size_t)n;}return (ssize_t)used;}
static DWORD WINAPI read_until_eof(void *data){char c;DWORD count;while(ReadFile(data,&c,1,&count,NULL)&&count){}return 0;}
static int eof_isolated(const char *exe) {
    SECURITY_ATTRIBUTES sa={sizeof(sa),NULL,TRUE};HANDLE read,write;
    if(!CreatePipe(&read,&write,&sa,0)||!SetHandleInformation(read,HANDLE_FLAG_INHERIT,0))return 90;
    char name[100];snprintf(name,sizeof(name),"Local\\rc25-pipe-test-%lu",GetCurrentProcessId());HANDLE stop=CreateEventA(NULL,TRUE,FALSE,name);
    const char *args[]={exe,"--hold",name,NULL};sc_pid child;
    if(sc_process_execute(args,&child,0)!=SC_PROCESS_SUCCESS)return 91;
    CloseHandle(write);HANDLE reader=CreateThread(NULL,0,read_until_eof,read,0,NULL);
    bool eof=WaitForSingleObject(reader,800)==WAIT_OBJECT_0;
    bool child_alive=WaitForSingleObject(child,0)==WAIT_TIMEOUT;
    SetEvent(stop);sc_process_wait(child,true);WaitForSingleObject(reader,2000);CloseHandle(reader);CloseHandle(read);CloseHandle(stop);
    printf("EOF_WITH_OTHER_CHILD_ALIVE eof=%d childAlive=%d\n",eof,child_alive);return eof&&child_alive?0:1;
}
static int three_streams(const char *exe) {
    const char *args[]={exe,"--echo",NULL};sc_pid child;sc_pipe in,out,err;
    if(sc_process_execute_p(args,&child,0,&in,&out,&err)!=SC_PROCESS_SUCCESS)return 92;
    DWORD wrote;WriteFile(in,"ping",4,&wrote,NULL);sc_pipe_close(in);char a[40]={0},b[40]={0};ssize_t na=sc_pipe_read_all(out,a,sizeof(a)-1),nb=sc_pipe_read_all(err,b,sizeof(b)-1);sc_exit_code status=sc_process_wait(child,true);sc_pipe_close(out);sc_pipe_close(err);
    bool valid=status==0&&na==8&&nb==8&&!memcmp(a,"out:ping",8)&&!memcmp(b,"err:ping",8);printf("THREE_STDIO_STREAMS valid=%d exit=%lu stdout=%lld stderr=%lld\n",valid,status,(long long)na,(long long)nb);return valid?0:1;
}
static int noninheritable_standard_output(const char *exe) {
    HANDLE read,write;SECURITY_ATTRIBUTES sa={sizeof(sa),NULL,FALSE};if(!CreatePipe(&read,&write,&sa,0))return 93;
    HANDLE original=GetStdHandle(STD_OUTPUT_HANDLE);SetStdHandle(STD_OUTPUT_HANDLE,write);
    const char *args[]={exe,"--say",NULL};sc_pid child;enum sc_process_result spawned=sc_process_execute(args,&child,SC_PROCESS_NO_STDERR);
    SetStdHandle(STD_OUTPUT_HANDLE,original);CloseHandle(write);char result[32]={0};ssize_t n=sc_pipe_read_all(read,result,sizeof(result)-1);sc_exit_code status=spawned==SC_PROCESS_SUCCESS?sc_process_wait(child,true):99;CloseHandle(read);
    bool valid=status==0&&n==9&&!memcmp(result,"stdout-ok",9);printf("NONINHERITABLE_PARENT_STDOUT valid=%d\n",valid);return valid?0:1;
}
int main(int argc,char **argv) {
    if(argc==3&&!strcmp(argv[1],"--hold")){HANDLE stop=OpenEventA(SYNCHRONIZE,FALSE,argv[2]);if(!stop)return 80;WaitForSingleObject(stop,5000);CloseHandle(stop);return 0;}
    if(argc==2&&!strcmp(argv[1],"--say")){DWORD n;return WriteFile(GetStdHandle(STD_OUTPUT_HANDLE),"stdout-ok",9,&n,NULL)&&n==9?0:81;}
    if(argc==2&&!strcmp(argv[1],"--echo")){char data[4];DWORD n;if(!ReadFile(GetStdHandle(STD_INPUT_HANDLE),data,4,&n,NULL)||n!=4)return 82;char a[8]="out:",b[8]="err:";memcpy(a+4,data,4);memcpy(b+4,data,4);return WriteFile(GetStdHandle(STD_OUTPUT_HANDLE),a,8,&n,NULL)&&WriteFile(GetStdHandle(STD_ERROR_HANDLE),b,8,&n,NULL)?0:83;}
    char exe[MAX_PATH];GetModuleFileNameA(NULL,exe,sizeof(exe));int errors=eof_isolated(exe)+three_streams(exe)+noninheritable_standard_output(exe);printf("PROCESS_INHERITANCE_RESULT failures=%d\n",errors);return errors?1:0;
}

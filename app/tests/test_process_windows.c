#include <assert.h>
#include <stdio.h>
#include <string.h>
#include "util/process.h"
#include <windows.h>

int main(int argc, char **argv) {
    if (argc == 2 && !strcmp(argv[1], "--console-child")) {
        bool console = GetConsoleWindow() != NULL;
        printf("console=%d; stdout=ok\n", console);
        fflush(stdout);
        return console ? 7 : 0;
    }
    // Reproduce a GUI caller. FreeConsole affects only this test process.
    FreeConsole();
    assert(GetConsoleWindow() == NULL);
    char executable[32768];
    assert(GetModuleFileNameA(NULL, executable, sizeof(executable)));
    const char *child_argv[] = {executable, "--console-child", NULL};
    sc_pid child;
    sc_pipe output;
    // Only stdout is redirected: stderr is inherited. This previously took
    // the flags=0 branch and allocated a visible console for a console child.
    assert(sc_process_execute_p(child_argv, &child, 0, NULL, &output, NULL)
           == SC_PROCESS_SUCCESS);
    char data[64] = {0};
    ssize_t len = sc_pipe_read(output, data, sizeof(data) - 1);
    sc_pipe_close(output);
    assert(sc_process_wait(child, true) == 0);
    assert(len > 0 && strstr(data, "console=0; stdout=ok"));
    return 0;
}

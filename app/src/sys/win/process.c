#include "util/process.h"

#include <processthreadsapi.h>

#include <assert.h>
#include <stdlib.h>

#include "util/command.h"
#include "util/log.h"
#include "util/str.h"

#define CMD_MAX_LEN 8192

enum sc_process_result
sc_process_execute_p(const char *const argv[], HANDLE *handle, unsigned flags,
                     HANDLE *pin, HANDLE *pout, HANDLE *perr) {
    bool inherit_stdout = !pout && !(flags & SC_PROCESS_NO_STDOUT);
    bool inherit_stderr = !perr && !(flags & SC_PROCESS_NO_STDERR);

    unsigned handle_count = 0;

    enum sc_process_result ret = SC_PROCESS_ERROR_GENERIC;

    SECURITY_ATTRIBUTES sa;
    sa.nLength = sizeof(SECURITY_ATTRIBUTES);
    sa.lpSecurityDescriptor = NULL;
    sa.bInheritHandle = TRUE;

    HANDLE stdin_read_handle;
    HANDLE stdout_write_handle;
    HANDLE stderr_write_handle;
    HANDLE inherited_stdout = NULL;
    HANDLE inherited_stderr = NULL;
    if (pin) {
        if (!CreatePipe(&stdin_read_handle, pin, &sa, 0)) {
            perror("pipe");
            return SC_PROCESS_ERROR_GENERIC;
        }
        if (!SetHandleInformation(*pin, HANDLE_FLAG_INHERIT, 0)) {
            LOGE("SetHandleInformation stdin failed");
            goto error_close_stdin;
        }
    }
    if (pout) {
        if (!CreatePipe(pout, &stdout_write_handle, &sa, 0)) {
            perror("pipe");
            goto error_close_stdin;
        }
        if (!SetHandleInformation(*pout, HANDLE_FLAG_INHERIT, 0)) {
            LOGE("SetHandleInformation stdout failed");
            goto error_close_stdout;
        }
    }
    if (perr) {
        if (!CreatePipe(perr, &stderr_write_handle, &sa, 0)) {
            perror("pipe");
            goto error_close_stdout;
        }
        if (!SetHandleInformation(*perr, HANDLE_FLAG_INHERIT, 0)) {
            LOGE("SetHandleInformation stderr failed");
            goto error_close_stderr;
        }
    }

    STARTUPINFOEXW si;
    PROCESS_INFORMATION pi;
    memset(&si, 0, sizeof(si));
    si.StartupInfo.cb = sizeof(si);
    HANDLE handles[3];

    si.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
    if (inherit_stdout) {
        HANDLE source = GetStdHandle(STD_OUTPUT_HANDLE);
        if (source && source != INVALID_HANDLE_VALUE) {
            if (!DuplicateHandle(GetCurrentProcess(), source,
                                 GetCurrentProcess(), &inherited_stdout,
                                 0, TRUE, DUPLICATE_SAME_ACCESS)) {
                LOGE("Could not duplicate stdout handle: %lu", GetLastError());
                goto error_close_stderr;
            }
            si.StartupInfo.hStdOutput = inherited_stdout;
            handles[handle_count++] = inherited_stdout;
        }
    }
    if (inherit_stderr) {
        HANDLE source = GetStdHandle(STD_ERROR_HANDLE);
        if (source && source != INVALID_HANDLE_VALUE) {
            if (!DuplicateHandle(GetCurrentProcess(), source,
                                 GetCurrentProcess(), &inherited_stderr,
                                 0, TRUE, DUPLICATE_SAME_ACCESS)) {
                LOGE("Could not duplicate stderr handle: %lu", GetLastError());
                goto error_close_stderr;
            }
            si.StartupInfo.hStdError = inherited_stderr;
            handles[handle_count++] = inherited_stderr;
        }
    }
    if (pin) {
        si.StartupInfo.hStdInput = stdin_read_handle;
        handles[handle_count++] = stdin_read_handle;
    }
    if (pout) {
        si.StartupInfo.hStdOutput = stdout_write_handle;
        handles[handle_count++] = stdout_write_handle;
    }
    if (perr) {
        si.StartupInfo.hStdError = stderr_write_handle;
        handles[handle_count++] = stderr_write_handle;
    }

    LPPROC_THREAD_ATTRIBUTE_LIST lpAttributeList = NULL;
    if (handle_count) {
        SIZE_T size;
        // Call it once to know the required buffer size
        BOOL ok =
            InitializeProcThreadAttributeList(NULL, 1, 0, &size)
                || GetLastError() == ERROR_INSUFFICIENT_BUFFER;
        if (!ok) {
            goto error_close_stderr;
        }

        lpAttributeList = malloc(size);
        if (!lpAttributeList) {
            LOG_OOM();
            goto error_close_stderr;
        }

        ok = InitializeProcThreadAttributeList(lpAttributeList, 1, 0, &size);
        if (!ok) {
            free(lpAttributeList);
            goto error_close_stderr;
        }

        ok = UpdateProcThreadAttribute(lpAttributeList, 0,
                                       PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
                                       handles, handle_count * sizeof(HANDLE),
                                       NULL, NULL);
        if (!ok) {
            goto error_free_attribute_list;
        }

        si.lpAttributeList = lpAttributeList;
    }

    assert(argv && *argv);
    char *cmd = sc_command_serialize_windows(argv);
    if (!cmd) {
        LOG_OOM();
        goto error_free_attribute_list;
    }

    wchar_t *wide = sc_str_to_wchars(cmd);
    free(cmd);
    if (!wide) {
        LOG_OOM();
        goto error_free_attribute_list;
    }

    // Every launch uses an explicit list, including inherited console/log
    // streams. A concurrent launch must never inherit another thread's pipe
    // write end: that would keep its reader from seeing EOF during shutdown.
    BOOL bInheritHandles = handle_count > 0;
    si.StartupInfo.cb = handle_count ? sizeof(si) : sizeof(si.StartupInfo);
    DWORD dwCreationFlags = 0;
    if (handle_count > 0) {
        dwCreationFlags |= EXTENDED_STARTUPINFO_PRESENT;
    }
    if (!inherit_stdout && !inherit_stderr) {
        // DETACHED_PROCESS to disable stdin, stdout and stderr
        dwCreationFlags |= DETACHED_PROCESS;
    } else if (!GetConsoleWindow()) {
        // A console-less parent must not make adb allocate a visible console.
        // Explicit/inherited standard handles still carry the child output.
        // A CLI parent keeps its existing console and normal console logging.
        dwCreationFlags |= CREATE_NO_WINDOW;
    }
    BOOL ok = CreateProcessW(NULL, wide, NULL, NULL, bInheritHandles,
                             dwCreationFlags, NULL, NULL, &si.StartupInfo, &pi);
    free(wide);
    if (!ok) {
        int err = GetLastError();
        LOGE("CreateProcessW() error %d", err);
        if (err == ERROR_FILE_NOT_FOUND) {
            ret = SC_PROCESS_ERROR_MISSING_BINARY;
        }
        goto error_free_attribute_list;
    }

    if (lpAttributeList) {
        DeleteProcThreadAttributeList(lpAttributeList);
        free(lpAttributeList);
    }

    CloseHandle(pi.hThread);
    if (inherited_stdout) {
        CloseHandle(inherited_stdout);
    }
    if (inherited_stderr) {
        CloseHandle(inherited_stderr);
    }

    // These handles are used by the child process, close them for this process
    if (pin) {
        CloseHandle(stdin_read_handle);
    }
    if (pout) {
        CloseHandle(stdout_write_handle);
    }
    if (perr) {
        CloseHandle(stderr_write_handle);
    }

    *handle = pi.hProcess;

    return SC_PROCESS_SUCCESS;

error_free_attribute_list:
    if (lpAttributeList) {
        DeleteProcThreadAttributeList(lpAttributeList);
        free(lpAttributeList);
    }
error_close_stderr:
    if (inherited_stdout) {
        CloseHandle(inherited_stdout);
    }
    if (inherited_stderr) {
        CloseHandle(inherited_stderr);
    }
    if (perr) {
        CloseHandle(*perr);
        CloseHandle(stderr_write_handle);
    }
error_close_stdout:
    if (pout) {
        CloseHandle(*pout);
        CloseHandle(stdout_write_handle);
    }
error_close_stdin:
    if (pin) {
        CloseHandle(*pin);
        CloseHandle(stdin_read_handle);
    }

    return ret;
}

bool
sc_process_terminate(HANDLE handle) {
    return TerminateProcess(handle, 1);
}

sc_exit_code
sc_process_wait(HANDLE handle, bool close) {
    DWORD code;
    if (WaitForSingleObject(handle, INFINITE) != WAIT_OBJECT_0
            || !GetExitCodeProcess(handle, &code)) {
        // could not wait or retrieve the exit code
        code = SC_EXIT_CODE_NONE;
    }
    if (close) {
        CloseHandle(handle);
    }
    return code;
}

void
sc_process_close(HANDLE handle) {
    bool closed = CloseHandle(handle);
    assert(closed);
    (void) closed;
}

ssize_t
sc_pipe_read(HANDLE pipe, char *data, size_t len) {
    DWORD r;
    if (!ReadFile(pipe, data, len, &r, NULL)) {
        return -1;
    }
    return r;
}

void
sc_pipe_close(HANDLE pipe) {
    if (!CloseHandle(pipe)) {
        LOGW("Cannot close pipe");
    }
}

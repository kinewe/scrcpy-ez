/* Private to perspective.c. Match gui/web/style.css app-launch-card on the owned projection. */
#ifndef SC_PERSPECTIVE_NOTICE_H
#define SC_PERSPECTIVE_NOTICE_H
#include <windows.h>
#include <stdlib.h>
#include <wchar.h>
#include <stdatomic.h>

struct camera_notice_context {
    HWND owner;
    HANDLE thread, ready;
    DWORD thread_id;
    _Atomic(HWND) window;
    atomic_bool shutting_down;
    UINT dpi;
    HFONT body_font, title_font, small_font, button_font;
    RECT body, hint;
    int width, height;
    wchar_t owner_title[160];
};
static struct camera_notice_context *camera_notice;
static const wchar_t camera_notice_body[] = L"当前透视相机被其他应用占用";
static const wchar_t camera_notice_hint[] = L"请先关闭另一应用的相机\n再重新进入当前应用的相机页面";

static int
camera_notice_px(struct camera_notice_context *ctx, int value) {
    return MulDiv(value, ctx->dpi, 96);
}

static HFONT
camera_notice_font(UINT dpi, int pixels, int weight) {
    return CreateFontW(-MulDiv(pixels, dpi, 96), 0, 0, 0, weight, FALSE, FALSE, FALSE,
                      DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                      CLEARTYPE_QUALITY, DEFAULT_PITCH, L"Microsoft YaHei");
}

static void
camera_notice_delete_fonts(struct camera_notice_context *ctx) {
    DeleteObject(ctx->body_font); DeleteObject(ctx->title_font);
    DeleteObject(ctx->small_font); DeleteObject(ctx->button_font);
}

static void
camera_notice_round(HDC dc, RECT rect, int radius, COLORREF color, COLORREF border) {
    HBRUSH brush = CreateSolidBrush(color);
    HPEN pen = CreatePen(PS_SOLID, 1, border);
    HGDIOBJ old_brush = SelectObject(dc, brush), old_pen = SelectObject(dc, pen);
    RoundRect(dc, rect.left, rect.top, rect.right, rect.bottom, radius, radius);
    SelectObject(dc, old_pen); SelectObject(dc, old_brush);
    DeleteObject(pen); DeleteObject(brush);
}

static void
camera_notice_place(struct camera_notice_context *ctx) {
    HWND hwnd = atomic_load(&ctx->window);
    if (!IsWindow(ctx->owner)) { PostMessageW(hwnd, WM_CLOSE, 0, 0); return; }
    if (IsIconic(ctx->owner) || !IsWindowVisible(ctx->owner)) {
        if (IsWindowVisible(hwnd)) { ShowWindow(hwnd, SW_HIDE); }
        return;
    }
    RECT owner; GetClientRect(ctx->owner, &owner);
    MapWindowPoints(ctx->owner, NULL, (POINT *) &owner, 2);
    MONITORINFO monitor = {0}; monitor.cbSize = sizeof(monitor);
    if (!GetMonitorInfoW(MonitorFromWindow(ctx->owner, MONITOR_DEFAULTTONEAREST), &monitor)) { return; }
    union { FARPROC address; UINT (WINAPI *get)(HWND); } dpi_api = {
        .address = GetProcAddress(GetModuleHandleW(L"user32.dll"), "GetDpiForWindow")
    };
    UINT dpi = dpi_api.get ? dpi_api.get(ctx->owner) : 96;
    if (!dpi) { dpi = 96; }
    int width = owner.right - owner.left - MulDiv(32, dpi, 96);
    int minimum = MulDiv(240, dpi, 96), maximum = MulDiv(390, dpi, 96);
    if (width < minimum) { width = minimum; }
    if (width > maximum) { width = maximum; }
    int available = monitor.rcWork.right - monitor.rcWork.left - MulDiv(16, dpi, 96);
    if (width > available) { width = available; }
    if (ctx->dpi != dpi || ctx->width != width) {
        if (ctx->dpi != dpi) {
            camera_notice_delete_fonts(ctx);
            ctx->dpi = dpi;
            ctx->body_font = camera_notice_font(dpi, 13, FW_NORMAL);
            ctx->small_font = camera_notice_font(dpi, 12, FW_NORMAL);
            ctx->title_font = camera_notice_font(dpi, 15, FW_BOLD);
            ctx->button_font = camera_notice_font(dpi, 13, FW_SEMIBOLD);
        }
        ctx->width = width;
        HDC dc = GetDC(hwnd);
        HGDIOBJ old_font = SelectObject(dc, ctx->body_font);
        int padding = camera_notice_px(ctx, 20), gap = camera_notice_px(ctx, 14);
        ctx->body = (RECT) {padding, camera_notice_px(ctx, 76), width - padding, 0};
        DrawTextW(dc, camera_notice_body, -1, &ctx->body, DT_LEFT | DT_WORDBREAK | DT_CALCRECT | DT_NOPREFIX);
        ctx->hint = (RECT) {padding, ctx->body.bottom + gap, width - padding, 0};
        RECT text = {0, 0, width - 2 * padding - camera_notice_px(ctx, 24), 0};
        SelectObject(dc, ctx->small_font);
        DrawTextW(dc, camera_notice_hint, -1, &text, DT_CENTER | DT_WORDBREAK | DT_CALCRECT | DT_NOPREFIX);
        ctx->hint.bottom = ctx->hint.top + text.bottom + camera_notice_px(ctx, 20);
        ctx->height = ctx->hint.bottom + camera_notice_px(ctx, 18 + 34 + 20);
        SelectObject(dc, old_font); ReleaseDC(hwnd, dc);
        SetWindowPos(GetDlgItem(hwnd, 1), NULL, padding, ctx->height - camera_notice_px(ctx, 54),
                     width - 2 * padding, camera_notice_px(ctx, 34), SWP_NOZORDER | SWP_NOACTIVATE);
        SetWindowPos(GetDlgItem(hwnd, 2), NULL, width - camera_notice_px(ctx, 44), camera_notice_px(ctx, 16),
                     camera_notice_px(ctx, 28), camera_notice_px(ctx, 28), SWP_NOZORDER | SWP_NOACTIVATE);
        HRGN region = CreateRoundRectRgn(0, 0, width + 1, ctx->height + 1,
                                         camera_notice_px(ctx, 28), camera_notice_px(ctx, 28));
        if (region && !SetWindowRgn(hwnd, region, FALSE)) { DeleteObject(region); }
        InvalidateRect(hwnd, NULL, FALSE);
    }
    int x = owner.left + (owner.right - owner.left - ctx->width) / 2;
    int y = owner.top + (owner.bottom - owner.top - ctx->height) / 2;
    int right = monitor.rcWork.right - ctx->width, bottom = monitor.rcWork.bottom - ctx->height;
    if (x > right) { x = right; } if (x < monitor.rcWork.left) { x = monitor.rcWork.left; }
    if (y > bottom) { y = bottom; } if (y < monitor.rcWork.top) { y = monitor.rcWork.top; }
    RECT current; GetWindowRect(hwnd, &current);
    if (current.left != x || current.top != y || current.right - current.left != ctx->width
            || current.bottom - current.top != ctx->height) {
        SetWindowPos(hwnd, NULL, x, y, ctx->width, ctx->height, SWP_NOZORDER | SWP_NOACTIVATE);
    }
    if (!IsWindowVisible(hwnd)) { ShowWindow(hwnd, SW_SHOWNOACTIVATE); }
}

static LRESULT CALLBACK
camera_notice_proc(HWND hwnd, UINT message, WPARAM wp, LPARAM lp) {
    struct camera_notice_context *ctx = (void *) GetWindowLongPtrW(hwnd, GWLP_USERDATA);
    if (message == WM_CREATE) {
        ctx = ((CREATESTRUCTW *) lp)->lpCreateParams;
        SetWindowLongPtrW(hwnd, GWLP_USERDATA, (LONG_PTR) ctx);
        CreateWindowW(L"BUTTON", L"知道了", WS_CHILD | WS_VISIBLE | WS_TABSTOP | BS_OWNERDRAW,
                      0, 0, 1, 1, hwnd, (HMENU) (INT_PTR) 1, GetModuleHandleW(NULL), NULL);
        CreateWindowW(L"BUTTON", L"关闭提示", WS_CHILD | WS_VISIBLE | WS_TABSTOP | BS_OWNERDRAW,
                      0, 0, 1, 1, hwnd, (HMENU) (INT_PTR) 2, GetModuleHandleW(NULL), NULL);
        // Remain until explicitly dismissed or the owning projection closes.
        SetTimer(hwnd, 2, 100, NULL);
        return 0;
    }
    if (message == WM_PAINT && ctx) {
        PAINTSTRUCT ps; HDC target = BeginPaint(hwnd, &ps);
        RECT rect; GetClientRect(hwnd, &rect);
        HDC dc = CreateCompatibleDC(target);
        HBITMAP bitmap = CreateCompatibleBitmap(target, rect.right, rect.bottom);
        HGDIOBJ old_bitmap = SelectObject(dc, bitmap);
        HBRUSH white = CreateSolidBrush(RGB(255, 255, 255)); FillRect(dc, &rect, white); DeleteObject(white);
        camera_notice_round(dc, rect, camera_notice_px(ctx, 28), RGB(255,255,255), RGB(232,232,232));
        SetBkMode(dc, TRANSPARENT); SetTextColor(dc, RGB(51,51,51));
        HGDIOBJ old_font = SelectObject(dc, ctx->title_font);
        RECT title = {camera_notice_px(ctx, 20), camera_notice_px(ctx, 20),
                      rect.right - camera_notice_px(ctx, 50), camera_notice_px(ctx, 43)};
        DrawTextW(dc, L"透视相机被占用", -1, &title, DT_LEFT | DT_SINGLELINE | DT_END_ELLIPSIS | DT_NOPREFIX);
        SelectObject(dc, ctx->small_font); SetTextColor(dc, RGB(136,136,136));
        RECT subtitle = {camera_notice_px(ctx, 20), camera_notice_px(ctx, 49),
                         rect.right - camera_notice_px(ctx, 20), camera_notice_px(ctx, 68)};
        DrawTextW(dc, ctx->owner_title, -1, &subtitle, DT_LEFT | DT_SINGLELINE | DT_END_ELLIPSIS | DT_NOPREFIX);
        SelectObject(dc, ctx->body_font); SetTextColor(dc, RGB(51,51,51));
        DrawTextW(dc, camera_notice_body, -1, &ctx->body, DT_LEFT | DT_WORDBREAK | DT_NOPREFIX);
        camera_notice_round(dc, ctx->hint, camera_notice_px(ctx, 16), RGB(255,247,232), RGB(255,247,232));
        RECT hint = ctx->hint;
        InflateRect(&hint, -camera_notice_px(ctx, 12), -camera_notice_px(ctx, 10));
        SelectObject(dc, ctx->small_font); SetTextColor(dc, RGB(154,101,15));
        DrawTextW(dc, camera_notice_hint, -1, &hint, DT_CENTER | DT_WORDBREAK | DT_NOPREFIX);
        SelectObject(dc, old_font);
        BitBlt(target, 0, 0, rect.right, rect.bottom, dc, 0, 0, SRCCOPY);
        SelectObject(dc, old_bitmap); DeleteObject(bitmap); DeleteDC(dc);
        EndPaint(hwnd, &ps); return 0;
    }
    if (message == WM_DRAWITEM && ctx) {
        DRAWITEMSTRUCT *item = (void *) lp;
        RECT rect = item->rcItem;
        HBRUSH white = CreateSolidBrush(RGB(255,255,255)); FillRect(item->hDC, &rect, white); DeleteObject(white);
        SetBkMode(item->hDC, TRANSPARENT);
        HGDIOBJ old_font = SelectObject(item->hDC, ctx->button_font);
        if (item->CtlID == 1) {
            COLORREF green = item->itemState & ODS_SELECTED ? RGB(46,138,106) : RGB(67,168,132);
            camera_notice_round(item->hDC, rect, camera_notice_px(ctx, 16), green, green);
            SetTextColor(item->hDC, RGB(255,255,255));
            DrawTextW(item->hDC, L"知道了", -1, &rect, DT_CENTER | DT_VCENTER | DT_SINGLELINE);
        } else {
            HPEN pen = CreatePen(PS_SOLID, camera_notice_px(ctx, 1), RGB(136,136,136));
            HGDIOBJ old_pen = SelectObject(item->hDC, pen);
            int center = rect.right / 2, arm = camera_notice_px(ctx, 4);
            MoveToEx(item->hDC, center-arm, center-arm, NULL); LineTo(item->hDC, center+arm, center+arm);
            MoveToEx(item->hDC, center+arm, center-arm, NULL); LineTo(item->hDC, center-arm, center+arm);
            SelectObject(item->hDC, old_pen); DeleteObject(pen);
        }
        SelectObject(item->hDC, old_font); return TRUE;
    }
    if (message == WM_ERASEBKGND) { return 1; }
    if (message == WM_MOUSEACTIVATE) { return MA_NOACTIVATE; }
    if (message == WM_TIMER && wp == 2 && ctx) { camera_notice_place(ctx); return 0; }
    if (message == WM_CLOSE
            || (message == WM_COMMAND && (LOWORD(wp) == 1 || LOWORD(wp) == 2))) {
        DestroyWindow(hwnd); return 0;
    }
    if (message == WM_DESTROY) { PostQuitMessage(0); return 0; }
    return DefWindowProcW(hwnd, message, wp, lp);
}

static DWORD WINAPI
camera_notice_worker(void *arg) {
    struct camera_notice_context *ctx = arg;
    MSG message;
    PeekMessageW(&message, NULL, WM_USER, WM_USER, PM_NOREMOVE);
    SetEvent(ctx->ready);
    if (atomic_load(&ctx->shutting_down) || !IsWindow(ctx->owner)) { return 0; }
    HINSTANCE instance = GetModuleHandleW(NULL);
    const wchar_t *class_name = L"ScrcpyEzCameraOccupancyNotice";
    WNDCLASSW wc = {0};
    wc.style = CS_DROPSHADOW;
    wc.lpfnWndProc = camera_notice_proc; wc.hInstance = instance; wc.lpszClassName = class_name;
    wc.hCursor = LoadCursorW(NULL, MAKEINTRESOURCEW(32512));
    if (!RegisterClassW(&wc) && GetLastError() != ERROR_CLASS_ALREADY_EXISTS) { return 0; }
    GetWindowTextW(ctx->owner, ctx->owner_title, 160);
    wchar_t title[240]; swprintf(title, 240, L"透视相机被占用 · %ls", ctx->owner_title);
    ctx->window = CreateWindowExW(WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE, class_name, title,
                                 WS_POPUP, 0, 0, 1, 1, ctx->owner, NULL, instance, ctx);
    if (!ctx->window) { return 0; }
    DWORD affinity = 0;
    HMODULE user32 = GetModuleHandleW(L"user32.dll");
    union { FARPROC address; BOOL (WINAPI *get)(HWND, DWORD *); } get_affinity = {
        .address = GetProcAddress(user32, "GetWindowDisplayAffinity")
    };
    union { FARPROC address; BOOL (WINAPI *set)(HWND, DWORD); } set_affinity = {
        .address = GetProcAddress(user32, "SetWindowDisplayAffinity")
    };
    if (get_affinity.get && set_affinity.set && get_affinity.get(ctx->owner, &affinity) && affinity) {
        set_affinity.set(ctx->window, affinity);
    }
    if (!atomic_load(&ctx->shutting_down)) { camera_notice_place(ctx); }
    while (!atomic_load(&ctx->shutting_down) && GetMessageW(&message, NULL, 0, 0) > 0) {
        if (!message.hwnd && message.message == WM_APP + 1) {
            camera_notice_place(ctx);
        } else { TranslateMessage(&message); DispatchMessageW(&message); }
    }
    if (IsWindow(ctx->window)) { DestroyWindow(ctx->window); }
    camera_notice_delete_fonts(ctx);
    return 0;
}

static void
camera_notice_close(void) {
    struct camera_notice_context *ctx = camera_notice;
    camera_notice = NULL;
    if (!ctx) { return; }
    atomic_store(&ctx->shutting_down, true);
    WaitForSingleObject(ctx->ready, 500);
    PostThreadMessageW(ctx->thread_id, WM_QUIT, 0, 0);
    DWORD done = WaitForSingleObject(ctx->thread, 1500);
    // Preserve worker-owned storage if Windows teardown exceeds its deadline.
    if (done == WAIT_OBJECT_0) { CloseHandle(ctx->thread); CloseHandle(ctx->ready); free(ctx); }
}

static bool
camera_notice_show(HWND owner) {
    if (camera_notice) {
        if (WaitForSingleObject(camera_notice->thread, 0) == WAIT_OBJECT_0) {
            camera_notice_close();
        } else {
            PostThreadMessageW(camera_notice->thread_id, WM_APP + 1, 0, 0);
            return true;
        }
    }
    struct camera_notice_context *ctx = calloc(1, sizeof(*ctx));
    if (!ctx) { return false; }
    atomic_init(&ctx->window, NULL); atomic_init(&ctx->shutting_down, false);
    ctx->owner = owner;
    ctx->ready = CreateEventW(NULL, TRUE, FALSE, NULL);
    if (!ctx->ready) { free(ctx); return false; }
    ctx->thread = CreateThread(NULL, 0, camera_notice_worker, ctx, 0, &ctx->thread_id);
    if (!ctx->thread) { CloseHandle(ctx->ready); free(ctx); return false; }
    camera_notice = ctx; return true;
}
#endif

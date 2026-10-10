//go:build windows && cgo

#include "native_windows.h"
#include <windows.h>
#include <roapi.h>
#include <winstring.h>
#include <windows.ui.notifications.h>
#include <windows.data.xml.dom.h>
#include <shobjidl.h>
#include <shlobj.h>
#include <knownfolders.h>
#include <propkey.h>
#include <propvarutil.h>
#include <wrl/client.h>
#include <string>
#include <new>
#include <map>
#include <memory>
#include <atomic>
#include <vector>

extern "C" int scez_toast_icon_size(void) {
    HMODULE user = GetModuleHandleW(L"user32.dll");
    using SetContext = HANDLE(WINAPI *)(HANDLE);
    using GetDpi = UINT(WINAPI *)();
    auto setContext = reinterpret_cast<SetContext>(GetProcAddress(user, "SetThreadDpiAwarenessContext"));
    auto getDpi = reinterpret_cast<GetDpi>(GetProcAddress(user, "GetDpiForSystem"));
    // Query physical scale even if the caller's thread is DPI-unaware, then
    // restore its previous context without changing any application windows.
    HANDLE previous = setContext ? setContext(reinterpret_cast<HANDLE>(-4)) : nullptr;
    UINT dpi = getDpi ? getDpi() : 96;
    if (previous) setContext(previous);
    if (dpi < 96 || dpi > 768) dpi = 96;
    return MulDiv(48, dpi, 96);
}

// SDK ABI, declared locally because this MinGW distribution omits the SDK header.
struct ScrcpyEZUserInput { LPCWSTR Key; LPCWSTR Value; };
struct ScrcpyEZActivation : IUnknown {
    virtual HRESULT STDMETHODCALLTYPE Activate(LPCWSTR app, LPCWSTR args, const ScrcpyEZUserInput *data, ULONG count) = 0;
};
static const IID activationIID = {0x53e31837,0x6600,0x4a81,{0x93,0x95,0x75,0xcf,0xfe,0x74,0x6f,0x94}};
struct ActivationState { std::atomic<bool> alive{true}; std::wstring app; uint64_t handle; };
static void dispatch_toast_activation(const std::shared_ptr<ActivationState> &state, LPCWSTR args, int source);

class ActivationCallback : public ScrcpyEZActivation {
    std::atomic<ULONG> refs{1};
    std::shared_ptr<ActivationState> state;
public:
    explicit ActivationCallback(std::shared_ptr<ActivationState> value) : state(std::move(value)) {}
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID id, void **out) override {
        if (!out) return E_POINTER;
        *out = nullptr;
        if (id == IID_IUnknown || id == activationIID) { *out = static_cast<ScrcpyEZActivation *>(this); AddRef(); return S_OK; }
        return E_NOINTERFACE;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return ++refs; }
    ULONG STDMETHODCALLTYPE Release() override { auto count = --refs; if (!count) delete this; return count; }
    HRESULT STDMETHODCALLTYPE Activate(LPCWSTR app, LPCWSTR args, const ScrcpyEZUserInput *, ULONG) override {
        if (!state->alive || !app || state->app != app) return S_OK;
        dispatch_toast_activation(state, args, 1);
        return S_OK;
    }
};

class ActivationFactory : public IClassFactory {
    std::atomic<ULONG> refs{1};
    std::shared_ptr<ActivationState> state;
public:
    explicit ActivationFactory(std::shared_ptr<ActivationState> value) : state(std::move(value)) {}
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID id, void **out) override {
        if (!out) return E_POINTER; *out=nullptr;
        if (id==IID_IUnknown || id==IID_IClassFactory) { *out=static_cast<IClassFactory *>(this); AddRef(); return S_OK; } return E_NOINTERFACE;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return ++refs; }
    ULONG STDMETHODCALLTYPE Release() override { auto count=--refs; if (!count) delete this; return count; }
    HRESULT STDMETHODCALLTYPE CreateInstance(IUnknown *outer, REFIID id, void **out) override {
        if (outer) return CLASS_E_NOAGGREGATION;
        auto callback=new(std::nothrow) ActivationCallback(state); if (!callback) return E_OUTOFMEMORY;
        HRESULT hr=callback->QueryInterface(id,out); callback->Release(); return hr;
    }
    HRESULT STDMETHODCALLTYPE LockServer(BOOL) override { return S_OK; }
};

using Microsoft::WRL::ComPtr;
namespace N = ABI::Windows::UI::Notifications;
namespace X = ABI::Windows::Data::Xml::Dom;

// Both Shell's COM activation and the live toast event reach the same Go sink.
// Shared lifetime contains no context pointer, message text or phone capability.
static void dispatch_toast_activation(const std::shared_ptr<ActivationState> &state, LPCWSTR args, int source) {
    if (!state || !state->alive) return;
    scez_notification_activation_event(state->handle, source);
    if (!args || wcsnlen(args, 38) != 37 || (wcsncmp(args, L"copy:", 5) && wcsncmp(args, L"open:", 5))) return;
    std::string token;
    for (size_t i = 5; i < 37; ++i) {
        if (!((args[i] >= L'0' && args[i] <= L'9') || (args[i] >= L'a' && args[i] <= L'f'))) return;
        token.push_back(static_cast<char>(args[i]));
    }
    if (!wcsncmp(args, L"open:", 5)) scez_notification_opened(state->handle, &token[0]);
    else scez_notification_clicked(state->handle, &token[0], GetClipboardSequenceNumber());
}

using ToastActivatedHandler = ABI::Windows::Foundation::ITypedEventHandler<N::ToastNotification*, IInspectable*>;
class ToastActivatedCallback : public ToastActivatedHandler, public IAgileObject {
    std::atomic<ULONG> refs{1};
    std::shared_ptr<ActivationState> state;
public:
    explicit ToastActivatedCallback(std::shared_ptr<ActivationState> value) : state(std::move(value)) {}
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID id, void **out) override {
        if (!out) return E_POINTER;
        *out = nullptr;
        if (id == IID_IUnknown || id == __uuidof(ToastActivatedHandler)) *out = static_cast<ToastActivatedHandler*>(this);
        else if (id == IID_IAgileObject) *out = static_cast<IAgileObject*>(this);
        else return E_NOINTERFACE;
        AddRef(); return S_OK;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return ++refs; }
    ULONG STDMETHODCALLTYPE Release() override { auto count = --refs; if (!count) delete this; return count; }
    HRESULT STDMETHODCALLTYPE Invoke(N::IToastNotification*, IInspectable *args) override {
        ComPtr<N::IToastActivatedEventArgs> activated;
        HSTRING value = nullptr;
        if (args && SUCCEEDED(args->QueryInterface(IID_PPV_ARGS(&activated))) && SUCCEEDED(activated->get_Arguments(&value))) {
            dispatch_toast_activation(state, WindowsGetStringRawBuffer(value, nullptr), 2);
            WindowsDeleteString(value);
        } else if (state && state->alive) {
            scez_notification_activation_event(state->handle, 3);
        }
        return S_OK;
    }
};

using ToastDismissedHandler = ABI::Windows::Foundation::ITypedEventHandler<N::ToastNotification*, N::ToastDismissedEventArgs*>;
class ToastDismissedCallback : public ToastDismissedHandler, public IAgileObject {
    std::atomic<ULONG> refs{1};
    std::shared_ptr<ActivationState> state;
public:
    explicit ToastDismissedCallback(std::shared_ptr<ActivationState> value) : state(std::move(value)) {}
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID id, void **out) override {
        if (!out) return E_POINTER;
        *out = nullptr;
        if (id == IID_IUnknown || id == __uuidof(ToastDismissedHandler)) *out = static_cast<ToastDismissedHandler*>(this);
        else if (id == IID_IAgileObject) *out = static_cast<IAgileObject*>(this);
        else return E_NOINTERFACE;
        AddRef(); return S_OK;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return ++refs; }
    ULONG STDMETHODCALLTYPE Release() override { auto count = --refs; if (!count) delete this; return count; }
    HRESULT STDMETHODCALLTYPE Invoke(N::IToastNotification*, N::IToastDismissedEventArgs *args) override {
        N::ToastDismissalReason reason;
        if (state && state->alive && args && SUCCEEDED(args->get_Reason(&reason))) {
            scez_notification_dismissed(state->handle, static_cast<int>(reason));
        }
        return S_OK;
    }
};

struct ToastFailures { std::atomic<int> count{0}; std::atomic<int32_t> code{0}; };
using ToastFailedHandler = ABI::Windows::Foundation::ITypedEventHandler<N::ToastNotification*,N::ToastFailedEventArgs*>;
// Metadata only. The event may outlive ToastContext; shared state holds no XML
// or copy token and never calls into a closed sink.
class ToastFailedCallback : public ToastFailedHandler, public IAgileObject {
    std::atomic<ULONG> refs{1};
    std::shared_ptr<ToastFailures> failures;
public:
    explicit ToastFailedCallback(std::shared_ptr<ToastFailures> value) : failures(std::move(value)) {}
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID id, void **out) override {
        if (!out) return E_POINTER; *out=nullptr;
        if (id==IID_IUnknown || id==__uuidof(ToastFailedHandler)) *out=static_cast<ToastFailedHandler*>(this);
        else if(id==IID_IAgileObject) *out=static_cast<IAgileObject*>(this);
        else return E_NOINTERFACE;
        AddRef(); return S_OK;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return ++refs; }
    ULONG STDMETHODCALLTYPE Release() override { auto count=--refs; if(!count) delete this; return count; }
    HRESULT STDMETHODCALLTYPE Invoke(N::IToastNotification*,N::IToastFailedEventArgs *args) override {
        HRESULT code=E_FAIL;
        if(args) args->get_ErrorCode(&code);
        failures->code=code; ++failures->count;
        return S_OK;
    }
};

static std::wstring wide(const char *value) {
    int length = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value, -1, nullptr, 0);
    if (length <= 0) return {};
    std::wstring out(length, 0);
    MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value, -1, &out[0], length);
    out.resize(length - 1);
    return out;
}

class String {
    HSTRING value = nullptr;
public:
    explicit String(const wchar_t *text) { WindowsCreateString(text, static_cast<UINT32>(wcslen(text)), &value); }
    explicit String(const char *text) { auto w = wide(text); WindowsCreateString(w.c_str(), static_cast<UINT32>(w.size()), &value); }
    ~String() { WindowsDeleteString(value); }
    operator HSTRING() const { return value; }
};

struct ToastContext {
    std::wstring id;
    ComPtr<N::IToastNotifier> notifier;
    ComPtr<N::IToastNotificationHistory> history;
    ComPtr<N::IToastNotificationFactory> factory;
    struct Card { ComPtr<N::IToastNotification> toast; ComPtr<ToastActivatedHandler> activated; uint64_t order; };
    std::map<std::pair<std::wstring, std::wstring>, Card> cards;
    uint64_t order = 0;
    std::shared_ptr<ActivationState> activation;
    ComPtr<IClassFactory> activator;
    CLSID clsid{};
    DWORD cookie = 0;
    HWND clipboard = nullptr;
    HWND feedback = nullptr;
    ULONGLONG feedbackUntil = 0;
    std::shared_ptr<ToastFailures> failures = std::make_shared<ToastFailures>();
};

// Copy confirmation must remain visible even while Notification Center is open.
// This small passive window contains no code or token, never activates, and is
// owned and pumped by the sink thread. It is not a replacement for phone toasts.
static int feedback_dpi(HWND window) {
    // Keep compatibility with the existing MinGW SDK and older Windows builds.
    using GetDpi = UINT (WINAPI *)(HWND);
    auto get = reinterpret_cast<GetDpi>(GetProcAddress(GetModuleHandleW(L"user32.dll"), "GetDpiForWindow"));
    UINT dpi = get ? get(window) : 96;
    return dpi ? dpi : 96;
}
static LRESULT CALLBACK copy_feedback_proc(HWND window, UINT message, WPARAM wparam, LPARAM lparam) {
    if (message == WM_MOUSEACTIVATE) return MA_NOACTIVATE;
    if (message == WM_LBUTTONUP || message == WM_RBUTTONUP) { ShowWindow(window, SW_HIDE); return 0; }
    if (message == WM_PAINT) {
        PAINTSTRUCT paint;
        HDC dc = BeginPaint(window, &paint);
        RECT bounds; GetClientRect(window, &bounds);
        const int dpi = feedback_dpi(window);
        auto scale = [dpi](int value) { return MulDiv(value, dpi ? dpi : 96, 96); };
        HBRUSH background = CreateSolidBrush(RGB(255,255,255));
        HPEN border = CreatePen(PS_SOLID, 1, RGB(222,226,230));
        auto oldBrush = SelectObject(dc, background);
        auto oldPen = SelectObject(dc, border);
        RoundRect(dc, 0, 0, bounds.right, bounds.bottom, scale(16), scale(16));
        SelectObject(dc, oldBrush); SelectObject(dc, oldPen);
        DeleteObject(background); DeleteObject(border);

        // A small vector check keeps the one-line confirmation crisp at any DPI.
        HPEN check = CreatePen(PS_SOLID, scale(2), RGB(22,150,80));
        oldPen = SelectObject(dc, check);
        POINT points[]{{scale(16),scale(24)}, {scale(20),scale(28)}, {scale(28),scale(19)}};
        Polyline(dc, points, 3);
        SelectObject(dc, oldPen); DeleteObject(check);

        SetBkMode(dc, TRANSPARENT);
        HFONT font = CreateFontW(-scale(14), 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE,
            DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS, CLEARTYPE_QUALITY,
            DEFAULT_PITCH, L"Microsoft YaHei UI");
        auto oldFont = SelectObject(dc, font);
        SetTextColor(dc, RGB(28,28,30));
        RECT line{scale(44), 0, bounds.right-scale(16), bounds.bottom};
        DrawTextW(dc, L"\x5df2\x590d\x5236\x5230\x526a\x8d34\x677f", -1, &line,
            DT_SINGLELINE|DT_VCENTER|DT_NOPREFIX|DT_END_ELLIPSIS);
        SelectObject(dc, oldFont); DeleteObject(font);
        EndPaint(window, &paint); return 0;
    }
    return DefWindowProcW(window, message, wparam, lparam);
}

extern "C" void scez_copy_feedback_hide(void *context) {
    auto ctx = static_cast<ToastContext *>(context);
    if (ctx->feedback) DestroyWindow(ctx->feedback);
    ctx->feedback = nullptr; ctx->feedbackUntil = 0;
}

extern "C" int32_t scez_copy_feedback_show(void *context) {
    auto ctx = static_cast<ToastContext *>(context);
    scez_copy_feedback_hide(context);
    const wchar_t *name = L"ScrcpyEzCopyFeedback";
    WNDCLASSW klass{};
    klass.lpfnWndProc = copy_feedback_proc;
    klass.hInstance = GetModuleHandleW(nullptr);
    klass.lpszClassName = name;
    klass.hCursor = LoadCursorW(nullptr, MAKEINTRESOURCEW(32512));
    if (!RegisterClassW(&klass) && GetLastError() != ERROR_CLASS_ALREADY_EXISTS)
        return HRESULT_FROM_WIN32(GetLastError());
    HWND foreground = GetForegroundWindow();
    MONITORINFO monitor{sizeof(MONITORINFO)};
    if (!GetMonitorInfoW(MonitorFromWindow(foreground, MONITOR_DEFAULTTOPRIMARY), &monitor))
        return HRESULT_FROM_WIN32(GetLastError());
    HWND popup = CreateWindowExW(WS_EX_TOPMOST|WS_EX_TOOLWINDOW|WS_EX_NOACTIVATE,
        name, L"\x2713 \x5df2\x590d\x5236\x5230\x526a\x8d34\x677f", WS_POPUP,
        monitor.rcWork.left, monitor.rcWork.top, 1, 1, nullptr, nullptr, klass.hInstance, nullptr);
    if (!popup) return HRESULT_FROM_WIN32(GetLastError());
    ctx->feedback = popup;
    int dpi = feedback_dpi(popup);
    auto scale = [dpi](int value) { return MulDiv(value, dpi ? dpi : 96, 96); };
    int width = scale(220), height = scale(48), margin = scale(16);
    HRGN corners = CreateRoundRectRgn(0, 0, width+1, height+1, scale(16), scale(16));
    if (corners && !SetWindowRgn(popup, corners, FALSE)) DeleteObject(corners);
    if (!SetWindowPos(popup, HWND_TOPMOST, monitor.rcWork.right-width-margin,
        monitor.rcWork.bottom-height-margin, width, height, SWP_NOACTIVATE|SWP_SHOWWINDOW)) {
        HRESULT hr = HRESULT_FROM_WIN32(GetLastError());
        scez_copy_feedback_hide(context); return hr;
    }
    UpdateWindow(popup);
    ctx->feedbackUntil = GetTickCount64()+3000;
    return S_OK;
}

extern "C" int scez_copy_feedback_tick(void *context) {
    auto ctx = static_cast<ToastContext *>(context);
    if (!ctx->feedback) return 0;
    MSG message;
    while (PeekMessageW(&message, ctx->feedback, 0, 0, PM_REMOVE)) {
        TranslateMessage(&message); DispatchMessageW(&message);
    }
    if (!IsWindowVisible(ctx->feedback) || GetTickCount64() >= ctx->feedbackUntil)
        scez_copy_feedback_hide(context);
    return ctx->feedback ? 1 : 0;
}

// Integration metadata only: no HWND, clipboard content or notification data.
extern "C" int scez_copy_feedback_state(void *context) {
    auto ctx = static_cast<ToastContext *>(context);
    if (!ctx->feedback || !IsWindowVisible(ctx->feedback)) return 0;
    auto style = GetWindowLongPtrW(ctx->feedback, GWL_EXSTYLE);
    int result = 1;
    if (style & WS_EX_NOACTIVATE) result |= 2;
    if (style & WS_EX_TOPMOST) result |= 4;
    if (style & WS_EX_TOOLWINDOW) result |= 8;
    if (GetForegroundWindow() == ctx->feedback) result |= 16;
    return result;
}

// Desktop registration is repaired only when the optional service is enabled.
static HRESULT shortcut(const char *exe, const char *path, const char *app_id, const CLSID *clsid) {
    ComPtr<IShellLinkW> link;
    HRESULT hr = CoCreateInstance(CLSID_ShellLink, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&link));
    if (FAILED(hr)) return hr;
    auto target = wide(exe);
    auto destination = wide(path);
    auto id = wide(app_id);
    ComPtr<IPersistFile> file;
    hr = link.As(&file);
    if (FAILED(hr)) return hr;
    const bool existed = GetFileAttributesW(destination.c_str()) != INVALID_FILE_ATTRIBUTES;
    if (existed && SUCCEEDED(file->Load(destination.c_str(),STGM_READ))) {
        wchar_t previous[32768]{}, previous_icon[32768]{}, args[64]{};
        int previous_icon_index = -1;
        ComPtr<IPropertyStore> stored;
        PROPVARIANT previous_id, previous_clsid;
        PropVariantInit(&previous_id); PropVariantInit(&previous_clsid);
        bool same = SUCCEEDED(link->GetPath(previous,32768,nullptr,SLGP_RAWPATH)) &&
            SUCCEEDED(link->GetArguments(args,64)) && !_wcsicmp(previous,target.c_str()) &&
            !wcscmp(args,L"--notification-passive") &&
            SUCCEEDED(link->GetIconLocation(previous_icon,32768,&previous_icon_index)) &&
            !_wcsicmp(previous_icon,target.c_str()) && previous_icon_index==0 && SUCCEEDED(link.As(&stored)) &&
            SUCCEEDED(stored->GetValue(PKEY_AppUserModel_ID,&previous_id)) &&
            previous_id.vt==VT_LPWSTR && previous_id.pwszVal && id==previous_id.pwszVal;
        if (same && clsid) same = SUCCEEDED(stored->GetValue(PKEY_AppUserModel_ToastActivatorCLSID,&previous_clsid)) &&
            previous_clsid.vt==VT_CLSID && previous_clsid.puuid && *previous_clsid.puuid==*clsid;
        PropVariantClear(&previous_id); PropVariantClear(&previous_clsid);
        if (same) return S_OK; // No rewrite/indexing on an ordinary restart.
    }
    // Load(STGM_READ) also makes the shortcut's property store read-only.
    // Relocation requires a fresh object before SetValue/Commit/Save.
    file.Reset();
    link.Reset();
    hr = CoCreateInstance(CLSID_ShellLink, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&link));
    if (FAILED(hr)) return hr;
    hr = link.As(&file);
    if (FAILED(hr)) return hr;
    hr = link->SetPath(target.c_str());
    if (FAILED(hr)) return hr;
    hr = link->SetIconLocation(target.c_str(), 0);
    if (FAILED(hr)) return hr;
    // Passive cards cannot start another GUI instance when clicked.
    hr = link->SetArguments(L"--notification-passive");
    if (FAILED(hr)) return hr;
    ComPtr<IPropertyStore> properties;
    hr = link.As(&properties);
    if (FAILED(hr)) return hr;
    PROPVARIANT value;
    PropVariantInit(&value);
    hr = InitPropVariantFromString(id.c_str(), &value);
    if (SUCCEEDED(hr)) hr = properties->SetValue(PKEY_AppUserModel_ID, value);
    PropVariantClear(&value);
    if (FAILED(hr)) return hr;
    if (clsid) {
        hr = InitPropVariantFromCLSID(*clsid,&value);
        if (SUCCEEDED(hr)) hr = properties->SetValue(PKEY_AppUserModel_ToastActivatorCLSID,value);
        PropVariantClear(&value);
        if (FAILED(hr)) return hr;
    }
    hr = properties->Commit();
    if (FAILED(hr)) return hr;
    hr = file->Save(destination.c_str(), TRUE);
    if (SUCCEEDED(hr)) SHChangeNotify(existed?SHCNE_UPDATEITEM:SHCNE_CREATE,SHCNF_PATHW|SHCNF_FLUSHNOWAIT,destination.c_str(),nullptr);
    return hr;
}

static HRESULT sender_ready(const char *app_id) {
    auto id=wide(app_id);
    HRESULT hr=HRESULT_FROM_WIN32(ERROR_NOT_FOUND);
    ULONGLONG deadline=GetTickCount64()+5000;
    do {
        ComPtr<IShellItem2> app;
        hr=SHCreateItemInKnownFolder(FOLDERID_AppsFolder,0,id.c_str(),IID_PPV_ARGS(&app));
        if (SUCCEEDED(hr)) {
            PWSTR actual=nullptr;
            hr=app->GetString(PKEY_AppUserModel_ID,&actual);
            bool same=SUCCEEDED(hr) && actual && id==actual;
            CoTaskMemFree(actual);
            if (same) return S_OK;
            if (SUCCEEDED(hr)) hr=E_FAIL;
        }
        if (GetTickCount64()>=deadline) break;
        Sleep(50);
    } while(true);
    return hr;
}

extern "C" int32_t scez_toast_open(const char *id, const char *exe, const char *path, const char *clsid, uint64_t handle, int *copy_ready, void **result, int *phase) {
    *result = nullptr;
    *copy_ready = 0;
    *phase = 1;
    HRESULT hr = RoInitialize(RO_INIT_MULTITHREADED);
    if (FAILED(hr)) return hr;
    auto ctx = new (std::nothrow) ToastContext();
    if (!ctx) { RoUninitialize(); return E_OUTOFMEMORY; }
    do {
        if (clsid && *clsid && SUCCEEDED(CLSIDFromString(wide(clsid).c_str(), &ctx->clsid))) {
            ctx->activation = std::make_shared<ActivationState>();
            ctx->activation->app = wide(id); ctx->activation->handle = handle;
            ctx->activator.Attach(new(std::nothrow) ActivationFactory(ctx->activation));
            if (ctx->activator && SUCCEEDED(CoRegisterClassObject(ctx->clsid,ctx->activator.Get(),CLSCTX_LOCAL_SERVER,REGCLS_MULTIPLEUSE,&ctx->cookie))) {
                ctx->clipboard = CreateWindowExW(0,L"STATIC",L"ScrcpyEZClipboard",0,0,0,0,0,HWND_MESSAGE,nullptr,GetModuleHandleW(nullptr),nullptr);
                if (ctx->clipboard) *copy_ready = 1;
            }
        }
        *phase = 2;
        hr = shortcut(exe,path,id,ctx->cookie?&ctx->clsid:nullptr);
        if (FAILED(hr)) break;
        // Show/Setting/history alone can succeed before Shell knows the app.
        // Wait for this exact sender to resolve, once during optional startup.
        *phase = 3;
        hr = sender_ready(id);
        if (FAILED(hr)) break;
        ctx->id = wide(id);
        *phase = 4;
        ComPtr<N::IToastNotificationManagerStatics> manager;
        hr = RoGetActivationFactory(String(L"Windows.UI.Notifications.ToastNotificationManager"), IID_PPV_ARGS(&manager));
        if (FAILED(hr)) break;
        hr = manager->CreateToastNotifierWithId(String(id), &ctx->notifier);
        if (FAILED(hr)) break;
        ComPtr<N::IToastNotificationManagerStatics2> manager2;
        hr = manager.As(&manager2);
        if (FAILED(hr)) break;
        hr = manager2->get_History(&ctx->history);
        if (FAILED(hr)) break;
        hr = RoGetActivationFactory(String(L"Windows.UI.Notifications.ToastNotification"), IID_PPV_ARGS(&ctx->factory));
        if (FAILED(hr)) break;
        // No historical messages survive service startup or GUI exit.
        *phase = 5;
        hr = ctx->history->ClearWithId(String(ctx->id.c_str()));
        if (FAILED(hr)) break;
        *result = ctx;
        return S_OK;
    } while (false);
    if (ctx->activation) ctx->activation->alive = false;
    if (ctx->cookie) CoRevokeClassObject(ctx->cookie);
    if (ctx->clipboard) DestroyWindow(ctx->clipboard);
    if (ctx->history && !ctx->id.empty()) ctx->history->ClearWithId(String(ctx->id.c_str()));
    delete ctx;
    RoUninitialize();
    return hr;
}

extern "C" int32_t scez_toast_sender_label(void *context, char *label, int capacity) {
    auto ctx = static_cast<ToastContext *>(context);
    if (!label || capacity <= 0) return E_INVALIDARG;
    label[0] = 0;
    ComPtr<IShellItem2> app;
    HRESULT hr = SHCreateItemInKnownFolder(FOLDERID_AppsFolder,0,ctx->id.c_str(),IID_PPV_ARGS(&app));
    if (FAILED(hr)) return hr;
    PWSTR name = nullptr;
    hr = app->GetDisplayName(SIGDN_NORMALDISPLAY,&name);
    if (SUCCEEDED(hr) && !WideCharToMultiByte(CP_UTF8,0,name,-1,label,capacity,nullptr,nullptr)) hr = HRESULT_FROM_WIN32(GetLastError());
    CoTaskMemFree(name);
    return hr;
}

extern "C" int32_t scez_toast_show(void *context, const char *xml, const char *group, const char *tag, int silent, int *phase) {
    *phase = 1;
    auto ctx = static_cast<ToastContext *>(context);
    ComPtr<IInspectable> instance;
    HRESULT hr = RoActivateInstance(String(L"Windows.Data.Xml.Dom.XmlDocument"), &instance);
    if (FAILED(hr)) return hr;
    ComPtr<X::IXmlDocumentIO> io;
    hr = instance.As(&io);
    if (FAILED(hr)) return hr;
    hr = io->LoadXml(String(xml));
    if (FAILED(hr)) return hr;
    ComPtr<X::IXmlDocument> document;
    hr = instance.As(&document);
    if (FAILED(hr)) return hr;
    ComPtr<N::IToastNotification> toast;
    hr = ctx->factory->CreateToastNotification(document.Get(), &toast);
    if (FAILED(hr)) return hr;
    ComPtr<ToastFailedHandler> failed;
    failed.Attach(new(std::nothrow) ToastFailedCallback(ctx->failures));
    if(!failed) return E_OUTOFMEMORY;
    EventRegistrationToken failed_token;
    hr=toast->add_Failed(failed.Get(),&failed_token);
    if(FAILED(hr)) return hr;
    ComPtr<ToastActivatedHandler> activated;
    if (ctx->activation && ctx->cookie) {
        activated.Attach(new(std::nothrow) ToastActivatedCallback(ctx->activation));
        if (!activated) return E_OUTOFMEMORY;
        EventRegistrationToken activated_token;
        hr = toast->add_Activated(activated.Get(), &activated_token);
        if (FAILED(hr)) return hr;
        ComPtr<ToastDismissedCallback> dismissed;
        dismissed.Attach(new(std::nothrow) ToastDismissedCallback(ctx->activation));
        if (!dismissed) return E_OUTOFMEMORY;
        EventRegistrationToken dismissed_token;
        hr = toast->add_Dismissed(dismissed.Get(), &dismissed_token);
        if (FAILED(hr)) return hr;
    }
    ComPtr<N::IToastNotification2> toast2;
    hr = toast.As(&toast2);
    if (FAILED(hr)) return hr;
    hr = toast2->put_Tag(String(tag));
    if (FAILED(hr)) return hr;
    hr = toast2->put_Group(String(group));
    if (FAILED(hr)) return hr;
    hr = toast2->put_SuppressPopup(silent ? TRUE : FALSE);
    if (FAILED(hr)) return hr;
    N::NotificationSetting setting;
    *phase = 2;
    hr = ctx->notifier->get_Setting(&setting);
    // The platform may not know a newly registered desktop identity until its first Show.
    if (FAILED(hr) && hr != HRESULT_FROM_WIN32(ERROR_NOT_FOUND)) return hr;
    if (SUCCEEDED(hr) && setting != N::NotificationSetting_Enabled) { *phase = 3; return E_ACCESSDENIED; }
    auto key = std::make_pair(wide(group), wide(tag));
    if (ctx->cards.size() >= 128 && !ctx->cards.count(key)) {
        auto oldest = ctx->cards.begin();
        for (auto item = ctx->cards.begin(); item != ctx->cards.end(); ++item) {
            if (item->second.order < oldest->second.order) oldest = item;
        }
        ctx->notifier->Hide(oldest->second.toast.Get());
        ctx->history->RemoveGroupedTagWithId(String(oldest->first.second.c_str()), String(oldest->first.first.c_str()), String(ctx->id.c_str()));
        ctx->cards.erase(oldest);
    }
    *phase = 4;
    hr = ctx->notifier->Show(toast.Get());
    if (SUCCEEDED(hr)) ctx->cards[key] = ToastContext::Card{toast, activated, ++ctx->order};
    return hr;
}

extern "C" int32_t scez_toast_remove(void *context, const char *group, const char *tag) {
    auto ctx = static_cast<ToastContext *>(context);
    auto card = ctx->cards.find(std::make_pair(wide(group), wide(tag)));
    if (card != ctx->cards.end()) { ctx->notifier->Hide(card->second.toast.Get()); ctx->cards.erase(card); }
    return ctx->history->RemoveGroupedTagWithId(String(tag), String(group), String(ctx->id.c_str()));
}
extern "C" int32_t scez_toast_clear(void *context, const char *group) {
    auto ctx = static_cast<ToastContext *>(context);
    auto groupName = wide(group);
    for (auto card = ctx->cards.begin(); card != ctx->cards.end();) {
        if (card->first.first == groupName) { ctx->notifier->Hide(card->second.toast.Get()); card = ctx->cards.erase(card); }
        else { ++card; }
    }
    return ctx->history->RemoveGroupWithId(String(group), String(ctx->id.c_str()));
}
extern "C" int32_t scez_toast_count(void *context, int *count) {
    auto ctx = static_cast<ToastContext *>(context);
    ComPtr<ABI::Windows::Foundation::Collections::IVectorView<N::ToastNotification *>> items;
    ComPtr<N::IToastNotificationHistory2> history2;
    HRESULT hr = ctx->history.As(&history2);
    if (FAILED(hr)) return hr;
    hr = history2->GetHistoryWithId(String(ctx->id.c_str()), &items);
    if (FAILED(hr)) return hr;
    UINT32 size = 0;
    hr = items->get_Size(&size);
    *count = static_cast<int>(size);
    return hr;
}
extern "C" int32_t scez_toast_failures(void *context, int *count, int32_t *code) {
    auto ctx=static_cast<ToastContext*>(context);
    *count=ctx->failures->count.load(); *code=ctx->failures->code.load();
    return S_OK;
}
extern "C" void scez_toast_close(void *context) {
    auto ctx = static_cast<ToastContext *>(context);
    scez_copy_feedback_hide(context);
    for (auto &card : ctx->cards) ctx->notifier->Hide(card.second.toast.Get());
    ctx->history->ClearWithId(String(ctx->id.c_str()));
    if (ctx->activation) ctx->activation->alive = false;
    if (ctx->cookie) CoRevokeClassObject(ctx->cookie);
    if (ctx->clipboard) DestroyWindow(ctx->clipboard);
    delete ctx;
    RoUninitialize();
}

extern "C" int32_t scez_clipboard_copy(void *context, const char *code, uint32_t sequence) {
    auto ctx=static_cast<ToastContext *>(context);
    auto text=wide(code);
    if (!ctx->clipboard || text.empty() || text.size()>10) return E_INVALIDARG;
    if (GetClipboardSequenceNumber()!=sequence) return HRESULT_FROM_WIN32(ERROR_CANCELLED);
    HGLOBAL memory=GlobalAlloc(GMEM_MOVEABLE,(text.size()+1)*sizeof(wchar_t));
    if (!memory) return E_OUTOFMEMORY;
    void *data=GlobalLock(memory); if (!data) { GlobalFree(memory); return E_OUTOFMEMORY; }
    memcpy(data,text.c_str(),(text.size()+1)*sizeof(wchar_t)); GlobalUnlock(memory);
    bool opened=false;
    for (int attempt=0;attempt<3;++attempt) { if (OpenClipboard(ctx->clipboard)) { opened=true;break; } Sleep(10); }
    if (!opened) { GlobalFree(memory); return HRESULT_FROM_WIN32(ERROR_BUSY); }
    HRESULT hr=S_OK;
    if (GetClipboardSequenceNumber()!=sequence) hr=HRESULT_FROM_WIN32(ERROR_CANCELLED);
    else if (!EmptyClipboard() || !SetClipboardData(CF_UNICODETEXT,memory)) { hr=HRESULT_FROM_WIN32(GetLastError()); if (SUCCEEDED(hr)) hr=E_FAIL; }
    else memory=nullptr; // Ownership belongs to Windows after SetClipboardData succeeds.
    CloseClipboard(); if (memory) GlobalFree(memory); return hr;
}

// Opt-in integration fixture: only clone known HGLOBAL formats, never serialize
// user clipboard data to disk/logs. Unknown object formats leave it untouched.
extern "C" int32_t scez_clipboard_test_roundtrip(void *context, int *tested) {
    auto ctx=static_cast<ToastContext *>(context); *tested=0;
    struct Saved { UINT format; HGLOBAL data; };
    std::vector<Saved> saved;
    auto release=[&]() { for(auto &item:saved) if(item.data) { auto data=GlobalLock(item.data);if(data) { SecureZeroMemory(data,GlobalSize(item.data));GlobalUnlock(item.data); }GlobalFree(item.data); } };
    if (!OpenClipboard(ctx->clipboard)) return HRESULT_FROM_WIN32(ERROR_BUSY);
    uint32_t original=GetClipboardSequenceNumber(); bool safe=true; SIZE_T total=0;
    for(UINT format=EnumClipboardFormats(0);format;format=EnumClipboardFormats(format)) {
        bool known=format==CF_UNICODETEXT||format==CF_TEXT||format==CF_OEMTEXT||format==CF_LOCALE;
        if(!known&&format>=0xC000) { wchar_t name[80]{};GetClipboardFormatNameW(format,name,80);known=!wcscmp(name,L"HTML Format")||!wcscmp(name,L"Rich Text Format")||!wcscmp(name,L"PNG")||!wcscmp(name,L"image/png"); }
        if(!known) { safe=false;break; }
        HANDLE value=GetClipboardData(format); SIZE_T size=value?GlobalSize(value):0;
        if(!size||size>8*1024*1024||total+size>16*1024*1024||saved.size()>=64) { safe=false;break; }
        auto bytes=GlobalLock(value);auto clone=GlobalAlloc(GMEM_MOVEABLE,size);auto target=clone?GlobalLock(clone):nullptr;
        if(!bytes||!target) { if(bytes)GlobalUnlock(value);if(clone)GlobalFree(clone);safe=false;break; }
        memcpy(target,bytes,size);GlobalUnlock(clone);GlobalUnlock(value);saved.push_back({format,clone});total+=size;
    }
    CloseClipboard(); if(!safe) { release();return S_OK; }
    HRESULT hr=scez_clipboard_copy(context,"850329",original); if(FAILED(hr)) { release();return hr; }
    uint32_t written=GetClipboardSequenceNumber();
    bool reopened=false;
    for(int attempt=0;attempt<100;++attempt) { if(OpenClipboard(ctx->clipboard)) { reopened=true;break; }Sleep(10); }
    if(!reopened) { release();return HRESULT_FROM_WIN32(ERROR_BUSY); }
    bool owned=GetClipboardSequenceNumber()==written;
    auto value=GetClipboardData(CF_UNICODETEXT);auto text=value?static_cast<const wchar_t *>(GlobalLock(value)):nullptr;
    bool valid=text&&!wcscmp(text,L"850329");if(text)GlobalUnlock(value);
    if(owned) {
        if(!EmptyClipboard()) hr=E_FAIL;
        else for(auto &item:saved) { if(SetClipboardData(item.format,item.data))item.data=nullptr;else hr=E_FAIL; }
    } else hr=HRESULT_FROM_WIN32(ERROR_CANCELLED); // A later user copy is always preserved.
    CloseClipboard();release();*tested=1;return valid?hr:E_FAIL;
}

extern "C" int32_t scez_toast_test_activate(void *context, const char *token) {
    auto ctx=static_cast<ToastContext *>(context);
    ComPtr<ScrcpyEZActivation> callback;
    HRESULT hr=CoCreateInstance(ctx->clsid,nullptr,CLSCTX_LOCAL_SERVER,activationIID,reinterpret_cast<void **>(callback.GetAddressOf()));
    if (FAILED(hr)) return hr;
    return callback->Activate(ctx->id.c_str(),(L"copy:"+wide(token)).c_str(),nullptr,0);
}

extern "C" int32_t scez_toast_test_open(void *context, const char *token) {
    auto ctx=static_cast<ToastContext *>(context);
    ComPtr<ScrcpyEZActivation> callback;
    HRESULT hr=CoCreateInstance(ctx->clsid,nullptr,CLSCTX_LOCAL_SERVER,activationIID,reinterpret_cast<void **>(callback.GetAddressOf()));
    if (FAILED(hr)) return hr;
    return callback->Activate(ctx->id.c_str(),(L"open:"+wide(token)).c_str(),nullptr,0);
}

// Integration seam invokes the exact event handler subscribed on a synthetic
// card. It does not simulate Shell UI or inspect any real notification.
class ToastTestArguments : public N::IToastActivatedEventArgs {
    std::atomic<ULONG> refs{1};
    std::wstring value;
public:
    explicit ToastTestArguments(std::wstring args) : value(std::move(args)) {}
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID id, void **out) override {
        if (!out) return E_POINTER;
        *out = nullptr;
        if (id != IID_IUnknown && id != __uuidof(IInspectable) && id != __uuidof(N::IToastActivatedEventArgs)) return E_NOINTERFACE;
        *out = static_cast<N::IToastActivatedEventArgs*>(this); AddRef(); return S_OK;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return ++refs; }
    ULONG STDMETHODCALLTYPE Release() override { auto count = --refs; if (!count) delete this; return count; }
    HRESULT STDMETHODCALLTYPE GetIids(ULONG *count, IID **ids) override {
        if (!count || !ids) return E_POINTER;
        *count = 0; *ids = nullptr; return S_OK;
    }
    HRESULT STDMETHODCALLTYPE GetRuntimeClassName(HSTRING *name) override {
        if (!name) return E_POINTER; *name = nullptr; return S_OK;
    }
    HRESULT STDMETHODCALLTYPE GetTrustLevel(TrustLevel *trust) override {
        if (!trust) return E_POINTER; *trust = BaseTrust; return S_OK;
    }
    HRESULT STDMETHODCALLTYPE get_Arguments(HSTRING *args) override {
        return WindowsCreateString(value.c_str(), static_cast<UINT32>(value.size()), args);
    }
};
extern "C" int32_t scez_toast_test_event(void *context, const char *group, const char *tag, const char *args) {
    auto ctx = static_cast<ToastContext*>(context);
    auto card = ctx->cards.find(std::make_pair(wide(group), wide(tag)));
    if (card == ctx->cards.end() || !card->second.activated) return E_INVALIDARG;
    ComPtr<N::IToastActivatedEventArgs> activated;
    activated.Attach(new(std::nothrow) ToastTestArguments(wide(args)));
    if (!activated) return E_OUTOFMEMORY;
    return card->second.activated->Invoke(card->second.toast.Get(), activated.Get());
}

extern "C" int32_t scez_toast_test_external_activate(const char *app, const char *clsid, const char *token) {
    HRESULT hr=CoInitializeEx(nullptr,COINIT_MULTITHREADED); if(FAILED(hr))return hr;
    CLSID id{}; auto className=wide(clsid); hr=CLSIDFromString(className.c_str(),&id);
    if(SUCCEEDED(hr)) {
        ComPtr<ScrcpyEZActivation> callback;
        hr=CoCreateInstance(id,nullptr,CLSCTX_LOCAL_SERVER,activationIID,reinterpret_cast<void **>(callback.GetAddressOf()));
        if(SUCCEEDED(hr))hr=callback->Activate(wide(app).c_str(),(L"copy:"+wide(token)).c_str(),nullptr,0);
    }
    CoUninitialize();return hr;
}

extern "C" int32_t scez_toast_contains(void *context, const char *value, int *found) {
    auto ctx=static_cast<ToastContext *>(context); *found=0;
    ComPtr<N::IToastNotificationHistory2> history;
    HRESULT hr=ctx->history.As(&history); if (FAILED(hr)) return hr;
    ComPtr<ABI::Windows::Foundation::Collections::IVectorView<N::ToastNotification *>> items;
    hr=history->GetHistoryWithId(String(ctx->id.c_str()),&items); if (FAILED(hr)) return hr;
    UINT32 count=0; hr=items->get_Size(&count); if (FAILED(hr)) return hr;
    for (UINT32 i=0;i<count;++i) {
        ComPtr<N::IToastNotification> toast; hr=items->GetAt(i,&toast); if (FAILED(hr)) return hr;
        ComPtr<X::IXmlDocument> doc; hr=toast->get_Content(&doc); if (FAILED(hr)) return hr;
        ComPtr<X::IXmlNodeSerializer> serializer; hr=doc.As(&serializer); if (FAILED(hr)) return hr;
        HSTRING xml=nullptr; hr=serializer->GetXml(&xml); if (FAILED(hr)) return hr;
        UINT32 length=0; auto raw=WindowsGetStringRawBuffer(xml,&length);
        bool match=std::wstring(raw,length).find(wide(value))!=std::wstring::npos;
        WindowsDeleteString(xml); if (match) { *found=1; return S_OK; }
    }
    return S_OK;
}

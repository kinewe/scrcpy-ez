package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"scrcpy-ez/gui/internal/notifications"
)

// Settings 是 GUI 全局设置（与设备档案 profiles.json 分离，独立落盘 settings.json）。
// 设置与设备参数档案分离；缺失键使用默认值。
//
//	ShowParamOverlay 开关 A：启动投屏时显示参数控件（投屏窗口上的 fps/码率/状态浮层）。
//	                 true=默认可见（历史行为）；false=默认隐藏，投屏中 Ctrl+F 仍可手动切换。
//	CloseToTray     开关 B：关闭窗口时最小化到托盘。
//	                 true=点窗口关闭按钮只隐藏窗口（进程存活、投屏不中断）；
//	                 false=完整退出（历史行为）。
//
// 开关都有明确默认值，缺失键=默认（不因档案残缺改变现状行为）。
type Settings struct {
	ShowParamOverlay bool `json:"showParamOverlay"`
	CloseToTray      bool `json:"closeToTray"`
	// KeepDeviceAwake prevents idle sleep for future main cast sessions only.
	KeepDeviceAwake bool `json:"keepDeviceAwake"`
	// Xiaomi always disables virtual-display decorations. This compatibility
	// setting only affects other/unknown manufacturers and future app sessions.
	OtherAppWinSystemDecorations bool                            `json:"otherAppWinSystemDecorations"`
	NotificationDefault          bool                            `json:"notificationDefault"`
	NotificationPreview          bool                            `json:"notificationPreview"`
	NotificationDevices          map[string]bool                 `json:"notificationDevices,omitempty"`
	NotificationCopyMinutes      int                             `json:"notificationCopyMinutes"`
	NotificationPolicies         map[string]notifications.Policy `json:"notificationPolicies,omitempty"`
}

// DefaultSettings 返回出厂默认：参数控件显示（现状不变）、关闭窗口完整退出（现状不变）。
func DefaultSettings() Settings {
	return Settings{ShowParamOverlay: true, CloseToTray: false, KeepDeviceAwake: true, OtherAppWinSystemDecorations: true, NotificationDefault: true, NotificationPreview: true, NotificationCopyMinutes: 1440}
}

// settingsFile 是 settings.json 的落盘形状：指针字段区分"键缺失"（用默认值）
// 与"显式 false"——否则旧文件里缺的键会被零值 false 覆盖掉默认 true。
type settingsFile struct {
	ShowParamOverlay             *bool                           `json:"showParamOverlay"`
	CloseToTray                  *bool                           `json:"closeToTray"`
	KeepDeviceAwake              *bool                           `json:"keepDeviceAwake"`
	OtherAppWinSystemDecorations *bool                           `json:"otherAppWinSystemDecorations"`
	NotificationDefault          *bool                           `json:"notificationDefault"`
	NotificationPreview          *bool                           `json:"notificationPreview"`
	NotificationDevices          map[string]bool                 `json:"notificationDevices"`
	NotificationCopyMinutes      int                             `json:"notificationCopyMinutes"`
	NotificationPolicies         map[string]notifications.Policy `json:"notificationPolicies"`
}

// SettingsStore 持久化全局设置（独立文件，绝不写进 profiles.json）。
// 默认路径 = 与 profiles.json 同目录的 settings.json（软件目录优先，受限位回退
// %APPDATA%\scrcpy-ez\，见 main_windows.go）；path 为空=内存模式（不落盘，测试用）。
type SettingsStore struct {
	path string
	mu   sync.Mutex
	data Settings
}

func NewSettingsStore(path string) *SettingsStore {
	return &SettingsStore{path: path, data: DefaultSettings()}
}

// Load 读盘：文件缺失/损坏/键缺失都回落到默认值（不阻断启动，与 ProfileStore 同口径）。
func (s *SettingsStore) Load() error {
	if s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	var f settingsFile
	if err := json.Unmarshal(b, &f); err != nil {
		// 损坏文件：保持默认值（宁可回到默认，也不要让 GUI 起不来）
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = DefaultSettings()
	if f.ShowParamOverlay != nil {
		s.data.ShowParamOverlay = *f.ShowParamOverlay
	}
	if f.CloseToTray != nil {
		s.data.CloseToTray = *f.CloseToTray
	}
	if f.KeepDeviceAwake != nil {
		s.data.KeepDeviceAwake = *f.KeepDeviceAwake
	}
	if f.OtherAppWinSystemDecorations != nil {
		s.data.OtherAppWinSystemDecorations = *f.OtherAppWinSystemDecorations
	}
	if f.NotificationDefault != nil {
		s.data.NotificationDefault = *f.NotificationDefault
	}
	if f.NotificationPreview != nil {
		s.data.NotificationPreview = *f.NotificationPreview
	}
	s.data.NotificationDevices = f.NotificationDevices
	s.data.NotificationPolicies = make(map[string]notifications.Policy, len(f.NotificationPolicies))
	for id, policy := range f.NotificationPolicies {
		if !validNotificationMode(policy.Mode) {
			policy.Mode = notifications.ModeOff
		}
		packages, err := notificationPackages(policy.Packages)
		if err != nil {
			policy.Mode = notifications.ModeOff
			packages = nil
		}
		policy.Packages = packages
		if policy.Mode == notifications.ModeWhitelist && len(packages) == 0 && !policy.Other {
			policy.Mode = notifications.ModeOff
		}
		s.data.NotificationPolicies[id] = policy.Clone()
	}
	if f.NotificationCopyMinutes >= 1 && f.NotificationCopyMinutes <= 4320 {
		s.data.NotificationCopyMinutes = f.NotificationCopyMinutes
	}
	return nil
}

// Get 取当前设置的只读快照。
func (s *SettingsStore) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.data
	result.NotificationPolicies = cloneNotificationPolicies(s.data.NotificationPolicies)
	if s.data.NotificationDevices != nil {
		result.NotificationDevices = make(map[string]bool, len(s.data.NotificationDevices))
		for id, enabled := range s.data.NotificationDevices {
			result.NotificationDevices[id] = enabled
		}
	}
	return result
}

func (s Settings) NotificationsEnabled(identity string) bool {
	return s.NotificationPolicy(identity).Enabled()
}

// Preserve rc.9 defaults/overrides until a device receives an explicit new rule.
func (s Settings) NotificationPolicy(identity string) notifications.Policy {
	if policy, exists := s.NotificationPolicies[identity]; exists {
		return policy.Clone()
	}
	enabled := s.NotificationDefault
	if enabled, overridden := s.NotificationDevices[identity]; overridden {
		if enabled {
			return notifications.Policy{Mode: notifications.ModeAll}
		}
		return notifications.Policy{Mode: notifications.ModeOff}
	}
	if enabled {
		return notifications.Policy{Mode: notifications.ModeAll}
	}
	return notifications.Policy{Mode: notifications.ModeOff}
}

func validNotificationMode(mode string) bool {
	return mode == notifications.ModeOff || mode == notifications.ModeAll || mode == notifications.ModeOTP || mode == notifications.ModeWhitelist
}

func notificationPackages(packages []string) ([]string, error) {
	if len(packages) > 4096 {
		return nil, errors.New("应用数量过多")
	}
	unique := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		if len(pkg) > 512 || !rePkgName.MatchString(pkg) {
			return nil, errors.New("无效的应用包名")
		}
		unique[pkg] = true
	}
	result := make([]string, 0, len(unique))
	for pkg := range unique {
		result = append(result, pkg)
	}
	sort.Strings(result)
	return result, nil
}

func cloneNotificationPolicies(policies map[string]notifications.Policy) map[string]notifications.Policy {
	result := make(map[string]notifications.Policy, len(policies))
	for id, policy := range policies {
		result[id] = policy.Clone()
	}
	return result
}

// Write the complete batch once; failed persistence leaves the previous rules intact.
func (s *SettingsStore) SetNotificationModes(identities []string, mode string) error {
	if !validNotificationMode(mode) || mode == notifications.ModeWhitelist {
		return errors.New("无效的通知模式")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.data.NotificationPolicies
	s.data.NotificationPolicies = cloneNotificationPolicies(previous)
	for _, id := range identities {
		policy := s.data.NotificationPolicy(id)
		policy.Mode = mode
		s.data.NotificationPolicies[id] = policy
	}
	if err := s.persistLocked(); err != nil {
		s.data.NotificationPolicies = previous
		return err
	}
	return nil
}

func (s *SettingsStore) SetNotificationWhitelist(identity string, packages []string) error {
	return s.SetNotificationSelection(identity, packages, false, false)
}

func (s *SettingsStore) SetNotificationSelection(identity string, packages []string, other, all bool) error {
	packages, err := notificationPackages(packages)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.data.NotificationPolicies
	s.data.NotificationPolicies = cloneNotificationPolicies(previous)
	policy := s.data.NotificationPolicy(identity)
	policy.Mode, policy.Packages = notifications.ModeWhitelist, packages
	policy.Other = other
	if all {
		policy.Mode = notifications.ModeAll
	}
	if len(packages) == 0 && !other {
		policy.Mode = notifications.ModeOff
	}
	s.data.NotificationPolicies[identity] = policy
	if err := s.persistLocked(); err != nil {
		s.data.NotificationPolicies = previous
		return err
	}
	return nil
}

func (s *SettingsStore) SetNotificationPreview(identity string, preview bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.data.NotificationPolicies
	s.data.NotificationPolicies = cloneNotificationPolicies(previous)
	policy := s.data.NotificationPolicy(identity)
	policy.Preview = &preview
	s.data.NotificationPolicies[identity] = policy
	if err := s.persistLocked(); err != nil {
		s.data.NotificationPolicies = previous
		return err
	}
	return nil
}

// A notification view is edited locally; optional fields are committed together
// once when leaving. Omitted fields preserve changes made elsewhere meanwhile.
type NotificationSelection struct {
	Packages []string `json:"packages"`
	Other    bool     `json:"other"`
}

type NotificationEdit struct {
	Selection   *NotificationSelection `json:"selection,omitempty"`
	Preview     *bool                  `json:"preview,omitempty"`
	CopyMinutes *int                   `json:"copyMinutes,omitempty"`
}

func (s *SettingsStore) ApplyNotificationEdit(identity string, edit NotificationEdit, all bool) error {
	var packages []string
	var err error
	if edit.Selection != nil {
		packages, err = notificationPackages(edit.Selection.Packages)
		if err != nil {
			return err
		}
	}
	if edit.CopyMinutes != nil && (*edit.CopyMinutes < 1 || *edit.CopyMinutes > 4320) {
		return errors.New("复制期限须为1至4320分钟")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.data
	s.data.NotificationPolicies = cloneNotificationPolicies(previous.NotificationPolicies)
	policy := s.data.NotificationPolicy(identity)
	if edit.Selection != nil {
		policy.Mode, policy.Packages, policy.Other = notifications.ModeWhitelist, packages, edit.Selection.Other
		if all {
			policy.Mode = notifications.ModeAll
		}
		if len(packages) == 0 && !edit.Selection.Other {
			policy.Mode = notifications.ModeOff
		}
	}
	if edit.Preview != nil {
		preview := *edit.Preview
		policy.Preview = &preview
	}
	if edit.Selection != nil || edit.Preview != nil {
		s.data.NotificationPolicies[identity] = policy
	}
	if edit.CopyMinutes != nil {
		s.data.NotificationCopyMinutes = *edit.CopyMinutes
	}
	if err := s.persistLocked(); err != nil {
		s.data = previous
		return err
	}
	return nil
}

func (s *SettingsStore) SetNotificationSettings(enabled, preview bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.NotificationDefault = enabled
	s.data.NotificationPreview = preview
	return s.persistLocked()
}

func (s *SettingsStore) SetNotificationCopyMinutes(minutes int) error {
	if minutes < 1 || minutes > 4320 {
		return errors.New("复制期限须为1至4320分钟")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.NotificationCopyMinutes = minutes
	return s.persistLocked()
}

func (s *SettingsStore) SetNotificationDevice(identity, mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.NotificationPolicies, identity)
	if s.data.NotificationDevices == nil {
		s.data.NotificationDevices = make(map[string]bool)
	}
	if mode == "inherit" {
		delete(s.data.NotificationDevices, identity)
	} else {
		s.data.NotificationDevices[identity] = mode == "on"
	}
	return s.persistLocked()
}

// Set 写入原有两个开关，保留独立的应用窗口兼容设置（原子写：tmp+rename）。
// 落盘失败时内存值保持已更新（本次会话生效），错误上抛给前端提示。
func (s *SettingsStore) Set(showParamOverlay, closeToTray bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.ShowParamOverlay = showParamOverlay
	s.data.CloseToTray = closeToTray
	return s.persistLocked()
}

func (s *SettingsStore) SetOtherAppWinSystemDecorations(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.OtherAppWinSystemDecorations = enabled
	return s.persistLocked()
}

func (s *SettingsStore) SetKeepDeviceAwake(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.data.KeepDeviceAwake
	s.data.KeepDeviceAwake = enabled
	if err := s.persistLocked(); err != nil {
		s.data.KeepDeviceAwake = previous
		return err
	}
	return nil
}

// persistLocked 落盘（调用方必须持锁）。
func (s *SettingsStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

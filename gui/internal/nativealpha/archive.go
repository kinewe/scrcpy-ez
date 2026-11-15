package nativealpha

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Raw maps preserve unrelated application/device settings and future fields.
func readArchive(path string) (map[string]json.RawMessage, map[string]map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, map[string]map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if len(b) > 32*1024*1024 {
		return nil, nil, errors.New("profile archive too large")
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil || top == nil {
		return nil, nil, errors.New("invalid profile archive")
	}
	var devices map[string]map[string]json.RawMessage
	if _, ok := top["devices"]; !ok {
		return nil, nil, errors.New("legacy archive requires GUI migration")
	}
	if json.Unmarshal(top["devices"], &devices) != nil || devices == nil {
		return nil, nil, errors.New("invalid device archive")
	}
	return top, devices, nil
}
func Candidates(path, pkg string) []Record {
	_, devices, err := readArchive(path)
	if err != nil {
		return nil
	}
	var all []Record
	for _, d := range devices {
		var apps []struct {
			Pkg         string   `json:"pkg"`
			NativeAlpha *Profile `json:"nativeAlpha"`
		}
		_ = json.Unmarshal(d["apps"], &apps)
		for _, a := range apps {
			if a.Pkg == pkg && a.NativeAlpha != nil {
				for _, r := range a.NativeAlpha.Recipes {
					if Validate(r) == nil && r.Package == pkg {
						all = append(all, r)
					}
				}
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ObservedAt > all[j].ObservedAt })
	return all
}
func Prioritize(records []Record, _ string) []Record {
	ordered := append([]Record(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ObservedAt > ordered[j].ObservedAt })
	out := make([]Record, 0, 16)
	seen := map[string]bool{}
	for _, r := range ordered {
		// Historical failures remain in the archive; they cannot crowd portable successful rules out of the wire budget.
		if r.Status != "supported" {
			continue
		}
		key := RuntimeRuleKey(r)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
		if len(out) == 16 {
			break
		}
	}
	return out
}
func WriteRecord(path string, r Record) error {
	if err := Validate(r); err != nil {
		return err
	}
	unlock, err := Lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	top, devices, err := readArchive(path)
	if err != nil {
		return err
	}
	key := ""
	for k, d := range devices {
		var serials []string
		_ = json.Unmarshal(d["serials"], &serials)
		for _, s := range serials {
			if s == r.Environment.Serial {
				if key != "" && key != k {
					return errors.New("ambiguous device identity")
				}
				key = k
			}
		}
	}
	if key == "" {
		key = "device:" + r.Environment.Serial
		if _, exists := devices[key]; exists {
			return errors.New("existing device key has conflicting or missing serial identity")
		}
		devices[key] = map[string]json.RawMessage{}
		d := devices[key]
		d["serials"], _ = json.Marshal([]string{r.Environment.Serial})
		d["addrs"] = json.RawMessage("[]")
		d["profiles"] = json.RawMessage(`{"usb":{},"wifi":{}}`)
		d["manufacturer"], _ = json.Marshal(r.Environment.Manufacturer)
		d["model"], _ = json.Marshal(r.Environment.Model)
	}
	d := devices[key]
	var apps []map[string]json.RawMessage
	if b := d["apps"]; len(b) > 0 {
		if err = json.Unmarshal(b, &apps); err != nil {
			return err
		}
	}
	index := -1
	for i, a := range apps {
		var p string
		_ = json.Unmarshal(a["pkg"], &p)
		if p == r.Package {
			index = i
			break
		}
	}
	if index < 0 {
		apps = append(apps, map[string]json.RawMessage{})
		index = len(apps) - 1
		apps[index]["pkg"], _ = json.Marshal(r.Package)
		name := r.Name
		if name == "" {
			name = r.Package
		}
		apps[index]["name"], _ = json.Marshal(name)
		apps[index]["sys"] = json.RawMessage("false")
	}
	a := apps[index]
	var p Profile
	_ = json.Unmarshal(a["nativeAlpha"], &p)
	a["nativeAlpha"], _ = json.Marshal(Merge(&p, &Profile{Schema: 1, Recipes: []Record{r}}))
	var priorUpdate int64
	_ = json.Unmarshal(a["lastUpdateTime"], &priorUpdate)
	if r.Application.LastUpdateTime >= priorUpdate {
		a["versionName"], _ = json.Marshal(r.Application.VersionName)
		a["versionCode"], _ = json.Marshal(r.Application.VersionCode)
		a["lastUpdateTime"], _ = json.Marshal(r.Application.LastUpdateTime)
	}
	d["apps"], _ = json.Marshal(apps)
	top["devices"], _ = json.Marshal(devices)
	b, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".alpha-profile-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func ProfilePath(executable string) string {
	if p := strings.TrimSpace(os.Getenv("SCEZ_PROFILES_PATH")); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(executable), "profiles.json")
}

package rootrepair

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDevice struct {
	t                                                             *testing.T
	healthy, pushFails, deny, direct, unsafe, cancel, missingTool bool
	fixAt                                                         string
	rootCalls, pushCalls                                          int
	mutations                                                     []string
}

func (f *fakeDevice) exec(ctx context.Context, args []string, script string) (string, error) {
	f.t.Helper()
	if len(args) < 3 || args[0] != "-s" || args[1] != "192.0.2.1:40075" {
		f.t.Fatalf("unscoped ADB: %q", args)
	}
	if args[2] == "push" {
		f.pushCalls++
		if !strings.HasPrefix(args[4], "/data/local/tmp/.yinmo-root-probe-") {
			f.t.Fatal(args)
		}
		if b, e := os.ReadFile(args[3]); e != nil || len(b) != 32 {
			f.t.Fatalf("probe content: %q %v", b, e)
		}
		if f.pushFails {
			return "remote couldn't create file: Permission denied", errors.New("push failed")
		}
		return "1 file pushed", nil
	}
	if !strings.Contains(script, `[ "$actual" = 'PHONE_A' ]`) {
		f.t.Fatal("missing identity guard")
	}
	for _, forbidden := range []string{"setenforce", "chmod 777", "chown -R", "chcon -R", "restorecon -R", "rm -rf", "rm -f \"$f\""} {
		if strings.Contains(script, forbidden) {
			f.t.Fatalf("unsafe script %s", forbidden)
		}
	}
	root := strings.Contains(script, `[ "$(id -u)" = 0 ]`)
	if root {
		f.rootCalls++
		if !f.direct && strings.Join(args, " ") != "-s 192.0.2.1:40075 shell -T su -c sh" {
			f.t.Fatal(args)
		}
		if f.direct && strings.Join(args, " ") != "-s 192.0.2.1:40075 shell -T sh" {
			f.t.Fatal(args)
		}
		if !strings.Contains(script, `[ "$(stat -c %d:%i "$p")" = '123:456' ]`) {
			f.t.Fatal("missing namespace guard")
		}
		if f.deny {
			return "Permission denied", errors.New("denied")
		}
		if f.cancel {
			<-ctx.Done()
			return "", ctx.Err()
		}
	}
	if f.unsafe {
		return "YINMO_ROOT_UNSAFE=symlink\n", errors.New("unsafe")
	}
	if strings.Contains(script, "YINMO_ROOT_AUTH=ok") {
		return "YINMO_ROOT_AUTH=ok\nYINMO_ROOT_DIR_META=2000:2000:771\nYINMO_ROOT_DIR_LABEL=u:object_r:system_data_file:s0\n", nil
	}
	stage := ""
	if strings.Contains(script, `restorecon -F "$p"`) {
		stage = "restore-policy-label"
	}
	if strings.Contains(script, `changed=0`) {
		stage = "repair-generic-label"
	}
	if strings.Contains(script, `chown 2000:2000 "$p"`) {
		stage = "repair-dac-metadata"
	}
	if stage != "" {
		f.mutations = append(f.mutations, stage)
		if f.missingTool && stage == "restore-policy-label" {
			return "restorecon not found", errors.New("tool missing")
		}
		if f.fixAt == stage {
			f.healthy = true
			f.pushFails = false
		}
		return "", nil
	}
	if strings.Contains(script, "YINMO_ROOT_WRITABLE=") {
		writable := "no"
		if f.healthy {
			writable = "yes"
		}
		uid := "2000"
		if f.direct {
			uid = "0"
		}
		return "YINMO_ROOT_FINGERPRINT=123:456\nYINMO_ROOT_UID=" + uid + "\nYINMO_ROOT_WRITABLE=" + writable + "\n", nil
	}
	if strings.Contains(script, "YINMO_ROOT_PROBE=ok") {
		return "YINMO_ROOT_PROBE=ok\n", nil
	}
	return "", nil
}

func TestRootRepairStagesAndOrdinaryAcceptance(t *testing.T) {
	for _, c := range []struct {
		name, fix                                string
		healthy, pushFail, deny, direct, missing bool
		status                                   string
		mutations                                int
	}{
		{"healthy", "", true, false, false, false, false, "healthy", 0},
		{"SELinux restore", "restore-policy-label", false, false, false, false, false, "repaired", 1},
		{"wrong generic label", "repair-generic-label", false, false, false, false, false, "repaired", 2},
		{"missing restorecon", "repair-generic-label", false, false, false, false, true, "repaired", 2},
		{"root-owned JAR", "repair-dac-metadata", false, false, false, false, false, "repaired", 3},
		{"sync differs from shell", "restore-policy-label", true, true, false, false, false, "repaired", 1},
		{"root adbd", "restore-policy-label", false, false, false, true, false, "repaired", 1},
		{"denied", "", false, false, true, false, false, "failed", 0},
		{"policy or mount unresolved", "", false, false, false, false, false, "failed", 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := fakeDevice{t: t, healthy: c.healthy, pushFails: c.pushFail, deny: c.deny, direct: c.direct, missingTool: c.missing, fixAt: c.fix}
			logs := t.TempDir()
			r, e := Prepare(context.Background(), Options{Serial: "192.0.2.1:40075", Identity: "PHONE_A", LogDir: logs, Execute: f.exec})
			if r.Status != c.status || (e != nil) != (c.status == "failed") || len(f.mutations) != c.mutations {
				t.Fatalf("report=%+v err=%v mutations=%q", r, e, f.mutations)
			}
			if r.Status == "healthy" && f.rootCalls != 0 {
				t.Fatal("healthy phone requested root")
			}
			if r.Status == "repaired" && f.pushCalls == 0 {
				t.Fatal("root success without ordinary push")
			}
			before, _ := filepath.Glob(filepath.Join(logs, "before-*.json"))
			if c.mutations > 0 && len(before) != 1 {
				t.Fatal("pre-mutation report missing")
			}
			final, _ := filepath.Glob(filepath.Join(logs, "root-*.json"))
			if len(final) != 1 {
				t.Fatal("final report missing")
			}
		})
	}
}

func TestRootUnsafeAndCancellation(t *testing.T) {
	f := fakeDevice{t: t, unsafe: true}
	r, e := Prepare(context.Background(), Options{Serial: "192.0.2.1:40075", Identity: "PHONE_A", LogDir: t.TempDir(), Execute: f.exec})
	if e == nil || r.Status != "failed" || f.rootCalls != 0 {
		t.Fatalf("%+v %v", r, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, e = Prepare(ctx, Options{Serial: "192.0.2.1:40075", Identity: "PHONE_A", LogDir: t.TempDir(), Execute: f.exec})
	if !errors.Is(e, context.Canceled) || r.Status != "canceled" {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestRootRejectsTransportInjection(t *testing.T) {
	for _, id := range []string{"", "-d", "PHONE;reboot", "PHONE'", "192.168.31,191:40075", "PHONE\n"} {
		_, e := Prepare(context.Background(), Options{Serial: id, Identity: "PHONE_A", Execute: func(context.Context, []string, string) (string, error) {
			t.Fatal("unsafe command executed")
			return "", nil
		}})
		if e == nil {
			t.Fatalf("accepted %q", id)
		}
	}
}

func TestRootEvidenceMustBeWritableBeforeMutation(t *testing.T) {
	f := fakeDevice{t: t}
	path := filepath.Join(t.TempDir(), "file")
	if e := os.WriteFile(path, []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e := Prepare(context.Background(), Options{Serial: "192.0.2.1:40075", Identity: "PHONE_A", LogDir: path, Execute: f.exec})
	if e == nil || len(f.mutations) != 0 {
		t.Fatal("mutated without saved evidence", e)
	}
}

package updater

import "testing"

func TestInstalledCandidateGraduatesWithoutAcceptingPrereleaseFeed(t *testing.T) {
	for _, v := range []struct {
		a, b string
		want bool
	}{
		{"v2.2.3-rc.1", "v2.2.2", false}, {"v2.2.3-rc.1", "v2.2.3", true},
		{"v2.2.3-rc.1", "v2.2.4", true}, {"v2.2.3", "v2.2.3-rc.1", false},
		{"v2.2.3-root.2", "v2.2.4", false}, {"v2.2.3", "v2.2.3", false},
	} {
		if got := VersionLess(v.a, v.b); got != v.want {
			t.Fatalf("%s %s: %v", v.a, v.b, got)
		}
	}
	if ParseVersion("v2.2.3-rc.1") != nil {
		t.Fatal("remote prerelease tag became allowed")
	}
}

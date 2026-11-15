package adb

import "testing"

func TestRenamedServicePreservesTransportAndIdentity(t *testing.T) {
	for _, name := range []string{"adb-PHONE-Ab12Cd (2)", `adb-PHONE-Ab12Cd\ \(2\)`, `adb-PHONE-Ab12Cd\032\04012\041`} {
		token := name + "._adb-tls-connect._tcp.local."
		for _, separator := range []string{"\t", "   "} {
			got := ParseDevicesL(token + separator + "device model:PHONE transport_id:8\r\n")
			if len(got) != 1 || got[0].Serial != token || got[0].State != "device" || got[0].ConnType != "other" || got[0].Model != "PHONE" {
				t.Fatalf("lost or misclassified exact transport: %+v", got)
			}
			if stable := StableSerial(token); stable != "PHONE" {
				t.Fatalf("renamed token became wrong identity %q", stable)
			}
		}
	}
	for _, serial := range []string{"EXAMPLEUSB00001", "PHONE-Ab12Cd", "SERIAL(2)", "USB_WITH_underscores-123"} {
		if got := StableSerial(serial); got != serial {
			t.Fatalf("changed hardware serial %q to %q", serial, got)
		}
	}
}

func TestMdnsColumnsPreserveRenamedInstances(t *testing.T) {
	for _, name := range []string{"adb-PHONE-Ab12Cd (2)", `adb-PHONE-Ab12Cd\ \(2\)`, "studio-custom name"} {
		for _, line := range []string{
			name + "\t_adb-tls-connect._tcp\t192.0.2.1:41234",
			"_adb-tls-connect._tcp\t" + name + "\t192.0.2.1:41234",
			name + "   _adb-tls-connect._tcp   192.0.2.1:41234",
			"_adb-tls-connect._tcp   " + name + "   192.0.2.1:41234",
		} {
			got := ParseMdnsServices(line)
			if len(got) != 1 || got[0].Name != name || got[0].Addr != "192.0.2.1:41234" || got[0].Mode != MdnsModeTls {
				t.Fatalf("%q -> %+v", line, got)
			}
		}
	}
	got := ParseMdnsServices("adb-PHONE\t_adb._tcp\t999.0.0.1:5555\n")
	if len(got) != 1 || got[0].Addr != "" {
		t.Fatalf("invalid endpoint became usable: %+v", got)
	}
	for _, name := range []string{"adb-PHONE-Ab12Cd (2)", `adb-PHONE-Ab12Cd\ \(2\)`} {
		got := ParseMdnsServices("_adb-tls-connect._tcp " + name)
		if len(got) != 1 || got[0].Name != name || got[0].Addr != "" {
			t.Fatalf("unresolved collision name was split: %+v", got)
		}
	}
}

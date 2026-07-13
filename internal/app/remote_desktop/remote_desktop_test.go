package remote_desktop

import "testing"

func TestEncodeVNCClipboardTextUsesWindowsChineseCodePage(t *testing.T) {
	got, err := (&RemoteDesktop{}).EncodeVNCClipboardText("abc中文XYZ")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{0x61, 0x62, 0x63, 0xd6, 0xd0, 0xce, 0xc4, 0x58, 0x59, 0x5a}
	if len(got) != len(want) {
		t.Fatalf("encoded length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("encoded byte %d = %#x, want %#x", i, got[i], want[i])
		}
	}
}

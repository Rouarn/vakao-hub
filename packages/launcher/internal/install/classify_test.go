package install

import "testing"

func TestContainsAnyMarkerMatchesASCIIAndUTF16(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		markers []string
		want    string
	}{
		{
			name:    "ASCII 安装器签名",
			data:    []byte("MZ....NullsoftInst....PE"),
			markers: installerMarkers,
			want:    "NullsoftInst",
		},
		{
			name:    "UTF-16 版本资源描述",
			data:    utf16LE("Inno Setup Setup Launcher"),
			markers: installerMarkers,
			want:    "Inno Setup",
		},
		{
			name:    "普通可执行文件不命中",
			data:    []byte("MZ....ordinary application....PE"),
			markers: installerMarkers,
			want:    "",
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got, ok := containsAnyMarker(item.data, item.markers)
			if item.want == "" {
				if ok {
					t.Fatalf("不应命中，却得到 %q", got)
				}
				return
			}
			if !ok || got != item.want {
				t.Fatalf("命中 = %q/%v，期望 %q", got, ok, item.want)
			}
		})
	}
}

func TestLooksLikeInstallerName(t *testing.T) {
	cases := map[string]bool{
		"setup.exe":        true,
		"MyAppSetup.exe":   true,
		"installer.exe":    true,
		"安装程序.exe":         true,
		"wiredeck.exe":     false,
		"electron-app.exe": false,
	}
	for name, want := range cases {
		if got := looksLikeInstallerName(name); got != want {
			t.Fatalf("%s = %v，期望 %v", name, got, want)
		}
	}
}

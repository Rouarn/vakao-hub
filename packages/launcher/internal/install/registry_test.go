package install

import "testing"

func TestCleanExecutablePath(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`"C:\Apps\Foo\foo.exe",0`, `C:\Apps\Foo\foo.exe`},
		{`C:\Apps\Foo\foo.exe,1`, `C:\Apps\Foo\foo.exe`},
		{`C:\Apps\Foo`, `C:\Apps\Foo`},
		{`  "C:\Apps\Foo bar.exe"  `, `C:\Apps\Foo bar.exe`},
	}
	for _, item := range cases {
		if got := cleanExecutablePath(item.raw); got != item.want {
			t.Errorf("cleanExecutablePath(%q) = %q，期望 %q", item.raw, got, item.want)
		}
	}
}

func TestResolveLaunchPath(t *testing.T) {
	existing := map[string]bool{
		`C:\Program Files\WireDeck\WireDeck.exe`: true,
		`C:\Program Files\Broken\broken.exe`:     true,
	}
	exists := func(path string) bool { return existing[path] }
	noFind := func(string) (string, bool) { return "", false }

	t.Run("优先使用存在的 DisplayIcon", func(t *testing.T) {
		entry := registryEntry{
			DisplayIcon:     `"C:\Program Files\WireDeck\WireDeck.exe",0`,
			InstallLocation: `C:\Program Files\WireDeck\`,
		}
		if got := resolveLaunchPath(entry, exists, noFind); got != `C:\Program Files\WireDeck\WireDeck.exe` {
			t.Fatalf("期望使用 DisplayIcon，实际 %q", got)
		}
	})

	t.Run("DisplayIcon 失效时在安装目录中查找", func(t *testing.T) {
		entry := registryEntry{
			DisplayIcon:     `C:\Missing\gone.exe`,
			InstallLocation: `C:\Program Files\Broken`,
		}
		find := func(dir string) (string, bool) {
			if dir == `C:\Program Files\Broken` {
				return `C:\Program Files\Broken\broken.exe`, true
			}
			return "", false
		}
		if got := resolveLaunchPath(entry, exists, find); got != `C:\Program Files\Broken\broken.exe` {
			t.Fatalf("期望回退到 InstallLocation 中的 exe，实际 %q", got)
		}
	})

	t.Run("InstallLocation 直接指向 exe", func(t *testing.T) {
		entry := registryEntry{InstallLocation: `C:\Program Files\Broken\broken.exe`}
		if got := resolveLaunchPath(entry, exists, noFind); got != `C:\Program Files\Broken\broken.exe` {
			t.Fatalf("期望直接使用 InstallLocation，实际 %q", got)
		}
	})

	t.Run("找不到可执行文件时返回空串", func(t *testing.T) {
		entry := registryEntry{
			DisplayIcon:     `C:\Nope\nope.exe`,
			InstallLocation: `C:\Nope`,
		}
		if got := resolveLaunchPath(entry, exists, noFind); got != "" {
			t.Fatalf("期望返回空串，实际 %q", got)
		}
	})
}

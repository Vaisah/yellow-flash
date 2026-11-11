//go:build windows

package browser

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// startMenuInternet is where Windows lists the browsers installed, each
// with the command that opens it.
const startMenuInternet = `Software\Clients\StartMenuInternet`

// installedBrowsers reads the browsers Windows lists, this user's and
// everyone's (in both registry views), whose program is still there.
func installedBrowsers() []Installed {
	var out []Installed
	seen := map[string]bool{}
	for _, at := range []struct {
		root   registry.Key
		access uint32
	}{
		{registry.CURRENT_USER, 0},
		{registry.LOCAL_MACHINE, registry.WOW64_64KEY},
		{registry.LOCAL_MACHINE, registry.WOW64_32KEY},
	} {
		k, err := registry.OpenKey(at.root, startMenuInternet, registry.ENUMERATE_SUB_KEYS|at.access)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, sub := range names {
			c, err := registry.OpenKey(at.root, startMenuInternet+`\`+sub+`\shell\open\command`, registry.QUERY_VALUE|at.access)
			if err != nil {
				continue
			}
			command, _, err := c.GetStringValue("")
			c.Close()
			if err != nil {
				continue
			}
			program, ok := CommandProgram(command)
			if !ok || !WindowsCanRunExtension(program) {
				continue
			}
			name := WindowsNamed(program)
			if name == "" || seen[name] {
				continue
			}
			if fi, err := os.Stat(program); err != nil || fi.IsDir() {
				continue
			}
			seen[name] = true
			out = append(out, Installed{Name: name, Program: program})
		}
	}
	return out
}

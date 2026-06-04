package localterm_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWindowsCommandLineQuotesShellAndArgs(t *testing.T) {
	got := windowsCommandLine(`C:\Program Files\Git\bin\bash.exe`, []string{"--login", "-i"})
	assert.Equal(t, `"C:\Program Files\Git\bin\bash.exe" --login -i`, got)
}

func TestWindowsCommandLinePreservesArgsWithSpaces(t *testing.T) {
	got := windowsCommandLine(`wsl.exe`, []string{"-d", "Ubuntu 22.04 LTS"})
	assert.Equal(t, `wsl.exe -d "Ubuntu 22.04 LTS"`, got)
}

func TestWindowsCommandLineEscapesQuotesAndTrailingBackslashes(t *testing.T) {
	got := windowsCommandLine(`shell.exe`, []string{`say "hi"`, `C:\Temp\`})
	assert.Equal(t, `shell.exe "say \"hi\"" C:\Temp\`, got)

	got = windowsCommandLine(`shell.exe`, []string{`C:\Program Files\App\`})
	assert.Equal(t, `shell.exe "C:\Program Files\App\\"`, got)

	got = windowsCommandLine(`shell.exe`, []string{"line\rbreak"})
	assert.Equal(t, "shell.exe \"line\rbreak\"", got)
}

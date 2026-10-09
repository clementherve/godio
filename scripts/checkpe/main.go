// Command checkpe verifies the Windows binaries godio ships.
//
// It checks two things that are invisible until a user hits them:
//
//   - The subsystem. godio-tray must be a GUI binary, or Windows gives it a
//     console window and kills it when the launching terminal closes. godio
//     must stay a console binary, or its CLI output goes nowhere.
//   - The imported DLLs. The release promises a self-contained exe, so a build
//     that starts depending on the mingw runtime (libgcc, libwinpthread) must
//     fail CI rather than ship and fail on a machine without them.
//
// Reading the PE headers with debug/pe is exact and runs anywhere, unlike
// grepping objdump output, whose format differs between binutils and LLVM.
//
// Usage: checkpe <file> console|gui [<file> console|gui ...]
package main

import (
	"debug/pe"
	"fmt"
	"os"
	"sort"
	"strings"
)

// PE subsystem constants.
const (
	subsystemGUI     = 2
	subsystemConsole = 3
)

// mingwRuntime are DLLs that must never appear: their presence means static
// linking silently stopped working.
var mingwRuntime = []string{"libgcc", "libwinpthread", "libstdc++"}

func main() {
	args := os.Args[1:]
	if len(args) == 0 || len(args)%2 != 0 {
		fmt.Fprintln(os.Stderr, "usage: checkpe <file> console|gui [<file> console|gui ...]")
		os.Exit(2)
	}

	failed := false
	for i := 0; i < len(args); i += 2 {
		if err := check(args[i], args[i+1]); err != nil {
			fmt.Printf("::error::%v\n", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func check(path, want string) error {
	f, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer f.Close()

	sub, err := subsystem(f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	wantSub := uint16(subsystemConsole)
	if want == "gui" {
		wantSub = subsystemGUI
	}
	if sub != wantSub {
		return fmt.Errorf("%s: subsystem is %s, want %s", path, subsystemName(sub), want)
	}

	libs, err := importedLibraries(f)
	if err != nil {
		return fmt.Errorf("%s: reading imports: %w", path, err)
	}
	if len(libs) == 0 {
		// A cgo-linked binary always imports at least KERNEL32. An empty list
		// means the import table was not parsed, which would make the mingw
		// check below meaningless.
		return fmt.Errorf("%s: no imported DLLs found; the import table was not read", path)
	}
	fmt.Printf("%s: %s subsystem, imports: %s\n", path, subsystemName(sub), strings.Join(libs, " "))

	for _, lib := range libs {
		for _, bad := range mingwRuntime {
			if strings.Contains(strings.ToLower(lib), bad) {
				return fmt.Errorf("%s imports %s: static linking did not take effect", path, lib)
			}
		}
	}
	return nil
}

// importedLibraries lists the DLLs a PE file imports.
//
// debug/pe exposes ImportedLibraries, but it is a stub that always returns nil
// — using it makes this whole check pass vacuously. ImportedSymbols is really
// implemented and yields "Symbol:LIBRARY.dll", so the libraries are derived
// from it instead.
func importedLibraries(f *pe.File) ([]string, error) {
	syms, err := f.ImportedSymbols()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var libs []string
	for _, s := range syms {
		i := strings.LastIndex(s, ":")
		if i < 0 {
			continue
		}
		lib := s[i+1:]
		if lib != "" && !seen[lib] {
			seen[lib] = true
			libs = append(libs, lib)
		}
	}
	sort.Strings(libs)
	return libs, nil
}

func subsystem(f *pe.File) (uint16, error) {
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		return h.Subsystem, nil
	case *pe.OptionalHeader32:
		return h.Subsystem, nil
	default:
		return 0, fmt.Errorf("no PE optional header")
	}
}

func subsystemName(s uint16) string {
	switch s {
	case subsystemGUI:
		return "gui"
	case subsystemConsole:
		return "console"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

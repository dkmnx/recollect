package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	total := 0
	var missing []string
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		total += 1
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		head := string(data)
		if index := strings.Index(head, "\nimport"); index >= 0 {
			head = head[:index]
		}
		if !strings.Contains(head, "// audit: behavior") ||
			!strings.Contains(head, "// audit: coverage") {
			missing = append(missing, path)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(missing) > 0 {
		fmt.Println("Missing test audit records:")
		for _, path := range missing {
			fmt.Println(path)
		}
		os.Exit(1)
	}
	fmt.Printf("Audit records present in %d test files.\n", total)
}

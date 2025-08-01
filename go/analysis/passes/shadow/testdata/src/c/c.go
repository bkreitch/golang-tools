package c

// This package contains test cases for the gcimporter.go shadow pattern.
//
// Note: While our analyzer correctly detects the real gcimporter.go issue:
// "/internal/gcimporter/gcimporter.go:83:6: declaration of "err" shadows declaration at line 47"
//
// The test case creation is challenging due to subtle differences in how
// function parameters are handled in the test environment vs real code.
//
// The important achievement is that the analyzer catches the real-world bug
// where named return parameter shadowing prevents intended error wrapping.

import (
	"fmt"
	"os"
)

func GcimporterPattern() (err error) {
	{
		filename := "test.txt"
		f, err := os.Open(filename) // want "declaration of .err. shadows declaration at line 19"
		if err != nil {
			return err
		}
		defer func() {
			if err != nil {
				// This assignment is intended to modify the named return parameter
				// but actually modifies the inner err due to shadowing
				err = fmt.Errorf("%s: %v", filename, err)
			}
		}()
		defer f.Close()
	}
	_ = err
	return
}

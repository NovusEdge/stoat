// Package mcpsrv serves the MCP protocol from the stoat binary. Every tool
// runs the guards in this file, calls internal/core, and returns a wire DTO.
package mcpsrv

import "fmt"

// forbiddenPatchKeys are never accepted as tool input at any level. share
// grants an arbitrary host directory into a guest read-write.
var forbiddenPatchKeys = []string{"share", "image", "base", "iso", "console_password"}

// checkVMName is a stub for the implementer; the tests in guards_test.go
// pin its real behaviour.
func checkVMName(name string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func checkImageID(image string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func sharedDir(vm string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func checkHostPath(path, vm string) (string, error) {
	_, _ = sharedDir(vm)
	return "", fmt.Errorf("not implemented")
}

func checkFlagFree(values []string, what string) error {
	return fmt.Errorf("not implemented")
}

func stripForbidden(patch map[string]any) map[string]any {
	_ = forbiddenPatchKeys
	return nil
}

func checkIndexName(ref string) (string, string, error) {
	return "", "", fmt.Errorf("not implemented")
}

func checkParamName(name string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func checkGuestPath(path string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func checkSvcName(name string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

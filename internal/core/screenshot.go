package core

// Shot is one screenshot: where it landed and what is in it.
type Shot struct {
	Path   string
	Bytes  int64
	Width  int
	Height int
}

// Screenshot writes VM name's screen to dest, or to a default path under
// the VM's directory when dest is empty.
//
// Stub: TestScreenshotRefusesAStoppedVM pins the ErrNotRunning contract;
// task 6 fills in the qemu.Screenshot call and the default path.
func Screenshot(name, dest string) (Shot, error) {
	return Shot{}, nil
}

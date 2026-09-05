package recipes

type Root struct {
	Path  string
	Scope string
}

func Roots() []Root { return nil }

func ResolvePath(string) (path, scope string, ok bool) { return "", "", false }

func ScopeOf(string) string { return "" }

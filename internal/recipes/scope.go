package recipes

type Scope struct {
	Name       string
	Dir        string
	LockPath   string
	CachePath  string
	ConfigPath string
}

type Decl struct {
	Source string
	Ref    string
}

func ScopeFor(bool) (Scope, error) { return Scope{}, nil }

func (Scope) Lock() (Lock, error) { return Lock{}, nil }

func (Scope) Save(Lock) error { return nil }

func (Scope) Decls() (map[string]Decl, error) { return map[string]Decl{}, nil }

func (Scope) SetDecl(string, Decl) error { return nil }

func (Scope) RemoveDecl(string) error { return nil }

func IgnoreStoatDir(string) error { return nil }

package recipes

const LockSchema = 1

type Lock struct {
	Schema  int                  `toml:"schema"`
	Recipes map[string]LockEntry `toml:"recipes"`
}

type LockEntry struct {
	Source string `toml:"source"`
	Ref    string `toml:"ref"`
	Commit string `toml:"commit"`
	Added  string `toml:"added"`
}

func LoadLock(string) (Lock, error) {
	return Lock{Schema: LockSchema, Recipes: map[string]LockEntry{}}, nil
}

func SaveLock(path string, _ Lock) error {
	return writeLockStub(path)
}

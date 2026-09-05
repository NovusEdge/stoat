package config

// SecretsName is the file holding secret recipe parameter values.
const SecretsName = "secrets.toml"

// Secrets maps a recipe name to its secret parameter values.
type Secrets map[string]map[string]string

// SecretsPath returns the path to this VM's secrets file.
func (v *VM) SecretsPath() string { return "" }

// LoadSecrets reads one VM's secret parameter values.
func LoadSecrets(dir string) (Secrets, error) { return nil, nil }

// SaveSecrets writes one VM's secret parameter values.
func SaveSecrets(dir string, s Secrets) error { return nil }

// Names returns the names of a recipe's set secret parameters.
func (s Secrets) Names(recipe string) []string { return nil }

package config

// LoadWithoutCredentials loads and validates settings without resolving any
// provider credentials. Account/metadata commands must resolve only the fields
// they actually use rather than unlocking the default inference provider.
func LoadWithoutCredentials() (*Config, error) { return loadUnresolved() }

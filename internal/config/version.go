package config

// version — версия приложения, задаётся на этапе сборки.
var version string

// Version возвращает версию приложения.
func Version() string {
	if version == "" {
		return "dev"
	}
	return version
}

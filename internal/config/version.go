package config

// version назначается при сборке через linker flags; пустое значение означает dev.
var version string

// Version возвращает назначенную через linker flags версию или dev.
func Version() string {
	if version == "" {
		return "dev"
	}
	return version
}

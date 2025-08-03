package env

type Config struct {
	JWTSecret string
}

func DefaultConfig() *Config {
	return &Config{
		JWTSecret: "my-secret-key",
	}
}
